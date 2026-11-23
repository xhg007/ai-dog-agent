package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeChatModel 测试用 ChatModel：Generate 返回固定文本或错误；Stream 返回错误。
type fakeChatModel struct {
	name string
	text string
	err  error
}

func (f *fakeChatModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if f.err != nil {
		return nil, f.err
	}
	return schema.AssistantMessage(fmt.Sprintf("%s:%s", f.name, f.text), nil), nil
}

func (f *fakeChatModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if f.err != nil {
		return nil, f.err
	}
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(schema.AssistantMessage(fmt.Sprintf("%s:%s", f.name, f.text), nil), nil)
	sw.Close()
	return sr, nil
}

func (f *fakeChatModel) BindTools(tools []*schema.ToolInfo) error {
	return nil
}

// TestIsTransientError 错误分类：网络错误/429/5xx 可切换，4xx 配置错误不切换。
func TestIsTransientError(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{errors.New("rate limit exceeded (429)"), true},
		{errors.New("HTTP 500 Internal Server Error"), true},
		{errors.New("502 Bad Gateway"), true},
		{errors.New("503 Service Unavailable"), true},
		{errors.New("context deadline exceeded"), true},
		{errors.New("connection refused"), true},
		{errors.New("dial tcp: lookup api.example.com: no such host"), true},
		{errors.New("401 Unauthorized: invalid api key"), false},
		{errors.New("400 Bad Request: invalid model id"), false},
		{errors.New("缺少 API Key"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsTransientError(c.err); got != c.want {
			t.Errorf("IsTransientError(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

// TestFallbackSwitchesOnError 主模型 429 → 切换备用模型成功 + 事件上报。
func TestFallbackSwitchesOnError(t *testing.T) {
	events := [][]string{}
	notify := func(from, to, reason string) { events = append(events, []string{from, to, reason}) }

	fm := NewFallbackModel(
		[]namedModel{
			{"primary", &fakeChatModel{name: "primary", err: errors.New("429 rate limited")}},
			{"backup", &fakeChatModel{name: "backup", text: "ok"}},
		}, notify)

	msg, err := fm.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("应切换成功: %v", err)
	}
	if msg.Content != "backup:ok" {
		t.Fatalf("结果应来自备用模型: %s", msg.Content)
	}
	if len(events) != 1 || events[0][0] != "primary" || events[0][1] != "backup" {
		t.Fatalf("切换事件不符: %v", events)
	}
}

// TestFallbackNoSwitchOnClientError 401 配置错误不切换。
func TestFallbackNoSwitchOnClientError(t *testing.T) {
	called := 0
	notify := func(from, to, reason string) { called++ }

	primaryErr := errors.New("401 Unauthorized: invalid key")
	fm := NewFallbackModel(
		[]namedModel{
			{"primary", &fakeChatModel{name: "primary", err: primaryErr}},
			{"backup", &fakeChatModel{name: "backup", text: "ok"}},
		}, notify)

	_, err := fm.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if !errors.Is(err, primaryErr) && err.Error() != primaryErr.Error() {
		t.Fatalf("应原样返回主模型错误: %v", err)
	}
	if called != 0 {
		t.Fatal("4xx 不应触发切换事件")
	}
}

// TestFallbackAllFail 全部失败返回最后错误。
func TestFallbackAllFail(t *testing.T) {
	notify := func(from, to, reason string) {}
	lastErr := errors.New("503 down")
	fm := NewFallbackModel(
		[]namedModel{
			{"a", &fakeChatModel{name: "a", err: errors.New("429 x")}},
			{"b", &fakeChatModel{name: "b", err: lastErr}},
		}, notify)

	_, err := fm.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err == nil || !IsTransientError(err) {
		t.Fatalf("应返回最后的瞬态错误: %v", err)
	}
	if !contains(err.Error(), "503 down") {
		t.Fatalf("错误应含最终失败原因: %v", err)
	}
}

// TestFallbackStream Stream 路径同样支持切换。
func TestFallbackStream(t *testing.T) {
	notify := func(from, to, reason string) {}
	fm := NewFallbackModel(
		[]namedModel{
			{"primary", &fakeChatModel{name: "primary", err: errors.New("timeout")} },
			{"backup", &fakeChatModel{name: "backup", text: "ok"}},
		}, notify)

	sr, err := fm.Stream(context.Background(), []*schema.Message{schema.UserMessage("hi")})
	if err != nil {
		t.Fatalf("Stream 应切换成功: %v", err)
	}
	msg, err := sr.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if msg.Content != "backup:ok" {
		t.Fatalf("流式结果应来自备用模型: %s", msg.Content)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
