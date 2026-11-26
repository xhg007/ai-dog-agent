// Package task 任务生命周期管理（M5 FR-5.1/5.2）：
// 一次用户消息触发的执行轮次 = 一个任务，按状态机流转
// （pending → ready → assigned → running → review → completed，异常走 failed/cancelled/blocked）。
// Manager 组合「store 异步落库 + eventbus 推送」，对外提供统一入口，
// 使 ChatService / callbacks / fallback 都不必各自处理持久化细节。
package task

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
)

// Manager 任务管理器。写操作经 AsyncWriter 异步落库；读操作直连 store。
type Manager struct {
	store store.Store
	w     *store.AsyncWriter
	bus   *eventbus.Bus

	mu      sync.Mutex
	current map[string]string // sessionID -> 运行中 taskID
	seq     map[string]int64  // taskID -> checkpoint 序号计数器
}

// New 创建管理器。store 仅用于读与透传，写入一律走 w。
func New(s store.Store, w *store.AsyncWriter, bus *eventbus.Bus) *Manager {
	return &Manager{
		store:   s,
		w:       w,
		bus:     bus,
		current: make(map[string]string),
		seq:     make(map[string]int64),
	}
}

// transitions 合法状态迁移表（设计文档 §3.11）。
var transitions = map[string]map[string]bool{
	store.StatusPending:   {store.StatusReady: true, store.StatusCancelled: true},
	store.StatusReady:     {store.StatusAssigned: true, store.StatusCancelled: true},
	store.StatusAssigned:  {store.StatusRunning: true, store.StatusCancelled: true},
	store.StatusRunning:   {store.StatusBlocked: true, store.StatusReview: true, store.StatusCancelled: true, store.StatusFailed: true},
	store.StatusBlocked:   {store.StatusRunning: true, store.StatusCancelled: true, store.StatusFailed: true},
	store.StatusReview:    {store.StatusCompleted: true, store.StatusRejected: true, store.StatusRunning: true, store.StatusCancelled: true, store.StatusFailed: true},
	store.StatusRejected:  {store.StatusRunning: true, store.StatusFailed: true, store.StatusCancelled: true},
	store.StatusCompleted: {},
	store.StatusCancelled: {},
	store.StatusFailed:    {},
}

// CanTransition 判断状态迁移是否合法（旧状态先归一）。
func CanTransition(from, to string) bool {
	return transitions[store.NormalizeStatus(from)][to]
}

// Start 创建任务并完成内联调度：PENDING → READY → ASSIGNED → RUNNING。
// 当前没有独立调度器，中间状态只做迁移合法性校验，持久化与事件只记录最终 RUNNING。
func (m *Manager) Start(sessionID, title string) (string, error) {
	id := uuid.NewString()
	t := store.Task{ID: id, SessionID: sessionID, Title: title, Status: store.StatusPending}
	if err := m.w.Store().CreateTask(t); err != nil {
		return "", fmt.Errorf("创建任务失败: %w", err)
	}
	status := store.StatusPending
	for _, next := range []string{store.StatusReady, store.StatusAssigned, store.StatusRunning} {
		if !CanTransition(status, next) {
			return "", fmt.Errorf("任务调度状态迁移非法: %s -> %s", status, next)
		}
		status = next
	}
	if err := m.w.Store().UpdateTaskStatus(id, store.StatusRunning, ""); err != nil {
		return "", fmt.Errorf("更新任务状态失败: %w", err)
	}
	m.mu.Lock()
	m.current[sessionID] = id
	m.mu.Unlock()

	m.pushState(sessionID, id, title, store.StatusRunning, "")
	m.appendEvent(id, eventbus.EvTaskState, eventbus.TaskStateData{
		TaskID: id, Status: store.StatusRunning, Title: title,
	})
	return id, nil
}

// Finish 结束任务：正常完成经 REVIEW → COMPLETED；失败/取消从 RUNNING/BLOCKED 直接进入终态。
// 已是终态时幂等返回（重复结束不报错）。
func (m *Manager) Finish(sessionID, taskID, status, errMsg string) error {
	target := store.NormalizeStatus(status)
	cur, err := m.store.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("查询任务状态失败: %w", err)
	}
	from := store.NormalizeStatus(cur.Status)
	if store.IsTerminal(from) {
		m.clearCurrent(sessionID, taskID)
		return nil
	}
	// 正常完成先进入 REVIEW（待校验），再由调用方/校验逻辑推进 COMPLETED。
	if target == store.StatusCompleted && from == store.StatusRunning {
		if err := m.Transition(sessionID, taskID, store.StatusReview, ""); err != nil {
			return err
		}
		from = store.StatusReview
	}
	if !CanTransition(from, target) {
		return fmt.Errorf("非法任务状态迁移: %s -> %s", from, target)
	}
	if err := m.updateStatus(sessionID, taskID, target, errMsg); err != nil {
		return err
	}
	m.clearCurrent(sessionID, taskID)
	return nil
}

