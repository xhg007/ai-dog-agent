package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestSessionStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := newSessionStore(dir)
	rec := sessionRecord{
		ID: "s1", AgentID: "a1", Title: "理解项目",
		CreatedAt: 1000, LastActive: 2000,
		Messages: []StoredMessage{
			{Role: "user", Content: "你好"},
			{Role: "tool", Tool: "read_file", Content: "line1"},
			{Role: "assistant", Content: "这是回复"},
		},
	}
	if err := st.save(rec); err != nil {
		t.Fatalf("save 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1.json")); err != nil {
		t.Fatalf("会话文件未落盘: %v", err)
	}

	loaded, err := st.load()
	if err != nil {
		t.Fatalf("load 失败: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("期望 1 条会话，实际 %d", len(loaded))
	}
	got := loaded[0]
	if got.ID != "s1" || got.Title != "理解项目" || got.AgentID != "a1" ||
		got.CreatedAt != 1000 || got.LastActive != 2000 {
		t.Fatalf("元数据回读不符: %+v", got)
	}
	if len(got.Messages) != 3 || got.Messages[1].Tool != "read_file" ||
		got.Messages[2].Content != "这是回复" {
		t.Fatalf("消息历史回读不符: %+v", got.Messages)
	}
}

func TestSessionStoreDeleteAndCorruptFile(t *testing.T) {
	dir := t.TempDir()
	st := newSessionStore(dir)
	if err := st.save(sessionRecord{ID: "keep", Title: "保留"}); err != nil {
		t.Fatal(err)
	}
	if err := st.save(sessionRecord{ID: "drop", Title: "删除"}); err != nil {
		t.Fatal(err)
	}
	if err := st.delete("drop"); err != nil {
		t.Fatalf("delete 失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.load()
	if err != nil {
		t.Fatalf("损坏文件不应导致整体失败: %v", err)
	}
	if len(loaded) != 1 || loaded[0].ID != "keep" {
		t.Fatalf("回读结果不符: %+v", loaded)
	}
}

func TestSessionStoreLoadEmptyDir(t *testing.T) {
	st := newSessionStore(t.TempDir())
	loaded, err := st.load()
	if err != nil {
		t.Fatalf("空目录 load 失败: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("空目录应返回空列表: %+v", loaded)
	}
}

func TestHistoryMessagesOnlyUserAssistant(t *testing.T) {
	sess := &chatSession{messages: []StoredMessage{
		{Role: "user", Content: "第一个问题"},
		{Role: "tool", Tool: "read_file", Content: "工具输出"},
		{Role: "assistant", Content: "第一个回答"},
		{Role: "error", Content: "出错了"},
		{Role: "user", Content: "第二个问题"},
	}}
	msgs := historyMessages(sess.messages)
	if len(msgs) != 3 {
		t.Fatalf("应只保留 user/assistant 共 3 条，实际 %d", len(msgs))
	}
	if msgs[0].Role != schema.User || msgs[0].Content != "第一个问题" {
		t.Fatalf("第 1 条不符: %+v", msgs[0])
	}
	if msgs[1].Role != schema.Assistant || msgs[1].Content != "第一个回答" {
		t.Fatalf("第 2 条不符: %+v", msgs[1])
	}
	if msgs[2].Role != schema.User || msgs[2].Content != "第二个问题" {
		t.Fatalf("第 3 条不符: %+v", msgs[2])
	}
}

func TestChatServiceRestoresSessionsFromDisk(t *testing.T) {
	dir := t.TempDir()
	st := newSessionStore(dir)
	if err := st.save(sessionRecord{
		ID: "restored", AgentID: "agent-x", Title: "上次的会话",
		CreatedAt: 111, LastActive: 222,
		Messages: []StoredMessage{{Role: "user", Content: "之前的问题"}},
	}); err != nil {
		t.Fatal(err)
	}

	svc := &ChatService{store: st, sessions: make(map[string]*chatSession)}
	if err := svc.RestoreSessions(); err != nil {
		t.Fatalf("RestoreSessions 失败: %v", err)
	}
	list := svc.ListSessions()
	if len(list) != 1 || list[0].ID != "restored" || list[0].Title != "上次的会话" {
		t.Fatalf("会话列表未恢复: %+v", list)
	}
	msgs := svc.GetMessages("restored")
	if len(msgs) != 1 || msgs[0].Content != "之前的问题" {
		t.Fatalf("消息历史未恢复: %+v", msgs)
	}
	if list[0].Running {
		t.Fatal("恢复的会话不应处于 running 状态")
	}
}
