package llm

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// gateModel 发出首个 chunk 后阻塞在 gate 上，模拟「模型仍在生成」；
// 用于证明旁路在聚合完成前就能拿到增量。
type gateModel struct {
	chunks []string
	gate   chan struct{}
}

func (m *gateModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return &schema.Message{Role: schema.Assistant, Content: "done"}, nil
}

func (m *gateModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](0)
	go func() {
		defer sw.Close()
		for i, c := range m.chunks {
			sw.Send(&schema.Message{Role: schema.Assistant, Content: c}, nil)
			if i == 0 {
				<-m.gate
			}
		}
	}()
	return sr, nil
}

type recordingTap struct {
	mu     sync.Mutex
	chunks []string
	begins int
	ends   int
	got1   chan struct{}
	once   sync.Once
}

func (t *recordingTap) Begin() {
	t.mu.Lock()
	t.begins++
	t.mu.Unlock()
}

func (t *recordingTap) Chunk(content string) {
	t.mu.Lock()
	t.chunks = append(t.chunks, content)
	t.mu.Unlock()
	t.once.Do(func() { close(t.got1) })
}

func (t *recordingTap) End() {
	t.mu.Lock()
	t.ends++
	t.mu.Unlock()
}

func (t *recordingTap) snapshot() (chunks []string, begins, ends int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.chunks...), t.begins, t.ends
}

func TestWithTapModelDeliversChunksBeforeAggregation(t *testing.T) {
	m := &gateModel{chunks: []string{"a", "b", "c"}, gate: make(chan struct{})}
	wrapped := WithTapModel(m)
	tap := &recordingTap{got1: make(chan struct{})}

	sr, err := wrapped.Stream(WithTap(context.Background(), tap), []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer sr.Close()

	// 主副本读到首个 chunk 时 gate 尚未放行（模型没生成完），旁路此时必须已拿到增量
	if _, err := sr.Recv(); err != nil {
		t.Fatalf("读取首个 chunk: %v", err)
	}
	select {
	case <-tap.got1:
	case <-time.After(time.Second):
		t.Fatal("旁路未在聚合完成前收到首个 chunk")
	}

	close(m.gate)
	for {
		if _, err := sr.Recv(); err != nil {
			break
		}
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, begins, ends := tap.snapshot(); ends >= begins && ends > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	chunks, begins, ends := tap.snapshot()
	if begins != 1 || ends != 1 {
		t.Fatalf("Begin/End = %d/%d，期望 1/1", begins, ends)
	}
	got := ""
	for _, c := range chunks {
		got += c
	}
	if got != "abc" {
		t.Fatalf("旁路内容 = %q，期望 %q", got, "abc")
	}
}

func TestWithTapModelWithoutTapPassesThrough(t *testing.T) {
	gate := make(chan struct{})
	close(gate) // 不阻塞：验证无 Tap 时流原样透传
	wrapped := WithTapModel(&gateModel{chunks: []string{"x", "y"}, gate: gate})

	sr, err := wrapped.Stream(context.Background(), []*schema.Message{{Role: schema.User, Content: "hi"}})
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
	if got != "xy" {
		t.Fatalf("无 Tap 时内容 = %q，期望 %q", got, "xy")
	}
}

// TestWithTapModelWithoutTapContextDoesNotForward 子 Agent 的 ctx 清除 Tap 后，
// 即使用同一个包装模型也不得把内部推理写入主对话增量。
func TestWithTapModelWithoutTapContextDoesNotForward(t *testing.T) {
	gate := make(chan struct{})
	close(gate)
	wrapped := WithTapModel(&gateModel{chunks: []string{"a", "b"}, gate: gate})
	tap := &recordingTap{got1: make(chan struct{})}

	ctx := WithoutTap(WithTap(context.Background(), tap))
	sr, err := wrapped.Stream(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
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
	sr.Close()

	if got != "ab" {
		t.Fatalf("主副本内容 = %q，期望 %q", got, "ab")
	}
	chunks, begins, ends := tap.snapshot()
	if len(chunks) != 0 || begins != 0 || ends != 0 {
		t.Fatalf("WithoutTap 后不应转发: chunks=%v begins=%d ends=%d", chunks, begins, ends)
	}
}
