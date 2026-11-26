package approval

// 回归测试：删除会话时必须清理「批准并记住」记录，避免长期累积。

import (
	"context"
	"testing"
	"time"

	"ai-dog-agent/internal/eventbus"
)

func waitPending(t *testing.T, m *Manager) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(m.ListPending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	list := m.ListPending()
	ids := make([]string, 0, len(list))
	for _, p := range list {
		ids = append(ids, p.ID)
	}
	return ids
}

func TestForgetSessionClearsRemembered(t *testing.T) {
	m := New(eventbus.New())

	// 第一次：批准并记住
	done := make(chan error, 1)
	go func() {
		_, err := m.RequestContext(context.Background(), "s1", "write_file", "a.txt", "")
		done <- err
	}()
	for _, id := range waitPending(t, m) {
		if err := m.Decide(id, true, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("首次审批失败: %v", err)
	}

	// 记住后同一路径直接放行
	if ok, err := m.RequestContext(context.Background(), "s1", "write_file", "a.txt", ""); err != nil || !ok {
		t.Fatalf("记住后应直接放行: ok=%v err=%v", ok, err)
	}

	m.ForgetSession("s1")

	// 清理后必须重新走审批
	ctx, cancel := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() {
		_, err := m.RequestContext(ctx, "s1", "write_file", "a.txt", "")
		done2 <- err
	}()
	ids := waitPending(t, m)
	if len(ids) == 0 {
		t.Fatal("清理 remembered 后应重新发起审批请求")
	}
	cancel()
	if err := <-done2; err == nil {
		t.Fatal("取消审批等待后应返回错误")
	}
}

// 清理一个会话不影响其他会话的记住记录。
func TestForgetSessionScopedToSession(t *testing.T) {
	m := New(eventbus.New())
	m.mu.Lock()
	m.remembered["s1"] = map[string]bool{"write_file:a.txt": true}
	m.remembered["s2"] = map[string]bool{"write_file:b.txt": true}
	m.mu.Unlock()

	m.ForgetSession("s1")

	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.remembered["s1"]; ok {
		t.Fatal("s1 的记住记录应被清理")
	}
	if !m.remembered["s2"]["write_file:b.txt"] {
		t.Fatal("s2 的记住记录不应受影响")
	}
}
