package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
	"ai-dog-agent/internal/workspace"
)

// TestRebuildRecoveryMessages 断点恢复的消息重建：
// before 快照 + 一条 user 续跑指令（内含已完成的工具调用与结果，避免伪造 tool role 消息）。
func TestRebuildRecoveryMessages(t *testing.T) {
	before := &store.Checkpoint{
		Seq: 1, Phase: "before", ToolName: "edit_file",
		ToolArgs: `{"path":"a.go"}`,
		Messages: mustJSON(t, []StoredMessage{
			{Role: "user", Content: "修复 bug"},
			{Role: "assistant", Content: "我来修复"},
		}),
	}
	after := &store.Checkpoint{
		Seq: 2, Phase: "after", ToolName: "edit_file",
		ToolResult: "文件已修改",
	}

	msgs, err := rebuildRecoveryMessages(before, after)
	if err != nil {
		t.Fatalf("rebuildRecoveryMessages: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("应重建 3 条消息（2 快照历史 + 1 续跑指令），got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("快照历史不符: %+v", msgs[:2])
	}
	last := msgs[2]
	if last.Role != "user" {
		t.Fatalf("续跑指令应为 user: %+v", last)
	}
	for _, want := range []string{"edit_file", `{"path":"a.go"}`, "文件已修改"} {
		if !strings.Contains(last.Content, want) {
			t.Fatalf("续跑指令应包含 %q: %s", want, last.Content)
		}
	}
}

// TestRebuildRecoveryMessages_NoBefore 无 before 快照：仅续跑指令。
func TestRebuildRecoveryMessages_NoBefore(t *testing.T) {
	after := &store.Checkpoint{
		Seq: 2, Phase: "after", ToolName: "go_test", ToolResult: "all ok",
	}
	msgs, err := rebuildRecoveryMessages(nil, after)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("无快照应只有 1 条续跑指令: %+v", msgs)
	}
}

// TestRebuildRecoveryMessages_EmptySnapshot 快照为空 JSON 的边界。
func TestRebuildRecoveryMessages_EmptySnapshot(t *testing.T) {
	before := &store.Checkpoint{Seq: 1, Phase: "before", ToolName: "x", Messages: "[]"}
	after := &store.Checkpoint{Seq: 2, Phase: "after", ToolName: "x", ToolResult: "r"}
	msgs, err := rebuildRecoveryMessages(before, after)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("空快照应只有续跑指令，got %d", len(msgs))
	}
}

// TestRecoverTaskIntegration 端到端（不含模型调用）：RecoverTask 对不存在任务报错、
// 对无断点任务报错。
func TestRecoverTaskIntegration(t *testing.T) {
	s := newRecoveryTestService(t)
	if err := s.RecoverTask("no-such-task"); err == nil {
		t.Fatal("不存在的任务应报错")
	}
	// 有任务无 checkpoint
	taskID, err := s.tasks.Start("s-rt", "无断点任务")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	s.tasks.Finish("s-rt", taskID, store.StatusInterrupted, "")
	if err := s.RecoverTask(taskID); err == nil {
		t.Fatal("无断点任务应报错")
	}
}

// newRecoveryTestService 构造带 DuckDB 任务层的 ChatService（不触发模型调用）。
func newRecoveryTestService(t *testing.T) *ChatService {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "rec.duckdb"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	w := store.NewAsyncWriter(st, 32)
	t.Cleanup(w.Close)
	bus := eventbus.New()
	tm := task.New(st, w, bus)
	cfg, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return NewChatService(cfg, workspace.New(), bus, nil, "", "", tm)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
