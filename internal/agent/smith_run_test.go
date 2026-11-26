package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
)

type smithRunResult struct {
	p   *SmithProposal
	err error
}

func newSmithModelServer(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		payload := fmt.Sprintf(`{"id":"1","object":"chat.completion","created":1,"model":"test",
			"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":10,"total_tokens":20}}`, reply)
		_, _ = w.Write([]byte(payload))
	}))
}

func newRunSmith(t *testing.T, bus *eventbus.Bus, reply string) (*Smith, *httptest.Server) {
	t.Helper()
	ws := setupProjectWS(t)
	srv := newSmithModelServer(t, reply)
	cfg, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	if err := cfg.Save(config.Config{
		Models:        []config.ModelConfig{{ID: "main", Name: "Main", BaseURL: srv.URL, APIKey: "sk", Model: "test"}},
		ActiveModelID: "main",
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	if err := cfg.TrustProject(ws.Root()); err != nil {
		t.Fatalf("TrustProject: %v", err)
	}
	return &Smith{
		cfg: cfg, ws: ws, appr: approval.New(bus), bus: bus,
		id: "smith-test", running: make(map[string]bool),
	}, srv
}

func waitPendingID(t *testing.T, appr *approval.Manager) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ids := appr.ListPending(); len(ids) > 0 {
			return ids[0].ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待审批请求超时")
	return ""
}

// TestSmithRunSkip 模型建议 skip 时不写配置，事件为 started/skipped。
func TestSmithRunSkip(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	s, srv := newRunSmith(t, bus, `{"action":"skip","reason":"无需修改"}`)
	defer srv.Close()

	p, err := s.Run(context.Background(), SmithTrigger{SessionID: "s1", MainID: "main-id"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p == nil || p.Action != "skip" {
		t.Fatalf("应返回 skip: %+v", p)
	}
	evs := drainEvents(sub)
	phases := smithPhases(evs)
	if len(phases) != 2 || phases[0] != "started" || phases[1] != "skipped" {
		t.Fatalf("事件序列不符: %v", phases)
	}
}

// TestSmithRunRejected 审批拒绝时不写配置，返回 ErrSmithRejected，事件 rejected。
func TestSmithRunRejected(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	reply := `{"action":"create","name":"coder","spec":{"name":"coder","description":"d","system_prompt":"你是 coder"},"reason":"新增"}`
	s, srv := newRunSmith(t, bus, reply)
	defer srv.Close()

	resCh := make(chan smithRunResult, 1)
	go func() {
		p, err := s.Run(context.Background(), SmithTrigger{SessionID: "s1", MainID: "main-id"})
		resCh <- smithRunResult{p, err}
	}()
	id := waitPendingID(t, s.appr)
	if err := s.appr.Decide(id, false, false); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	res := <-resCh
	if !errors.Is(res.err, ErrSmithRejected) {
		t.Fatalf("应返回 ErrSmithRejected: %v", res.err)
	}
	specs, _ := agentspec.List(s.ws.Root())
	if len(specs) != 0 {
		t.Fatalf("拒绝后不应写入配置: %+v", specs)
	}
	phases := smithPhases(drainEvents(sub))
	if len(phases) != 2 || phases[0] != "started" || phases[1] != "rejected" {
		t.Fatalf("事件序列不符: %v", phases)
	}
}

// TestSmithRunAppliedWithBackup 审批通过后保存配置并备份旧版本。
func TestSmithRunAppliedWithBackup(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	reply := `{"action":"update","name":"coder","spec":{"name":"coder","description":"new","system_prompt":"v2"},"reason":"更新提示词"}`
	s, srv := newRunSmith(t, bus, reply)
	defer srv.Close()
	if err := agentspec.Save(s.ws.Root(), agentspec.Spec{
		Name: "coder", Description: "old", SystemPrompt: "v1",
	}); err != nil {
		t.Fatalf("准备旧配置: %v", err)
	}

	resCh := make(chan smithRunResult, 1)
	go func() {
		p, err := s.Run(context.Background(), SmithTrigger{SessionID: "s1", MainID: "main-id"})
		resCh <- smithRunResult{p, err}
	}()
	id := waitPendingID(t, s.appr)
	if err := s.appr.Decide(id, true, false); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	res := <-resCh
	if res.err != nil {
		t.Fatalf("Run: %v", res.err)
	}
	specs, err := agentspec.List(s.ws.Root())
	if err != nil || len(specs) != 1 {
		t.Fatalf("List: %v %+v", err, specs)
	}
	if specs[0].SystemPrompt != "v2" || specs[0].Description != "new" {
		t.Fatalf("配置未更新: %+v", specs[0])
	}
	backups, err := agentspec.ListBackups(s.ws.Root(), "coder")
	if err != nil || len(backups) != 1 {
		t.Fatalf("应有 1 份旧配置备份: %v %+v", err, backups)
	}
	evs := drainEvents(sub)
	phases := smithPhases(evs)
	if len(phases) != 2 || phases[0] != "started" || phases[1] != "applied" {
		t.Fatalf("事件序列不符: %v", phases)
	}
	var applied eventbus.AgentSmithData
	for _, ev := range evs {
		if ev.Type == eventbus.EvAgentSmith {
			if d, ok := ev.Data.(eventbus.AgentSmithData); ok && d.Phase == "applied" {
				applied = d
			}
		}
	}
	if applied.Name != "coder" || applied.Reason != "更新提示词" {
		t.Fatalf("applied 事件载荷不符: %+v", applied)
	}
}

// TestSmithRunValidationFailure 模型输出非法时不进入审批，事件 failed。
func TestSmithRunValidationFailure(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	reply := `{"action":"create","name":"x","spec":{"name":"x","system_prompt":"x","tools":["nope"]}}`
	s, srv := newRunSmith(t, bus, reply)
	defer srv.Close()

	if _, err := s.Run(context.Background(), SmithTrigger{SessionID: "s1", MainID: "main-id"}); err == nil {
		t.Fatal("未知工具应校验失败")
	}
	phases := smithPhases(drainEvents(sub))
	if len(phases) != 2 || phases[0] != "started" || phases[1] != "failed" {
		t.Fatalf("事件序列不符: %v", phases)
	}
	if len(s.appr.ListPending()) != 0 {
		t.Fatal("校验失败不应产生审批请求")
	}
}

// TestSmithAcquireBusy 同一项目根只能有一个 smith 运行。
func TestSmithAcquireBusy(t *testing.T) {
	s := &Smith{running: make(map[string]bool)}
	if !s.acquire("root") {
		t.Fatal("首次 acquire 应成功")
	}
	if s.acquire("root") {
		t.Fatal("重复 acquire 应失败")
	}
	s.release("root")
	if !s.acquire("root") {
		t.Fatal("release 后应可再次 acquire")
	}
	s.release("root")
}

// TestSmithRunBusy Run 在项目锁占用时返回 ErrSmithBusy。
func TestSmithRunBusy(t *testing.T) {
	bus := eventbus.New()
	s, srv := newRunSmith(t, bus, `{"action":"skip","reason":"x"}`)
	defer srv.Close()
	if !s.acquire(s.ws.Root()) {
		t.Fatal("准备锁失败")
	}
	defer s.release(s.ws.Root())
	if _, err := s.Run(context.Background(), SmithTrigger{SessionID: "s1", MainID: "main-id"}); !errors.Is(err, ErrSmithBusy) {
		t.Fatalf("应返回 ErrSmithBusy: %v", err)
	}
}

func smithPhases(events []eventbus.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		if ev.Type != eventbus.EvAgentSmith {
			continue
		}
		if d, ok := ev.Data.(eventbus.AgentSmithData); ok {
			out = append(out, d.Phase)
		}
	}
	return out
}
