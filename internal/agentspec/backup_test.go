package agentspec

import (
	"os"
	"path/filepath"
	"testing"
)

// TestValidate 合法/非法 Spec 校验。
func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		ok   bool
	}{
		{"empty-name", Spec{SystemPrompt: "x"}, false},
		{"empty-prompt", Spec{Name: "ok"}, false},
		{"negative-budget", Spec{Name: "ok", SystemPrompt: "x", Budget: Budget{MaxSteps: -1}}, false},
		{"bad-threshold", Spec{Name: "ok", SystemPrompt: "x", Context: ContextConfig{CompressThreshold: 1.5}}, false},
		{"valid", Spec{Name: "ok", SystemPrompt: "x"}, true},
	}
	for _, c := range cases {
		err := Validate(c.spec)
		if c.ok && err != nil {
			t.Fatalf("%s: 应通过校验: %v", c.name, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("%s: 应校验失败", c.name)
		}
	}
}

// TestBackupMissingSpec 配置不存在时不产生备份。
func TestBackupMissingSpec(t *testing.T) {
	root := t.TempDir()
	p, err := Backup(root, "missing")
	if err != nil || p != "" {
		t.Fatalf("不存在的配置应返回空路径: path=%q err=%v", p, err)
	}
}

// TestBackupRestoreAndRetention 备份可恢复，且同名备份只保留最近 10 份。
func TestBackupRestoreAndRetention(t *testing.T) {
	root := t.TempDir()
	spec := Spec{Name: "coder", Description: "d", SystemPrompt: "v1"}
	if err := Save(root, spec); err != nil {
		t.Fatalf("Save v1: %v", err)
	}
	first, err := Backup(root, "coder")
	if err != nil || first == "" {
		t.Fatalf("Backup: path=%q err=%v", first, err)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}

	// 改到 v2，再产生一批备份；最新备份内容应为 v2。
	spec.SystemPrompt = "v2"
	if err := Save(root, spec); err != nil {
		t.Fatalf("Save v2: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := Backup(root, "coder"); err != nil {
			t.Fatalf("第 %d 次 Backup: %v", i+1, err)
		}
	}
	backups, err := ListBackups(root, "coder")
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != backupLimit {
		t.Fatalf("应保留 %d 份备份，实际 %d", backupLimit, len(backups))
	}
	latest := backups[0]

	// 改到 v3，再用最新备份恢复 → v2。
	spec.SystemPrompt = "v3"
	if err := Save(root, spec); err != nil {
		t.Fatalf("Save v3: %v", err)
	}
	if err := Restore(root, "coder", latest.Path); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	specs, err := List(root)
	if err != nil || len(specs) != 1 {
		t.Fatalf("List: %v %+v", err, specs)
	}
	if specs[0].SystemPrompt != "v2" {
		t.Fatalf("恢复后内容应为 v2，实际 %q", specs[0].SystemPrompt)
	}
}

// TestRestoreRejectsOutsidePath 备份路径必须位于 .backup 目录内。
func TestRestoreRejectsOutsidePath(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, Spec{Name: "coder", SystemPrompt: "x"}); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "coder-20200101-000000.000000000.yml")
	if err := os.WriteFile(outside, []byte(`{"name":"coder","system_prompt":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restore(root, "coder", outside); err == nil {
		t.Fatal("目录外备份应被拒绝")
	}
}

// TestRestoreNameMismatch 备份不属于目标 Agent 时拒绝。
func TestRestoreNameMismatch(t *testing.T) {
	root := t.TempDir()
	if err := Save(root, Spec{Name: "coder", SystemPrompt: "x"}); err != nil {
		t.Fatal(err)
	}
	backup, err := Backup(root, "coder")
	if err != nil || backup == "" {
		t.Fatalf("Backup: %v", err)
	}
	if err := Save(root, Spec{Name: "reviewer", SystemPrompt: "y"}); err != nil {
		t.Fatal(err)
	}
	if err := Restore(root, "reviewer", backup); err == nil {
		t.Fatal("备份与目标 Agent 不一致时应拒绝")
	}
}
