// Package approval 审批管理器：写操作等高风险动作需用户批准。
// 工具调用方 Request 阻塞等待；前端通过 Wails Service Decide 决策。
// 支持按会话记住同路径的批准（同一路径本会话内不再询问）。
package approval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"

	"ai-dog-agent/internal/eventbus"
)

// ErrDenied 用户拒绝了审批请求。
var ErrDenied = errors.New("用户拒绝了该操作")

// pendingReq 一次待决策的审批。
type pendingReq struct {
	req    eventbus.ApprovalRequestData
	result chan decision
}

type decision struct {
	approved bool
	remember bool
}

// StateHook 审批生命周期回调（任务状态机在等待审批时进入 BLOCKED，决策后恢复 RUNNING）。
type StateHook func(sessionID string)

// Manager 审批管理器。
type Manager struct {
	bus        *eventbus.Bus
	mu         sync.Mutex
	pending    map[string]*pendingReq
	remembered map[string]map[string]bool // sessionID -> "tool:path" 集合
	onWait     StateHook
	onResume   StateHook
}

// New 创建审批管理器。
func New(bus *eventbus.Bus) *Manager {
	return &Manager{
		bus:        bus,
		pending:    make(map[string]*pendingReq),
		remembered: make(map[string]map[string]bool),
	}
}

// SetStateHooks 注册审批等待/恢复回调（应用启动时调用一次）。
// 回调在锁外执行，可安全调用 task.Manager.Transition。
func (m *Manager) SetStateHooks(onWait, onResume StateHook) {
	m.mu.Lock()
	m.onWait = onWait
	m.onResume = onResume
	m.mu.Unlock()
}

// stateHooks 返回当前钩子快照（调用方不得持锁）。
func (m *Manager) stateHooks() (wait, resume StateHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onWait, m.onResume
}

// Request 请求审批：推送事件给前端并阻塞等待决策。
// 返回 true 表示批准；拒绝返回 ErrDenied。
// sessionID+tool+path 已被记住时直接放行。
func (m *Manager) Request(sessionID, tool, path, preview string) (bool, error) {
	return m.RequestContext(context.Background(), sessionID, tool, path, preview)
}

// RequestContext 同 Request，但等待期间可由 ctx 取消（用户中断/会话删除）。
// 取消时撤下待审批项并推送 approval_decided，调用方不会永久阻塞。
func (m *Manager) RequestContext(ctx context.Context, sessionID, tool, path, preview string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key := tool + ":" + path
	m.mu.Lock()
	if m.remembered[sessionID][key] {
		m.mu.Unlock()
		slog.Debug("审批命中记住策略，直接放行", "session", sessionID, "tool", tool, "path", path)
		return true, nil
	}
	id := uuid.NewString()
	pr := &pendingReq{result: make(chan decision, 1)}
	pr.req = eventbus.ApprovalRequestData{ID: id, SessionID: sessionID, Tool: tool, Path: path, Preview: preview}
	m.pending[id] = pr
	m.mu.Unlock()

	m.bus.Emit(eventbus.Event{
		Type: eventbus.EvApprovalRequest, SessionID: sessionID, Data: pr.req,
	})
	if wait, _ := m.stateHooks(); wait != nil {
		wait(sessionID)
	}

	var dec decision
	select {
	case dec = <-pr.result:
	case <-ctx.Done():
		m.mu.Lock()
		delete(m.pending, id)
		m.mu.Unlock()
		m.bus.Emit(eventbus.Event{
			Type: eventbus.EvApprovalDecided, SessionID: sessionID,
			Data: eventbus.ApprovalDecidedData{ID: id, Approved: false},
		})
		if _, resume := m.stateHooks(); resume != nil {
			resume(sessionID)
		}
		return false, ctx.Err()
	}

	m.mu.Lock()
	delete(m.pending, id)
	if dec.approved && dec.remember {
		if m.remembered[sessionID] == nil {
			m.remembered[sessionID] = make(map[string]bool)
		}
		m.remembered[sessionID][key] = true
	}
	m.mu.Unlock()

	m.bus.Emit(eventbus.Event{
		Type: eventbus.EvApprovalDecided, SessionID: sessionID,
		Data: eventbus.ApprovalDecidedData{ID: id, Approved: dec.approved},
	})
	if _, resume := m.stateHooks(); resume != nil {
		resume(sessionID)
	}

	if !dec.approved {
		return false, fmt.Errorf("%w: %s %s", ErrDenied, tool, path)
	}
	return true, nil
}

// CancelSession 撤销某会话的全部待审批请求（会话被删除/中断时调用）。
// 等待方按「未批准」返回 ErrDenied，pending 由等待方自行清理。
func (m *Manager) CancelSession(sessionID string) {
	m.mu.Lock()
	targets := make([]*pendingReq, 0, len(m.pending))
	for _, pr := range m.pending {
		if pr.req.SessionID == sessionID {
			targets = append(targets, pr)
		}
	}
	m.mu.Unlock()

	for _, pr := range targets {
		select {
		case pr.result <- decision{approved: false}:
		default: // 已有决策在队列中，无需重复
		}
	}
}

// ForgetSession 清理会话的「批准并记住」记录（会话删除时调用）。
// remembered 以 sessionID 为键，不清理会随删除的会话长期累积。
func (m *Manager) ForgetSession(sessionID string) {
	m.mu.Lock()
	delete(m.remembered, sessionID)
	m.mu.Unlock()
}

// Decide 用户决策（Wails Service 调用）。
func (m *Manager) Decide(id string, approved, remember bool) error {
	m.mu.Lock()
	pr, ok := m.pending[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("审批请求不存在或已处理: %s", id)
	}
	pr.result <- decision{approved: approved, remember: remember}
	return nil
}

// ListPending 返回全部待审批请求。
func (m *Manager) ListPending() []eventbus.ApprovalRequestData {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := make([]eventbus.ApprovalRequestData, 0, len(m.pending))
	for _, pr := range m.pending {
		list = append(list, pr.req)
	}
	return list
}

// pendingIDs 测试辅助：当前待审批 ID 列表。
func (m *Manager) pendingIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.pending))
	for id := range m.pending {
		ids = append(ids, id)
	}
	return ids
}
