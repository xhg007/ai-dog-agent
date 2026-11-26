package agent

// runTurn 端到端回归测试：用脚本化模型覆盖对话主链路（此前 0% 覆盖），
// 校验流式内容拼接、chat_done、助手消息落盘与中断事件。

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/eventbus"
)

type streamScriptModel struct {
	chunks []string
	usage  *schema.TokenUsage
}

func (m *streamScriptModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg := schema.AssistantMessage(strings.Join(m.chunks, ""), nil)
	if m.usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{Usage: m.usage}
	}
	return msg, nil
}

func (m *streamScriptModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, sw := schema.Pipe[*schema.Message](len(m.chunks))
	for i, c := range m.chunks {
		msg := schema.AssistantMessage(c, nil)
		if i == len(m.chunks)-1 && m.usage != nil {
			msg.ResponseMeta = &schema.ResponseMeta{Usage: m.usage}
		}
		sw.Send(msg, nil)
	}
	sw.Close()
	return sr, nil
}

func (m *streamScriptModel) BindTools(tools []*schema.ToolInfo) error { return nil }

func drainEvents(ch <-chan eventbus.Event) []eventbus.Event {
	var out []eventbus.Event
collect:
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			break collect
		}
	}
	return out
}

func TestRunTurnCompletes(t *testing.T) {
	ws := setupProjectWS(t)
	bus := eventbus.New()
	ch, cancel := bus.Subscribe()
	defer cancel()

	cm := &streamScriptModel{
		chunks: []string{"你好", "，世界"},
		usage:  &schema.TokenUsage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
	}
	ag, err := BuildOrchestrator(context.Background(), ws, cm, nil, "", "sess-1", nil, PromptData{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := &chatSession{
		id:       "sess-1",
		runner:   adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: ag, EnableStreaming: true}),
		messages: []StoredMessage{{Role: "user", Content: "hi"}},
	}
	svc := &ChatService{bus: bus, sessions: map[string]*chatSession{"sess-1": sess}}

	svc.runTurn(context.Background(), sess)

	evs := drainEvents(ch)
	var done bool
	var content string
	for _, ev := range evs {
		switch ev.Type {
		case eventbus.EvChatDone:
			done = true
			if d, ok := ev.Data.(eventbus.ChatDoneData); ok {
				content = d.Content
			}
		case eventbus.EvChatError:
			t.Fatalf("意外错误事件: %+v", ev.Data)
		}
	}
	if !done {
		t.Fatalf("未收到 chat_done，事件: %+v", evs)
	}
	if content != "你好，世界" {
		t.Fatalf("chat_done 内容 = %q，期望流式拼接结果", content)
	}
	if len(sess.messages) != 2 || sess.messages[1].Role != "assistant" || sess.messages[1].Content != "你好，世界" {
		t.Fatalf("助手消息未落盘到会话历史: %+v", sess.messages)
	}
}

func TestRunTurnCanceled(t *testing.T) {
	ws := setupProjectWS(t)
	bus := eventbus.New()
	ch, cancelSub := bus.Subscribe()
	defer cancelSub()

	cm := &streamScriptModel{chunks: []string{"x"}}
	ag, err := BuildOrchestrator(context.Background(), ws, cm, nil, "", "sess-2", nil, PromptData{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := &chatSession{
		id:       "sess-2",
		runner:   adk.NewRunner(context.Background(), adk.RunnerConfig{Agent: ag, EnableStreaming: true}),
		messages: []StoredMessage{{Role: "user", Content: "hi"}},
	}
	svc := &ChatService{bus: bus, sessions: map[string]*chatSession{"sess-2": sess}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.runTurn(ctx, sess)

	evs := drainEvents(ch)
	var interrupted bool
	for _, ev := range evs {
		if ev.Type != eventbus.EvChatError {
			continue
		}
		if d, ok := ev.Data.(map[string]string); ok && strings.Contains(d["message"], "中断") {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("取消后应推送「已中断」错误事件，实际: %+v", evs)
	}
}
