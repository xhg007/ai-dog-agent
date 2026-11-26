package prompts

import (
	"strings"
	"testing"
)

func TestTemplatesEmbedded(t *testing.T) {
	names := Names()
	want := map[string]bool{"orchestrator.tmpl": false, "explorer.tmpl": false}
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
		"Agents": []map[string]any{{
			"Name": "coder", "Description": "写代码",
			"Tools": []string{"read_file"}, "Skills": []string{"go-review"},
			"MCPServers": []string{"local-fs"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"主 Agent", "当前项目", "go-review", "local-fs", "coder", "read_file"} {
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
