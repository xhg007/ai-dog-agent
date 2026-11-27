package config

// 回归测试：保存失败时必须回滚内存与版本，避免「保存失败但配置已生效」。

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveRollsBackOnPersistFailure(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerAt(dir)
	if err != nil {
		t.Fatalf("NewManagerAt: %v", err)
	}
	before := m.Version()

	// 让 persistLocked 失败：删掉配置目录后 rename 无法落盘
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	cfg := Config{
		Models: []ModelConfig{{
			ID: "m1", Name: "模型", Model: "test-model", APIKey: "sk-test",
		}},
		ActiveModelID: "m1",
	}
	if err := m.Save(cfg); err == nil {
		t.Fatal("目录不可写时 Save 应返回错误")
	}
	if got := m.Get(); len(got.Models) != 0 || got.ActiveModelID != "" {
		t.Fatalf("保存失败后内存配置应回滚，实际: %+v", got)
	}
	if m.Version() != before {
		t.Fatalf("保存失败不应递增版本: before=%d after=%d", before, m.Version())
	}
}

// 落盘成功时版本递增、配置生效（防止回滚逻辑把保存整体关掉）。
func TestSaveSucceedsAndBumpsVersion(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	before := m.Version()
	cfg := Config{
		Models: []ModelConfig{{
			ID: "m1", Name: "模型", Model: "test-model", APIKey: "sk-test",
		}},
		ActiveModelID: "m1",
	}
	if err := m.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if m.Version() != before+1 {
		t.Fatalf("保存成功应递增版本: before=%d after=%d", before, m.Version())
	}
	// 重新加载确认已落盘
	reloaded, err := NewManagerAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Get(); len(got.Models) != 1 || got.Models[0].ID != "m1" {
		t.Fatalf("配置未落盘: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "config.yml")); err != nil {
		t.Fatalf("config.yml 不存在: %v", err)
	}
}
