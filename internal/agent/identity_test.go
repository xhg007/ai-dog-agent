package agent

// 主 Agent 稳定 ID 测试：随项目固化、跨调用稳定、项目间隔离。

import (
	"os"
	"path/filepath"
	"testing"

	"ai-dog-agent/internal/agentspec"
)

func TestAgentIDForFollowsProject(t *testing.T) {
	proj := t.TempDir()
	id1 := agentIDFor(proj, t.TempDir())
	if !agentspec.IsValidAgentID(id1) {
		t.Fatalf("ID 格式不符: %q", id1)
	}
	// 同一项目稳定
	if id2 := agentIDFor(proj, t.TempDir()); id2 != id1 {
		t.Fatalf("同一项目 ID 应稳定: %q != %q", id2, id1)
	}
	// 已固化到项目文件
	if b, err := os.ReadFile(agentspec.AgentIDFile(proj)); err != nil || string(b) != id1 {
		t.Fatalf("项目 agent_id 文件不符: %s, %v", b, err)
	}
	// 不同项目不同 ID
	if id3 := agentIDFor(t.TempDir(), t.TempDir()); id3 == id1 {
		t.Fatal("不同项目应生成不同 ID")
	}
}

func TestAgentIDForFallsBackToAppDir(t *testing.T) {
	app := t.TempDir()
	a1 := agentIDFor("", app)
	if !agentspec.IsValidAgentID(a1) {
		t.Fatalf("应用级 ID 格式不符: %q", a1)
	}
	if a2 := agentIDFor("", app); a2 != a1 {
		t.Fatalf("应用级 ID 应稳定: %q != %q", a2, a1)
	}
	if _, err := os.Stat(filepath.Join(app, "agent_id")); err != nil {
		t.Fatalf("应用级 agent_id 文件未固化: %v", err)
	}
}

func TestAgentIDForNoDirGeneratesValid(t *testing.T) {
	if id := agentIDFor("", ""); !agentspec.IsValidAgentID(id) {
		t.Fatalf("兜底 ID 格式不符: %q", id)
	}
}
