package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
)

// summaryModel 压缩单测用：Generate 返回固定摘要或错误。
type summaryModel struct {
	reply string
	err   error
}

func (m *summaryModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	msg := schema.AssistantMessage(m.reply, nil)
	msg.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{
		PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15,
	}}
	return msg, nil
}

func (m *summaryModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(schema.AssistantMessage(m.reply, nil), nil)
	sw.Close()
	return sr, nil
}

func longMessages(n, chars int) []*schema.Message {
	out := make([]*schema.Message, 0, n)
	for i := 0; i < n; i++ {
		text := strings.Repeat("x", chars)
		if i%2 == 1 {
			out = append(out, schema.AssistantMessage(text, nil))
		} else {
			out = append(out, schema.UserMessage(text))
		}
	}
	return out
}

// summaryServer 返回 OpenAI 兼容的固定摘要响应（主 Agent/私聊压缩路径用真实 llm 构建）。
func summaryServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"1","object":"chat.completion","created":1,"model":"test",
			"choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, content)
	}))
}

// TestEstimateTokens 保守估算：rune/3 + 每条开销。
func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(nil); got != 0 {
		t.Fatalf("空历史应为 0，got %d", got)
	}
	msgs := []*schema.Message{schema.UserMessage("你好世界")}
	if got := EstimateTokens(msgs); got != 5 { // 4 runes/3=1 + 4 开销
		t.Fatalf("估算不符: %d", got)
	}
}

// TestShouldCompress 阈值边界。
func TestShouldCompress(t *testing.T) {
	msgs := longMessages(4, 100) // ≈148 tokens
	if ShouldCompress(msgs, 1000, 0.7) {
		t.Fatal("未超阈值不应触发")
	}
	if !ShouldCompress(msgs, 100, 0.7) {
		t.Fatal("超过窗口×阈值应触发")
	}
}

// TestCompressHistorySummary 摘要成功：保留最近 2 轮 + 摘要消息。
func TestCompressHistorySummary(t *testing.T) {
	msgs := longMessages(10, 100)
	out, res := CompressHistory(context.Background(), &summaryModel{reply: "早期摘要"}, msgs,
		CompressOptions{ContextWindow: 200, Threshold: 0.5, KeepRecent: 2})
	if !res.Compressed || res.Strategy != "summary" || res.Summarized != 6 {
		t.Fatalf("压缩结果不符: %+v", res)
	}
	if len(out) != 5 {
		t.Fatalf("应保留摘要+4条最近消息，实际 %d", len(out))
	}
	if !strings.HasPrefix(out[0].Content, "[历史摘要]") || !strings.Contains(out[0].Content, "早期摘要") {
		t.Fatalf("摘要消息不符: %q", out[0].Content)
	}
	if res.AfterTokens >= res.BeforeTokens {
		t.Fatalf("压缩后 token 应下降: %d -> %d", res.BeforeTokens, res.AfterTokens)
	}
	if res.Usage == nil || res.Usage.CompletionTokens != 5 {
		t.Fatalf("摘要 usage 未记录: %+v", res.Usage)
	}
}

// TestCompressHistoryFallback 摘要失败降级为规则截断。
func TestCompressHistoryFallback(t *testing.T) {
	msgs := longMessages(10, 100)
	out, res := CompressHistory(context.Background(), &summaryModel{err: errors.New("boom")}, msgs,
		CompressOptions{ContextWindow: 200, Threshold: 0.5, KeepRecent: 2})
	if !res.Compressed || res.Strategy != "truncate" || res.Err == nil {
		t.Fatalf("应降级为截断: %+v", res)
	}
	if !strings.Contains(out[0].Content, "摘要生成失败") {
		t.Fatalf("降级提示缺失: %q", out[0].Content)
	}
	if len(out) != 5 {
		t.Fatalf("降级后仍应保留最近轮: %d", len(out))
	}
}

// TestCompressHistoryNoTrigger 未超阈值原样返回。
func TestCompressHistoryNoTrigger(t *testing.T) {
	msgs := longMessages(2, 10)
	out, res := CompressHistory(context.Background(), &summaryModel{reply: "x"}, msgs,
		CompressOptions{ContextWindow: 10000, Threshold: 0.9, KeepRecent: 6})
	if res.Compressed || len(out) != len(msgs) {
		t.Fatalf("未超阈值不应压缩: %+v", res)
	}
}

// TestCompressSessionEvent 主 Agent 压缩写回会话并推送 context_compressed。
func TestCompressSessionEvent(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	srv := summaryServer(t, "摘要内容")
	defer srv.Close()

	mc := config.ModelConfig{
		ID: "m", Name: "M", BaseURL: srv.URL, APIKey: "sk", Model: "test", ContextWindow: 200,
	}
	s := &ChatService{bus: bus}
	sess := &chatSession{id: "s1"}
	input := longMessages(10, 100)
	compressed, res := s.compressSession(context.Background(), sess, input, mc, "agent-x", "",
		agentspec.ContextConfig{CompressThreshold: 0.5, KeepRecent: 2})
	if compressed == nil || res == nil {
		t.Fatalf("应压缩: compressed=%v res=%v", compressed, res)
	}
	if len(sess.messages) == 0 || !strings.Contains(sess.messages[0].Content, "摘要内容") {
		t.Fatalf("会话历史未写回摘要: %+v", sess.messages)
	}
	ev := waitEventCb(t, sub, eventbus.EvContextCompressed)
	d, ok := ev.Data.(eventbus.ContextCompressedData)
	if !ok || d.AgentID != "agent-x" || d.BeforeTokens == 0 || d.Strategy != "summary" {
		t.Fatalf("context_compressed 载荷不符: %+v", ev.Data)
	}
}

// TestCompressAgentHistoryAndCooldown 私聊压缩 + 冷却跳过。
func TestCompressAgentHistoryAndCooldown(t *testing.T) {
	srv := summaryServer(t, "私聊摘要")
	defer srv.Close()

	as := &agentSession{
		spec: agentspec.Spec{
			AgentID: "a1", Name: "coder",
			Context: agentspec.ContextConfig{CompressThreshold: 0.5, KeepRecent: 2},
		},
		model: config.ModelConfig{
			ID: "m", Name: "M", BaseURL: srv.URL, APIKey: "sk", Model: "test", ContextWindow: 200,
		},
		history: longMessages(10, 100),
	}
	compressed, res := compressAgentHistory(context.Background(), as)
	if compressed == nil || res == nil || !strings.Contains(compressed[0].Content, "私聊摘要") {
		t.Fatalf("私聊压缩不符: %+v", res)
	}
	if as.lastCompress.IsZero() {
		t.Fatal("应记录上次压缩时间")
	}

	before := len(as.history)
	again, res2 := compressAgentHistory(context.Background(), as)
	if res2 != nil || len(again) != before {
		t.Fatalf("冷却期内应跳过压缩: res=%+v len=%d", res2, len(again))
	}
}
