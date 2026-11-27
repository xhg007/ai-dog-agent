package service

import (
	"testing"

	"ai-dog-agent/internal/agent"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/workspace"
)

// Snapshot 在无统计层（tasks=nil）时也要返回前端可直接使用的空数组与时间戳。
func TestObservabilitySnapshotWithoutTasks(t *testing.T) {
	cfg, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	chats := agent.NewChatService(cfg, workspace.New(), eventbus.New(), nil, "", "", nil, nil)
	svc := NewObservabilityService(chats, nil, nil)

	snap, err := svc.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Sessions == nil || snap.Agents == nil || snap.Tasks == nil {
		t.Fatalf("快照数组不应为 nil（前端按数组遍历）: %+v", snap)
	}
	if snap.RunningTasks == nil {
		t.Fatal("RunningTasks 不应为 nil")
	}
	if snap.ServerTime == 0 {
		t.Fatal("ServerTime 未填充")
	}
}

// buildRunningTasks 只收集有当前任务的会话。
func TestBuildRunningTasks(t *testing.T) {
	got := buildRunningTasks([]agent.SessionObservation{
		{ID: "s1", CurrentTaskID: "t1"},
		{ID: "s2"},
		{ID: "s3", CurrentTaskID: "t3"},
	})
	if len(got) != 2 || got["s1"] != "t1" || got["s3"] != "t3" {
		t.Fatalf("buildRunningTasks=%v", got)
	}
	if _, ok := got["s2"]; ok {
		t.Fatal("空 taskId 不应进入映射")
	}
}
