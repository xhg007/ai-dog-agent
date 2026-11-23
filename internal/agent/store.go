package agent

import (
	"encoding/json"
	"errors"
	"fmt"
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
	Role    string `json:"role"`    // user | assistant | tool | error
	Content string `json:"content"`
	Tool    string `json:"tool,omitempty"`
}

// sessionRecord 单个会话的磁盘快照。
type sessionRecord struct {
	ID         string          `json:"id"`
	AgentID    string          `json:"agentId"`
	Title      string          `json:"title"`
	CreatedAt  int64           `json:"createdAt"`
	LastActive int64           `json:"lastActive"`
	Messages   []StoredMessage `json:"messages"`
}

// sessionStore 以「一个会话一个 JSON 文件」的方式持久化，避免引入数据库依赖。
type sessionStore struct {
	dir string
	mu  sync.Mutex
}

func newSessionStore(dir string) *sessionStore { return &sessionStore{dir: dir} }

func (s *sessionStore) path(id string) string { return filepath.Join(s.dir, id+".json") }

func (s *sessionStore) save(rec sessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	target := s.path(rec.ID)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("替换会话文件失败: %w", err)
	}
	return nil
}

// load 读取全部会话；损坏文件跳过，目录不存在返回空列表。
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
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var rec sessionRecord
		if err := json.Unmarshal(data, &rec); err != nil || rec.ID == "" {
			continue
		}
		if rec.Messages == nil {
			rec.Messages = []StoredMessage{}
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive > out[j].LastActive })
	return out, nil
}

func (s *sessionStore) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(id)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("会话文件不存在: %s", id)
		}
		return err
	}
	return nil
}

// RestoreSessions 从磁盘恢复会话（应用启动时调用）。执行中的状态复位为未运行。
func (s *ChatService) RestoreSessions() error {
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

// persistLocked 落盘当前会话（调用方持有 s.mu）。
func (s *ChatService) persistLocked(sess *chatSession) {
	if s.store == nil {
		return
	}
	if err := s.store.save(s.snapshotLocked(sess)); err != nil {
		slog.Error("会话持久化失败", "err", err)
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
