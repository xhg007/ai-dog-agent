package agent

// 回归测试：删除运行中的会话后，迟到的落盘不应把会话目录重新建出来。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/workspace"
)

func TestPersistDeletedSessionDoesNotResurrect(t *testing.T) {
	dir := t.TempDir()
	svc := &ChatService{store: newSessionStore(dir), sessions: make(map[string]*chatSession)}

	id := svc.NewSession()
	svc.mu.Lock()
	sess := svc.sessions[id]
	svc.mu.Unlock()

	if ok := svc.DeleteSession(id); !ok {
		t.Fatal("DeleteSession 返回 false")
	}
	if _, err := os.Stat(filepath.Join(dir, id)); !os.IsNotExist(err) {
		t.Fatalf("删除后仍存在会话目录: err=%v", err)
	}

	// 模拟运行中 goroutine 退出时的 defer：s.persistLocked(sess)
	svc.mu.Lock()
	svc.persistLocked(sess)
	svc.mu.Unlock()

	if _, err := os.Stat(filepath.Join(dir, id, "meta.json")); !os.IsNotExist(err) {
		t.Fatalf("已删除会话被迟到的落盘重建: err=%v", err)
	}
	if len(svc.ListSessions()) != 0 {
		t.Fatalf("删除后会话列表应为空: %+v", svc.ListSessions())
	}
}

// 正常会话的落盘仍应生效（防止上面的检查把持久化整体关掉）。
func TestPersistLiveSessionStillWorks(t *testing.T) {
	dir := t.TempDir()
	svc := &ChatService{store: newSessionStore(dir), sessions: make(map[string]*chatSession)}
	id := svc.NewSession()

	svc.mu.Lock()
	sess := svc.sessions[id]
	sess.messages = append(sess.messages, StoredMessage{Role: "user", Content: "hi"})
	svc.persistLocked(sess)
	svc.mu.Unlock()

	loaded, err := newSessionStore(dir).load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Messages) != 1 || loaded[0].Messages[0].Content != "hi" {
		t.Fatalf("正常会话落盘失败: %+v", loaded)
	}
}

// MCP 已接入：提示词不能再声称「M6 提供」，且要列出启用的 server（stdio 与远程）。
func TestPromptDataMCPWording(t *testing.T) {
	cfg, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Get()
	c.MCPServers = []config.MCPServerConfig{
		{ID: "m1", Name: "local-fs", Command: "npx", Args: []string{"-y", "srv"}, Enabled: true},
		{ID: "m2", Name: "remote", Transport: "http", URL: "https://example.com/mcp", Enabled: true},
		{ID: "m3", Name: "off", Command: "x", Enabled: false},
	}
	if err := cfg.Save(c); err != nil {
		t.Fatal(err)
	}

	svc := &ChatService{cfg: cfg, ws: workspace.New()}
	// 隔离真实 ~/.agents/skills：测试只验证 MCP 文案，不依赖本机技能内容。
	cfg.SetUserSkillsDir(t.TempDir())
	rendered, err := renderOrchestratorPrompt(svc.buildPromptData())
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(rendered, "M6") {
		t.Fatalf("MCP 文案仍在提示 M6: %s", rendered)
	}
	if !strings.Contains(rendered, "mcp_<server>_<tool>") {
		t.Fatalf("应说明 MCP 工具命名: %s", rendered)
	}
	if !strings.Contains(rendered, "local-fs") || !strings.Contains(rendered, "remote") {
		t.Fatalf("未列出启用的 server 名称: %s", rendered)
	}
	if strings.Contains(rendered, "https://example.com/mcp") || strings.Contains(rendered, "npx -y srv") {
		t.Fatalf("不应把 MCP 连接细节（可能含密钥）写入系统提示词: %s", rendered)
	}
	if strings.Contains(rendered, "- off") {
		t.Fatalf("未启用的 server 不应进入上下文: %s", rendered)
	}
}
