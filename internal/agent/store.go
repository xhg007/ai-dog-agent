package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
)

// StoredMessage 持久化的对话消息（与前端 Msg 对齐）。
type StoredMessage struct {
	Role    string `json:"role"` // user | assistant | tool | error
	Content string `json:"content"`
	Tool    string `json:"tool,omitempty"`
}

// sessionRecord 单个会话的内存快照（落盘时拆成 meta.json + messages.jsonl）。
type sessionRecord struct {
	ID         string          `json:"id"`
	AgentID    string          `json:"agentId"`
	Title      string          `json:"title"`
	CreatedAt  int64           `json:"createdAt"`
	LastActive int64           `json:"lastActive"`
	Messages   []StoredMessage `json:"messages"`
}

// sessionMeta 会话元数据（meta.json 内容）。
type sessionMeta struct {
	ID         string `json:"id"`
	AgentID    string `json:"agentId"`
	Title      string `json:"title"`
	CreatedAt  int64  `json:"createdAt"`
	LastActive int64  `json:"lastActive"`
}

// sessionStore 以「会话目录 + 消息追加日志」持久化：
//
//	sessions/<id>/meta.json       元数据（标题/时间），很小，原子重写
//	sessions/<id>/messages.jsonl  消息，逐条追加（正常轮次只写新增条数）
//
// 兼容旧格式 sessions/<id>.json：首次 load 时迁移到目录，写新文件成功后再删旧文件。
type sessionStore struct {
	dir      string
	mu       sync.Mutex
	appended map[string]int // sessionID -> 已写入 messages.jsonl 的条数
}

func newSessionStore(dir string) *sessionStore {
	return &sessionStore{dir: dir, appended: make(map[string]int)}
}

func (s *sessionStore) sessionDir(id string) string {
	return filepath.Join(s.dir, id)
}

func (s *sessionStore) metaPath(id string) string {
	return filepath.Join(s.sessionDir(id), "meta.json")
}

func (s *sessionStore) messagesPath(id string) string {
	return filepath.Join(s.sessionDir(id), "messages.jsonl")
}

