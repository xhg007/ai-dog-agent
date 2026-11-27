package prompts

import (
	"strings"
	"testing"
)

func TestTemplatesEmbedded(t *testing.T) {
	names := Names()
	want := map[string]bool{
		"orchestrator.tmpl": false,
		"explorer.tmpl":     false,
		"smith_system.tmpl": false,
		"smith_user.tmpl":   false,
	}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Fatalf("缺少模板 %s，实际: %v", n, names)
		}
	}
}

func TestRenderOrchestrator(t *testing.T) {
	out, err := Render("orchestrator.tmpl", map[string]any{
		"ProjectContext": "\n\n# 当前项目\nworkspace: /tmp/demo",
		"Skills": []map[string]any{
			{"Name": "go-review", "Description": "审查", "Prompt": "只读"},
		},
		"MCPServers": []map[string]any{{"Name": "local-fs"}},
		"Dispatches": []map[string]any{{
			"Name": "dispatch_coder", "Description": "写代码",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"主 Agent", "当前项目", "go-review", "local-fs", "dispatch_coder", "写代码"} {
		if !strings.Contains(out, want) {
			t.Fatalf("渲染结果缺少 %q:\n%s", want, out)
		}
	}
}

func TestRenderExplorer(t *testing.T) {
	out, err := Render("explorer.tmpl", map[string]any{
		"ProjectContext": "\n\n# 当前项目\nworkspace: /tmp/demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"explorer", "当前项目"} {
		if !strings.Contains(out, want) {
			t.Fatalf("渲染结果缺少 %q:\n%s", want, out)
		}
	}
}

func TestRenderUnknownTemplate(t *testing.T) {
	if _, err := Render("missing.tmpl", nil); err == nil {
		t.Fatal("未知模板应返回错误")
	}
}

func TestRenderSmithTemplates(t *testing.T) {
	system, err := Render("smith_system.tmpl", map[string]any{
		"ToolNames":  "read_file, devtools",
		"ToolGroups": "devtools",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agent-smith", "read_file, devtools", "devtools"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system 缺少 %q:\n%s", want, system)
		}
	}

	user, err := Render("smith_user.tmpl", map[string]any{
		"SessionID":  "s1",
		"TaskID":     "",
		"Task":       nil,
		"Events":     []map[string]any{{"Type": "tool_call", "Payload": `{"x":1}`}},
		"AgentsJSON": "[]",
		"Tools": []map[string]any{
			{"Name": "read_file", "Group": "readonly", "Description": "读取文件"},
		},
		"Models": []map[string]any{
			{"ID": "m1", "Name": "Main", "Model": "x"},
		},
		"Skills": []map[string]any{
			{"ID": "s1", "Name": "review", "Description": "审查", "Enabled": true, "PromptSummary": "检查错误"},
		},
		"MCPServers": []map[string]any{
			{"ID": "m1", "Name": "fs", "Kind": "stdio", "Enabled": true},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"session: s1",
		"手动触发",
		"tool_call",
		"read_file [readonly]：读取文件",
		"m1 / Main / x",
		"指令摘要：检查错误",
		"fs（stdio，enabled=true）",
	} {
		if !strings.Contains(user, want) {
			t.Fatalf("user 缺少 %q:\n%s", want, user)
		}
	}
}
