package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeLegacyConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(content), 0o600); err != nil {
		t.Fatalf("写入旧配置: %v", err)
	}
}

func readDiskConfig(t *testing.T, dir string) Config {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatalf("读取配置: %v", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	return cfg
}

// 旧配置（每个模型自带 baseURL/apiKey）加载时按 baseURL+apiKey 归并为供应商，
// 模型只保留 providerId，磁盘上不再重复存连接字段。
func TestLoadMigratesLegacyModelsToProviders(t *testing.T) {
	dir := t.TempDir()
	writeLegacyConfig(t, dir, `{
  "models": [
    {"id":"m1","name":"DS Chat","baseURL":"https://api.deepseek.com/v1","apiKey":"sk-1","model":"deepseek-chat"},
    {"id":"m2","name":"DS Reasoner","baseURL":"https://api.deepseek.com/v1","apiKey":"sk-1","model":"deepseek-reasoner"},
    {"id":"m3","name":"Other","baseURL":"https://example.com/v1","apiKey":"sk-2","model":"other"}
  ],
  "activeModelId": "m1",
  "skills": [],
  "mcpServers": []
}`)

	m, err := NewManagerAt(dir)
	if err != nil {
		t.Fatalf("加载配置: %v", err)
	}
	cfg := m.Get()
	if len(cfg.Providers) != 2 {
		t.Fatalf("供应商数量 = %d，期望 2（相同 baseURL+key 应合并）", len(cfg.Providers))
	}
	if cfg.Models[0].ProviderID == "" || cfg.Models[0].ProviderID != cfg.Models[1].ProviderID {
		t.Fatalf("共享同一供应商的模型应指向同一 providerId: %q / %q",
			cfg.Models[0].ProviderID, cfg.Models[1].ProviderID)
	}
	if cfg.Models[2].ProviderID == cfg.Models[0].ProviderID {
		t.Fatal("不同 baseURL 的模型不应合并到同一供应商")
	}
	if cfg.Models[0].BaseURL != "https://api.deepseek.com/v1" || cfg.Models[0].APIKey != "sk-1" {
		t.Fatalf("运行时未填充供应商连接信息: baseURL=%q apiKey=%q",
			cfg.Models[0].BaseURL, cfg.Models[0].APIKey)
	}

	disk := readDiskConfig(t, dir)
	if len(disk.Providers) != 2 {
		t.Fatalf("磁盘供应商数量 = %d，期望 2", len(disk.Providers))
	}
	for _, mc := range disk.Models {
		if mc.BaseURL != "" || mc.APIKey != "" {
			t.Fatalf("磁盘上不应重复保存连接字段: %+v", mc)
		}
	}
	if disk.Providers[0].Name != "api.deepseek.com" {
		t.Fatalf("供应商默认名 = %q，期望 api.deepseek.com", disk.Providers[0].Name)
	}

	// 幂等：再次加载不应重新生成供应商或改变 providerId
	m2, err := NewManagerAt(dir)
	if err != nil {
		t.Fatalf("二次加载: %v", err)
	}
	cfg2 := m2.Get()
	if len(cfg2.Providers) != 2 || cfg2.Models[0].ProviderID != cfg.Models[0].ProviderID {
		t.Fatal("重复加载后供应商结构发生变化（迁移不幂等）")
	}
}

// 保存供应商模型时磁盘只存 providerId；读取时自动填充连接信息。
func TestSaveProviderModelsStripsConnectionFields(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerAt(dir)
	if err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	cfg := m.Get()
	cfg.Providers = []ProviderConfig{{
		ID: "p1", Name: "Test", BaseURL: "https://api.test/v1", APIKey: "sk-test",
	}}
	cfg.Models = []ModelConfig{{
		ID: "m1", ProviderID: "p1", Name: "M", Model: "test-model",
	}}
	cfg.ActiveModelID = "m1"
	if err := m.Save(cfg); err != nil {
		t.Fatalf("保存配置: %v", err)
	}

	disk := readDiskConfig(t, dir)
	if len(disk.Models) != 1 || disk.Models[0].BaseURL != "" || disk.Models[0].APIKey != "" {
		t.Fatalf("磁盘模型应只保留 providerId: %+v", disk.Models)
	}

	got := m.Get()
	if got.Models[0].BaseURL != "https://api.test/v1" || got.Models[0].APIKey != "sk-test" {
		t.Fatalf("读取时未填充连接信息: %+v", got.Models[0])
	}
	active, err := m.ActiveModel()
	if err != nil {
		t.Fatalf("ActiveModel: %v", err)
	}
	if active.BaseURL != "https://api.test/v1" || active.APIKey != "sk-test" {
		t.Fatalf("ActiveModel 未填充连接信息: %+v", active)
	}
}

func TestSaveRejectsDanglingProvider(t *testing.T) {
	m, err := NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	cfg := m.Get()
	cfg.Models = []ModelConfig{{ID: "m1", Name: "M", Model: "x", ProviderID: "missing"}}
	if err := m.Save(cfg); err == nil {
		t.Fatal("引用不存在的供应商时应拒绝保存")
	}
}

func TestSaveRejectsProviderWithoutBaseURL(t *testing.T) {
	m, err := NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatalf("创建配置: %v", err)
	}
	cfg := m.Get()
	cfg.Providers = []ProviderConfig{{ID: "p1", Name: "No URL"}}
	if err := m.Save(cfg); err == nil {
		t.Fatal("供应商缺少 baseURL 时应拒绝保存")
	}
}
