// Package skills 从约定目录中发现 SKILL.md 形式的技能包。
//
// 默认目录：
//   - 用户级：~/.agents/skills
//   - 项目级：<workspace>/.aidog/skills（是否加载由调用方按项目信任状态决定）
//
// 每个技能占一个子目录：skills/<name>/SKILL.md。
// SKILL.md 可选 YAML frontmatter：
//
//	---
//	name: Go 代码审查
//	description: 检查错误处理与并发安全
//	enabled: true
//	---
//	正文作为追加给 Agent 的指令。
package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// SkillFile 技能目录内的定义文件名。
	SkillFile = "SKILL.md"
	// MaxFileSize 单个 SKILL.md 的大小上限（256KB）。
	MaxFileSize = 256 << 10
	// maxDescriptionLen 自动提取描述的截断长度（rune）。
	maxDescriptionLen = 200
)

// Skill 从目录解析出的技能包（与 config.SkillConfig 解耦）。
type Skill struct {
	ID          string
	Name        string
	Description string
	Prompt      string
	Path        string // SKILL.md 的绝对路径
	Enabled     bool
}

type frontmatter struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Enabled     *bool  `yaml:"enabled"`
}

// UserDir 返回用户级技能目录 ~/.agents/skills。
// 不创建目录；目录不存在时 Discover 返回空结果。
func UserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

// ProjectDir 返回项目级技能目录 <root>/.aidog/skills。
func ProjectDir(root string) string {
	return filepath.Join(root, ".aidog", "skills")
}

// Discover 扫描 dir 下的每个子目录并读取其中的 SKILL.md。
// 目录不存在返回空；单个技能解析失败会被跳过并在 warnings 中说明。
// 结果按目录名排序，保证提示词稳定。
func Discover(dir string) ([]Skill, []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("读取技能目录失败 %s: %v", dir, err)}
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		// 只接受真实子目录：跳过点目录与符号链接，避免越界读取。
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var out []Skill
	var warnings []string
	for _, name := range names {
		p := filepath.Join(dir, name, SkillFile)
		s, err := ParseFile(p, name)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				warnings = append(warnings, fmt.Sprintf("跳过技能 %s: %v", p, err))
			}
			continue
		}
		out = append(out, s)
	}
	return out, warnings
}

// ParseFile 解析一个 SKILL.md。fallbackName 用于缺省 ID/Name（通常为目录名）。
func ParseFile(path, fallbackName string) (Skill, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Skill{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Skill{}, errors.New("SKILL.md 不能是符号链接")
	}
	if info.Size() > MaxFileSize {
		return Skill{}, fmt.Errorf("文件超过 %d 字节", MaxFileSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	meta, body, err := splitFrontmatter(data)
	if err != nil {
		return Skill{}, err
	}

	s := Skill{
		ID:          strings.TrimSpace(meta.ID),
		Name:        strings.TrimSpace(meta.Name),
		Description: strings.TrimSpace(meta.Description),
		Prompt:      strings.TrimSpace(body),
		Path:        path,
		Enabled:     true, // 目录内默认启用；frontmatter 可显式关闭
	}
	if s.ID == "" {
		s.ID = fallbackName
	}
	if s.Name == "" {
		s.Name = fallbackName
	}
	if meta.Enabled != nil {
		s.Enabled = *meta.Enabled
	}
	if s.Prompt == "" {
		return Skill{}, errors.New("SKILL.md 正文为空")
	}
	if s.Description == "" {
		s.Description = firstLine(s.Prompt)
	}
	return s, nil
}

// splitFrontmatter 拆分可选的 YAML frontmatter 与正文。
// frontmatter 必须以首行 --- 开始、以单独一行 --- 结束。
func splitFrontmatter(data []byte) (frontmatter, string, error) {
	var fm frontmatter
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, text, nil
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return frontmatter{}, "", errors.New("frontmatter 缺少结束符 ---")
	}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &fm); err != nil {
		return frontmatter{}, "", fmt.Errorf("解析 frontmatter 失败: %w", err)
	}
	return fm, strings.Join(lines[end+1:], "\n"), nil
}

// firstLine 取正文第一行非空内容作为描述，去掉 Markdown 标题符号并截断。
func firstLine(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line == "" {
			continue
		}
		return truncateRunes(line, maxDescriptionLen)
	}
	return ""
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
