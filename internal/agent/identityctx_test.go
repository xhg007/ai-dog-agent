package agent

// 子 Agent 身份注入必须清除主对话的流式旁路：内置 explorer 与主 Agent
// 共用同一个 tap 包装模型，否则其内部推理会以 chat_delta 混入主回复。

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/llm"
)

type recordTap struct {
	mu     sync.Mutex
	chunks []string
}

func (t *recordTap) Begin() {}
func (t *recordTap) End()   {}

func (t *recordTap) Chunk(content string) {
	t.mu.Lock()
	t.chunks = append(t.chunks, content)
	t.mu.Unlock()
}

func (t *recordTap) received() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.chunks...)
}

func drainModelStream(t *testing.T, m model.BaseChatModel, ctx context.Context) string {
	t.Helper()
	sr, err := m.Stream(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()
	got := ""
	for {
		chunk, err := sr.Recv()
		if err != nil {
			break
		}
		if chunk != nil {
			got += chunk.Content
		}
	}
	return got
}

func waitTapChunks(t *testing.T, tap *recordTap, want int) []string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := tap.received(); len(got) >= want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	return tap.received()
}

func TestWithAgentIdentityDisablesTap(t *testing.T) {
	m := llm.WithTapModel(&streamScriptModel{chunks: []string{"内部推理"}})
	tap := &recordTap{}
	parent := llm.WithTap(context.Background(), tap)

	// 对照组：主对话 ctx 应当转发增量（旁路 goroutine 异步写入）
	if got := drainModelStream(t, m, parent); got != "内部推理" {
		t.Fatalf("主对话内容 = %q", got)
	}
	if got := waitTapChunks(t, tap, 1); len(got) != 1 {
		t.Fatalf("主对话应收到 1 个增量，实际 %v", got)
	}

	// 子 Agent ctx：同一模型也不得再追加到主对话 tap
	child := withAgentIdentity(parent, "explorer", config.ModelConfig{})
	if got := drainModelStream(t, m, child); got != "内部推理" {
		t.Fatalf("子 Agent 内容 = %q", got)
	}
	// 给（错误的）旁路 goroutine 一点时间暴露问题
	time.Sleep(100 * time.Millisecond)
	if got := tap.received(); len(got) != 1 {
		t.Fatalf("子 Agent 不应追加增量，实际 %v", got)
	}
}