func (s *sessionStore) legacyPath(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// save 写入会话快照：元数据重写，消息按增量追加（首次或历史变短时全量重写）。
func (s *sessionStore) save(rec sessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(rec)
}

func (s *sessionStore) saveLocked(rec sessionRecord) error {
	if err := os.MkdirAll(s.sessionDir(rec.ID), 0o755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	written, ok := s.appended[rec.ID]
	switch {
	case !ok || written > len(rec.Messages):
		// 首次写入，或历史被整体替换（变短）：全量重写
		if err := s.rewriteMessagesLocked(rec); err != nil {
			return err
		}
	case written < len(rec.Messages):
		if err := s.appendMessagesLocked(rec.ID, rec.Messages[written:]); err != nil {
			return err
		}
	}
	return s.writeMetaLocked(rec)
}

// replace 用快照整体替换消息历史（断点恢复重建消息后使用）。
func (s *sessionStore) replace(rec sessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rewriteMessagesLocked(rec); err != nil {
		return err
	}
	return s.writeMetaLocked(rec)
}

func (s *sessionStore) appendMessagesLocked(id string, msgs []StoredMessage) error {
	if len(msgs) == 0 {
		return nil
	}
	f, err := os.OpenFile(s.messagesPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开消息日志失败: %w", err)
	}
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		if err := enc.Encode(m); err != nil {
			_ = f.Close()
			return fmt.Errorf("追加消息失败: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("刷新消息日志失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭消息日志失败: %w", err)
	}
	s.appended[id] += len(msgs)
	return nil
}

func (s *sessionStore) rewriteMessagesLocked(rec sessionRecord) error {
	if err := os.MkdirAll(s.sessionDir(rec.ID), 0o755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	target := s.messagesPath(rec.ID)
	tmp := target + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("写入消息日志失败: %w", err)
	}
	enc := json.NewEncoder(f)
	for _, m := range rec.Messages {
		if err := enc.Encode(m); err != nil {
			_ = f.Close()
			return fmt.Errorf("序列化消息失败: %w", err)
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("刷新消息日志失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭消息日志失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("替换消息日志失败: %w", err)
	}
	s.appended[rec.ID] = len(rec.Messages)
	return nil
}

func (s *sessionStore) writeMetaLocked(rec sessionRecord) error {
	data, err := json.MarshalIndent(sessionMeta{
		ID: rec.ID, AgentID: rec.AgentID, Title: rec.Title,
		CreatedAt: rec.CreatedAt, LastActive: rec.LastActive,
	}, "", "  ")
	if err != nil {
		return err
	}
	target := s.metaPath(rec.ID)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入会话元数据失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("替换会话元数据失败: %w", err)
	}
	return nil
}

// load 读取全部会话（新目录格式 + 旧单文件迁移）；损坏条目跳过。
func (s *sessionStore) load() ([]sessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]sessionRecord, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			rec, err := s.loadDirLocked(e.Name())
			if err != nil {
				slog.Warn("跳过损坏的会话目录", "dir", e.Name(), "err", err)
				continue
			}
			if rec != nil {
				out = append(out, *rec)
			}
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		rec, err := s.migrateLegacyLocked(id)
		if err != nil {
			slog.Warn("跳过无效的旧会话文件", "file", name, "err", err)
			continue
		}
		if rec != nil {
			out = append(out, *rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive > out[j].LastActive })
	return out, nil
}

func (s *sessionStore) loadDirLocked(id string) (*sessionRecord, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return nil, fmt.Errorf("读取元数据失败: %w", err)
	}
	var meta sessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("解析元数据失败: %w", err)
	}
	if meta.ID == "" {
		meta.ID = id
	}
	msgs := s.readMessagesLocked(meta.ID)
	s.appended[meta.ID] = len(msgs)
	return &sessionRecord{
		ID: meta.ID, AgentID: meta.AgentID, Title: meta.Title,
		CreatedAt: meta.CreatedAt, LastActive: meta.LastActive,
		Messages: msgs,
	}, nil
}

// readMessagesLocked 逐条解码 JSONL；损坏行截断已读部分并告警，不影响启动。
func (s *sessionStore) readMessagesLocked(id string) []StoredMessage {
	f, err := os.Open(s.messagesPath(id))
	if err != nil {
		return []StoredMessage{}
	}
	defer f.Close()
	out := []StoredMessage{}
	dec := json.NewDecoder(f)
	for {
		var m StoredMessage
		if err := dec.Decode(&m); err != nil {
			if !errors.Is(err, io.EOF) {
				slog.Warn("会话消息日志损坏，已读取部分消息", "session", id, "err", err)
			}
			break
		}
		out = append(out, m)
	}
	return out
}

// migrateLegacyLocked 把旧 <id>.json 迁移为目录格式；成功写入新文件后才删除旧文件。
func (s *sessionStore) migrateLegacyLocked(id string) (*sessionRecord, error) {
	data, err := os.ReadFile(s.legacyPath(id))
	if err != nil {
		return nil, err
	}
	var rec sessionRecord
	if err := json.Unmarshal(data, &rec); err != nil || rec.ID == "" {
		return nil, fmt.Errorf("旧会话文件无效")
	}
	if rec.Messages == nil {
		rec.Messages = []StoredMessage{}
	}
	// 新格式目录已存在：以新格式为准，旧文件备份后让目录条目自行加载
	if _, err := os.Stat(s.sessionDir(rec.ID)); err == nil {
		s.backupLegacy(id)
		return nil, nil
	}
	if err := s.rewriteMessagesLocked(rec); err != nil {
		return nil, err
	}
	if err := s.writeMetaLocked(rec); err != nil {
		return nil, err
	}
	// 迁移成功后保留 <id>.json.bak 作为可回滚副本（不直接删除）
	s.backupLegacy(id)
	return &rec, nil
}

// backupLegacy 把旧单文件重命名为 .bak；文件不存在时静默跳过。
func (s *sessionStore) backupLegacy(id string) {
	legacy := s.legacyPath(id)
	if _, err := os.Stat(legacy); err != nil {
		return
	}
	if err := os.Rename(legacy, legacy+".bak"); err != nil {
		slog.Warn("迁移旧会话后备份失败", "file", id+".json", "err", err)
	}
}

func (s *sessionStore) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.sessionDir(id)
	legacy := s.legacyPath(id)
	bak := legacy + ".bak"
	_, derr := os.Stat(dir)
	_, lerr := os.Stat(legacy)
	_, berr := os.Stat(bak)
	if errors.Is(derr, os.ErrNotExist) && errors.Is(lerr, os.ErrNotExist) && errors.Is(berr, os.ErrNotExist) {
		return fmt.Errorf("会话不存在: %s", id)
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("删除会话目录失败: %w", err)
	}
	if err := os.Remove(legacy); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(bak); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	delete(s.appended, id)
	return nil
}

