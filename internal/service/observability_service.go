package service

// ObservabilityService Agent 协作观测台：
//   - OpenWindow 打开/聚焦独立的观测窗口（单例）；
//   - Snapshot 提供窗口打开时的初始快照。
//
// 实时数据走事件总线（agent:event 会广播到所有窗口），本服务不维护第二份状态。

import (
	"errors"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"ai-dog-agent/internal/agent"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
)

// observabilityWindowName 观测窗口单例名；Wails 在窗口关闭时会从窗口表移除。
const observabilityWindowName = "observability"

// ObservabilityService 观测台窗口与快照服务（Wails Service）。
type ObservabilityService struct {
	chats *agent.ChatService
	tasks *task.Manager
	// app 延迟取应用实例：main 在 application.New 之前构造本服务。
	// 用闭包而非导出方法注入，避免把 application.App 暴露成前端 RPC 参数。
	app func() *application.App
}

// NewObservabilityService 创建观测服务；tasks 可为 nil（无统计层时只返回会话与 Agent）。
func NewObservabilityService(chats *agent.ChatService, tasks *task.Manager, appProvider func() *application.App) *ObservabilityService {
	return &ObservabilityService{chats: chats, tasks: tasks, app: appProvider}
}

// OpenWindow 打开 Agent 协作观测窗口；已存在则显示并聚焦。
func (o *ObservabilityService) OpenWindow() error {
	var app *application.App
	if o.app != nil {
		app = o.app()
	}
	if app == nil {
		return errors.New("应用尚未就绪")
	}
	if w, ok := app.Window.GetByName(observabilityWindowName); ok {
		w.Show()
		w.Focus()
		return nil
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             observabilityWindowName,
		Title:            "Agent 协作观测台",
		Width:            1100,
		Height:           760,
		MinWidth:         880,
		MinHeight:        560,
		Frameless:        true,
		BackgroundColour: application.NewRGB(13, 17, 23),
		URL:              "/?view=observability",
	})
	return nil
}

// ObservationSnapshot 观测窗口的初始快照（前端据此建立 Agent 节点与任务起点）。
type ObservationSnapshot struct {
	Sessions     []agent.SessionObservation `json:"sessions"`
	Agents       []agent.AgentObservation   `json:"agents"`
	Tasks        []store.Task               `json:"tasks"`
	RunningTasks map[string]string          `json:"runningTasks"` // sessionID -> taskID
	ServerTime   int64                      `json:"serverTime"`   // Unix 毫秒
}

// Snapshot 汇总当前会话、子 Agent 与最近任务。
func (o *ObservabilityService) Snapshot() (ObservationSnapshot, error) {
	sessions := o.chats.ObserveSessions()
	agents := o.chats.ObserveAgents()
	snap := ObservationSnapshot{
		Sessions:     sessions,
		Agents:       agents,
		Tasks:        []store.Task{},
		RunningTasks: buildRunningTasks(sessions),
		ServerTime:   time.Now().UnixMilli(),
	}
	if snap.Agents == nil {
		snap.Agents = []agent.AgentObservation{}
	}
	if o.tasks != nil {
		tasks, err := o.tasks.ListTasks(100)
		if err != nil {
			return ObservationSnapshot{}, err
		}
		if tasks != nil {
			snap.Tasks = tasks
		}
	}
	return snap, nil
}

// buildRunningTasks 提取会话 → 运行中任务 ID；空任务 ID 不进入映射。
func buildRunningTasks(sessions []agent.SessionObservation) map[string]string {
	out := make(map[string]string, len(sessions))
	for _, s := range sessions {
		if s.CurrentTaskID != "" {
			out[s.ID] = s.CurrentTaskID
		}
	}
	return out
}
