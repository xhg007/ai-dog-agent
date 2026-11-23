package approval

import (
	"errors"
	"testing"
	"time"

	"ai-dog-agent/internal/eventbus"
)

func newTestManager() *Manager {
	return New(eventbus.New())
}

// TestRequestApproved 批准后 Request 返回 true。
func TestRequestApproved(t *testing.T) {
	m := newTestManager()
	done := make(chan error, 1)
	var approved bool
	go func() {
		ok, err := m.Request("s1", "write_file", "a.txt", "+ new line")
		approved = ok
		done <- err
	}()
	// 等待 pending 出现
	deadline := time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	ids := m.pendingIDs()
	if len(ids) != 1 {
		t.Fatalf("应有 1 个待审批，got %d", len(ids))
	}
	if err := m.Decide(ids[0], true, false); err != nil {
		t.Fatalf("Decide 失败: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Request 返回错误: %v", err)
	}
	if !approved {
		t.Fatal("批准后 Request 应返回 true")
	}
}

// TestRequestRejected 拒绝后 Request 返回 ErrDenied。
func TestRequestRejected(t *testing.T) {
	m := newTestManager()
	done := make(chan error, 1)
	go func() {
		_, err := m.Request("s1", "write_file", "a.txt", "+ x")
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	ids := m.pendingIDs()
	if err := m.Decide(ids[0], false, false); err != nil {
		t.Fatalf("Decide 失败: %v", err)
	}
	err := <-done
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("拒绝应返回 ErrDenied，got %v", err)
	}
}

// TestRememberSamePath 记住后同会话同路径不再询问；不同路径仍询问。
func TestRememberSamePath(t *testing.T) {
	m := newTestManager()
	done := make(chan error, 1)
	go func() {
		_, err := m.Request("s1", "write_file", "a.txt", "+ x")
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := m.Decide(m.pendingIDs()[0], true, true); err != nil {
		t.Fatalf("Decide 失败: %v", err)
	}
	<-done

	// 同会话同路径：直接放行，不产生 pending
	ok, err := m.Request("s1", "write_file", "a.txt", "+ y")
	if err != nil || !ok {
		t.Fatalf("记住后应直接放行，got ok=%v err=%v", ok, err)
	}
	if n := len(m.pendingIDs()); n != 0 {
		t.Fatalf("记住后不应产生待审批，got %d", n)
	}

	// 不同路径：仍需审批
	go func() {
		_, _ = m.Request("s1", "write_file", "b.txt", "+ y")
		done <- nil
	}()
	deadline = time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(m.pendingIDs()) != 1 {
		t.Fatal("不同路径应产生待审批")
	}
	_ = m.Decide(m.pendingIDs()[0], true, false)
	<-done
}

// TestRememberScopedBySession 记住按会话隔离。
func TestRememberScopedBySession(t *testing.T) {
	m := newTestManager()
	done := make(chan error, 1)
	go func() {
		_, _ = m.Request("s1", "write_file", "a.txt", "+ x")
		done <- nil
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	_ = m.Decide(m.pendingIDs()[0], true, true)
	<-done

	go func() {
		_, _ = m.Request("s2", "write_file", "a.txt", "+ x")
		done <- nil
	}()
	deadline = time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(m.pendingIDs()) != 1 {
		t.Fatal("记住应按会话隔离：s2 首次仍需审批")
	}
	_ = m.Decide(m.pendingIDs()[0], true, false)
	<-done
}

// TestDecideUnknownID 对不存在 ID 的决策应报错。
func TestDecideUnknownID(t *testing.T) {
	m := newTestManager()
	if err := m.Decide("no-such-id", true, false); err == nil {
		t.Fatal("未知 ID 应返回错误")
	}
}

// TestRequestEmitsEvent 审批请求应推送事件（含工具、路径、预览）。
func TestRequestEmitsEvent(t *testing.T) {
	bus := eventbus.New()
	ch, _ := bus.Subscribe()
	m := New(bus)
	done := make(chan error, 1)
	go func() {
		_, _ = m.Request("s1", "write_file", "a.txt", "+ new")
		done <- nil
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	_ = m.Decide(m.pendingIDs()[0], true, false)
	<-done

	deadline = time.Now().Add(time.Second)
	var got *eventbus.Event
	for time.Now().Before(deadline) {
		select {
		case ev := <-ch:
			if ev.Type == eventbus.EvApprovalRequest {
				copyEv := ev
				got = &copyEv
			}
		default:
			time.Sleep(5 * time.Millisecond)
		}
		if got != nil {
			break
		}
	}
	if got == nil {
		t.Fatal("未收到 approval_request 事件")
	}
	data, ok := got.Data.(eventbus.ApprovalRequestData)
	if !ok {
		t.Fatalf("事件载荷类型不符: %T", got.Data)
	}
	if data.Tool != "write_file" || data.Path != "a.txt" || data.Preview != "+ new" {
		t.Fatalf("事件载荷不符: %+v", data)
	}
}
