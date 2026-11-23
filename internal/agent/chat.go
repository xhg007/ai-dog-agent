package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/llm"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
	"ai-dog-agent/internal/workspace"
)

// ChatService 会话服务（Wails Service）。
// 每个会话持有一个独立的 ADK ChatModelAgent 实例：Agent 内部维护会话历史，
// 即「独立上下文」的最小实现；子 Agent 的独立上下文机制在后续里程碑复用同一路径。
type ChatService struct {
	cfg        *config.Manager
	ws         *workspace.Manager
	bus        *eventbus.Bus
	appr       *approval.Manager
	backupRoot string
	store      *sessionStore   // 会话持久化（一个会话一个 JSON 文件）
	tasks      *task.Manager   // 任务生命周期（M5；nil 时跳过任务记录，仅事件推送）

	mu       sync.Mutex
	sessions map[string]*chatSession
}

type chatSession struct {
	id             string
	agentID        string // Agent 唯一 ID，随请求体 agent_id 字段上送
	agent          adk.Agent
	runner         *adk.Runner
	cancel         context.CancelFunc
	running        bool
	currentTaskID  string    // 当前轮次的任务 ID（M5）
	title          string    // 线程标题（取首条用户消息摘要）
	createdAt      time.Time
	lastActive     time.Time
	messages       []StoredMessage // 持久化消息历史
}

// SessionInfo 线程列表条目（对齐 Codex 左栏 Threads）。
type SessionInfo struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreatedAt  int64  `json:"createdAt"`  // Unix 毫秒
	LastActive int64  `json:"lastActive"` // Unix 毫秒
	Running    bool   `json:"running"`
}

// NewChatService 创建服务实例，并从 sessionDir 恢复历史会话。
// tasks 为任务管理器（M5 FR-5.2）；传 nil 时跳过任务记录（仅事件推送）。
func NewChatService(cfg *config.Manager, ws *workspace.Manager, bus *eventbus.Bus,
	appr *approval.Manager, backupRoot, sessionDir string, tasks *task.Manager) *ChatService {
	s := &ChatService{
		cfg:        cfg,
		ws:         ws,
		bus:        bus,
		appr:       appr,
		backupRoot: backupRoot,
		tasks:      tasks,
		sessions:   make(map[string]*chatSession),
	}
	// 注册全局观测 handler（M5）：tasks 非 nil 时才注册，避免测试中重复注册全局 handler
	if tasks != nil {
		RegisterObsHandler(tasks, func() (string, float64, float64) {
			if mc, err := cfg.ActiveModel(); err == nil {
				return mc.Model, mc.PriceIn, mc.PriceOut
			}
			return "", 0, 0
		})
	}
	if sessionDir != "" {
		s.store = newSessionStore(sessionDir)
		if err := s.RestoreSessions(); err != nil {
			slog.Error("恢复历史会话失败", "err", err)
		}
	}
	return s
}

// NewSession 创建新会话，返回会话 ID。Agent 实例在首次发消息时惰性构建。
func (s *ChatService) NewSession() string {
	id := uuid.NewString()
	agentID := uuid.NewString() // 每个会话对应一个独立 Agent 实例，拥有唯一 ID
	now := time.Now()
	s.mu.Lock()
	s.sessions[id] = &chatSession{id: id, agentID: agentID, createdAt: now, lastActive: now}
	s.persistLocked(s.sessions[id])
	s.mu.Unlock()
	return id
}

// ListSessions 返回全部线程，按最近活跃倒序。
func (s *ChatService) ListSessions() []SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]SessionInfo, 0, len(s.sessions))
	for _, sess := range s.sessions {
		list = append(list, SessionInfo{
			ID:         sess.id,
			Title:      sess.title,
			CreatedAt:  sess.createdAt.UnixMilli(),
			LastActive: sess.lastActive.UnixMilli(),
			Running:    sess.running,
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].LastActive > list[j].LastActive })
	return list
}

// DeleteSession 删除线程；正在执行的先中断。
func (s *ChatService) DeleteSession(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false
	}
	if sess.cancel != nil {
		sess.cancel()
	}
	delete(s.sessions, sessionID)
	if s.store != nil {
		if err := s.store.delete(sessionID); err != nil {
			slog.Warn("删除会话文件失败", "session", sessionID, "err", err)
		}
	}
	return true
}

// EnsureSession 注册前端已有的会话 ID（幂等，不存在则创建）。
func (s *ChatService) EnsureSession(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	if _, ok := s.sessions[sessionID]; !ok {
		now := time.Now()
		s.sessions[sessionID] = &chatSession{id: sessionID, agentID: uuid.NewString(), createdAt: now, lastActive: now}
		s.persistLocked(s.sessions[sessionID])
	}
	s.mu.Unlock()
}

