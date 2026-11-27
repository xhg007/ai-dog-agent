// Ported from github.com/fpt/go-dev-mcp (MIT License).
// Adapted: shared pagination and fixed-domain input validation.
package devtools

import (
	"fmt"
	"strings"
)

// DefaultLinesPerPage 文档分页默认行数（与上游一致）。
const DefaultLinesPerPage = 100

// pageLines 按行分页；offset 从 0 开始，limit<=0 时使用默认值。
func pageLines(document string, offset, limit int) (string, int, bool) {
	if limit <= 0 {
		limit = DefaultLinesPerPage
	}
	lines := strings.Split(document, "\n")
	total := len(lines)
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return "", total, false
	}
	end := offset + limit
	hasMore := end < total
	if end > total {
		end = total
	}
	return strings.Join(lines[offset:end], "\n"), total, hasMore
}

// validateDocSlug 校验将要拼进固定域名的包路径 / crate 名 / 模块名。
// 只允许字母、数字与 / . - _ ~，并拒绝协议、绝对路径与 ..，避免越域抓取。
func validateDocSlug(kind, s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s 不能为空", kind)
	}
	if strings.Contains(s, "://") || strings.HasPrefix(s, "/") || strings.Contains(s, "..") {
		return fmt.Errorf("%s 含非法路径片段: %s", kind, s)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/', r == '.', r == '-', r == '_', r == '~':
		default:
			return fmt.Errorf("%s 含非法字符 %q: %s", kind, r, s)
		}
	}
	return nil
}
