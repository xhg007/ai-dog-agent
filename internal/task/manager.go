// Package task 任务生命周期管理（M5 FR-5.1/5.2）：
// 一次用户消息触发的执行轮次 = 一个任务（running → completed/failed/interrupted）。
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

// Start 创建运行中任务：落库 + 推送 task_state + 事件落表。返回任务 ID。
func (m *Manager) Start(sessionID, title string) (string, error) {
	id := uuid.NewString()
	t := store.Task{ID: id, SessionID: sessionID, Title: title, Status: store.StatusRunning}
	if err := m.w.Store().CreateTask(t); err != nil {
		return "", fmt.Errorf("创建任务失败: %w", err)
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

// Finish 结束任务：更新状态 + 推送 task_state + 事件落表，并清除会话当前任务。
func (m *Manager) Finish(sessionID, taskID, status, errMsg string) error {
	if err := m.w.Store().UpdateTaskStatus(taskID, status, errMsg); err != nil {
		return fmt.Errorf("更新任务状态失败: %w", err)
	}
	m.mu.Lock()
	if m.current[sessionID] == taskID {
		delete(m.current, sessionID)
	}
	m.mu.Unlock()
	m.pushState(sessionID, taskID, "", status, errMsg)
	m.appendEvent(taskID, eventbus.EvTaskState, eventbus.TaskStateData{
		TaskID: taskID, Status: status, Error: errMsg,
	})
	return nil
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

// ListTasks / TaskEvents / DailyUsage 读透传（TaskService RPC 用）。
func (m *Manager) ListTasks(limit int) ([]store.Task, error)    { return m.store.ListTasks(limit) }
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
