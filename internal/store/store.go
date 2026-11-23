// Package store 持久层：任务、任务事件、Token 用量、工具检查点（M5 FR-5.2/5.3/5.4）。
// 实现基于 DuckDB（单文件分析型库，聚合报表用 SQL 完成）。
package store

import "time"

// 任务状态（FR-5.2）。
const (
	StatusRunning     = "running"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
)

// Task 一次用户消息触发的执行轮次。
type Task struct {
	ID        string // UUID，由调用方生成
	SessionID string
	Title     string
	Status    string // StatusRunning/StatusCompleted/StatusFailed/StatusInterrupted
	Error     string
	CreatedAt time.Time
	// FinishedAt 终态时间（运行中为零值；扫描侧用 sql.NullTime）。
	FinishedAt time.Time
}

// TaskEvent 任务事件流记录（task_events 表，FR-5.1 事件落库）。
type TaskEvent struct {
	ID        int64
	TaskID    string
	Type      string // task_state/tool_call/tool_result/llm_usage/model_fallback/chat_error...
	Payload   string // JSON 文本
	CreatedAt time.Time
}

// TokenUsage 一次 LLM 调用的用量与成本（token_usage 表，FR-5.4）。
type TokenUsage struct {
	ID           int64
	TaskID       string
	Model        string
	InputTokens  int64
	OutputTokens int64
	CostIn       float64 // 元
	CostOut      float64 // 元
	CreatedAt    time.Time
}

// Checkpoint 工具级检查点（checkpoints 表，FR-5.3 断点恢复）。
// phase=before 在工具执行前写（含 args + 消息快照）；phase=after 在执行后写（追加 result）。
type Checkpoint struct {
	ID         int64
	TaskID     string
	Seq        int64 // 任务内递增序号
	Phase      string // "before" / "after"
	ToolName   string
	ToolArgs   string // JSON 文本
	ToolResult string // JSON 文本（仅 after）
	Messages   string // 会话消息快照 JSON（仅 before 携带，供断点重建历史）
	CreatedAt  time.Time
}

// DailyUsageRow 日维度用量汇总（DailyUsage 聚合结果）。
type DailyUsageRow struct {
	Date         string  // YYYY-MM-DD
	Calls        int64
	InputTokens  int64
	OutputTokens int64
	CostTotal    float64
}

// Store 持久层接口（DuckDB 实现；写入建议经 AsyncWriter 异步化）。
type Store interface {
	CreateTask(t Task) error
	UpdateTaskStatus(id, status, errMsg string) error
	AppendTaskEvent(taskID, evType string, payload string) error
	AppendTokenUsage(u TokenUsage) error
	AppendCheckpoint(c Checkpoint) error
	// LastAfterCheckpoint 返回任务最后一个 after 检查点（无则返回 nil, nil）。
	LastAfterCheckpoint(taskID string) (*Checkpoint, error)
	ListTasks(limit int) ([]Task, error)
	// TaskEvents 返回任务的事件流（按时间升序）。
	TaskEvents(taskID string, limit int) ([]TaskEvent, error)
	// DailyUsage 近 days 天按日聚合的用量报表（日期降序）。
	DailyUsage(days int) ([]DailyUsageRow, error)
	Close() error
}
