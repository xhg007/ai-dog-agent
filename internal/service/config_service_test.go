package service

import (
	"testing"

	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/workspace"
)

// 设置页全量保存不携带工作区/信任字段时，SaveConfig 必须保留现有值，
// 否则保存一次模型配置就会清空工作区与项目信任。
func TestSaveConfigPreservesWorkspaceAndTrust(t *testing.T) {
	mgr, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	cfg := mgr.Get()
	cfg.WorkspacePath = `C:\proj`
	cfg.TrustedProjects = []string{`C:\proj`}
	cfg.BlockedProjects = []string{`C:\bad`}
	if err := mgr.Save(cfg); err != nil {
		t.Fatalf("保存初始配置: %v", err)
	}

	svc := NewConfigService(mgr, workspace.New())
	// 模拟前端设置页保存：只带模型，不带 workspacePath/trustedProjects
	next := config.Config{
		Models:        []config.ModelConfig{{ID: "m1", Name: "M", Model: "x"}},
		ActiveModelID: "m1",
	}
	if err := svc.SaveConfig(next); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got := mgr.Get()
	if got.WorkspacePath != `C:\proj` {
		t.Fatalf("workspacePath 被清空: %q", got.WorkspacePath)
	}
	if len(got.TrustedProjects) != 1 || got.TrustedProjects[0] != `C:\proj` {
		t.Fatalf("trustedProjects 被清空: %+v", got.TrustedProjects)
	}
	if len(got.BlockedProjects) != 1 || got.BlockedProjects[0] != `C:\bad` {
		t.Fatalf("blockedProjects 被清空: %+v", got.BlockedProjects)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "m1" {
		t.Fatalf("模型未保存: %+v", got.Models)
	}
}

// TestGetConfigIncludesToolCatalog 设置页可从 GetConfig 获取工具目录。
func TestGetConfigIncludesToolCatalog(t *testing.T) {
	mgr, err := config.NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	mgr.SetUserSkillsDir(t.TempDir()) // 隔离真实 ~/.agents/skills
	svc := NewConfigService(mgr, workspace.New())
	cfg := svc.GetConfig()
	if len(cfg.ToolCatalog) == 0 {
		t.Fatal("ToolCatalog 不应为空")
	}
	found := false
	for _, opt := range cfg.ToolCatalog {
		if opt.Name == "dev_validate_go_code" && opt.Description != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("工具目录缺少 dev_validate_go_code: %+v", cfg.ToolCatalog)
	}
}
