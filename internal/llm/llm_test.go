package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/config"
)

// TestAgentIDBodyField 验证每个 Agent 的唯一 ID 被注入请求体 agent_id 字段。
func TestAgentIDBodyField(t *testing.T) {
	var gotAgentID string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			if v, ok := payload[BodyAgentIDField].(string); ok {
				gotAgentID = v
			}
		}
		// 最小 OpenAI chat.completions 响应
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","created":1,"model":"test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	mc := config.ModelConfig{
		ID: "t1", Name: "local", BaseURL: srv.URL, APIKey: "sk-test", Model: "test-model",
	}
	cm, err := BuildModel(context.Background(), mc, "agent-1234")
	if err != nil {
		t.Fatalf("BuildModel 失败: %v", err)
	}
	msgs := []*schema.Message{schema.UserMessage("hi")}
	if _, err := cm.Generate(context.Background(), msgs); err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if gotAgentID != "agent-1234" {
		t.Fatalf("请求体 %s 字段不符: got %q", BodyAgentIDField, gotAgentID)
	}
	if !strings.HasSuffix(gotPath, "/chat/completions") {
		t.Fatalf("请求路径异常: %s", gotPath)
	}
}

// TestAgentIDEmptyNoField 验证 agentID 为空时不注入请求体字段。
func TestAgentIDEmptyNoField(t *testing.T) {
	var hasField bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err == nil {
			_, hasField = payload[BodyAgentIDField]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","created":1,"model":"test",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	mc := config.ModelConfig{ID: "t2", Name: "local", BaseURL: srv.URL, APIKey: "sk-test", Model: "test-model"}
	cm, err := BuildModel(context.Background(), mc, "")
	if err != nil {
		t.Fatalf("BuildModel 失败: %v", err)
	}
	if _, err := cm.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}); err != nil {
		t.Fatalf("Generate 失败: %v", err)
	}
	if hasField {
		t.Fatalf("agentID 为空时不应注入 %s 字段", BodyAgentIDField)
	}
}

// TestDeepSeekLive 直连 DeepSeek 的流式对话联调。
// 需要 $env:DEEPSEEK_KEY，未设置时跳过。不打印 Key。
func TestDeepSeekLive(t *testing.T) {
	key := os.Getenv("DEEPSEEK_KEY")
	if key == "" {
		t.Skip("未设置 DEEPSEEK_KEY，跳过联调")
	}
	baseURL := os.Getenv("DEEPSEEK_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	modelID := os.Getenv("DEEPSEEK_MODEL")
	if modelID == "" {
		modelID = "deepseek-flash"
	}

	mc := config.ModelConfig{
		ID: "live", Name: "DeepSeek", BaseURL: baseURL, APIKey: key, Model: modelID,
	}
	cm, err := BuildModel(context.Background(), mc, "llm-live-test-agent")
	if err != nil {
		t.Fatalf("BuildModel 失败: %v", err)
	}

	ctx := context.Background()
	stream, err := cm.Stream(ctx, []*schema.Message{
		schema.SystemMessage("你是测试助手，回答保持一句话。"),
		schema.UserMessage("用一句话介绍你自己"),
	})
	if err != nil {
		t.Fatalf("Stream 调用失败: %v", err)
	}
	defer stream.Close()

	var sb strings.Builder
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("读取流失败: %v", err)
		}
		sb.WriteString(msg.Content)
	}
	out := strings.TrimSpace(sb.String())
	t.Logf("模型回复: %s", out)
	if out == "" {
		t.Fatal("模型回复为空")
	}
}
