package agent

import (
	"errors"
	"testing"
	"time"

	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/workspace"
)

// TestShouldRunSmith 开关关闭/开启、状态过滤与同一任务去重。
func TestShouldRunSmith(t *testing.T) {
	cfg := newSmithTestConfig(t)
	svc := &ChatService{cfg: cfg}

	if svc.shouldRunSmith(store.StatusFailed, "t1") {
		t.Fatal("默认关闭时不应触发")
	}
	c := cfg.Get()
	c.AgentSmithAuto = true
	if err := cfg.Save(c); err != nil {
		t.Fatalf("开启开关: %v", err)
	}
	if !svc.shouldRunSmith(store.StatusFailed, "t1") {
		t.Fatal("开启后 failed 应触发")
	}
	if svc.shouldRunSmith(store.StatusFailed, "t1") {
		t.Fatal("同一 taskID 应去重")
	}
	if !svc.shouldRunSmith(store.StatusCancelled, "t2") {
		t.Fatal("cancelled 应触发")
	}
	if svc.shouldRunSmith(store.StatusCompleted, "t3") {
		t.Fatal("completed 不应触发")
	}
}

// TestOptimizeAgentsBusy 项目已有 smith 运行时手动触发返回 ErrSmithBusy。
func TestOptimizeAgentsBusy(t *testing.T) {
	bus := eventbus.New()
	s, srv := newRunSmith(t, bus, `{"action":"skip","reason":"x"}`)
	defer srv.Close()
	if !s.acquire(s.ws.Root()) {
		t.Fatal("准备锁失败")
	}
	defer s.release(s.ws.Root())

	svc := &ChatService{
		cfg: s.cfg, ws: s.ws, smith: s, bus: bus,
		sessions: map[string]*chatSession{"s1": {id: "s1", agentID: "main"}},
	}
	if err := svc.OptimizeAgents("s1"); !errors.Is(err, ErrSmithBusy) {
		t.Fatalf("应返回 ErrSmithBusy: %v", err)
	}
}

// TestOptimizeAgentsValidation 无会话 / 无工作区 / smith 不可用。
func TestOptimizeAgentsValidation(t *testing.T) {
	bus := eventbus.New()
	s, srv := newRunSmith(t, bus, `{"action":"skip","reason":"x"}`)
	defer srv.Close()

	svc := &ChatService{cfg: s.cfg, ws: s.ws, smith: s, bus: bus, sessions: map[string]*chatSession{}}
	if err := svc.OptimizeAgents("missing"); err == nil {
		t.Fatal("会话不存在应报错")
	}

	svc2 := &ChatService{
		cfg: s.cfg, ws: workspace.New(), smith: s, bus: bus,
		sessions: map[string]*chatSession{"s1": {id: "s1", agentID: "main"}},
	}
	if err := svc2.OptimizeAgents("s1"); !errors.Is(err, workspace.ErrNoWorkspace) {
		t.Fatalf("无工作区应返回 ErrNoWorkspace: %v", err)
	}

	svc3 := &ChatService{
		cfg: s.cfg, ws: s.ws, bus: bus,
		sessions: map[string]*chatSession{"s1": {id: "s1", agentID: "main"}},
	}
	if err := svc3.OptimizeAgents("s1"); err == nil {
		t.Fatal("smith 不可用应报错")
	}
}

// TestOptimizeAgentsRuns 手动触发与自动触发共用 Smith.Run，异步推送事件。
func TestOptimizeAgentsRuns(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	s, srv := newRunSmith(t, bus, `{"action":"skip","reason":"手动"}`)
	defer srv.Close()

	svc := &ChatService{
		cfg: s.cfg, ws: s.ws, smith: s, bus: bus,
		sessions: map[string]*chatSession{"s1": {id: "s1", agentID: "main"}},
	}
	if err := svc.OptimizeAgents("s1"); err != nil {
		t.Fatalf("OptimizeAgents: %v", err)
	}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-sub:
			if ev.Type != eventbus.EvAgentSmith {
				continue
			}
			if d, ok := ev.Data.(eventbus.AgentSmithData); ok && d.Phase == "skipped" {
				return
			}
		case <-deadline:
			t.Fatal("等待手动优化事件超时")
		}
	}
}

// TestConfigAgentSmithAutoJSON 开关可持久化并回读。
func TestConfigAgentSmithAutoJSON(t *testing.T) {
	cfg := newSmithTestConfig(t)
	c := cfg.Get()
	c.AgentSmithAuto = true
	if err := cfg.Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !cfg.Get().AgentSmithAuto {
		t.Fatal("AgentSmithAuto 应持久化")
	}
}
