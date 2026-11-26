// Package eventbus 将 Agent 执行事件推送到前端（Wails Event），后续同时落库。
package eventbus

import (
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// 事件类型常量（前端按 type 分发渲染）。
const (
	EvChatStarted = "chat_started"
	EvChatDelta   = "chat_delta"
	EvChatDone    = "chat_done"
	EvChatError   = "chat_error"
	EvToolResult  = "tool_result"

	EvApprovalRequest = "approval_request" // 审批请求（前端弹审批卡片）
	EvApprovalDecided = "approval_decided" // 审批已决策（前端移除卡片）

	// M5 生产化事件（FR-5.1），同时落库 task_events 表
	EvTaskState     = "task_state"     // 任务状态流转（运行/完成/失败/中断）
	EvToolCall      = "tool_call"      // 工具调用开始（含参数摘要）
	EvLLMUsage      = "llm_usage"      // LLM 调用用量与成本
	EvModelFallback = "model_fallback" // 模型故障切换
	EvAgentMessage  = "agent_message"  // Agent 私聊消息
)

// TaskStateData 任务状态载荷。
type TaskStateData struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"` // running/completed/failed/interrupted
	Title  string `json:"title,omitempty"`
	Error  string `json:"error,omitempty"`
}

// EventName Wails 事件名。
const EventName = "agent:event"

// Event 推送到前端的统一事件结构。
type Event struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Data      any    `json:"data"`
}

// ChatStartedData 开始载荷：Agent 唯一 ID 与所用模型。
type ChatStartedData struct {
	AgentID string `json:"agentId"`
	Model   string `json:"model"`
}

// ChatDeltaData 流式增量。
type ChatDeltaData struct {
	Delta string `json:"delta"`
}

// ToolResultData 工具执行结果载荷。
type ToolResultData struct {
	Tool    string `json:"tool"`
	Content string `json:"content"`
}

// ChatDoneData 完成载荷：完整回复 + Token 用量。
type ChatDoneData struct {
	Content          string `json:"content"`
	PromptTokens     int64  `json:"promptTokens"`
	CompletionTokens int64  `json:"completionTokens"`
	TotalTokens      int64  `json:"totalTokens"`
}

// ToolCallData 工具调用开始载荷（TaskID 关联任务；Args 为 JSON 摘要）。
type ToolCallData struct {
	TaskID string `json:"taskId,omitempty"`
	Tool   string `json:"tool"`
	Args   string `json:"args,omitempty"`
}

// LLMUsageData LLM 调用用量与成本载荷（FR-5.4）。
// Model 为显示名，ModelID 为实际调用模型配置 ID（Agent 独立模型时用于对账）。
type LLMUsageData struct {
	TaskID       string  `json:"taskId,omitempty"`
	AgentID      string  `json:"agentId,omitempty"`
	Model        string  `json:"model"`
	ModelID      string  `json:"modelId,omitempty"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostIn       float64 `json:"costIn"`  // 元
	CostOut      float64 `json:"costOut"` // 元
}

// ModelFallbackData 模型故障切换载荷（FR-5.6）。
type ModelFallbackData struct {
	AgentID string `json:"agentId,omitempty"`
	From    string `json:"from"`
	To      string `json:"to"`
	Reason  string `json:"reason"`
}

// AgentMessageData Agent 私聊消息载荷（事件流展示来源/目标与结果）。
type AgentMessageData struct {
	From     string `json:"from"`
	FromName string `json:"fromName,omitempty"`
	To       string `json:"to"`
	ToName   string `json:"toName,omitempty"`
	Content  string `json:"content"` // 请求内容
	Reply    string `json:"reply,omitempty"`
	Depth    int    `json:"depth"`
	Error    string `json:"error,omitempty"`
}

// ApprovalRequestData 审批请求载荷。
type ApprovalRequestData struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Tool      string `json:"tool"`
	Path      string `json:"path"`
	Preview   string `json:"preview"`
}

// ApprovalDecidedData 审批决策结果载荷。
type ApprovalDecidedData struct {
	ID       string `json:"id"`
	Approved bool   `json:"approved"`
}

// Bus 事件总线：推送 Wails Event，同时支持进程内订阅（供测试与内部模块）。
type Bus struct {
	app  *application.App
	mu   sync.Mutex
	subs map[int]chan Event
	next int
}

// New 创建总线；app 在 application.New 之后通过 SetApp 注入。
func New() *Bus { return &Bus{subs: make(map[int]chan Event)} }

// SetApp 注入 Wails 应用实例。
func (b *Bus) SetApp(app *application.App) { b.app = app }

// Subscribe 订阅进程内事件，返回只读通道与取消函数。
func (b *Bus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	ch := make(chan Event, 64)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(ch)
		}
	}
}

// notify 分发给进程内订阅者（缓冲满则丢弃，不阻塞业务）。
func (b *Bus) notify(ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Emit 推送事件到前端与进程内订阅者。
func (b *Bus) Emit(ev Event) {
	b.notify(ev)
	if b.app == nil {
		slog.Debug("Wails 应用未就绪，仅进程内分发", "type", ev.Type)
		return
	}
	b.app.Event.Emit(EventName, ev)
}
