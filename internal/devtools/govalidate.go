// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Original: internal/app/validation.go — Go 代码检查结果归纳。
// 进程执行由 internal/tools 层复用 runCommand 完成，本文件只负责结果分类与摘要。
package devtools

import (
	"fmt"
	"strings"
)

// 检查项名称（与 tools 层执行的命令一一对应）。
const (
	CheckGoVet     = "go vet"
	CheckGoBuild   = "go build (dry-run)"
	CheckGoModTidy = "go mod tidy (check)"
	CheckGofmt     = "gofmt check"
)

// 检查状态。
const (
	StatusPass  = "pass"
	StatusFail  = "fail"
	StatusError = "error"
)

// ValidationResult 单项检查结果。
type ValidationResult struct {
	Check   string `json:"check"`
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Summary string `json:"summary"`
}

// ValidationReport 完整检查报告。
type ValidationReport struct {
	Directory string             `json:"directory"`
	Results   []ValidationResult `json:"results"`
	Summary   string             `json:"summary"`
}

// SummarizeCheck 根据命令输出与退出码归纳单项检查结果。
// runErr 非 nil 表示命令无法启动（与“命令执行但失败”区分）。
func SummarizeCheck(check, stdout, stderr string, exitCode int, runErr error) ValidationResult {
	result := ValidationResult{Check: check}

	if runErr != nil {
		result.Status = StatusError
		result.Output = runErr.Error()
		result.Summary = fmt.Sprintf("无法执行 %s: %v", check, runErr)
		return result
	}

	if exitCode != 0 {
		result.Status = StatusFail
		if stderr != "" {
			result.Output = stderr
		} else if stdout != "" {
			result.Output = stdout
		}
		result.Summary = failSummary(check, stdout, stderr)
		return result
	}

	result.Status = StatusPass
	switch check {
	case CheckGofmt:
		// gofmt -l 即使需要格式化也返回 0，需要看 stdout 是否列出文件。
		if strings.TrimSpace(stdout) != "" {
			result.Status = StatusFail
			result.Output = stdout
			result.Summary = "需要格式化的文件: " + strings.ReplaceAll(strings.TrimSpace(stdout), "\n", ", ")
		} else {
			result.Summary = "所有文件格式正确"
		}
	case CheckGoVet:
		result.Summary = "未发现 vet 问题"
	case CheckGoBuild:
		result.Summary = "代码可编译"
	case CheckGoModTidy:
		result.Summary = "go.mod 已是 tidy 状态"
	default:
		if stdout != "" {
			result.Output = stdout
		}
		result.Summary = check + " 通过"
	}
	return result
}

func failSummary(check, stdout, stderr string) string {
	switch check {
	case CheckGofmt:
		if strings.TrimSpace(stdout) != "" {
			return "需要格式化的文件: " + strings.ReplaceAll(strings.TrimSpace(stdout), "\n", ", ")
		}
		return "存在需要格式化的文件"
	case CheckGoModTidy:
		return "go.mod 需要 tidy"
	case CheckGoVet:
		issues := stderr
		if strings.TrimSpace(issues) == "" {
			issues = stdout
		}
		if n := len(strings.Split(strings.TrimSpace(issues), "\n")); strings.TrimSpace(issues) != "" {
			return fmt.Sprintf("发现 %d 条 vet 问题", n)
		}
		return "vet 发现问题"
	case CheckGoBuild:
		return "编译会失败"
	default:
		return check + " 未通过"
	}
}

// BuildValidationReport 汇总各项检查，生成报告与总结。
func BuildValidationReport(directory string, results []ValidationResult) ValidationReport {
	report := ValidationReport{Directory: directory, Results: results}
	passed, failed, errs := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case StatusPass:
			passed++
		case StatusFail:
			failed++
		case StatusError:
			errs++
		}
	}
	switch {
	case errs > 0:
		report.Summary = fmt.Sprintf("检查完成：%d 项错误，%d 项未通过，%d 项通过", errs, failed, passed)
	case failed > 0:
		report.Summary = fmt.Sprintf("检查未通过：%d 项失败，%d 项通过", failed, passed)
	default:
		report.Summary = fmt.Sprintf("全部 %d 项检查通过", passed)
	}
	return report
}

// FormatValidationReport 输出面向模型的文本报告。
func FormatValidationReport(report ValidationReport) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Go 代码检查: %s\n%s\n", report.Directory, report.Summary)
	for _, r := range report.Results {
		status := map[string]string{
			StatusPass:  "通过",
			StatusFail:  "未通过",
			StatusError: "无法执行",
		}[r.Status]
		fmt.Fprintf(&sb, "\n- [%s] %s：%s\n", status, r.Check, r.Summary)
		if strings.TrimSpace(r.Output) != "" {
			fmt.Fprintf(&sb, "  输出:\n%s\n", indentLines(strings.TrimSpace(r.Output), "  "))
		}
	}
	return sb.String()
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}
