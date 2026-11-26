package llm

// 回归测试：用户中断（ctx 已取消）不应被当作瞬态故障触发模型切换。

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type canceledChatModel struct{ err error }

func (m *canceledChatModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return nil, m.err
}

func (m *canceledChatModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, m.err
}

func (m *canceledChatModel) BindTools(tools []*schema.ToolInfo) error { return nil }

func TestFallbackSkipsSwitchWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	chain := []namedModel{
		{Name: "primary", CM: &canceledChatModel{err: context.Canceled}},
		{Name: "backup", CM: &canceledChatModel{err: context.Canceled}},
	}
	var switched []string
	f := NewFallbackModel(chain, func(from, to, reason string) {
		switched = append(switched, from+"->"+to)
	})

	if _, err := f.Generate(ctx, []*schema.Message{schema.UserMessage("hi")}); err == nil {
		t.Fatal("取消后应返回错误")
	}
	if len(switched) != 0 {
		t.Fatalf("ctx 已取消时不应切换模型，实际: %v", switched)
	}
}

// 未取消时瞬态错误仍应切换（防止上面的判断把故障切换整体关掉）。
func TestFallbackSwitchesOnTransientError(t *testing.T) {
	chain := []namedModel{
		{Name: "primary", CM: &canceledChatModel{err: errTimeout}},
		{Name: "backup", CM: &canceledChatModel{err: nil}},
	}
	var switched []string
	f := NewFallbackModel(chain, func(from, to, reason string) {
		switched = append(switched, from+"->"+to)
	})
	if _, err := f.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}); err != nil {
		t.Fatalf("切换后应成功: %v", err)
	}
	if len(switched) != 1 || switched[0] != "primary->backup" {
		t.Fatalf("应切换一次 primary->backup，实际: %v", switched)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string { return "timeout" }

var errTimeout = timeoutErr{}
