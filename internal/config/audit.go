package config

// 项目级配置安全审计（阶段 E）：
// 对 .aidog 下的可执行配置做启发式规则扫描；高危命中由 Manager.ProjectStatus
// 自动拉黑。注意：这是启发式规则，不保证查杀所有后门/病毒，仅作第一道防线。

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"ai-dog-agent/internal/agentspec"
)

// Severity 审计严重级别。
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

// AuditFinding 一条审计发现。
type AuditFinding struct {
	Severity Severity `json:"severity" yaml:"severity"`
	Rule     string   `json:"rule" yaml:"rule"`
	File     string   `json:"file" yaml:"file"`
	Evidence string   `json:"evidence" yaml:"evidence"`
}

// AuditReport 审计结果汇总。
type AuditReport struct {
	Findings    []AuditFinding `json:"findings" yaml:"findings"`
	HighCount   int            `json:"highCount" yaml:"highCount"`
	MediumCount int            `json:"mediumCount" yaml:"mediumCount"`
	LowCount    int            `json:"lowCount" yaml:"lowCount"`
	Blocked     bool           `json:"blocked" yaml:"blocked"` // 存在高危即应自动拉黑
}

const maxAuditFileSize = 1 << 20 // 单文件最多扫描 1MB

type auditLineRule struct {
	severity Severity
	rule     string
	match    func(lowerLine string) bool
}

var auditLineRules = []auditLineRule{
	{SeverityHigh, "download_exec", func(l string) bool {
		download := strings.Contains(l, "curl ") || strings.Contains(l, "wget ") ||
			strings.Contains(l, "iwr ") || strings.Contains(l, "irm ") ||
			strings.Contains(l, "invoke-webrequest")
		exec := strings.Contains(l, "| sh") || strings.Contains(l, "| bash") ||
			strings.Contains(l, "|sh") || strings.Contains(l, "|bash") ||
			strings.Contains(l, "iex") || strings.Contains(l, "invoke-expression")
		return download && exec
	}},
	{SeverityHigh, "powershell_encoded", func(l string) bool {
		return (strings.Contains(l, "powershell") &&
			(strings.Contains(l, "-enc") || strings.Contains(l, "-encodedcommand"))) ||
			(strings.Contains(l, "certutil") &&
				(strings.Contains(l, "-urlcache") || strings.Contains(l, "-decode"))) ||
			(strings.Contains(l, "bitsadmin") && strings.Contains(l, "/transfer"))
	}},
	{SeverityHigh, "destructive_command", func(l string) bool {
		return strings.Contains(l, "rm -rf /") || strings.Contains(l, "rm -rf ~") ||
			strings.Contains(l, "del /f /s /q") || strings.Contains(l, "format ") ||
			strings.Contains(l, "diskpart") ||
			(strings.Contains(l, "reg add") && strings.Contains(l, "run")) ||
			(strings.Contains(l, "schtasks") && strings.Contains(l, "/create"))
	}},
	{SeverityHigh, "credential_access", func(l string) bool {
		return strings.Contains(l, ".ssh/id_rsa") || strings.Contains(l, ".ssh/id_ed25519") ||
			strings.Contains(l, ".aws/credentials") || strings.Contains(l, "wallet.dat") ||
			strings.Contains(l, "metamask") || strings.Contains(l, "keystore")
	}},
	{SeverityHigh, "base64_exec", func(l string) bool {
		decode := strings.Contains(l, "base64 -d") || strings.Contains(l, "frombase64string")
		exec := strings.Contains(l, "| sh") || strings.Contains(l, "| bash") ||
			strings.Contains(l, "iex") || strings.Contains(l, "exec")
		return decode && exec
	}},
	{SeverityMedium, "shell_wrapper", func(l string) bool {
		return strings.Contains(l, "cmd /c") || strings.Contains(l, "bash -c") ||
			strings.Contains(l, "sh -c")
	}},
	{SeverityMedium, "system_path_write", func(l string) bool {
		return strings.Contains(l, "c:\\windows") || strings.Contains(l, "/etc/") ||
			strings.Contains(l, "/usr/bin") || strings.Contains(l, "program files")
	}},
}

// 与 tools.KnownToolNames() 对齐（audit 在底层 config 包，避免反向依赖）。
var auditKnownTools = map[string]bool{
	"read_file": true, "list_files": true, "search_code": true, "search_symbols": true,
	"git_diff": true, "git_status": true,
	"write_file": true, "edit_file": true,
	"run_shell": true,
	// devtools 工具组及成员（移植自 go-dev-mcp）。
	"devtools":                  true,
	"dev_search_godoc":          true,
	"dev_read_godoc":            true,
	"dev_search_within_godoc":   true,
	"dev_search_rustdoc":        true,
	"dev_read_rustdoc":          true,
	"dev_search_within_rustdoc": true,
	"dev_search_pydoc":          true,
	"dev_read_pydoc":            true,
	"dev_search_within_pydoc":   true,
	"dev_tree_dir":              true,
	"dev_search_local_files":    true,
	"dev_scan_markdown":         true,
	"dev_outline_go_package":    true,
	"dev_validate_go_code":      true,
}

