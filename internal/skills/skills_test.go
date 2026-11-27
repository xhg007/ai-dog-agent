package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFile(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, SkillFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFileFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "go-review",
		"---\nname: Go 审查\ndescription: 检查错误处理\nenabled: false\n---\n\n# 指令\n检查错误处理与并发安全。\n")

	s, err := ParseFile(filepath.Join(dir, "go-review", SkillFile), "go-review")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if s.ID != "go-review" || s.Name != "Go 审查" || s.Description != "检查错误处理" {
		t.Fatalf("字段不符: %+v", s)
	}
	if s.Enabled {
		t.Fatal("enabled: false 应禁用")
	}
	if !strings.Contains(s.Prompt, "检查错误处理与并发安全") || strings.Contains(s.Prompt, "name:") {
		t.Fatalf("正文不应包含 frontmatter: %q", s.Prompt)
	}
}

func TestParseFileNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "quick", "# 快速开始\n\n正文内容\n")

	s, err := ParseFile(filepath.Join(dir, "quick", SkillFile), "quick")
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if s.ID != "quick" || s.Name != "quick" {
		t.Fatalf("缺省 ID/Name 应回退目录名: %+v", s)
	}
	if s.Description != "快速开始" {
		t.Fatalf("描述应取正文首行: %q", s.Description)
	}
	if !s.Enabled {
		t.Fatal("默认应启用")
	}
}

func TestParseFileInvalidFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "bad", "---\nname: [\n---\nbody\n")
	if _, err := ParseFile(filepath.Join(dir, "bad", SkillFile), "bad"); err == nil {
		t.Fatal("非法 YAML 应返回错误")
	}
}

func TestParseFileMissingClosingFence(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "unclosed", "---\nname: x\nbody\n")
	if _, err := ParseFile(filepath.Join(dir, "unclosed", SkillFile), "unclosed"); err == nil {
		t.Fatal("缺少结束 --- 应返回错误")
	}
}

func TestParseFileEmptyBody(t *testing.T) {
	dir := t.TempDir()
	writeSkillFile(t, dir, "empty", "---\nname: 空\n---\n\n")
	if _, err := ParseFile(filepath.Join(dir, "empty", SkillFile), "empty"); err == nil {
		t.Fatal("空正文应返回错误")
	}
}

func TestParseFileSizeLimit(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("a", MaxFileSize+1)
	writeSkillFile(t, dir, "big", big)
	if _, err := ParseFile(filepath.Join(dir, "big", SkillFile), "big"); err == nil {
		t.Fatal("超过大小上限应返回错误")
	}
}

func TestDiscoverSortsAndSkips(t *testing.T) {
	root := t.TempDir()
	writeSkillFile(t, root, "beta", "beta body\n")
	writeSkillFile(t, root, "alpha", "alpha body\n")
	// 缺 SKILL.md 的目录应跳过
	if err := os.MkdirAll(filepath.Join(root, "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 点目录应跳过
	writeSkillFile(t, root, ".hidden", "hidden\n")

	list, warnings := Discover(root)
	if len(warnings) != 0 {
		t.Fatalf("缺 SKILL.md 不应产生警告: %v", warnings)
	}
	if len(list) != 2 || list[0].ID != "alpha" || list[1].ID != "beta" {
		t.Fatalf("结果应按目录名排序且跳过缺失/点目录: %+v", list)
	}
}

func TestDiscoverMissingDir(t *testing.T) {
	list, warnings := Discover(filepath.Join(t.TempDir(), "nope"))
	if len(list) != 0 || len(warnings) != 0 {
		t.Fatalf("目录不存在应静默返回空: %v %v", list, warnings)
	}
}

func TestUserDir(t *testing.T) {
	dir, err := UserDir()
	if err != nil {
		t.Fatalf("UserDir: %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join(".agents", "skills")) {
		t.Fatalf("用户技能目录不符: %s", dir)
	}
}
