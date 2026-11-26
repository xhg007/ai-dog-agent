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
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/llm"
	"ai-dog-agent/internal/mcp"
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
	store      *sessionStore // 会话持久化（一个会话一个 JSON 文件）
	tasks      *task.Manager // 任务生命周期（M5；nil 时跳过任务记录，仅事件推送）
	mcpMgr     *mcp.Manager  // 外部 MCP Server 连接（nil 时不接入 MCP 工具）

	mu       sync.Mutex
	sessions map[string]*chatSession
}

type chatSession struct {
	id            string
	agentID       string // Agent 唯一 ID，随请求体 agent_id 字段上送
	agent         adk.Agent
	runner        *adk.Runner
	cancel        context.CancelFunc
	running       bool
	currentTaskID string             // 当前轮次的任务 ID（M5）
	title         string             // 线程标题（取首条用户消息摘要）
	modelName     string             // 构建 Agent 时使用的主模型显示名（chat_started 事件上报）
	modelCfg      config.ModelConfig // 实际主模型配置（用量按真实模型归因）
	createdAt     time.Time
	lastActive    time.Time
	messages      []StoredMessage // 持久化消息历史
	configVersion uint64          // 构建 Agent 时的配置版本（变更后下一轮重建）
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
	appr *approval.Manager, backupRoot, sessionDir string, tasks *task.Manager,
	mcpMgr *mcp.Manager) *ChatService {
	s := &ChatService{
		cfg:        cfg,
		ws:         ws,
		bus:        bus,
		appr:       appr,
		backupRoot: backupRoot,
		tasks:      tasks,
		mcpMgr:     mcpMgr,
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
		if err := s.restoreSessions(); err != nil {
			slog.Error("恢复历史会话失败", "err", err)
		}
	}
	return s
}

// NewSession 创建新会话，返回会话 ID。Agent 实例在首次发消息时惰性构建。
func (s *ChatService) NewSession() string {
	id := uuid.NewString()
	agentID := uuid.NewString() // 占位；首次构建 Agent 时固化为随项目的稳定 ID（identity.go）
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
	// 撤下该会话挂起的审批，避免工具 goroutine 永久阻塞、卡片残留在前端
	if s.appr != nil {
		s.appr.CancelSession(sessionID)
		s.appr.ForgetSession(sessionID)
	}
	delete(s.sessions, sessionID)
	if s.store != nil {
		if err := s.store.delete(sessionID); err != nil {
			slog.Warn("删除会话文件失败", "session", sessionID, "err", err)
		}
	}
	return true
}

// claimSession 原子地「检查空闲并占用」会话。
// 检查与占用必须在同一临界区内完成：否则并发两次 SendMessage 会同时通过
// running 检查，同一会话并行执行两轮、历史被写两遍。
func (s *ChatService) claimSession(sessionID string) (*chatSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("会话不存在: %s", sessionID)
	}
	if sess.running {
		return nil, errors.New("当前会话正在执行中，请等待完成或先中断")
	}
	sess.running = true
	return sess, nil
}

// releaseSession 复位占用状态（SendMessage 前置步骤失败时调用）。
func (s *ChatService) releaseSession(sess *chatSession) {
	s.mu.Lock()
	sess.running = false
	s.mu.Unlock()
}

// configChanged 报告会话 Agent 构建后配置是否已变更（调用方持有 s.mu）。
func (s *ChatService) configChanged(sess *chatSession) bool {
	return sess.configVersion != s.cfg.Version()
}