// AuditProject 扫描项目 .aidog 配置并返回审计报告。
func AuditProject(root string) AuditReport {
	report := AuditReport{}
	if strings.TrimSpace(root) == "" {
		return report
	}
	dir := ProjectDir(root)
	files := []string{
		projectConfigPath(dir, "main"),
		projectConfigPath(dir, "mcp_servers"),
		projectConfigPath(dir, "skills"),
	}
	agentDir := agentspec.Dir(root)
	if entries, err := os.ReadDir(agentDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !hasConfigExt(e.Name()) {
				continue
			}
			files = append(files, filepath.Join(agentDir, e.Name()))
		}
	}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil || len(data) > maxAuditFileSize {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		lines := strings.Split(string(data), "\n")
		for _, rule := range auditLineRules {
			for _, line := range lines {
				if rule.match(strings.ToLower(line)) {
					report.add(rule.severity, rule.rule, rel, truncateEvidence(line))
					break // 同一规则每文件只报一条
				}
			}
		}
	}

	report.addMCPURLFindings(root)
	report.addAgentRefFindings(root)
	report.Blocked = report.HighCount > 0
	return report
}

func (r *AuditReport) add(sev Severity, rule, file, evidence string) {
	r.Findings = append(r.Findings, AuditFinding{
		Severity: sev, Rule: rule, File: file, Evidence: evidence,
	})
	switch sev {
	case SeverityHigh:
		r.HighCount++
	case SeverityMedium:
		r.MediumCount++
	default:
		r.LowCount++
	}
}

// addMCPURLFindings 检查项目 MCP Server 是否使用明文 HTTP。
func (r *AuditReport) addMCPURLFindings(root string) {
	path := projectConfigPath(ProjectDir(root), "mcp_servers")
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxAuditFileSize {
		return
	}
	var servers []MCPServerConfig
	if yaml.Unmarshal(data, &servers) != nil {
		return
	}
	rel := filepath.Join(".aidog", filepath.Base(path))
	for _, s := range servers {
		u := strings.ToLower(strings.TrimSpace(s.URL))
		if strings.HasPrefix(u, "http://") {
			r.add(SeverityMedium, "insecure_mcp", rel, s.Name+" -> "+s.URL)
		}
	}
}

// addAgentRefFindings 检查未知工具与项目本地悬空引用。
func (r *AuditReport) addAgentRefFindings(root string) {
	agentDir := agentspec.Dir(root)
	entries, err := os.ReadDir(agentDir)
	if err != nil {
		return
	}
	localSkills := loadSkillRefs(projectConfigPath(ProjectDir(root), "skills"))
	localMCP := loadMCPRefs(projectConfigPath(ProjectDir(root), "mcp_servers"))

	for _, e := range entries {
		if e.IsDir() || !hasConfigExt(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(agentDir, e.Name()))
		if err != nil || len(data) > maxAuditFileSize {
			continue
		}
		var spec agentspec.Spec
		if yaml.Unmarshal(data, &spec) != nil {
			continue
		}
		rel := filepath.Join(".aidog", "agents", e.Name())
		for _, tool := range spec.Tools {
			if !auditKnownTools[tool] {
				r.add(SeverityLow, "unknown_tool", rel, tool)
			}
		}
		// 仅在项目本地定义了 Skill/MCP 时检查悬空引用，避免误报全局引用
		for _, ref := range spec.Skills {
			if len(localSkills) > 0 && !localSkills[ref] {
				r.add(SeverityLow, "dangling_ref", rel, "skill: "+ref)
			}
		}
		for _, ref := range spec.MCPServers {
			if len(localMCP) > 0 && !localMCP[ref] {
				r.add(SeverityLow, "dangling_ref", rel, "mcp: "+ref)
			}
		}
	}
}

func loadSkillRefs(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxAuditFileSize {
		return nil
	}
	var skills []SkillConfig
	if yaml.Unmarshal(data, &skills) != nil {
		return nil
	}
	out := make(map[string]bool, len(skills)*2)
	for _, s := range skills {
		if s.ID != "" {
			out[s.ID] = true
		}
		if s.Name != "" {
			out[s.Name] = true
		}
	}
	return out
}

func loadMCPRefs(path string) map[string]bool {
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxAuditFileSize {
		return nil
	}
	var servers []MCPServerConfig
	if yaml.Unmarshal(data, &servers) != nil {
		return nil
	}
	out := make(map[string]bool, len(servers)*2)
	for _, s := range servers {
		if s.ID != "" {
			out[s.ID] = true
		}
		if s.Name != "" {
			out[s.Name] = true
		}
	}
	return out
}

// projectConfigPath 返回项目配置文件的现有路径：优先 .yml，兼容旧 .json。
func projectConfigPath(dir, base string) string {
	yml := filepath.Join(dir, base+".yml")
	if _, err := os.Stat(yml); err == nil {
		return yml
	}
	legacy := filepath.Join(dir, base+".json")
	if _, err := os.Stat(legacy); err == nil {
		return legacy
	}
	return yml
}

// hasConfigExt 判断是否为项目配置/Agent 配置文件（.yml；兼容旧 .json）。
func hasConfigExt(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".json")
}

func truncateEvidence(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= 200 {
		return s
	}
	return string(r[:200]) + "…"
}
