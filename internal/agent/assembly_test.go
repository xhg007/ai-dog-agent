package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/workspace"
)

// fakeModel 假模型：仅用于装配测试，不实际调用。
type fakeModel struct{}

func (f *fakeModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return nil, errors.New("fake model 不支持 Generate")
}

func (f *fakeModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("fake model 不支持 Stream")
}

func setupProjectWS(t *testing.T) *workspace.Manager {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.26\n")
	write("README.md", "# demo\n示例项目。\n")
	write("main.go", "package main\n\nfunc main() {\n\tdemo()\n}\n")
	write("demo.go", "package main\n\n// Demo 示例函数。\nfunc Demo() {}\n")

	ws := workspace.New()
	if err := ws.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestBuildProjectContext(t *testing.T) {
	ws := setupProjectWS(t)
	ctxText, err := BuildProjectContext(ws)
	if err != nil {
		t.Fatalf("BuildProjectContext 失败: %v", err)
	}
	for _, want := range []string{"demo", "main.go", "Demo"} {
		if !strings.Contains(ctxText, want) {
			t.Fatalf("项目上下文缺少 %q:\n%s", want, ctxText)
		}
	}
}

func TestBuildProjectContextNoWorkspace(t *testing.T) {
	ws := workspace.New()
	if _, err := BuildProjectContext(ws); err == nil {
		t.Fatal("未设置 workspace 应返回错误")
	}
}

func TestBuildExplorerAgent(t *testing.T) {
	ws := setupProjectWS(t)
	ag, err := BuildExplorer(context.Background(), ws, &fakeModel{})
	if err != nil {
		t.Fatalf("BuildExplorer 失败: %v", err)
	}
	if ag == nil {
		t.Fatal("explorer agent 为空")
	}
	if ag.Name(context.Background()) != "explorer" {
		t.Fatalf("explorer 名称不符: %s", ag.Name(context.Background()))
	}
}

func TestBuildOrchestratorWithExplorer(t *testing.T) {
	ws := setupProjectWS(t)
	ag, err := BuildOrchestrator(context.Background(), ws, &fakeModel{}, nil, "", "", nil, "", nil)
	if err != nil {
		t.Fatalf("BuildOrchestrator 失败: %v", err)
	}
	if ag == nil {
		t.Fatal("orchestrator agent 为空")
	}
}

func TestBuildOrchestratorWithWriteTools(t *testing.T) {
	ws := setupProjectWS(t)
	appr := approval.New(eventbus.New())
	ag, err := BuildOrchestrator(context.Background(), ws, &fakeModel{}, appr, t.TempDir(), "s-test", nil, "", nil)
	if err != nil {
		t.Fatalf("BuildOrchestrator(含写工具) 失败: %v", err)
	}
	if ag == nil {
		t.Fatal("orchestrator agent 为空")
	}
}

// TestOrchestratorToolsIncludeExecTools 主 Agent 工具集应包含执行类工具（M4）。
func TestOrchestratorToolsIncludeExecTools(t *testing.T) {
	ws := setupProjectWS(t)
	appr := approval.New(eventbus.New())
	toolList, err := OrchestratorTools(context.Background(), ws, &fakeModel{}, appr, t.TempDir(), "s-test", []string{"go", "git"}, nil)
	if err != nil {
		t.Fatalf("OrchestratorTools 失败: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range toolList {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info 失败: %v", err)
		}
		names[info.Name] = true
	}
	for _, want := range []string{"run_shell", "go_test", "go_vet", "go_build", "write_file", "edit_file"} {
		if !names[want] {
			t.Fatalf("主 Agent 工具集缺少 %s，实际: %v", want, names)
		}
	}
}