// Transition 按状态机迁移任务（审批阻塞/恢复等使用）。
func (m *Manager) Transition(sessionID, taskID, to, errMsg string) error {
	target := store.NormalizeStatus(to)
	cur, err := m.store.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("查询任务状态失败: %w", err)
	}
	from := store.NormalizeStatus(cur.Status)
	if from == target {
		return nil
	}
	if !CanTransition(from, target) {
		return fmt.Errorf("非法任务状态迁移: %s -> %s", from, target)
	}
	if err := m.updateStatus(sessionID, taskID, target, errMsg); err != nil {
		return err
	}
	if store.IsTerminal(target) {
		m.clearCurrent(sessionID, taskID)
	}
	return nil
}

// updateStatus 落库 + 推送 + 事件落表。
func (m *Manager) updateStatus(sessionID, taskID, status, errMsg string) error {
	if err := m.w.Store().UpdateTaskStatus(taskID, status, errMsg); err != nil {
		return fmt.Errorf("更新任务状态失败: %w", err)
	}
	m.pushState(sessionID, taskID, "", status, errMsg)
	m.appendEvent(taskID, eventbus.EvTaskState, eventbus.TaskStateData{
		TaskID: taskID, Status: status, Error: errMsg,
	})
	return nil
}

// clearCurrent 清理会话当前任务与检查点计数器。
func (m *Manager) clearCurrent(sessionID, taskID string) {
	m.mu.Lock()
	if m.current[sessionID] == taskID {
		delete(m.current, sessionID)
	}
	delete(m.seq, taskID)
	m.mu.Unlock()
}

// RunningTaskID 返回会话当前运行中的任务 ID（无则空串）。
func (m *Manager) RunningTaskID(sessionID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current[sessionID]
}

// Emit 落库并推送一条任务关联事件（payload 自动 JSON 序列化）。
func (m *Manager) Emit(sessionID, taskID, evType string, data any) {
	var payload []byte
	if data != nil {
		payload, _ = json.Marshal(data)
	}
	m.appendEvent(taskID, evType, string(payload))
	m.bus.Emit(eventbus.Event{Type: evType, SessionID: sessionID, Data: data})
}

// AppendTokenUsage 异步记录一次 LLM 用量（FR-5.4）。
func (m *Manager) AppendTokenUsage(u store.TokenUsage) {
	m.submit(func(s store.Store) error { return s.AppendTokenUsage(u) })
}

// AppendCheckpoint 异步记录工具检查点（FR-5.3）。
func (m *Manager) AppendCheckpoint(c store.Checkpoint) {
	m.submit(func(s store.Store) error { return s.AppendCheckpoint(c) })
}

// NextCheckpointSeq 返回任务内下一个检查点序号（原子递增）。
func (m *Manager) NextCheckpointSeq(taskID string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seq[taskID]++
	return m.seq[taskID]
}

// LastAfterCheckpoint 读任务最后一个 after 检查点（断点恢复用）。
func (m *Manager) LastAfterCheckpoint(taskID string) (*store.Checkpoint, error) {
	return m.store.LastAfterCheckpoint(taskID)
}

// GetTask 单任务查询。
func (m *Manager) GetTask(id string) (*store.Task, error) { return m.store.GetTask(id) }

// CheckpointBySeq 指定序号检查点查询。
func (m *Manager) CheckpointBySeq(taskID string, seq int64) (*store.Checkpoint, error) {
	return m.store.CheckpointBySeq(taskID, seq)
}

// ListTasks / TaskEvents / DailyUsage 读透传（TaskService RPC 用）。
func (m *Manager) ListTasks(limit int) ([]store.Task, error) { return m.store.ListTasks(limit) }
func (m *Manager) TaskEvents(id string, n int) ([]store.TaskEvent, error) {
	return m.store.TaskEvents(id, n)
}
func (m *Manager) DailyUsage(days int) ([]store.DailyUsageRow, error) {
	return m.store.DailyUsage(days)
}

// appendEvent 异步落事件（不推送前端——推送由 Emit 或调用方显式处理）。
func (m *Manager) appendEvent(taskID, evType string, data any) {
	payload := ""
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			payload = string(b)
		}
	}
	p := payload
	m.submit(func(s store.Store) error { return s.AppendTaskEvent(taskID, evType, p) })
}

// pushState 推送任务状态到前端（不含落库，落库由 appendEvent 负责）。
func (m *Manager) pushState(sessionID, taskID, title, status, errMsg string) {
	m.bus.Emit(eventbus.Event{Type: eventbus.EvTaskState, SessionID: sessionID,
		Data: eventbus.TaskStateData{TaskID: taskID, Status: status, Title: title, Error: errMsg}})
}

// submit 提交异步写；w 为 nil（测试环境无持久层）时跳过。
func (m *Manager) submit(op func(store.Store) error) {
	if m.w == nil {
		return
	}
	m.w.Submit(op)
}
