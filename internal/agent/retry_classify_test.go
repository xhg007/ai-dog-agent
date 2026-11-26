package agent

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
)

// TestClassifyModelErrorRetryExhausted ADK 重试用尽错误归一为 ErrMaxRetries，保留次数与最后错误。
func TestClassifyModelErrorRetryExhausted(t *testing.T) {
	raw := &adk.RetryExhaustedError{LastErr: errors.New("429 rate limit"), TotalRetries: 3}
	err := ClassifyModelError(raw)
	if !errors.Is(err, ErrMaxRetries) {
		t.Fatalf("应可判定为 ErrMaxRetries，实际: %v", err)
	}
	var exhausted *RetryExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("应为 *RetryExhaustedError，实际 %T", err)
	}
	if exhausted.TotalRetries != 3 {
		t.Fatalf("重试次数不符: %d", exhausted.TotalRetries)
	}
	msg := err.Error()
	if !strings.Contains(msg, "3") || !strings.Contains(msg, "429") {
		t.Fatalf("错误消息应含重试次数与最后错误: %q", msg)
	}
}

// TestClassifyModelErrorSentinel 仅包裹 adk.ErrExceedMaxRetries 时同样可判定。
func TestClassifyModelErrorSentinel(t *testing.T) {
	err := ClassifyModelError(fmt.Errorf("执行失败: %w", adk.ErrExceedMaxRetries))
	if !errors.Is(err, ErrMaxRetries) {
		t.Fatalf("应可判定为 ErrMaxRetries，实际: %v", err)
	}
}

// TestClassifyModelErrorPassthrough 其它错误原样返回（不改变判定）。
func TestClassifyModelErrorPassthrough(t *testing.T) {
	raw := errors.New("工具执行失败")
	if got := ClassifyModelError(raw); got != raw {
		t.Fatalf("普通错误应原样返回，实际: %v", got)
	}
	if got := ClassifyModelError(nil); got != nil {
		t.Fatalf("nil 应返回 nil，实际: %v", got)
	}
}
