package config

// Skill 目录发现：优先级、项目信任边界与保存过滤。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDirSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "SKILL.md"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findSkill(list []SkillConfig, id string) (SkillConfig, bool) {
	for _, s := range list {
		if s.ID == id {
			return s, true
		}
	}
	return SkillConfig{}, false
}

// TestEffectiveSkillPriority 优先级：全局 YAML → 用户目录 → 项目 skills.yml → 项目目录。
func TestEffectiveSkillPriority(t *testing.T) {
	m, err := NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	userDir := t.TempDir()
	m.userSkillsDir = userDir
	root := t.TempDir()

	cfg := m.Get()
	cfg.Skills = []SkillConfig{{ID: "dup", Name: "dup", Prompt: "global", Enabled: true}}
	if err := m.Save(cfg); err != nil {
		t.Fatal(err)
	}

	writeDirSkill(t, userDir, "dup", "user")
	if got, ok := findSkill(m.Effective(root).Skills, "dup"); !ok ||
		got.Prompt != "user" || got.Source != SkillSourceUserDir {
		t.Fatalf("用户目录应覆盖全局 YAML: %+v", got)
	}

	if err := SaveProject(root, ProjectConfig{Skills: []SkillConfig{
		{ID: "dup", Name: "dup", Prompt: "project-yaml", Enabled: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := m.TrustProject(root); err != nil {
		t.Fatal(err)
	}
	if got, ok := findSkill(m.Effective(root).Skills, "dup"); !ok || got.Prompt != "project-yaml" {
		t.Fatalf("项目 skills.yml 应覆盖用户目录: %+v", got)
	}

	writeDirSkill(t, filepath.Join(root, ".aidog", "skills"), "dup", "project-dir")
	if got, ok := findSkill(m.Effective(root).Skills, "dup"); !ok ||
		got.Prompt != "project-dir" || got.Source != SkillSourceProjectDir {
		t.Fatalf("项目目录应最优先: %+v", got)
	}
}

// TestEffectiveProjectSkillsRequireTrust 未信任项目不加载 .aidog/skills。
func TestEffectiveProjectSkillsRequireTrust(t *testing.T) {
	m, err := NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	userDir := t.TempDir()
	m.userSkillsDir = userDir
	root := t.TempDir()
	writeDirSkill(t, userDir, "only-user", "u")
	writeDirSkill(t, filepath.Join(root, ".aidog", "skills"), "project-only", "p")

	if _, ok := findSkill(m.Effective(root).Skills, "project-only"); ok {
		t.Fatal("未信任项目不应加载 .aidog/skills")
	}
	if _, ok := findSkill(m.Effective(root).Skills, "only-user"); !ok {
		t.Fatal("用户目录技能应始终加载")
	}

	if err := m.TrustProject(root); err != nil {
		t.Fatal(err)
	}
	if _, ok := findSkill(m.Effective(root).Skills, "project-only"); !ok {
		t.Fatal("信任后应加载项目目录技能")
	}
}

// TestDirectorySkillEnabledFalse frontmatter enabled:false 覆盖默认启用。
func TestDirectorySkillEnabledFalse(t *testing.T) {
	m, err := NewManagerAt(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	userDir := t.TempDir()
	m.userSkillsDir = userDir
	p := filepath.Join(userDir, "off")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "SKILL.md"),
		[]byte("---\nenabled: false\n---\noff body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := findSkill(m.Effective("").Skills, "off")
	if !ok || got.Enabled {
		t.Fatalf("enabled:false 应禁用: %+v", got)
	}
}

// TestSaveFiltersDirectorySkills 全局保存不写回目录来源 Skill。
func TestSaveFiltersDirectorySkills(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManagerAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := m.Get()
	cfg.Skills = []SkillConfig{
		{ID: "yaml", Name: "yaml", Prompt: "p", Enabled: true},
		{ID: "dir", Name: "dir", Prompt: "p", Enabled: true, Source: SkillSourceUserDir},
	}
	if err := m.Save(cfg); err != nil {
		t.Fatal(err)
	}
	got := m.Get().Skills
	if len(got) != 1 || got[0].ID != "yaml" {
		t.Fatalf("目录 Skill 不应写回内存配置: %+v", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "source:") {
		t.Fatalf("Source 字段不应落盘:\n%s", data)
	}
}

// TestProjectSaveFiltersDirectorySkills 项目保存不写回目录来源 Skill。
func TestProjectSaveFiltersDirectorySkills(t *testing.T) {
	root := t.TempDir()
	pc := ProjectConfig{Skills: []SkillConfig{
		{ID: "yaml", Name: "yaml", Prompt: "p", Enabled: true},
		{ID: "dir", Name: "dir", Prompt: "p", Enabled: true, Source: SkillSourceProjectDir},
	}}
	if err := SaveProject(root, pc); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Skills) != 1 || loaded.Skills[0].ID != "yaml" {
		t.Fatalf("目录 Skill 不应写回 skills.yml: %+v", loaded.Skills)
	}
}
