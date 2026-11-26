package task

import (
	"testing"

	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
)

// TestCanTransition 状态机合法/非法迁移表。
func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{store.StatusPending, store.StatusReady, true},
		{store.StatusReady, store.StatusAssigned, true},
		{store.StatusAssigned, store.StatusRunning, true},
		{store.StatusRunning, store.StatusBlocked, true},
		{store.StatusBlocked, store.StatusRunning, true},
		{store.StatusRunning, store.StatusReview, true},
		{store.StatusReview, store.StatusCompleted, true},
		{store.StatusReview, store.StatusRejected, true},
		{store.StatusRejected, store.StatusRunning, true},
		{store.StatusRunning, store.StatusCancelled, true},
		{store.StatusRunning, store.StatusFailed, true},
		{store.StatusBlocked, store.StatusCancelled, true},
		{store.StatusPending, store.StatusRunning, false},
		{store.StatusRunning, store.StatusCompleted, false},
		{store.StatusCompleted, store.StatusRunning, false},
		{store.StatusCancelled, store.StatusRunning, false},
		{store.StatusFailed, store.StatusReview, false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Fatalf("CanTransition(%s -> %s)=%v, want %v", c.from, c.to, got, c.want)
		}
	}
}

// TestTransitionRejectsIllegal 非法迁移返回错误且状态不变。
func TestTransitionRejectsIllegal(t *testing.T) {
	m, st, ch, cleanup := newTestManager(t)
	defer cleanup()

	id, err := m.Start("s1", "x")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = waitEvent(t, ch, eventbus.EvTaskState)

	if err := m.Transition("s1", id, store.StatusCompleted, ""); err == nil {
		t.Fatal("RUNNING -> COMPLETED 应被拒绝（需先经 REVIEW）")
	}
	m.w.Close()
	task, err := st.GetTask(id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Status != store.StatusRunning {
		t.Fatalf("非法迁移后状态应保持 running: %s", task.Status)
	}
}

// TestFinishCompletedPath 正常完成经 REVIEW → COMPLETED，事件序列完整。
func TestFinishCompletedPath(t *testing.T) {
	m, st, ch, cleanup := newTestManager(t)
	defer cleanup()

	id, _ := m.Start("s1", "x")
	_ = waitEvent(t, ch, eventbus.EvTaskState)

	if err := m.Finish("s1", id, store.StatusCompleted, ""); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	m.w.Close()
	task, _ := st.GetTask(id)
	if task.Status != store.StatusCompleted || task.FinishedAt == nil {
		t.Fatalf("终态不符: %+v", task)
	}
	evs, _ := st.TaskEvents(id, 100)
	var states []string
	for _, ev := range evs {
		if ev.Type == eventbus.EvTaskState {
			states = append(states, ev.Payload)
		}
	}
	if len(states) != 3 {
		t.Fatalf("应有 running/review/completed 三条状态事件: %+v", states)
	}
}

// TestBlockedRoundTrip 审批阻塞：RUNNING → BLOCKED → RUNNING → CANCELLED。
func TestBlockedRoundTrip(t *testing.T) {
	m, st, ch, cleanup := newTestManager(t)
	defer cleanup()

	id, _ := m.Start("s1", "x")
	_ = waitEvent(t, ch, eventbus.EvTaskState)

	if err := m.Transition("s1", id, store.StatusBlocked, ""); err != nil {
		t.Fatalf("进入 BLOCKED: %v", err)
	}
	if err := m.Transition("s1", id, store.StatusRunning, ""); err != nil {
		t.Fatalf("恢复 RUNNING: %v", err)
	}
	if err := m.Finish("s1", id, store.StatusCancelled, "用户中断"); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	m.w.Close()
	task, _ := st.GetTask(id)
	if task.Status != store.StatusCancelled || task.Error != "用户中断" {
		t.Fatalf("取消终态不符: %+v", task)
	}
}

// TestFinishIdempotentAndTerminalLocked 终态幂等，且不能再迁移。
func TestFinishIdempotentAndTerminalLocked(t *testing.T) {
	m, _, ch, cleanup := newTestManager(t)
	defer cleanup()

	id, _ := m.Start("s1", "x")
	_ = waitEvent(t, ch, eventbus.EvTaskState)

	if err := m.Finish("s1", id, store.StatusFailed, "boom"); err != nil {
		t.Fatalf("Finish(failed): %v", err)
	}
	if err := m.Finish("s1", id, store.StatusCompleted, ""); err != nil {
		t.Fatalf("终态重复 Finish 应幂等: %v", err)
	}
	if err := m.Transition("s1", id, store.StatusRunning, ""); err == nil {
		t.Fatal("终态不应允许迁移回 RUNNING")
	}
}
