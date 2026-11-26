package agent

import (
	"context"
	"os"
	"testing"

	"ai-dog-agent/internal/agentspec"
)

// registerWith 用脚本化模型注册一个带完整 Spec 的 work agent。
func registerWith(t *testing.T, r *Registry, sessionID string, spec agentspec.Spec, reply string) *agentSession {
	t.Helper()
	return r.Register(sessionID, &WorkAgent{
		Agent: scriptedAgent(t, "dispatch_"+spec.AgentID, &streamScriptModel{chunks: []string{reply}}),
		Spec:  spec,
	})
}

func persistSpec() agentspec.Spec {
	return agentspec.Spec{
		AgentID:      "a1",
		Name:         "coder",
		SystemPrompt: "你是编码 Agent",
		Tools:        []string{"read_file"},
	}
}

// TestAgentChatHistoryRestoredAcrossRestart 私聊历史落盘后，新 Registry 注册同配置可恢复。
func TestAgentChatHistoryRestoredAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	first := NewRegistry()
	first.SetChatStore(dir)
	as := registerWith(t, first, "s1", persistSpec(), "coder reply")
	if _, err := first.SendMessage(context.Background(), "s1", "orchestrator", as, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	// 模拟重启：新 Registry 从同一目录注册
	second := NewRegistry()
	second.SetChatStore(dir)
	restored := registerWith(t, second, "s1", persistSpec(), "coder reply")
	if len(restored.history) != 2 {
		t.Fatalf("应恢复 2 条历史，实际 %d", len(restored.history))
	}
	if got := restored.history[0].Content; got != "hello" {
		t.Fatalf("用户消息不符: %q", got)
	}
	if got := restored.history[1].Content; got != "coder reply" {
		t.Fatalf("助手消息不符: %q", got)
	}
}

// TestAgentChatHistoryDroppedWhenSpecChanges 配置指纹变化时丢弃旧历史并清理文件。
func TestAgentChatHistoryDroppedWhenSpecChanges(t *testing.T) {
	dir := t.TempDir()
	first := NewRegistry()
	first.SetChatStore(dir)
	as := registerWith(t, first, "s1", persistSpec(), "old reply")
	if _, err := first.SendMessage(context.Background(), "s1", "orchestrator", as, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	changed := persistSpec()
	changed.SystemPrompt = "你是审查 Agent"
	second := NewRegistry()
	second.SetChatStore(dir)
	restored := registerWith(t, second, "s1", changed, "new reply")
	if len(restored.history) != 0 {
		t.Fatalf("配置变化后历史应丢弃，实际 %d 条", len(restored.history))
	}
	if _, err := os.Stat(second.chats.messagesPath("s1", "a1")); !os.IsNotExist(err) {
		t.Fatalf("旧历史文件应被删除，stat err=%v", err)
	}
}

// TestAgentChatHistoryLimitPersisted 落盘后再恢复仍保留最近 maxHistoryMessages 条。
func TestAgentChatHistoryLimitPersisted(t *testing.T) {
	dir := t.TempDir()
	first := NewRegistry()
	first.SetChatStore(dir)
	as := registerWith(t, first, "s1", persistSpec(), "ok")
	for i := 0; i < maxHistoryMessages+10; i++ {
		if _, err := first.SendMessage(context.Background(), "s1", "orchestrator", as, "msg"); err != nil {
			t.Fatalf("SendMessage: %v", err)
		}
	}
	if len(as.history) != maxHistoryMessages {
		t.Fatalf("内存历史应截断到 %d，实际 %d", maxHistoryMessages, len(as.history))
	}

	second := NewRegistry()
	second.SetChatStore(dir)
	restored := registerWith(t, second, "s1", persistSpec(), "ok")
	if len(restored.history) != maxHistoryMessages {
		t.Fatalf("恢复后仍应保留 %d 条，实际 %d", maxHistoryMessages, len(restored.history))
	}
}

// TestAgentChatHistoryRemovedWithSession 删除会话时一并删除私聊历史目录。
func TestAgentChatHistoryRemovedWithSession(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	r.SetChatStore(dir)
	as := registerWith(t, r, "s1", persistSpec(), "ok")
	if _, err := r.SendMessage(context.Background(), "s1", "orchestrator", as, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	r.RemoveSession("s1")
	if _, err := os.Stat(r.chats.chatsDir("s1")); !os.IsNotExist(err) {
		t.Fatalf("会话删除后私聊历史目录应不存在，stat err=%v", err)
	}
}

// TestAgentChatHistoryClearedOnReregister 配置重建（同会话再次注册）清空内存与磁盘历史。
func TestAgentChatHistoryClearedOnReregister(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry()
	r.SetChatStore(dir)
	as := registerWith(t, r, "s1", persistSpec(), "ok")
	if _, err := r.SendMessage(context.Background(), "s1", "orchestrator", as, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	fresh := registerWith(t, r, "s1", persistSpec(), "ok")
	if len(fresh.history) != 0 {
		t.Fatalf("重新注册应清空内存历史，实际 %d 条", len(fresh.history))
	}
	if _, err := os.Stat(r.chats.messagesPath("s1", "a1")); !os.IsNotExist(err) {
		t.Fatalf("重新注册应删除磁盘历史，stat err=%v", err)
	}
}
