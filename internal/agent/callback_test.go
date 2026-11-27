package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
)

func newObsTestEnv(t *testing.T) (*task.Manager, *store.DuckDBStore, <-chan eventbus.Event, func()) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "obs.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := eventbus.New()
	w := store.NewAsyncWriter(st, 64)
	m := task.New(st, w, bus)
	sub, cancel := bus.Subscribe()
	return m, st, sub, func() {
		w.Close()
		st.Close()
		cancel()
	}
}

func waitEventCb(t *testing.T, ch <-chan eventbus.Event, evType string) eventbus.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("事件通道关闭")
			}
			if ev.Type == evType {
				return ev
			}
		case <-deadline:
			t.Fatalf("等待事件 %s 超时", evType)
		}
	}
}

// TestObsHandler_ModelUsage ChatModel OnEnd 采集 TokenUsage 落库并发 llm_usage 事件。
func TestObsHandler_ModelUsage(t *testing.T) {
	m, st, ch, cleanup := newObsTestEnv(t)
	defer cleanup()
	h := newObsHandler(m)

	taskID, _ := m.Start("s1", "t")
	_ = waitEventCb(t, ch, eventbus.EvTaskState)

	ctx := withObsInfo(context.Background(), &obsInfo{SessionID: "s1", TaskID: taskID, Snapshot: "[]"})
	info := &callbacks.RunInfo{Name: "chat", Type: "OpenAI", Component: components.ComponentOfChatModel}
	out := &model.CallbackOutput{TokenUsage: &model.TokenUsage{
		PromptTokens: 1000, CompletionTokens: 200, TotalTokens: 1200,
	}}
	h.OnEnd(ctx, info, out)

	ev := waitEventCb(t, ch, eventbus.EvLLMUsage)
	du, ok := ev.Data.(eventbus.LLMUsageData)
	if !ok || du.InputTokens != 1000 || du.OutputTokens != 200 {
		t.Fatalf("llm_usage 事件载荷不符: %+v", ev.Data)
	}
	rows, err := st.DailyUsage(30)
	if err != nil {
		t.Fatalf("DailyUsage: %v", err)
	}
	// AppendTokenUsage 是异步的，轮询等待落库
	deadline := time.Now().Add(2 * time.Second)
	for len(rows) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		rows, _ = st.DailyUsage(30)
	}
	if len(rows) != 1 || rows[0].InputTokens != 1000 || rows[0].OutputTokens != 200 {
		t.Fatalf("token_usage 落库不符: %+v", rows)
	}
}

// TestObsHandler_ModelUsageActualModel 用量按 obsInfo 中的实际模型归因，
// 事件携带 agentId/modelId，成本按实际模型单价折算。
func TestObsHandler_ModelUsageActualModel(t *testing.T) {
	m, st, ch, cleanup := newObsTestEnv(t)
	defer cleanup()
	_ = st
	h := newObsHandler(m)

	taskID, _ := m.Start("s1", "t")
	_ = waitEventCb(t, ch, eventbus.EvTaskState)

	ctx := withObsInfo(context.Background(), &obsInfo{
		SessionID: "s1", TaskID: taskID, Snapshot: "[]",
		AgentID: "agent-work", ModelID: "coder", ModelName: "Coder",
		PriceIn: 2, PriceOut: 4,
	})
	info := &callbacks.RunInfo{Name: "chat", Type: "OpenAI", Component: components.ComponentOfChatModel}
	h.OnEnd(ctx, info, &model.CallbackOutput{TokenUsage: &model.TokenUsage{
		PromptTokens: 1_000_000, CompletionTokens: 500_000, TotalTokens: 1_500_000,
	}})

	ev := waitEventCb(t, ch, eventbus.EvLLMUsage)
	du, ok := ev.Data.(eventbus.LLMUsageData)
	if !ok {
		t.Fatalf("llm_usage 事件载荷类型不符: %T", ev.Data)
	}
	if du.AgentID != "agent-work" || du.ModelID != "coder" || du.Model != "Coder" {
		t.Fatalf("实际模型归因不符: %+v", du)
	}
	if du.CostIn != 2 || du.CostOut != 2 {
		t.Fatalf("成本应按实际模型单价折算: in=%v out=%v", du.CostIn, du.CostOut)
	}
}

