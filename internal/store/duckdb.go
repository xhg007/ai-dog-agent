package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2" // 注册 "duckdb" database/sql 驱动

	// Windows + MSYS2 GCC16 下 libduckdb 预构建库缺失的 emutls 控制变量补丁（见包注释）
	_ "ai-dog-agent/internal/store/emutlfix"
)

// DuckDBStore Store 接口的 DuckDB 实现。
// 并发交给 database/sql 连接池与 DuckDB 自身：同一进程内多连接可并发读，
// 写事务由 DuckDB 内部串行化，应用层不再做单连接/互斥限制。
type DuckDBStore struct {
	db  *sql.DB
	now func() time.Time // 测试注入
}

// Open 打开（或创建）DuckDB 数据库文件并建表。
//
// 统计是可再生数据：文件损坏时按「先删 WAL、再重建主文件」自愈，保证统计库
// 不会阻塞应用启动。第一级只丢未 checkpoint 的数据；第二级把主文件重命名为
// .corrupt 备份后重建空库（不直接销毁，便于人工排查）。
func Open(path string) (*DuckDBStore, error) {
	st, err := openConn(path)
	if err == nil {
		if merr := st.migrate(); merr != nil {
			st.Close()
			return nil, merr
		}
		return st, nil
	}
	// 内存库或空路径没有文件可自愈
	if path == "" || path == ":memory:" {
		return nil, err
	}

	// 1) WAL 损坏：删掉 WAL 后重试，主文件中已 checkpoint 的数据仍可用
	if _, statErr := os.Stat(path + ".wal"); statErr == nil {
		if rmErr := os.Remove(path + ".wal"); rmErr == nil {
			if retried, retryErr := openAndMigrate(path); retryErr == nil {
				slog.Warn("统计库 WAL 不可恢复，已删除 WAL（丢失未 checkpoint 的统计数据）", "path", path)
				return retried, nil
			}
		}
	}

	// 2) 主文件损坏：备份为 .corrupt 后重建空库
	if _, statErr := os.Stat(path); statErr == nil {
		backup := path + ".corrupt"
		_ = os.Remove(backup)
		if renameErr := os.Rename(path, backup); renameErr == nil {
			_ = os.Remove(path + ".wal")
			if retried, retryErr := openAndMigrate(path); retryErr == nil {
				slog.Warn("统计库文件损坏，已重建空库（原文件已备份）", "path", path, "backup", backup)
				return retried, nil
			}
		}
	}
	return nil, err
}

// openAndMigrate 打开连接并建表；任一步失败都关闭连接。
func openAndMigrate(path string) (*DuckDBStore, error) {
	st, err := openConn(path)
	if err != nil {
		return nil, err
	}
	if err := st.migrate(); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

// openConn 建立连接并 Ping（不建表）。
func openConn(path string) (*DuckDBStore, error) {
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, fmt.Errorf("打开 DuckDB 失败: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 DuckDB 失败: %w", err)
	}
	// 并发由 database/sql 连接池与 DuckDB 自身管理，应用层不限制连接数。
	return &DuckDBStore{db: db, now: time.Now}, nil
}

