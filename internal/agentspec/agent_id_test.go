package agentspec

// 项目级 Agent 稳定 ID（M5 追加）：
// 1) 每个子 Agent 配置文件（.aidog/agents/*.yml）含唯一不变 agent_id
//   （UUID 去连字符），保存时自动生成、加载时对缺失项迁移固化；
// 2) 项目主 Agent ID 固化在 .aidog/main.yml 的 agent_id——同一项目恒定，
//   供应商侧（限流归因/上下文缓存）只有相同 agent_id 才能复用。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validAgentID(id string) bool {
	return len(id) == 32 && !strings.Contains(id, "-")
}

// TestSaveGeneratesAgentID 保存时缺省 agent_id 自动生成并固化。
func TestSaveGeneratesAgentID(t *testing.T) {
	root := t.TempDir()
	spec := Spec{Name: "reviewer", SystemPrompt: "审查代码"}
	if err := Save(root, spec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	list, err := List(root)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v, n=%d", err, len(list))
	}
	id := list[0].AgentID
	if !validAgentID(id) {
		t.Fatalf("agent_id 应为 32 位无连字符 UUID: %q", id)
	}
	// 已固化到文件
	data, _ := os.ReadFile(filepath.Join(Dir(root), "reviewer.yml"))
	if !strings.Contains(string(data), id) {
		t.Fatalf("agent_id 未固化到配置文件: %s", data)
	}
	// 再次保存（带上同一 ID）不变
	list[0].SystemPrompt = "审查代码 v2"
	if err := Save(root, list[0]); err != nil {
		t.Fatalf("Save2: %v", err)
	}
	list2, _ := List(root)
	if list2[0].AgentID != id {
		t.Fatalf("再次保存后 agent_id 不应变化: %q != %q", list2[0].AgentID, id)
	}
}

// TestListMigratesMissingAgentID 手写配置缺 agent_id：List 迁移生成并写回文件。
func TestListMigratesMissingAgentID(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	hand := `{"name":"legacy","system_prompt":"旧配置","description":"无 agent_id"}`
	if err := os.WriteFile(filepath.Join(Dir(root), "legacy.json"), []byte(hand), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := List(root)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v, n=%d", err, len(list))
	}
	id := list[0].AgentID
	if !validAgentID(id) {
		t.Fatalf("迁移后 agent_id 不符: %q", id)
	}
	// 已迁移为 YAML 并写回固化；旧 JSON 保留为 .bak
	data, err := os.ReadFile(filepath.Join(Dir(root), "legacy.yml"))
	if err != nil || !strings.Contains(string(data), id) {
		t.Fatalf("迁移结果未写回 YAML: %s, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(Dir(root), "legacy.json.bak")); err != nil {
		t.Fatalf("旧 JSON 未保留为 .bak: %v", err)
	}
	// 再次 List 稳定不变
	list2, _ := List(root)
	if list2[0].AgentID != id {
		t.Fatal("agent_id 应稳定不变")
	}
}

// TestSaveEditKeepsAgentID 编辑保存不带 agent_id（前端表单场景）：沿用已有 ID。
func TestSaveEditKeepsAgentID(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, Spec{Name: "x", SystemPrompt: "v1"}); err != nil {
		t.Fatal(err)
	}
	first, _ := List(root)
	id := first[0].AgentID
	if err := Save(root, Spec{Name: "x", SystemPrompt: "v2"}); err != nil {
		t.Fatal(err)
	}
	list, _ := List(root)
	if list[0].AgentID != id {
		t.Fatalf("编辑保存不应更换 agent_id: %q != %q", list[0].AgentID, id)
	}
	if list[0].SystemPrompt != "v2" {
		t.Fatal("编辑内容未生效")
	}
}
