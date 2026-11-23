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
)

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
