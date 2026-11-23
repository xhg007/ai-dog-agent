package agent

// 任务断点恢复（M5 FR-5.3）：
// 定位任务最后一个 after 检查点 → 取其 before 快照重建会话历史 →
// 以一条 user 续跑指令注入「已完成的工具调用与结果」→ 开新任务续跑。
// 不伪造 tool role 消息（DeepSeek/OpenAI 要求 ToolCallID 严格匹配，
// 伪造序列易 400）；工具结果对模型而言以 user 消息等价可用。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
)

// rebuildRecoveryMessages 由检查点对重建恢复后的消息历史。
func rebuildRecoveryMessages(before, after *store.Checkpoint) ([]StoredMessage, error) {
	var msgs []StoredMessage
	if before != nil && before.Messages != "" && before.Messages != "[]" {
		if err := json.Unmarshal([]byte(before.Messages), &msgs); err != nil {
			return nil, fmt.Errorf("解析消息快照失败: %w", err)
		}
	}
	var sb string
	if before != nil && before.ToolName != "" {
		args := before.ToolArgs
		if args == "" {
			args = "{}"
		}
		sb = fmt.Sprintf("此前你调用了工具 %s（参数：%s），其结果为：\n%s\n",
			before.ToolName, args, after.ToolResult)
	} else {
		sb = fmt.Sprintf("任务在工具 %s 执行后被中断，其结果为：\n%s\n", after.ToolName, after.ToolResult)
	}
	sb += "以上任务在此后被中断。请基于当前状态继续完成剩余工作；如需验证请直接运行相关工具。"
	msgs = append(msgs, StoredMessage{Role: "user", Content: sb})
	return msgs, nil
}

// RecoverTask 从断点恢复指定任务并续跑（新任务记录，原任务保持中断/失败终态）。
func (s *ChatService) RecoverTask(taskID string) error {
	if s.tasks == nil {
		return errors.New("无任务持久层，无法恢复")
	}
	t, err := s.tasks.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("查询任务失败: %w", err)
	}
	after, err := s.tasks.LastAfterCheckpoint(taskID)
	if err != nil {
		return fmt.Errorf("查询断点失败: %w", err)
	}
	if after == nil {
		return fmt.Errorf("任务 %s 无可用断点（中断发生在任何工具执行之前）", taskID)
	}
	before, err := s.tasks.CheckpointBySeq(taskID, after.Seq-1)
	if err == nil && (before == nil || before.Phase != "before") {
		before = nil
	}

	sessionID := t.SessionID
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("任务所在会话不存在: %s", sessionID)
	}
	if sess.running {
		s.mu.Unlock()
		return errors.New("会话正在执行中，无法恢复")
	}
	if sess.agent == nil {
		if err := s.initAgentLocked(sess); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	msgs, err := rebuildRecoveryMessages(before, after)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	sess.messages = msgs
	sess.running = true
	sess.lastActive = time.Now()
	s.persistLocked(sess)
	s.mu.Unlock()

	// 新任务记录：恢复轮次也走完整任务生命周期
	newTaskID, err := s.tasks.Start(sessionID, "恢复: "+truncateTitle(t.Title))
	if err != nil {
		slog.Warn("创建恢复任务失败（继续执行）", "err", err)
	} else {
		s.mu.Lock()
		sess.currentTaskID = newTaskID
		s.mu.Unlock()
	}

	s.bus.Emit(eventbus.Event{Type: eventbus.EvChatStarted, SessionID: sessionID,
		Data: eventbus.ChatStartedData{AgentID: sess.agentID}})

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	sess.cancel = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("恢复任务 panic", "session", sessionID, "panic", r)
				s.bus.Emit(eventbus.Event{Type: eventbus.EvChatError, SessionID: sessionID,
					Data: map[string]string{"message": fmt.Sprintf("内部错误: %v", r)}})
			}
			cancel()
			s.mu.Lock()
			sess.running = false
			sess.lastActive = time.Now()
			sess.cancel = nil
			s.persistLocked(sess)
			s.mu.Unlock()
		}()
		s.runTurn(ctx, sess)
	}()
	slog.Info("任务断点恢复", "task", taskID, "session", sessionID,
		"checkpoint", after.Seq, "tool", after.ToolName)
	return nil
}
