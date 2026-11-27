package agentspec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveListRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := Spec{
		Name:         "reviewer",
		Description:  "代码审查子 Agent",
		SystemPrompt: "你是代码审查专家，只读不改。",
		Model:        "deepseek-chat",
		Tools:        []string{"read_file", "search_code"},
		Skills:       []string{"go-review"},
		MCPServers:   []string{"local-fs"},
		MaxTurns:     12,
	}
	if err := Save(root, want); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".aidog", "agents", "reviewer.yml")); err != nil {
		t.Fatalf("配置文件未落盘: %v", err)
	}

	list, err := List(root)
	if err != nil {
		t.Fatalf("List 失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("期望 1 个配置，实际 %d", len(list))
	}
	got := list[0]
	if got.Name != want.Name || got.Description != want.Description ||
		got.SystemPrompt != want.SystemPrompt || got.Model != want.Model || got.MaxTurns != want.MaxTurns {
		t.Fatalf("回读内容不符: %+v", got)
	}
	if len(got.Tools) != 2 || len(got.Skills) != 1 || len(got.MCPServers) != 1 {
		t.Fatalf("列表字段丢失: %+v", got)
	}
}

func TestListSortedAndEmptyDir(t *testing.T) {
	root := t.TempDir()
	list, err := List(root)
	if err != nil {
		t.Fatalf("空目录 List 不应报错: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("空目录应返回空列表，实际 %d", len(list))
	}
	for _, name := range []string{"zeta", "alpha"} {
		if err := Save(root, Spec{Name: name, SystemPrompt: "p"}); err != nil {
			t.Fatal(err)
		}
	}
	list, err = List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zeta" {
		t.Fatalf("未按名称排序: %+v", list)
	}
}

func TestSaveRejectsInvalidNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"", "../escape", "a/b", "a\\b", "  "} {
		if err := Save(root, Spec{Name: name, SystemPrompt: "p"}); err == nil {
			t.Fatalf("非法名称 %q 应被拒绝", name)
		}
	}
	outside := filepath.Join(root, "escape.json")
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("路径穿越写出了工作区外文件")
	}
}

func TestSaveRequiresSystemPromptAndDelete(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, Spec{Name: "empty"}); err == nil {
		t.Fatal("缺少 system_prompt 应被拒绝")
	}
	if err := Save(root, Spec{Name: "temp", SystemPrompt: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := Delete(root, "temp"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("删除后仍有配置: %+v", list)
	}
	if err := Delete(root, "temp"); err == nil {
		t.Fatal("删除不存在的配置应报错")
	}
}
