package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/workspace"
)

func newCatalogWS(t *testing.T) *workspace.Manager {
	t.Helper()
	ws := workspace.New()
	if err := ws.SetRoot(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return ws
}

func catalogToolNames(t *testing.T, list []tool.BaseTool) []string {
	t.Helper()
	names := make([]string, 0, len(list))
	for _, tl := range list {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		names = append(names, info.Name)
	}
	return names
}

// TestCatalog_EmptyDefaultsReadOnly 空工具配置 → 默认只读集合。
func TestCatalog_EmptyDefaultsReadOnly(t *testing.T) {
	ws := newCatalogWS(t)
	list, err := Catalog(context.Background(), ws, CatalogDeps{}, nil)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	names := catalogToolNames(t, list)
	want := []string{ToolReadFile, ToolListFiles, ToolSearchCode, ToolSearchSymbols, ToolGitDiff, ToolGitStatus}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("默认只读集合不符: %v", names)
	}
}

// TestCatalog_WriteRequiresApproval 显式声明写工具但无审批管理器时报错。
func TestCatalog_WriteRequiresApproval(t *testing.T) {
	ws := newCatalogWS(t)
	if _, err := Catalog(context.Background(), ws, CatalogDeps{}, []string{ToolWriteFile}); err == nil {
		t.Fatal("无审批管理器时写工具应报错")
	}
	appr := approval.New(eventbus.New())
	list, err := Catalog(context.Background(), ws, CatalogDeps{
		Approval: appr, BackupDir: t.TempDir(), SessionID: "s1",
	}, []string{ToolWriteFile, ToolReadFile})
	if err != nil {
		t.Fatalf("Catalog(write): %v", err)
	}
	names := catalogToolNames(t, list)
	if len(names) != 2 || names[0] != ToolWriteFile || names[1] != ToolReadFile {
		t.Fatalf("显式工具顺序不符: %v", names)
	}
}

// TestCatalog_ExplicitExecTools 执行类工具按需构建（devtools / run_shell）。
func TestCatalog_ExplicitExecTools(t *testing.T) {
	ws := newCatalogWS(t)
	list, err := Catalog(context.Background(), ws, CatalogDeps{Whitelist: []string{"go"}},
		[]string{ToolDevTreeDir, ToolRunShell})
	if err != nil {
		t.Fatalf("Catalog(exec): %v", err)
	}
	names := catalogToolNames(t, list)
	want := []string{ToolDevTreeDir, ToolRunShell}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("执行工具集合不符: %v", names)
	}
}

// TestCatalog_UnknownTool 未知工具名显式报错并列出可用工具。
func TestCatalog_UnknownTool(t *testing.T) {
	ws := newCatalogWS(t)
	_, err := Catalog(context.Background(), ws, CatalogDeps{}, []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "未知工具名") {
		t.Fatalf("未知工具名应报错: %v", err)
	}
	if !strings.Contains(err.Error(), ToolReadFile) {
		t.Fatalf("错误信息应列出可用工具: %v", err)
	}
}

// TestToolOptionsCoverKnownTools 工具目录覆盖名录中的每个工具，且描述/分组完整。
func TestToolOptionsCoverKnownTools(t *testing.T) {
	opts := ToolOptions()
	byName := make(map[string]ToolOption, len(opts))
	for _, o := range opts {
		if o.Description == "" || o.Group == "" {
			t.Fatalf("工具目录项缺少描述或分组: %+v", o)
		}
		if _, dup := byName[o.Name]; dup {
			t.Fatalf("工具目录项重复: %s", o.Name)
		}
		byName[o.Name] = o
	}
	names := KnownToolNames()
	if len(opts) != len(names) {
		t.Fatalf("工具目录数量与 KnownToolNames 不一致: %d vs %d", len(opts), len(names))
	}
	for _, n := range names {
		if _, ok := byName[n]; !ok {
			t.Fatalf("工具 %s 缺少目录项", n)
		}
	}
}
