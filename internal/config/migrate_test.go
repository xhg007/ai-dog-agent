package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigJSONMigratesToYAML 旧 config.json 首次加载时迁移为 config.yml 并保留 .bak。
func TestConfigJSONMigratesToYAML(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"models":[{"id":"m1","name":"M","model":"x"}],"activeModelId":"m1","skills":[],"mcpServers":[]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManagerAt(dir)
	if err != nil {
		t.Fatalf("加载: %v", err)
	}
	if got := m.Get().ActiveModelID; got != "m1" {
		t.Fatalf("迁移后配置不符: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yml")); err != nil {
		t.Fatalf("config.yml 未生成: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json.bak")); err != nil {
		t.Fatalf("旧配置未保留为 .bak: %v", err)
	}
	// 再次加载读取 YAML，不重复迁移
	if _, err := NewManagerAt(dir); err != nil {
		t.Fatalf("二次加载: %v", err)
	}
}

// TestProjectJSONMigratesToYAML 项目 main.json 自动迁移为 main.yml 并保留 .bak。
func TestProjectJSONMigratesToYAML(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".aidog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.json"),
		[]byte(`{"model":"m1","tools":["read_file"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pc, err := LoadProject(root)
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	if pc.Main.Model != "m1" || len(pc.Main.Tools) != 1 {
		t.Fatalf("迁移后内容不符: %+v", pc.Main)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.yml")); err != nil {
		t.Fatalf("main.yml 未生成: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.json.bak")); err != nil {
		t.Fatalf("旧 main.json 未保留为 .bak: %v", err)
	}
}

// TestEnsureProjectAgentIDMigratesLegacyFile 旧 .aidog/agent_id 迁移进 main.yml。
func TestEnsureProjectAgentIDMigratesLegacyFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".aidog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(dir, "agent_id"), []byte(id), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureProjectAgentID(root)
	if err != nil || got != id {
		t.Fatalf("EnsureProjectAgentID = %q, %v", got, err)
	}
	pc, err := LoadProject(root)
	if err != nil || pc.Main.AgentID != id {
		t.Fatalf("main.yml 中的 agent_id 不符: %+v, %v", pc, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "agent_id.bak")); err != nil {
		t.Fatalf("旧 agent_id 未保留为 .bak: %v", err)
	}
	// 稳定
	again, err := EnsureProjectAgentID(root)
	if err != nil || again != id {
		t.Fatalf("再次获取 ID 应稳定: %q, %v", again, err)
	}
}

// TestConfigYAMLFieldNames 落盘 YAML 使用与 JSON 一致的字段名。
func TestConfigYAMLFieldNames(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.Get()
	cfg.WorkspacePath = "x"
	if err := m.Save(cfg); err != nil {
		t.Fatalf("保存: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"activeModelId:", "workspacePath:", "mcpServers:", "shellWhitelist:"} {
		if !strings.Contains(s, want) {
			t.Fatalf("YAML 缺少字段 %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "activemodelid:") || strings.Contains(s, "workspacepath:") {
		t.Fatalf("YAML 字段名被转成小写:\n%s", s)
	}
}