// SendMessage 向指定会话发送用户消息，异步执行，过程与结果通过事件流推送。
func (s *ChatService) SendMessage(sessionID string, content string) error {
	sess, err := s.claimSession(sessionID)
	if err != nil {
		return err
	}

	// 模型按当前激活配置构建（在 initAgentLocked 中完成）；修改模型配置后新建会话生效。
	if _, err := s.cfg.ActiveModel(); err != nil {
		s.releaseSession(sess)
		return err
	}

	s.mu.Lock()
	// 配置（模型 / Skills / MCP）变更后旧 Agent 已过期：下一轮沿用会话历史重建，
	// 使设置页的保存与模型切换对当前会话立即生效。
	if sess.agent != nil && s.configChanged(sess) {
		sess.agent = nil
		sess.runner = nil
	}
	if sess.agent == nil {
		if err := s.initAgentLocked(sess); err != nil {
			sess.running = false
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
		Data: eventbus.ChatStartedData{AgentID: sess.agentID, Model: sess.modelName}})

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
	// 主 Agent ID 随项目固化：同一项目恒定（不再随会话轮换），
	// 供应商侧相同 agent_id 才能复用缓存
	sess.agentID = s.agentIDForProject()
	s.persistLocked(sess)
	// 带故障切换的模型链（FR-5.6）：激活模型优先，其余按配置顺序备用；
	// 瞬态错误（网络/429/5xx/超时）自动切换并上报 model_fallback 事件
	sessionID := sess.id
	cm, modelName, modelCfg, err := llm.BuildModelChain(ctx, s.cfg, sess.agentID, sess.agentID,
		func(from, to, reason string) {
			slog.Warn("模型故障切换", "session", sessionID, "from", from, "to", to, "reason", reason)
			s.bus.Emit(eventbus.Event{Type: eventbus.EvModelFallback, SessionID: sessionID,
				Data: eventbus.ModelFallbackData{AgentID: sess.agentID, From: from, To: to, Reason: reason}})
		})
	if err != nil {
		return err
	}

	prompt := s.buildPromptData()
	// 外部 MCP 工具：启用的 server 懒连接并包装；单个 server 失败只跳过（不阻塞会话）
	var mcpTools []tool.BaseTool
	if s.mcpMgr != nil {
		mcpTools = s.mcpMgr.Tools(ctx, s.cfg.Get().MCPServers, s.ws.Root(), sess.id)
	}
	var ag adk.Agent
	if s.ws.IsSet() {
		// workspace 已设置：主 Agent 携带 explorer 分派工具 + 写操作审批工具 + 执行工具 + MCP 工具
		ag, err = BuildOrchestrator(ctx, s.ws, cm, s.appr, s.backupRoot, sess.id,
			s.cfg.ShellWhitelist(), prompt, mcpTools)
	} else {
		// 未设置 workspace：纯对话模式（仍可携带 MCP 工具）
		ag, err = newPlainOrchestrator(ctx, cm, prompt, mcpTools)
	}
	if err != nil {
		return fmt.Errorf("构建 Agent 失败: %w", err)
	}
	sess.agent = ag
	sess.runner = adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           ag,
		EnableStreaming: true,
	})
	// 记录主模型显示名与实际模型配置：chat_started 事件如实上报，用量按真实模型归因
	sess.modelName = modelName
	sess.modelCfg = modelCfg
	// 记录构建时的配置版本：Settings 保存后 configChanged 为 true，下一轮重建
	sess.configVersion = s.cfg.Version()
	slog.Info("会话 Agent 已构建", "session", sess.id, "agentId", sess.agentID,
		"model", modelName, "bodyField", llm.BodyAgentIDField)
	return nil
}

// buildPromptData 构建追加到稳定提示词尾部的动态数据：
// 已启用的 Skills、MCP Server 以及项目自定义子 Agent 配置（文本由模板渲染）。
func (s *ChatService) buildPromptData() PromptData {
	cfg := s.cfg.Get()
	var data PromptData
	for _, sk := range cfg.Skills {
		if sk.Enabled {
			data.Skills = append(data.Skills, sk)
		}
	}
	for _, m := range cfg.MCPServers {
		if m.Enabled {
			data.MCPServers = append(data.MCPServers, m)
		}
	}
	if s.ws.IsSet() {
		if specs, err := agentspec.List(s.ws.Root()); err == nil {
			data.Agents = specs
		}
	}
	return data
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
	agentID := sess.agentID
	modelCfg := sess.modelCfg
	s.mu.Unlock()
	// 观测上下文：会话/任务标识 + 消息快照 + 实际模型（用量归因）
	runCtx := withObsInfo(ctx, &obsInfo{
		SessionID: sessionID, TaskID: taskID, Snapshot: snapshot,
		AgentID: agentID, ModelID: modelCfg.ID, ModelName: modelCfg.Name,
		PriceIn: modelCfg.PriceIn, PriceOut: modelCfg.PriceOut,
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
