package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *DuckDBStore {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.duckdb"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTaskLifecycle(t *testing.T) {
	s := openTestStore(t)

	if err := s.CreateTask(Task{ID: "t1", SessionID: "s1", Title: "修测试", Status: StatusRunning, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := s.CreateTask(Task{ID: "t1", SessionID: "s1", Title: "重复", Status: StatusRunning}); err == nil {
		t.Fatal("重复主键应报错")
	}

	tasks, err := s.ListTasks(10)
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" || tasks[0].Status != StatusRunning {
		t.Fatalf("ListTasks 结果不符: %+v", tasks)
	}

	// 终态落 finished_at
	if err := s.UpdateTaskStatus("t1", StatusFailed, "go test 失败"); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	tasks, _ = s.ListTasks(10)
	if tasks[0].Status != StatusFailed || tasks[0].Error != "go test 失败" || tasks[0].FinishedAt.IsZero() {
		t.Fatalf("终态未正确落库: %+v", tasks[0])
	}
}

func TestTaskEvents(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateTask(Task{ID: "t1", SessionID: "s1", Title: "x", Status: StatusRunning}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"task_state", "tool_call", "tool_result", "llm_usage"} {
		if err := s.AppendTaskEvent("t1", ev, `{"k":1}`); err != nil {
			t.Fatalf("AppendTaskEvent(%s): %v", ev, err)
		}
	}
	evs, err := s.TaskEvents("t1", 100)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}
	if len(evs) != 4 || evs[0].Type != "task_state" || evs[3].Type != "llm_usage" {
		t.Fatalf("事件顺序/数量不符: %+v", evs)
	}
	if evs[0].ID == 0 {
		t.Fatal("事件 id 应由序列自增")
	}
}

func TestTokenUsageAndDailyReport(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	usages := []TokenUsage{
		{TaskID: "t1", Model: "m1", InputTokens: 1000, OutputTokens: 500, CostIn: 0.01, CostOut: 0.02, CreatedAt: now},
		{TaskID: "t2", Model: "m1", InputTokens: 2000, OutputTokens: 100, CostIn: 0.02, CostOut: 0.003, CreatedAt: now},
		{TaskID: "t3", Model: "m2", InputTokens: 10, OutputTokens: 5, CostIn: 0.0001, CostOut: 0.0001, CreatedAt: now.AddDate(0, 0, -10)},
	}
	for _, u := range usages {
		if err := s.AppendTokenUsage(u); err != nil {
			t.Fatalf("AppendTokenUsage: %v", err)
		}
	}
	rows, err := s.DailyUsage(30)
	if err != nil {
		t.Fatalf("DailyUsage: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应聚合成 2 天，got %d: %+v", len(rows), rows)
	}
	// 今天：calls=2 in=3000 out=600 cost=0.053
	today := rows[0]
	if today.Calls != 2 || today.InputTokens != 3000 || today.OutputTokens != 600 {
		t.Fatalf("今日聚合不符: %+v", today)
	}
	if today.CostTotal < 0.052 || today.CostTotal > 0.054 {
		t.Fatalf("今日成本聚合不符: %v", today.CostTotal)
	}
	// 近 1 天只剩今天
	if rows1, _ := s.DailyUsage(1); len(rows1) != 1 {
		t.Fatalf("近 1 天应只有 1 行: %+v", rows1)
	}
}

func TestCheckpoints(t *testing.T) {
	s := openTestStore(t)
	// before → after → before（seq 递增）
	cps := []Checkpoint{
		{TaskID: "t1", Seq: 1, Phase: "before", ToolName: "edit_file", ToolArgs: `{"path":"a.go"}`, Messages: `[{"role":"user"}]`},
		{TaskID: "t1", Seq: 2, Phase: "after", ToolName: "edit_file", ToolResult: `{"ok":true}`},
		{TaskID: "t1", Seq: 3, Phase: "before", ToolName: "go_test", ToolArgs: `{}`, Messages: `[{"role":"user"},{"role":"assistant"}]`},
	}
	for _, c := range cps {
		if err := s.AppendCheckpoint(c); err != nil {
			t.Fatalf("AppendCheckpoint: %v", err)
		}
	}
	last, err := s.LastAfterCheckpoint("t1")
	if err != nil {
		t.Fatalf("LastAfterCheckpoint: %v", err)
	}
	if last == nil || last.Seq != 2 || last.Phase != "after" || last.ToolResult != `{"ok":true}` {
		t.Fatalf("最后 after 检查点不符: %+v", last)
	}
	if none, _ := s.LastAfterCheckpoint("不存在"); none != nil {
		t.Fatalf("无检查点应返回 nil: %+v", none)
	}
}

func TestAsyncWriterDrain(t *testing.T) {
	s := openTestStore(t)
	w := NewAsyncWriter(s, 8)
	for i := 0; i < 5; i++ {
		w.Submit(func(st Store) error {
			return st.AppendTaskEvent("t1", "tool_call", `{"i":`+string(rune('0'+i))+`}`)
		})
	}
	w.Close()
	// Close 排空后事件应全部落库
	evs, err := s.TaskEvents("t1", 100)
	if err != nil {
		t.Fatalf("TaskEvents: %v", err)
	}
	if len(evs) != 5 {
		t.Fatalf("排空后应有 5 条事件，got %d", len(evs))
	}
}