// restoreSessions 从磁盘恢复会话（应用启动时调用）。执行中的状态复位为未运行。
func (s *ChatService) restoreSessions() error {
	if s.store == nil {
		return nil
	}
	recs, err := s.store.load()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range recs {
		s.sessions[rec.ID] = &chatSession{
			id: rec.ID, agentID: rec.AgentID, title: rec.Title,
			createdAt: time.UnixMilli(rec.CreatedAt), lastActive: time.UnixMilli(rec.LastActive),
			messages: append([]StoredMessage(nil), rec.Messages...),
		}
	}
	return nil
}

// GetMessages 返回指定会话的持久化消息历史（供前端切换线程时恢复）。
func (s *ChatService) GetMessages(sessionID string) []StoredMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return []StoredMessage{}
	}
	return append([]StoredMessage(nil), sess.messages...)
}

// snapshotLocked 生成会话磁盘快照（调用方持有 s.mu）。
func (s *ChatService) snapshotLocked(sess *chatSession) sessionRecord {
	return sessionRecord{
		ID: sess.id, AgentID: sess.agentID, Title: sess.title,
		CreatedAt: sess.createdAt.UnixMilli(), LastActive: sess.lastActive.UnixMilli(),
		Messages: append([]StoredMessage(nil), sess.messages...),
	}
}

// persistLocked 落盘当前会话（调用方持有 s.mu）：元数据原子重写 + 消息增量追加。
// 会话已被删除（用户删线程/会话被替换）时丢弃迟到的快照：否则运行中 goroutine
// 退出时的落盘会把刚删掉的会话目录重新建出来（线程"复活"）。
func (s *ChatService) persistLocked(sess *chatSession) {
	if s.store == nil {
		return
	}
	if cur, ok := s.sessions[sess.id]; !ok || cur != sess {
		return
	}
	if err := s.store.save(s.snapshotLocked(sess)); err != nil {
		slog.Error("会话持久化失败", "err", err)
	}
}

// persistReplaceLocked 用当前快照整体替换消息历史（断点恢复重建消息后使用）。
func (s *ChatService) persistReplaceLocked(sess *chatSession) {
	if s.store == nil {
		return
	}
	if cur, ok := s.sessions[sess.id]; !ok || cur != sess {
		slog.Warn("会话已删除，跳过历史替换", "session", sess.id)
		return
	}
	if err := s.store.replace(s.snapshotLocked(sess)); err != nil {
		slog.Error("会话历史替换失败", "err", err)
	}
}

// appendMessages 追加消息并落盘（会自行加锁）。
func (s *ChatService) appendMessages(sess *chatSession, msgs ...StoredMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.messages = append(sess.messages, msgs...)
	sess.lastActive = time.Now()
	s.persistLocked(sess)
}

// historyMessages 把持久化消息转换为模型输入；只保留 user/assistant，
// 工具步骤卡片与错误提示不回放给模型。
func historyMessages(msgs []StoredMessage) []*schema.Message {
	out := make([]*schema.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, schema.UserMessage(m.Content))
		case "assistant":
			if m.Content != "" {
				out = append(out, schema.AssistantMessage(m.Content, nil))
			}
		}
	}
	return out
}
