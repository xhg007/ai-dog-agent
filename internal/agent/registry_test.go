package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/agentspec"
)

// registryFakeAgent 仅用于 Registry 结构测试；Register 只构造 Runner，不调用 Run。
type registryFakeAgent struct{}

func (f *registryFakeAgent) Name(context.Context) string        { return "fake" }
func (f *registryFakeAgent) Description(context.Context) string { return "fake agent" }
func (f *registryFakeAgent) Run(context.Context, *adk.AgentInput,
	...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return nil
}

func testWorkAgent(id, name string) *WorkAgent {
	return &WorkAgent{
		Agent: &registryFakeAgent{},
		Spec:  agentspec.Spec{AgentID: id, Name: name},
	}
}

// TestRegistryRegisterLookupRemove 注册、按三种引用查找、会话隔离与移除。
func TestRegistryRegisterLookupRemove(t *testing.T) {
	r := NewRegistry()
	r.Register("s1", testWorkAgent("a1", "Coder"))

	for _, ref := range []string{"a1", "Coder", "dispatch_coder"} {
		if _, ok := r.Lookup("s1", ref); !ok {
			t.Fatalf("Lookup(%q) 应命中", ref)
		}
	}
	if _, ok := r.Lookup("s1", "missing"); ok {
		t.Fatal("未知引用不应命中")
	}
	if _, ok := r.Lookup("s2", "a1"); ok {
		t.Fatal("会话之间应隔离")
	}
	if n := r.Count("s1"); n != 1 {
		t.Fatalf("Count=%d, want 1", n)
	}
	r.RemoveSession("s1")
	if n := r.Count("s1"); n != 0 {
		t.Fatalf("RemoveSession 后 Count=%d", n)
	}
	if _, ok := r.Lookup("s1", "a1"); ok {
		t.Fatal("RemoveSession 后不应命中")
	}
}

// TestRegistryReRegisterResetsHistory 同 AgentID 覆盖时创建新会话并清空私聊历史。
func TestRegistryReRegisterResetsHistory(t *testing.T) {
	r := NewRegistry()
	as1 := r.Register("s1", testWorkAgent("a1", "Coder"))
	as1.history = []*schema.Message{schema.UserMessage("hi")}

	as2 := r.Register("s1", testWorkAgent("a1", "Coder v2"))
	if as2 == as1 {
		t.Fatal("覆盖应创建新的 agentSession")
	}
	if len(as2.history) != 0 {
		t.Fatalf("覆盖后历史应清空: %d", len(as2.history))
	}
	got, ok := r.Lookup("s1", "a1")
	if !ok || got != as2 {
		t.Fatal("Lookup 应返回新会话")
	}
}

// TestRegistryConcurrentLookup 并发查找/计数不 panic（-race 下验证无数据竞争）。
func TestRegistryConcurrentLookup(t *testing.T) {
	r := NewRegistry()
	r.Register("s1", testWorkAgent("a1", "Coder"))
	r.Register("s1", testWorkAgent("a2", "Reviewer"))

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			refs := []string{"a1", "Coder", "dispatch_coder", "a2", "missing"}
			_, _ = r.Lookup("s1", refs[i%len(refs)])
			_ = r.Count("s1")
		}(i)
	}
	wg.Wait()
}

// TestRegistryListAll 汇总所有会话的子 Agent 快照，并按会话、名称排序。
func TestRegistryListAll(t *testing.T) {
	r := NewRegistry()
	r.Register("s2", testWorkAgent("a2", "Reviewer"))
	r.Register("s1", testWorkAgent("a1", "Coder"))
	r.Register("s1", testWorkAgent("a3", "Architect"))

	got := r.ListAll()
	if len(got) != 3 {
		t.Fatalf("ListAll=%d, want 3", len(got))
	}
	want := []struct{ session, name, tool string }{
		{"s1", "Architect", "dispatch_architect"},
		{"s1", "Coder", "dispatch_coder"},
		{"s2", "Reviewer", "dispatch_reviewer"},
	}
	for i, w := range want {
		if got[i].SessionID != w.session || got[i].Name != w.name || got[i].Tool != w.tool {
			t.Fatalf("ListAll[%d]=%+v, want session=%s name=%s tool=%s", i, got[i], w.session, w.name, w.tool)
		}
		if got[i].AgentID == "" {
			t.Fatalf("ListAll[%d] 缺少 agentId", i)
		}
	}
}
