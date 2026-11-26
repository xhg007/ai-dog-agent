package agent

// work agent 私聊历史持久化：
//
//	sessions/<sessionID>/agent-chats/<agentID>.jsonl     消息（每行 {role, content}）
//	sessions/<sessionID>/agent-chats/<agentID>.meta.json 配置指纹
//
// 与主对话的 messages.jsonl 分开存放，避免私聊内容混入线程记录；随会话目录一起删除。
// 指纹用于判断「配置重建后旧历史是否仍适用」：提示词/工具/Skill/MCP/模型任一变化即失效。

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/agentspec"
)

// storedAgentMessage 私聊历史的持久化格式（只保留回放需要的 role/content）。
type storedAgentMessage struct {
	Role    string `json:"role"` // user | assistant
	Content string `json:"content"`
}

// agentChatMeta 私聊历史的配置指纹。
type agentChatMeta struct {
	AgentID     string `json:"agentId"`
	Fingerprint string `json:"fingerprint"`
}

// agentChatStore 按会话保存 work agent 私聊历史；dir 为会话根目录（sessions/）。
// nil 表示不持久化（未设置 sessionDir 的服务与单测）。
type agentChatStore struct {
	dir string
}

// newAgentChatStore 创建私聊历史存储；dir 为空时返回 nil（关闭持久化）。
func newAgentChatStore(dir string) *agentChatStore {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return &agentChatStore{dir: dir}
}

func (s *agentChatStore) chatsDir(sessionID string) string {
	return filepath.Join(s.dir, sessionID, "agent-chats")
}

func (s *agentChatStore) messagesPath(sessionID, agentID string) string {
	return filepath.Join(s.chatsDir(sessionID), sanitizeToolName(agentID)+".jsonl")
}

func (s *agentChatStore) metaPath(sessionID, agentID string) string {
	return filepath.Join(s.chatsDir(sessionID), sanitizeToolName(agentID)+".meta.json")
}

// load 读取指定 Agent 的私聊历史：指纹不匹配时丢弃旧文件，损坏行截断已读部分。
func (s *agentChatStore) load(sessionID, agentID, fingerprint string) []*schema.Message {
	data, err := os.ReadFile(s.metaPath(sessionID, agentID))
	if err != nil {
		return nil
	}
	var meta agentChatMeta
	if err := json.Unmarshal(data, &meta); err != nil || meta.Fingerprint != fingerprint {
		slog.Info("私聊历史配置已变化，丢弃旧历史", "session", sessionID, "agent", agentID)
		s.remove(sessionID, agentID)
		return nil
	}
	f, err := os.Open(s.messagesPath(sessionID, agentID))
	if err != nil {
		return nil
	}
	defer f.Close()
	out := make([]*schema.Message, 0, maxHistoryMessages)
	dec := json.NewDecoder(bufio.NewReader(f))
	for {
		var m storedAgentMessage
		if err := dec.Decode(&m); err != nil {
			if !errors.Is(err, io.EOF) {
				slog.Warn("私聊历史损坏，已读取部分消息", "session", sessionID, "agent", agentID, "err", err)
			}
			break
		}
		switch m.Role {
		case "user":
			out = append(out, schema.UserMessage(m.Content))
		case "assistant":
			out = append(out, schema.AssistantMessage(m.Content, nil))
		}
	}
	return out
}

// rewrite 整体重写某个 Agent 的私聊历史（截断/压缩后调用；上限 40 条，代价可忽略）。
func (s *agentChatStore) rewrite(sessionID, agentID, fingerprint string,
	msgs []*schema.Message) error {
	if err := os.MkdirAll(s.chatsDir(sessionID), 0o755); err != nil {
		return err
	}
	target := s.messagesPath(sessionID, agentID)
	tmp := target + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		role := string(m.Role)
		if role != "user" && role != "assistant" {
			continue
		}
		if err := enc.Encode(storedAgentMessage{Role: role, Content: m.Content}); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	meta, err := json.Marshal(agentChatMeta{AgentID: agentID, Fingerprint: fingerprint})
	if err != nil {
		return err
	}
	metaTmp := s.metaPath(sessionID, agentID) + ".tmp"
	if err := os.WriteFile(metaTmp, meta, 0o600); err != nil {
		return err
	}
	return os.Rename(metaTmp, s.metaPath(sessionID, agentID))
}

// remove 删除单个 Agent 的私聊历史（配置重建覆盖时清空）。
func (s *agentChatStore) remove(sessionID, agentID string) {
	_ = os.Remove(s.messagesPath(sessionID, agentID))
	_ = os.Remove(s.metaPath(sessionID, agentID))
}

// removeSession 删除整个会话的私聊历史目录。
func (s *agentChatStore) removeSession(sessionID string) {
	if err := os.RemoveAll(s.chatsDir(sessionID)); err != nil {
		slog.Warn("删除私聊历史失败", "session", sessionID, "err", err)
	}
}

// specFingerprint 计算 work agent 配置指纹：提示词/工具/Skill/MCP/模型任一变化都会使旧历史失效。
// 预算与压缩参数不参与：它们不改变历史语义。
func specFingerprint(spec agentspec.Spec) string {
	h := sha256.New()
	for _, part := range []string{
		spec.AgentID,
		spec.Name,
		spec.SystemPrompt,
		spec.Model,
		strings.Join(spec.Tools, "\x00"),
		strings.Join(spec.Skills, "\x00"),
		strings.Join(spec.MCPServers, "\x00"),
	} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