// TestObsHandler_ToolCheckpoints Tool 节点 before/after checkpoint + tool_call 事件。
func TestObsHandler_ToolCheckpoints(t *testing.T) {
	m, st, ch, cleanup := newObsTestEnv(t)
	defer cleanup()
	h := newObsHandler(m)

	taskID, _ := m.Start("s1", "t")
	_ = waitEventCb(t, ch, eventbus.EvTaskState)

	ctx := withObsInfo(context.Background(), &obsInfo{
		SessionID: "s1", TaskID: taskID, Snapshot: `[{"role":"user","content":"hi"}]`,
		AgentID: "agent-w",
	})
	tinfo := &callbacks.RunInfo{Name: "dev_validate_go_code", Type: "GoTool", Component: components.ComponentOfTool}

	// before
	h.OnStart(ctx, tinfo, &tool.CallbackInput{ArgumentsInJSON: `{"dir":"internal"}`})
	ev := waitEventCb(t, ch, eventbus.EvToolCall)
	tc, ok := ev.Data.(eventbus.ToolCallData)
	if !ok || tc.Tool != "dev_validate_go_code" || tc.AgentID != "agent-w" || tc.Args != `{"dir":"internal"}` {
		t.Fatalf("tool_call 事件不符: %+v", ev.Data)
	}

	// after
	h.OnEnd(ctx, tinfo, &tool.CallbackOutput{Response: "ok 0.5s"})
	m.AppendCheckpoint(store.Checkpoint{}) // 确认异步队列已有序（noop）
	deadline := time.Now().Add(2 * time.Second)
	var last *store.Checkpoint
	for time.Now().Before(deadline) {
		var err error
		last, err = st.LastAfterCheckpoint(taskID)
		if err == nil && last != nil && last.Seq == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if last == nil || last.Phase != "after" || last.ToolName != "dev_validate_go_code" || last.ToolResult != "ok 0.5s" {
		t.Fatalf("after checkpoint 不符: %+v", last)
	}
}

// TestObsHandler_IgnoresOtherComponents 非 ChatModel/Tool 组件不产生记录。
func TestObsHandler_IgnoresOtherComponents(t *testing.T) {
	m, _, _, cleanup := newObsTestEnv(t)
	defer cleanup()
	h := newObsHandler(m)

	ctx := withObsInfo(context.Background(), &obsInfo{SessionID: "s1", TaskID: "t1", Snapshot: "[]"})
	info := &callbacks.RunInfo{Name: "x", Type: "Graph", Component: "Agent"}
	h.OnStart(ctx, info, nil)
	h.OnEnd(ctx, info, nil)
	// 无 panic 即通过（Agent 组件被忽略）
}

// TestObsHandler_StreamUsage 流式 ChatModel：复制流异步聚合 usage。
func TestObsHandler_StreamUsage(t *testing.T) {
	m, st, _, cleanup := newObsTestEnv(t)
	defer cleanup()
	h := newObsHandler(m)

	taskID, _ := m.Start("s1", "t")
	ctx := withObsInfo(context.Background(), &obsInfo{SessionID: "s1", TaskID: taskID, Snapshot: "[]"})
	info := &callbacks.RunInfo{Name: "chat", Type: "OpenAI", Component: components.ComponentOfChatModel}

	// 构造流：chunk1 无 usage，chunk2 带 usage
	sr, sw := schema.Pipe[callbacks.CallbackOutput](2)
	sw.Send(&model.CallbackOutput{TokenUsage: nil}, nil)
	sw.Send(&model.CallbackOutput{TokenUsage: &model.TokenUsage{
		PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60,
	}}, nil)
	sw.Close()
	h.OnEndWithStreamOutput(ctx, info, sr)
	deadline := time.Now().Add(2 * time.Second)
	rows, _ := st.DailyUsage(30)
	for len(rows) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		rows, _ = st.DailyUsage(30)
	}
	if len(rows) == 0 || rows[0].InputTokens != 50 {
		t.Fatalf("流式 usage 落库不符: %+v", rows)
	}
}

// TestObsHandler_StreamUsageWaiter 流式 usage 异步采集可被等待：
// 等待完成后执行计数器已累计 token，预算超限能在当轮终止。
func TestObsHandler_StreamUsageWaiter(t *testing.T) {
	m, _, _, cleanup := newObsTestEnv(t)
	defer cleanup()
	h := newObsHandler(m)

	taskID, _ := m.Start("s1", "t")
	exec, _, cancel := StartExecution(context.Background(), Limits{MaxTokens: 10})
	defer cancel()
	waiter := &asyncWaiter{}
	ctx := withObsInfo(context.Background(), &obsInfo{
		SessionID: "s1", TaskID: taskID, Snapshot: "[]", Exec: exec, Usage: waiter,
	})
	info := &callbacks.RunInfo{Name: "chat", Type: "OpenAI", Component: components.ComponentOfChatModel}

	sr, sw := schema.Pipe[callbacks.CallbackOutput](1)
	sw.Send(&model.CallbackOutput{TokenUsage: &model.TokenUsage{
		PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60,
	}}, nil)
	sw.Close()
	h.OnEndWithStreamOutput(ctx, info, sr)

	if !waiter.Wait(2 * time.Second) {
		t.Fatal("usage 采集未在超时内完成")
	}
	if got := exec.Snapshot().Tokens; got != 60 {
		t.Fatalf("Token 累计 = %d，期望 60", got)
	}
	if err := exec.Termination(context.Background()); err == nil {
		t.Fatal("超过 MaxTokens 后应在当轮终止")
	}
}
