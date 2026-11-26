package agent

import (
	"context"
	"errors"
	"testing"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
)

func newCompressUsageSession(t *testing.T, bus *eventbus.Bus, srvURL string,
	budget agentspec.Budget) (*Registry, *agentSession) {
	t.Helper()
	wa := &WorkAgent{
		Agent: scriptedAgent(t, "dispatch_coder", &streamScriptModel{chunks: []string{"ok"}}),
		Spec: agentspec.Spec{
			AgentID: "a1", Name: "coder",
			Context: agentspec.ContextConfig{CompressThreshold: 0.5, KeepRecent: 2},
			Budget:  budget,
		},
		Model: config.ModelConfig{
			ID: "m", Name: "M", BaseURL: srvURL, APIKey: "sk", Model: "test", ContextWindow: 200,
		},
	}
	r := NewRegistry()
	r.SetBus(bus)
	as := r.Register("s1", wa)
	as.history = longMessages(10, 100)
	return r, as
}

// TestSendMessageCompressUsage 私聊压缩 usage 记录到 agentSession 并推送 llm_usage。
func TestSendMessageCompressUsage(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	srv := summaryServer(t, "摘要")
	defer srv.Close()

	r, as := newCompressUsageSession(t, bus, srv.URL, agentspec.Budget{MaxTokens: 1000})
	reply, err := r.SendMessage(context.Background(), "s1", "orchestrator", as, "hi")
	if err != nil || reply != "ok" {
		t.Fatalf("SendMessage: reply=%q err=%v", reply, err)
	}
	if as.compressTokens == 0 {
		t.Fatal("应累计压缩 token")
	}
	ev := waitEventCb(t, sub, eventbus.EvLLMUsage)
	d, ok := ev.Data.(eventbus.LLMUsageData)
	if !ok || d.AgentID != "a1" || d.ModelID != "m" || d.InputTokens == 0 {
		t.Fatalf("llm_usage 载荷不符: %+v", ev.Data)
	}
}

// TestSendMessageCompressBudgetExceeded 压缩用量超过 Agent 预算时终止私聊。
func TestSendMessageCompressBudgetExceeded(t *testing.T) {
	bus := eventbus.New()
	srv := summaryServer(t, "摘要")
	defer srv.Close()

	r, as := newCompressUsageSession(t, bus, srv.URL, agentspec.Budget{MaxTokens: 1})
	if _, err := r.SendMessage(context.Background(), "s1", "orchestrator", as, "hi"); !errors.Is(err, ErrMaxTokens) {
		t.Fatalf("应返回 ErrMaxTokens: %v", err)
	}
}
