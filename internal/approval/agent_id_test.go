package approval

import (
	"context"
	"testing"
	"time"
)

// TestRequestContextCarriesAgentID 审批请求携带 ctx 中的发起 Agent ID。
func TestRequestContextCarriesAgentID(t *testing.T) {
	m := newTestManager()
	ctx := WithAgentID(context.Background(), "agent-w")
	done := make(chan error, 1)
	go func() {
		_, err := m.RequestContext(ctx, "s1", "write_file", "a.txt", "+ x")
		done <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for len(m.pendingIDs()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	pending := m.ListPending()
	if len(pending) != 1 {
		t.Fatalf("应有 1 个待审批，实际 %d", len(pending))
	}
	if pending[0].AgentID != "agent-w" {
		t.Fatalf("审批 AgentID 不符: %+v", pending[0])
	}
	if err := m.Decide(pending[0].ID, true, false); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("RequestContext: %v", err)
	}
}

// TestAgentIDFromContextEmpty 空/nil ctx 返回空 ID。
func TestAgentIDFromContextEmpty(t *testing.T) {
	if id := AgentIDFromContext(context.Background()); id != "" {
		t.Fatalf("空 ctx 应返回空 ID: %q", id)
	}
	if id := AgentIDFromContext(nil); id != "" {
		t.Fatalf("nil ctx 应返回空 ID: %q", id)
	}
	if ctx := WithAgentID(context.Background(), ""); AgentIDFromContext(ctx) != "" {
		t.Fatal("空 AgentID 不应写入 ctx")
	}
}
