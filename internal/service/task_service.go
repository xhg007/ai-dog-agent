package service

import (
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
)

// TaskService 任务查询与统计 RPC（M5 FR-5.2/5.4，前端任务面板与用量 Tab）。
// 返回具体类型而非 any，让 Wails 生成的 TS binding 强类型可用。
type TaskService struct {
	tasks *task.Manager
}

// NewTaskService 创建任务服务。
func NewTaskService(m *task.Manager) *TaskService { return &TaskService{tasks: m} }

// ListTasks 最近任务列表（created_at 降序）。
func (t *TaskService) ListTasks(limit int) ([]store.Task, error) {
	if limit <= 0 {
		limit = 50
	}
	return t.tasks.ListTasks(limit)
}

// GetTaskEvents 任务事件流（升序）。
func (t *TaskService) GetTaskEvents(taskID string, limit int) ([]store.TaskEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	return t.tasks.TaskEvents(taskID, limit)
}

// GetDailyUsage 近 days 天按日聚合的 Token 用量与成本报表。
func (t *TaskService) GetDailyUsage(days int) ([]store.DailyUsageRow, error) {
	if days <= 0 {
		days = 30
	}
	return t.tasks.DailyUsage(days)
}
