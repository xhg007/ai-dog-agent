package config

// 项目级配置安全审计（阶段 E）：
// 对 .aidog 下的可执行配置做启发式规则扫描；高危命中由 Manager.ProjectStatus
// 自动拉黑。注意：这是启发式规则，不保证查杀所有后门/病毒，仅作第一道防线。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

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
	Severity Severity `json:"severity"`
	Rule     string   `json:"rule"`
	File     string   `json:"file"`
	Evidence string   `json:"evidence"`
}

// AuditReport 审计结果汇总。
type AuditReport struct {
	Findings    []AuditFinding `json:"findings"`
	HighCount   int            `json:"highCount"`
	MediumCount int            `json:"mediumCount"`
	LowCount    int            `json:"lowCount"`
	Blocked     bool           `json:"blocked"` // 存在高危即应自动拉黑
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
	"read_file": true, "list_files": true, "search_code": true,
	"git_diff": true, "git_status": true,
	"write_file": true, "edit_file": true,
	"go_test": true, "go_vet": true, "go_build": true,
	"run_shell": true,
}

// AuditProject 扫描项目 .aidog 配置并返回审计报告。
func AuditProject(root string) AuditReport {
	report := AuditReport{}
	if strings.TrimSpace(root) == "" {
		return report
	}
	dir := ProjectDir(root)
	files := []string{
		filepath.Join(dir, "main.json"),
		filepath.Join(dir, "mcp_servers.json"),
		filepath.Join(dir, "skills.json"),
	}
	agentDir := agentspec.Dir(root)
	if entries, err := os.ReadDir(agentDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
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
	path := filepath.Join(ProjectDir(root), "mcp_servers.json")
	data, err := os.ReadFile(path)
	if err != nil || len(data) > maxAuditFileSize {
		return
	}
	var servers []MCPServerConfig
	if json.Unmarshal(data, &servers) != nil {
		return
	}
	for _, s := range servers {
		u := strings.ToLower(strings.TrimSpace(s.URL))
		if strings.HasPrefix(u, "http://") {
			r.add(SeverityMedium, "insecure_mcp", filepath.Join(".aidog", "mcp_servers.json"),
				s.Name+" -> "+s.URL)
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
	localSkills := loadSkillRefs(filepath.Join(ProjectDir(root), "skills.json"))
	localMCP := loadMCPRefs(filepath.Join(ProjectDir(root), "mcp_servers.json"))

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(agentDir, e.Name()))
		if err != nil || len(data) > maxAuditFileSize {
			continue
		}
		var spec agentspec.Spec
		if json.Unmarshal(data, &spec) != nil {
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
	if json.Unmarshal(data, &skills) != nil {
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
	if json.Unmarshal(data, &servers) != nil {
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

func truncateEvidence(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= 200 {
		return s
	}
	return string(r[:200]) + "…"
}
