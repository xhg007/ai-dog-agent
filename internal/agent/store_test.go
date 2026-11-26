package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if _, err := os.Stat(filepath.Join(dir, "s1", "meta.json")); err != nil {
		t.Fatalf("会话元数据未落盘: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "s1", "messages.jsonl")); err != nil {
		t.Fatalf("消息日志未落盘: %v", err)
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
	if err := svc.restoreSessions(); err != nil {
		t.Fatalf("restoreSessions 失败: %v", err)
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

func readMessageLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取消息日志失败: %v", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// TestSessionStoreAppendIncremental 正常轮次只追加新增消息，重启后可恢复。
func TestSessionStoreAppendIncremental(t *testing.T) {
	dir := t.TempDir()
	st := newSessionStore(dir)
	rec := sessionRecord{ID: "s1", Title: "t", Messages: []StoredMessage{{Role: "user", Content: "a"}}}
	if err := st.save(rec); err != nil {
		t.Fatal(err)
	}
	rec.Messages = append(rec.Messages, StoredMessage{Role: "assistant", Content: "b"})
	if err := st.save(rec); err != nil {
		t.Fatal(err)
	}
	if lines := readMessageLines(t, filepath.Join(dir, "s1", "messages.jsonl")); len(lines) != 2 {
		t.Fatalf("应追加为 2 行，got %d: %v", len(lines), lines)
	}
	loaded, err := newSessionStore(dir).load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Messages) != 2 || loaded[0].Messages[1].Content != "b" {
		t.Fatalf("重启恢复不符: %+v", loaded)
	}
}

// TestSessionStoreReplace replace 后历史整体替换（变短也生效）。
func TestSessionStoreReplace(t *testing.T) {
	dir := t.TempDir()
	st := newSessionStore(dir)
	rec := sessionRecord{ID: "s1", Messages: []StoredMessage{
		{Role: "user", Content: "a"}, {Role: "assistant", Content: "b"}, {Role: "user", Content: "c"},
	}}
	if err := st.save(rec); err != nil {
		t.Fatal(err)
	}
	rec.Messages = []StoredMessage{{Role: "user", Content: "only"}}
	if err := st.replace(rec); err != nil {
		t.Fatal(err)
	}
	if lines := readMessageLines(t, filepath.Join(dir, "s1", "messages.jsonl")); len(lines) != 1 {
		t.Fatalf("replace 后应为 1 行，got %d", len(lines))
	}
	loaded, _ := st.load()
	if len(loaded) != 1 || len(loaded[0].Messages) != 1 || loaded[0].Messages[0].Content != "only" {
		t.Fatalf("replace 后历史不符: %+v", loaded)
	}
}

// TestSessionStoreMigratesLegacy 旧 <id>.json 自动迁移为目录格式并删除旧文件。
func TestSessionStoreMigratesLegacy(t *testing.T) {
	dir := t.TempDir()
	legacy := sessionRecord{
		ID: "old", AgentID: "a1", Title: "旧会话", CreatedAt: 1, LastActive: 2,
		Messages: []StoredMessage{{Role: "user", Content: "hi"}},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := newSessionStore(dir).load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Title != "旧会话" || len(loaded[0].Messages) != 1 {
		t.Fatalf("迁移结果不符: %+v", loaded)
	}
	if _, err := os.Stat(filepath.Join(dir, "old", "meta.json")); err != nil {
		t.Fatalf("迁移后元数据缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("迁移成功后旧文件应重命名: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.json.bak")); err != nil {
		t.Fatalf("迁移后应保留 .bak 备份: %v", err)
	}
	loaded2, err := newSessionStore(dir).load()
	if err != nil || len(loaded2) != 1 || len(loaded2[0].Messages) != 1 {
		t.Fatalf("迁移后重启恢复失败: %+v err=%v", loaded2, err)
	}
}
// TestSessionStoreDeleteCleansBackup 删除会话时一并清理迁移留下的 .bak。
func TestSessionStoreDeleteCleansBackup(t *testing.T) {
	dir := t.TempDir()
	legacy := sessionRecord{ID: "old", Title: "旧", Messages: []StoredMessage{{Role: "user", Content: "hi"}}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	st := newSessionStore(dir)
	if _, err := st.load(); err != nil {
		t.Fatal(err)
	}
	if err := st.delete("old"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.json.bak")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("删除会话应清理 .bak: %v", err)
	}
}