// SendMessage 向指定会话发送用户消息，异步执行，过程与结果通过事件流推送。
func (s *ChatService) SendMessage(sessionID string, content string) error {
	s.mu.Lock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	if sess.running {
		s.mu.Unlock()
		return errors.New("当前会话正在执行中，请等待完成或先中断")
	}
	s.mu.Unlock()

	// 模型按当前激活配置构建（在 initAgentLocked 中完成）；修改模型配置后新建会话生效。
	if _, err := s.cfg.ActiveModel(); err != nil {
		return err
	}

	s.mu.Lock()
	if sess.agent == nil {
		if err := s.initAgentLocked(sess); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	if sess.title == "" {
		sess.title = truncateTitle(content)
	}
	sess.messages = append(sess.messages, StoredMessage{Role: "user", Content: content})
	sess.lastActive = time.Now()
	sess.running = true
	s.persistLocked(sess)
	s.mu.Unlock()

	// 创建任务（FR-5.2）：一轮对话 = 一个任务
	if s.tasks != nil {
		taskID, err := s.tasks.Start(sessionID, truncateTitle(content))
		if err != nil {
			slog.Warn("创建任务失败（继续执行，仅缺少任务记录）", "err", err)
		} else {
			s.mu.Lock()
			sess.currentTaskID = taskID
			s.mu.Unlock()
		}
	}

	bus := s.bus
	bus.Emit(eventbus.Event{Type: eventbus.EvChatStarted, SessionID: sessionID,
		Data: eventbus.ChatStartedData{AgentID: sess.agentID}})

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	sess.cancel = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("会话执行 panic", "session", sessionID, "panic", r)
				bus.Emit(eventbus.Event{Type: eventbus.EvChatError, SessionID: sessionID,
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
	return nil
}

// Cancel 中断会话当前执行。
func (s *ChatService) Cancel(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok || !sess.running || sess.cancel == nil {
		return false
	}
	sess.cancel()
	return true
}

// initAgentLocked 惰性构建 Agent + Runner（调用方持有锁）。
func (s *ChatService) initAgentLocked(sess *chatSession) error {
	ctx := context.Background()
	mc, err := s.cfg.ActiveModel()
	if err != nil {
		return err
	}
	cm, err := llm.BuildModel(ctx, mc, sess.agentID)
	if err != nil {
		return err
	}

	extras := s.buildDynamicExtras()
	var ag adk.Agent
	if s.ws.IsSet() {
		// workspace 已设置：主 Agent 携带 explorer 分派工具 + 写操作审批工具 + 执行工具
		ag, err = BuildOrchestrator(ctx, s.ws, cm, s.appr, s.backupRoot, sess.id, s.cfg.ShellWhitelist(), extras)
	} else {
		// 未设置 workspace：纯对话模式
		ag, err = newPlainOrchestrator(ctx, cm, extras)
	}
	if err != nil {
		return fmt.Errorf("构建 Agent 失败: %w", err)
	}
	sess.agent = ag
	sess.runner = adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           ag,
		EnableStreaming: true,
	})
	slog.Info("会话 Agent 已构建", "session", sess.id, "agentId", sess.agentID,
		"model", mc.Model, "bodyField", llm.BodyAgentIDField)
	return nil
}

// buildDynamicExtras 构建追加到稳定提示词尾部的动态上下文：
// 已启用的 Skills、MCP Server 以及项目自定义子 Agent 配置。
func (s *ChatService) buildDynamicExtras() string {
	cfg := s.cfg.Get()
	var sb strings.Builder

	var skills []config.SkillConfig
	for _, sk := range cfg.Skills {
		if sk.Enabled {
			skills = append(skills, sk)
		}
	}
	if len(skills) > 0 {
		sb.WriteString("\n\n# 已启用 Skills\n")
		for _, sk := range skills {
			fmt.Fprintf(&sb, "- %s：%s\n", sk.Name, sk.Description)
			if sk.Prompt != "" {
				fmt.Fprintf(&sb, "  指令：%s\n", sk.Prompt)
			}
		}
	}

	var servers []config.MCPServerConfig
	for _, m := range cfg.MCPServers {
		if m.Enabled {
			servers = append(servers, m)
		}
	}
	if len(servers) > 0 {
		sb.WriteString("\n\n# 已配置 MCP Server（客户端接入将在 M6 提供，当前仅作为上下文）\n")
		for _, m := range servers {
			fmt.Fprintf(&sb, "- %s：%s %s\n", m.Name, m.Command, strings.Join(m.Args, " "))
		}
	}

	if s.ws.IsSet() {
		if specs, err := agentspec.List(s.ws.Root()); err == nil && len(specs) > 0 {
			sb.WriteString("\n\n# 项目自定义子 Agent（.aidog/agents/*.json）\n")
			for _, sp := range specs {
				fmt.Fprintf(&sb, "- %s：%s（工具: %s；技能: %s；MCP: %s）\n",
					sp.Name, sp.Description,
					strings.Join(sp.Tools, ","), strings.Join(sp.Skills, ","), strings.Join(sp.MCPServers, ","))
			}
		}
	}
	return sb.String()
}

// runTurn 执行一轮对话：回放持久化历史，消费事件流并转发到前端，同时落盘消息。
// 任务状态机：正常结束 → completed；中断 → interrupted；其余错误 → failed（FR-5.2）。
func (s *ChatService) runTurn(ctx context.Context, sess *chatSession) {
	bus := s.bus
	sessionID := sess.id
	taskID := sess.currentTaskID

	finishTask := func(status, errMsg string) {
		if s.tasks != nil && taskID != "" {
			if err := s.tasks.Finish(sessionID, taskID, status, errMsg); err != nil {
				slog.Warn("任务状态落库失败", "task", taskID, "err", err)
			}
		}
		s.mu.Lock()
		sess.currentTaskID = ""
		s.mu.Unlock()
	}
	emitError := func(msg string) {
		bus.Emit(eventbus.Event{Type: eventbus.EvChatError, SessionID: sessionID,
			Data: map[string]string{"message": msg}})
		s.appendMessages(sess, StoredMessage{Role: "error", Content: msg})
	}

	s.mu.Lock()
	input := historyMessages(sess.messages)
	snapshot := marshalMessages(sess.messages)
	s.mu.Unlock()
	// 观测上下文：会话/任务标识 + 消息快照（callbacks handler 写 checkpoint 用）
	runCtx := withObsInfo(ctx, &obsInfo{
		SessionID: sessionID, TaskID: taskID, Snapshot: snapshot,
	})
	iter := sess.runner.Run(runCtx, input)
	var sb []byte
	var usage schema.TokenUsage

	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			if ctx.Err() != nil {
				emitError("已中断")
				finishTask(store.StatusInterrupted, "用户中断")
				return
			}
			slog.Error("Agent 执行错误", "session", sessionID, "err", ev.Err)
			emitError(ev.Err.Error())
			finishTask(store.StatusFailed, truncateTitle(ev.Err.Error()))
			return
		}
		mo := ev.Output.MessageOutput
		if mo == nil {
			continue
		}
		// 工具执行结果（含 explorer 子 Agent 的工具活动）：单独推送，不进入助手文本
		if mo.Role == schema.Tool {
			if !mo.IsStreaming && mo.Message != nil && mo.Message.Content != "" {
				toolName := mo.ToolName
				bus.Emit(eventbus.Event{Type: eventbus.EvToolResult, SessionID: sessionID,
					Data: eventbus.ToolResultData{Tool: toolName, Content: mo.Message.Content}})
				s.appendMessages(sess, StoredMessage{Role: "tool", Tool: toolName, Content: mo.Message.Content})
			}
			continue
		}
		if mo.IsStreaming {
			if mo.MessageStream == nil {
				continue
			}
			for {
				msg, err := mo.MessageStream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					slog.Error("读取流失败", "session", sessionID, "err", err)
					emitError(err.Error())
					finishTask(store.StatusFailed, truncateTitle(err.Error()))
					return
				}
				collectDelta(&sb, msg, &usage)
				if msg.Content != "" {
					bus.Emit(eventbus.Event{Type: eventbus.EvChatDelta, SessionID: sessionID,
						Data: eventbus.ChatDeltaData{Delta: msg.Content}})
				}
			}
		} else if mo.Message != nil {
			collectDelta(&sb, mo.Message, &usage)
			if mo.Message.Content != "" {
				bus.Emit(eventbus.Event{Type: eventbus.EvChatDelta, SessionID: sessionID,
					Data: eventbus.ChatDeltaData{Delta: mo.Message.Content}})
			}
		}
	}

	// 中断可能表现为正常结束（无错误）：按 ctx 状态定终态
	if ctx.Err() != nil {
		emitError("已中断")
		finishTask(store.StatusInterrupted, "用户中断")
		return
	}

	bus.Emit(eventbus.Event{Type: eventbus.EvChatDone, SessionID: sessionID,
		Data: eventbus.ChatDoneData{
			Content:          string(sb),
			PromptTokens:     int64(usage.PromptTokens),
			CompletionTokens: int64(usage.CompletionTokens),
			TotalTokens:      int64(usage.TotalTokens),
		}})
	if len(sb) > 0 {
		s.appendMessages(sess, StoredMessage{Role: "assistant", Content: string(sb)})
	}
	// Token 用量按调用粒度由全局 callbacks handler 记录（见 callback.go）
	finishTask(store.StatusCompleted, "")
}

// collectDelta 累积文本内容并记录 Token 用量。
func collectDelta(sb *[]byte, msg *schema.Message, usage *schema.TokenUsage) {
	if msg == nil {
		return
	}
	*sb = append(*sb, msg.Content...)
	if msg.ResponseMeta != nil && msg.ResponseMeta.Usage != nil {
		*usage = *msg.ResponseMeta.Usage
	}
}

// marshalMessages 会话消息快照 JSON（checkpoint 断点恢复用）。
func marshalMessages(msgs []StoredMessage) string {
	if len(msgs) == 0 {
		return "[]"
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// truncateTitle 取首条用户消息前 30 字符作为线程标题。
func truncateTitle(content string) string {
	content = strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
	runes := []rune(content)
	if len(runes) > 30 {
		return string(runes[:30]) + "…"
	}
	return content
}
