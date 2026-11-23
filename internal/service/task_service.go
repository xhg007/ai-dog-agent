package service

import (
	"ai-dog-agent/internal/task"
)

// TaskService 任务查询与统计 RPC（M5 FR-5.2/5.4，前端任务面板与用量 Tab）。
type TaskService struct {
	tasks *task.Manager
}

// NewTaskService 创建任务服务。
func NewTaskService(m *task.Manager) *TaskService { return &TaskService{tasks: m} }

// ListTasks 最近任务列表（created_at 降序）。
func (t *TaskService) ListTasks(limit int) (any, error) {
	if limit <= 0 {
		limit = 50
	}
	return t.tasks.ListTasks(limit)
}

// GetTaskEvents 任务事件流（升序）。
func (t *TaskService) GetTaskEvents(taskID string, limit int) (any, error) {
	if limit <= 0 {
		limit = 500
	}
	return t.tasks.TaskEvents(taskID, limit)
}

// GetDailyUsage 近 days 天按日聚合的 Token 用量与成本报表。
func (t *TaskService) GetDailyUsage(days int) (any, error) {
	if days <= 0 {
		days = 30
	}
	return t.tasks.DailyUsage(days)
}