func (s *DuckDBStore) migrate() error {
	ctx := context.Background()
	// 初始化语句集中在同一连接上执行，用完立即归还连接池
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("获取建表连接失败: %w", err)
	}
	defer conn.Close()

	stmts := []string{
		`CREATE SEQUENCE IF NOT EXISTS seq_task_events`,
		`CREATE SEQUENCE IF NOT EXISTS seq_token_usage`,
		`CREATE SEQUENCE IF NOT EXISTS seq_checkpoints`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id VARCHAR PRIMARY KEY,
			session_id VARCHAR NOT NULL,
			title VARCHAR NOT NULL,
			status VARCHAR NOT NULL,
			error VARCHAR DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			finished_at TIMESTAMPTZ
		)`,
		`CREATE TABLE IF NOT EXISTS task_events (
			id BIGINT PRIMARY KEY DEFAULT nextval('seq_task_events'),
			task_id VARCHAR NOT NULL,
			type VARCHAR NOT NULL,
			payload VARCHAR NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events(task_id)`,
		`CREATE TABLE IF NOT EXISTS token_usage (
			id BIGINT PRIMARY KEY DEFAULT nextval('seq_token_usage'),
			task_id VARCHAR NOT NULL,
			model VARCHAR NOT NULL,
			input_tokens BIGINT NOT NULL,
			output_tokens BIGINT NOT NULL,
			cost_in DOUBLE NOT NULL DEFAULT 0,
			cost_out DOUBLE NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_token_usage_task ON token_usage(task_id)`,
		`CREATE TABLE IF NOT EXISTS checkpoints (
			id BIGINT PRIMARY KEY DEFAULT nextval('seq_checkpoints'),
			task_id VARCHAR NOT NULL,
			seq BIGINT NOT NULL,
			phase VARCHAR NOT NULL,
			tool_name VARCHAR NOT NULL DEFAULT '',
			tool_args VARCHAR NOT NULL DEFAULT '',
			tool_result VARCHAR NOT NULL DEFAULT '',
			messages VARCHAR NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_checkpoints_task ON checkpoints(task_id, seq)`,
	}
	for _, q := range stmts {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("建表失败: %w (sql=%s)", err, q)
		}
	}
	return nil
}

// CreateTask 写入新任务。
func (s *DuckDBStore) CreateTask(t Task) error {
	if t.ID == "" {
		return fmt.Errorf("task id 不能为空")
	}
	created := t.CreatedAt
	if created.IsZero() {
		created = s.now()
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx,
		`INSERT INTO tasks (id, session_id, title, status, error, created_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.SessionID, t.Title, t.Status, t.Error, created, nullableTimePtr(t.FinishedAt))
	return err
}

// UpdateTaskStatus 更新任务状态；终态时记录 finished_at。
func (s *DuckDBStore) UpdateTaskStatus(id, status, errMsg string) error {
	finished := isTerminal(status)
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx,
		`UPDATE tasks SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errMsg, nullableTimeIf(finished, s.now()), id)
	return err
}

// AppendTaskEvent 追加任务事件。
func (s *DuckDBStore) AppendTaskEvent(taskID, evType, payload string) error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx,
		`INSERT INTO task_events (task_id, type, payload, created_at) VALUES (?, ?, ?, ?)`,
		taskID, evType, payload, s.now())
	return err
}

// AppendTokenUsage 追加一次 LLM 调用用量。
func (s *DuckDBStore) AppendTokenUsage(u TokenUsage) error {
	created := u.CreatedAt
	if created.IsZero() {
		created = s.now()
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx,
		`INSERT INTO token_usage (task_id, model, input_tokens, output_tokens, cost_in, cost_out, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.TaskID, u.Model, u.InputTokens, u.OutputTokens, u.CostIn, u.CostOut, created)
	return err
}

// AppendCheckpoint 追加工具检查点。
func (s *DuckDBStore) AppendCheckpoint(c Checkpoint) error {
	created := c.CreatedAt
	if created.IsZero() {
		created = s.now()
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	_, err = conn.ExecContext(ctx,
		`INSERT INTO checkpoints (task_id, seq, phase, tool_name, tool_args, tool_result, messages, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.TaskID, c.Seq, c.Phase, c.ToolName, c.ToolArgs, c.ToolResult, c.Messages, created)
	return err
}

// LastAfterCheckpoint 返回任务最后一个 after 检查点（无则 nil, nil）。
func (s *DuckDBStore) LastAfterCheckpoint(taskID string) (*Checkpoint, error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	row := conn.QueryRowContext(ctx,
		`SELECT id, task_id, seq, phase, tool_name, tool_args, tool_result, messages, created_at
		 FROM checkpoints WHERE task_id = ? AND phase = 'after'
		 ORDER BY seq DESC LIMIT 1`, taskID)
	return scanCheckpoint(row)
}

func scanCheckpoint(row *sql.Row) (*Checkpoint, error) {
	var (
		c         Checkpoint
		createdAt time.Time
	)
	if err := row.Scan(&c.ID, &c.TaskID, &c.Seq, &c.Phase, &c.ToolName,
		&c.ToolArgs, &c.ToolResult, &c.Messages, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	c.CreatedAt = createdAt
	return &c, nil
}

// ErrNoTask 任务不存在。
var ErrNoTask = fmt.Errorf("任务不存在")

// GetTask 单任务查询。
func (s *DuckDBStore) GetTask(id string) (*Task, error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	row := conn.QueryRowContext(ctx,
		`SELECT id, session_id, title, status, error, created_at, finished_at
		 FROM tasks WHERE id = ?`, id)
	var (
		t          Task
		finishedAt sql.NullTime
	)
	if err := row.Scan(&t.ID, &t.SessionID, &t.Title, &t.Status, &t.Error, &t.CreatedAt, &finishedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoTask
		}
		return nil, err
	}
	if finishedAt.Valid {
		ft := finishedAt.Time
		t.FinishedAt = &ft
	}
	return &t, nil
}

// CheckpointBySeq 指定序号检查点查询。
func (s *DuckDBStore) CheckpointBySeq(taskID string, seq int64) (*Checkpoint, error) {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	row := conn.QueryRowContext(ctx,
		`SELECT id, task_id, seq, phase, tool_name, tool_args, tool_result, messages, created_at
		 FROM checkpoints WHERE task_id = ? AND seq = ?`, taskID, seq)
	return scanCheckpoint(row)
}

// ListTasks 最近任务（created_at 降序）。
func (s *DuckDBStore) ListTasks(limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx,
		`SELECT id, session_id, title, status, error, created_at, finished_at
		 FROM tasks ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var (
			t          Task
			finishedAt sql.NullTime
		)
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Title, &t.Status, &t.Error, &t.CreatedAt, &finishedAt); err != nil {
			return nil, err
		}
		if finishedAt.Valid {
			ft := finishedAt.Time
			t.FinishedAt = &ft
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TaskEvents 任务事件流（时间升序）。
func (s *DuckDBStore) TaskEvents(taskID string, limit int) ([]TaskEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx,
		`SELECT id, task_id, type, payload, created_at
		 FROM task_events WHERE task_id = ? ORDER BY id ASC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskEvent
	for rows.Next() {
		var (
			e         TaskEvent
			createdAt time.Time
		)
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Type, &e.Payload, &createdAt); err != nil {
			return nil, err
		}
		e.CreatedAt = createdAt
		out = append(out, e)
	}
	return out, rows.Err()
}

// DailyUsage 近 days 天按日聚合的用量报表（日期降序）。
func (s *DuckDBStore) DailyUsage(days int) ([]DailyUsageRow, error) {
	if days <= 0 {
		days = 30
	}
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	rows, err := conn.QueryContext(ctx,
		`SELECT strftime(CAST(created_at AS DATE), '%Y-%m-%d') AS day,
		        count(*) AS calls,
		        sum(input_tokens) AS in_tok,
		        sum(output_tokens) AS out_tok,
		        sum(cost_in + cost_out) AS cost
		 FROM token_usage
		 WHERE created_at >= now() - INTERVAL (`+fmt.Sprint(days)+`) DAY
		 GROUP BY day ORDER BY day DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyUsageRow
	for rows.Next() {
		var r DailyUsageRow
		var cost sql.NullFloat64
		var inTok, outTok sql.NullInt64
		if err := rows.Scan(&r.Date, &r.Calls, &inTok, &outTok, &cost); err != nil {
			return nil, err
		}
		r.InputTokens, r.OutputTokens, r.CostTotal = inTok.Int64, outTok.Int64, cost.Float64
		out = append(out, r)
	}
	return out, rows.Err()
}

// Close 关闭数据库（刷新 WAL 落盘，务必调用）。
func (s *DuckDBStore) Close() error { return s.db.Close() }

func isTerminal(status string) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusInterrupted:
		return true
	}
	return false
}

// nullableTimePtr 把可空时间转换为 SQL 参数（nil 写入 NULL）。
func nullableTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func nullableTimeIf(cond bool, t time.Time) any {
	if !cond {
		return nil
	}
	return t
}

// truncateForLog 日志用截断（避免把大 payload 全量打进日志）。
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "…"
}
