package approval

import (
	"context"
	"testing"
	"time"
)

// TestStateHooksOnWaitAndResume 审批等待触发 onWait，决策后触发 onResume。
func TestStateHooksOnWaitAndResume(t *testing.T) {
	m := newTestManager()
	waited := make(chan string, 1)
	resumed := make(chan string, 1)
	m.SetStateHooks(
		func(sessionID string) { waited <- sessionID },
		func(sessionID string) { resumed <- sessionID },
	)

	done := make(chan error, 1)
	go func() {
		_, err := m.Request("s1", "write_file", "a.txt", "+ x")
		done <- err
	}()
	select {
	case s := <-waited:
		if s != "s1" {
			t.Fatalf("onWait sessionID=%q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onWait 未触发")
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := m.Decide(m.pendingIDs()[0], true, false); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	select {
	case s := <-resumed:
		if s != "s1" {
			t.Fatalf("onResume sessionID=%q", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("onResume 未触发")
	}
	if err := <-done; err != nil {
		t.Fatalf("Request: %v", err)
	}
}

// TestStateHooksResumeOnCancel ctx 取消时也触发 onResume，任务不会卡在 BLOCKED。
func TestStateHooksResumeOnCancel(t *testing.T) {
	m := newTestManager()
	waited := make(chan struct{}, 1)
	resumed := make(chan struct{}, 1)
	m.SetStateHooks(
		func(string) { waited <- struct{}{} },
		func(string) { resumed <- struct{}{} },
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := m.RequestContext(ctx, "s1", "run_shell", "go test", "cmd")
		done <- err
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("onWait 未触发")
	}
	cancel()
	select {
	case <-resumed:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 onResume 未触发")
	}
	if err := <-done; err == nil {
		t.Fatal("ctx 取消应返回错误")
	}
}
