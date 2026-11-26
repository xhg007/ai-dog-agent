package task

import (
	"path/filepath"
	"testing"
	"time"

	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
)

func newTestManager(t *testing.T) (*Manager, *store.DuckDBStore, <-chan eventbus.Event, func()) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	bus := eventbus.New()
	w := store.NewAsyncWriter(st, 64)
	m := New(st, w, bus)
	sub, cancel := bus.Subscribe()
	return m, st, sub, func() {
		w.Close()
		st.Close()
		cancel()
	}
}

func waitEvent(t *testing.T, ch <-chan eventbus.Event, evType string) eventbus.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("事件通道关闭")
			}
			if ev.Type == evType {
				return ev
			}
		case <-deadline:
			t.Fatalf("等待事件 %s 超时", evType)
		}
	}
}

func TestTaskLifecycleViaManager(t *testing.T) {
	m, st, ch, cleanup := newTestManager(t)
	defer cleanup()

	id, err := m.Start("s1", "修复测试")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// EvTaskState 已推送
	ev := waitEvent(t, ch, eventbus.EvTaskState)
	if ev.SessionID != "s1" {
		t.Fatalf("事件 sessionId 不符: %s", ev.SessionID)
	}
	st2, _ := st.ListTasks(10)
	if len(st2) != 1 || st2[0].Status != store.StatusRunning {
		// 写是异步的，Submit 后可能尚未落库——用 Finish 后统一断言
		t.Logf("异步写未落库（可接受），当前: %+v", st2)
	}

	if err := m.Finish("s1", id, store.StatusFailed, "go test 失败"); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	waitEvent(t, ch, eventbus.EvTaskState)

	// 排空异步写后断言持久化
	m.w.Close()
	tasks, err := st.ListTasks(10)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != id || tasks[0].Status != store.StatusFailed ||
		tasks[0].Error != "go test 失败" || tasks[0].FinishedAt == nil {
		t.Fatalf("任务终态不符: %+v", tasks)
	}
	evs, _ := st.TaskEvents(id, 100)
	if len(evs) != 2 || evs[0].Type != eventbus.EvTaskState || evs[1].Type != eventbus.EvTaskState {
		t.Fatalf("task_state 事件应有 2 条: %+v", evs)
	}
}

func TestAppendTokenUsageAndEmit(t *testing.T) {
	m, st, ch, cleanup := newTestManager(t)
	defer cleanup()

	id, _ := m.Start("s1", "x")
	waitEvent(t, ch, eventbus.EvTaskState)

	m.Emit("s1", id, eventbus.EvLLMUsage, eventbus.LLMUsageData{
		TaskID: id, Model: "m1", InputTokens: 1000, OutputTokens: 500, CostIn: 0.01, CostOut: 0.02,
	})
	ev := waitEvent(t, ch, eventbus.EvLLMUsage)
	if ev.SessionID != "s1" {
		t.Fatalf("EvLLMUsage sessionId 不符: %s", ev.SessionID)
	}

	m.AppendTokenUsage(store.TokenUsage{
		TaskID: id, Model: "m1", InputTokens: 1000, OutputTokens: 500, CostIn: 0.01, CostOut: 0.02,
	})
	m.Finish("s1", id, store.StatusCompleted, "")
	m.w.Close()

	evs, _ := st.TaskEvents(id, 100)
	if len(evs) != 4 {
		t.Fatalf("事件数应为 4（3×task_state: running/review/completed + 1×llm_usage）: %+v", evs)
	}
	rows, _ := st.DailyUsage(30)
	if len(rows) != 1 || rows[0].Calls != 1 || rows[0].InputTokens != 1000 || rows[0].CostTotal < 0.029 {
		t.Fatalf("日报表聚合不符: %+v", rows)
	}
}

func TestAppendCheckpointViaManager(t *testing.T) {
	m, st, ch, cleanup := newTestManager(t)
	defer cleanup()

	id, _ := m.Start("s1", "x")
	_ = waitEvent(t, ch, eventbus.EvTaskState)

	m.AppendCheckpoint(store.Checkpoint{
		TaskID: id, Seq: 1, Phase: "before", ToolName: "edit_file", ToolArgs: `{}`, Messages: `[]`,
	})
	m.AppendCheckpoint(store.Checkpoint{
		TaskID: id, Seq: 2, Phase: "after", ToolName: "edit_file", ToolResult: `{"ok":true}`,
	})
	m.w.Close()

	cp, err := st.LastAfterCheckpoint(id)
	if err != nil || cp == nil || cp.Seq != 2 {
		t.Fatalf("LastAfterCheckpoint 不符: %+v err=%v", cp, err)
	}
}
