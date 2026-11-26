package agent

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/eventbus"
)

// TestPushMessageContextDepth 允许深度 4，第 5 层拒绝。
func TestPushMessageContextDepth(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < MaxMessageDepth; i++ {
		next, err := pushMessageContext(ctx, "a"+strconv.Itoa(i), "b"+strconv.Itoa(i))
		if err != nil {
			t.Fatalf("第 %d 层应允许: %v", i+1, err)
		}
		ctx = next
	}
	if _, err := pushMessageContext(ctx, "x", "y"); !errors.Is(err, ErrMessageDepth) {
		t.Fatalf("第 %d 层应拒绝，实际: %v", MaxMessageDepth+1, err)
	}
}

// TestPushMessageContextCycle A→B→A 直接拒绝。
func TestPushMessageContextCycle(t *testing.T) {
	ctx, err := pushMessageContext(context.Background(), "A", "B")
	if err != nil {
		t.Fatalf("A->B 应允许: %v", err)
	}
	if _, err := pushMessageContext(ctx, "B", "A"); !errors.Is(err, ErrMessageCycle) {
		t.Fatalf("B->A 应判定为循环，实际: %v", err)
	}
}

// TestPushMessageContextSelf 禁止给自己发消息。
func TestPushMessageContextSelf(t *testing.T) {
	if _, err := pushMessageContext(context.Background(), "A", "A"); err == nil {
		t.Fatal("自己给自己发消息应拒绝")
	}
}

// scriptedAgent 用脚本化模型构建一个真实 ChatModelAgent。
func scriptedAgent(t *testing.T, name string, cm model.BaseChatModel) adk.Agent {
	t.Helper()
	ag, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
		Name:        name,
		Description: name,
		Instruction: "你是 " + name,
		Model:       cm,
	})
	if err != nil {
		t.Fatalf("NewChatModelAgent: %v", err)
	}
	return ag
}

func registerScripted(t *testing.T, r *Registry, sessionID, agentID, name, reply string) *agentSession {
	t.Helper()
	wa := &WorkAgent{
		Agent: scriptedAgent(t, "dispatch_"+agentID, &streamScriptModel{chunks: []string{reply}}),
		Spec:  agentspec.Spec{AgentID: agentID, Name: name},
	}
	return r.Register(sessionID, wa)
}

// TestRegistrySendMessageRoundTripAndHistory 主→work 同步回复与历史追加。
func TestRegistrySendMessageRoundTripAndHistory(t *testing.T) {
	r := NewRegistry()
	as := registerScripted(t, r, "s1", "a1", "coder", "coder reply")

	reply, err := r.SendMessage(context.Background(), "s1", "orchestrator", as, "hello")
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if reply != "coder reply" {
		t.Fatalf("回复不符: %q", reply)
	}
	if len(as.history) != 2 {
		t.Fatalf("历史应为 user+assistant 两条: %d", len(as.history))
	}
	if _, err := r.SendMessage(context.Background(), "s1", "orchestrator", as, "again"); err != nil {
		t.Fatalf("第二次 SendMessage: %v", err)
	}
	if len(as.history) != 4 {
		t.Fatalf("历史应累积到 4 条: %d", len(as.history))
	}
}

// TestRegistrySendMessageHistoryLimit 历史超过 40 条时保留最近 40 条。
func TestRegistrySendMessageHistoryLimit(t *testing.T) {
	r := NewRegistry()
	as := registerScripted(t, r, "s1", "a1", "coder", "ok")
	for i := 0; i < 45; i++ {
		as.history = append(as.history, schema.UserMessage("old"))
	}
	if _, err := r.SendMessage(context.Background(), "s1", "orchestrator", as, "new"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(as.history) != maxHistoryMessages {
		t.Fatalf("历史应截断到 %d，实际 %d", maxHistoryMessages, len(as.history))
	}
}

// probeModel 记录并发 Stream 峰值，用于验证同 Agent 串行。
type probeModel struct {
	cur int32
	max int32
}

func (m *probeModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("ok", nil), nil
}

func (m *probeModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	n := atomic.AddInt32(&m.cur, 1)
	for {
		old := atomic.LoadInt32(&m.max)
		if n <= old || atomic.CompareAndSwapInt32(&m.max, old, n) {
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	atomic.AddInt32(&m.cur, -1)
	sr, sw := schema.Pipe[*schema.Message](1)
	sw.Send(schema.AssistantMessage("ok", nil), nil)
	sw.Close()
	return sr, nil
}

func (m *probeModel) BindTools([]*schema.ToolInfo) error { return nil }

// TestRegistrySendMessageSerial 同一 Agent 并发私聊串行执行（峰值并发为 1）。
func TestRegistrySendMessageSerial(t *testing.T) {
	probe := &probeModel{}
	r := NewRegistry()
	as := r.Register("s1", &WorkAgent{
		Agent: scriptedAgent(t, "dispatch_coder", probe),
		Spec:  agentspec.Spec{AgentID: "a1", Name: "coder"},
	})

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.SendMessage(context.Background(), "s1", "orchestrator", as, "hi")
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&probe.max); got != 1 {
		t.Fatalf("同一 Agent 应串行，峰值并发=%d", got)
	}
}

// blockingModel 阻塞到 ctx 取消，用于超时测试。
type blockingModel struct{}

func (m *blockingModel) Generate(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingModel) Stream(ctx context.Context, in []*schema.Message,
	opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *blockingModel) BindTools([]*schema.ToolInfo) error { return nil }

// TestRegistrySendMessageTimeout 调用方 ctx 超时会终止私聊。
func TestRegistrySendMessageTimeout(t *testing.T) {
	r := NewRegistry()
	as := r.Register("s1", &WorkAgent{
		Agent: scriptedAgent(t, "dispatch_slow", &blockingModel{}),
		Spec:  agentspec.Spec{AgentID: "a1", Name: "slow"},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.SendMessage(ctx, "s1", "orchestrator", as, "hi"); err == nil {
		t.Fatal("超时后应返回错误")
	}
}

func invokeTool(t *testing.T, tl tool.BaseTool, args string) (string, error) {
	t.Helper()
	inv, ok := tl.(tool.InvokableTool)
	if !ok {
		t.Fatalf("工具未实现 InvokableTool: %T", tl)
	}
	return inv.InvokableRun(context.Background(), args)
}

// TestSendMessageToolRoundTripAndEvent 主→work 工具调用与 agent_message 事件载荷。
func TestSendMessageToolRoundTripAndEvent(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	r := NewRegistry()
	registerScripted(t, r, "s1", "a1", "coder", "coder reply")

	tl, err := SendMessageTool(r, bus, "s1", "orchestrator", "主 Agent")
	if err != nil {
		t.Fatalf("SendMessageTool: %v", err)
	}
	out, err := invokeTool(t, tl, `{"to":"coder","content":"hi"}`)
	if err != nil || out != "coder reply" {
		t.Fatalf("工具调用不符: out=%q err=%v", out, err)
	}
	ev := waitEventCb(t, sub, eventbus.EvAgentMessage)
	data, ok := ev.Data.(eventbus.AgentMessageData)
	if !ok {
		t.Fatalf("事件载荷类型不符: %T", ev.Data)
	}
	if data.From != "orchestrator" || data.FromName != "主 Agent" ||
		data.To != "a1" || data.ToName != "coder" ||
		data.Content != "hi" || data.Reply != "coder reply" || data.Depth != 1 {
		t.Fatalf("agent_message 载荷不符: %+v", data)
	}
}

// TestSendMessageToolWorkToWork work→work 私聊。
func TestSendMessageToolWorkToWork(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	r := NewRegistry()
	registerScripted(t, r, "s1", "a1", "coder", "coder reply")
	registerScripted(t, r, "s1", "a2", "reviewer", "reviewer reply")

	tl, err := SendMessageTool(r, bus, "s1", "a1", "coder")
	if err != nil {
		t.Fatalf("SendMessageTool: %v", err)
	}
	out, err := invokeTool(t, tl, `{"to":"reviewer","content":"请审查"}`)
	if err != nil || out != "reviewer reply" {
		t.Fatalf("work→work 不符: out=%q err=%v", out, err)
	}
	ev := waitEventCb(t, sub, eventbus.EvAgentMessage)
	data := ev.Data.(eventbus.AgentMessageData)
	if data.From != "a1" || data.To != "a2" || data.ToName != "reviewer" {
		t.Fatalf("work→work 事件不符: %+v", data)
	}
}

// TestSendMessageToolUnknownTarget 目标不存在时返回错误并推送错误事件。
func TestSendMessageToolUnknownTarget(t *testing.T) {
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	r := NewRegistry()
	tl, _ := SendMessageTool(r, bus, "s1", "orchestrator", "主 Agent")
	if _, err := invokeTool(t, tl, `{"to":"missing","content":"hi"}`); err == nil {
		t.Fatal("未知目标应报错")
	}
	ev := waitEventCb(t, sub, eventbus.EvAgentMessage)
	data := ev.Data.(eventbus.AgentMessageData)
	if data.Error == "" || data.To != "missing" {
		t.Fatalf("错误事件载荷不符: %+v", data)
	}
}

// TestSendMessageToolCycleRejected 通过工具触发 A→B→A 时应拒绝。
func TestSendMessageToolCycleRejected(t *testing.T) {
	r := NewRegistry()
	registerScripted(t, r, "s1", "a1", "coder", "ok")
	registerScripted(t, r, "s1", "a2", "reviewer", "ok")
	tl, _ := SendMessageTool(r, nil, "s1", "a1", "coder")

	ctx, err := pushMessageContext(context.Background(), "orchestrator", "a2")
	if err != nil {
		t.Fatalf("准备调用链失败: %v", err)
	}
	inv := tl.(tool.InvokableTool)
	if _, err := inv.InvokableRun(ctx, `{"to":"a2","content":"hi"}`); !errors.Is(err, ErrMessageCycle) {
		t.Fatalf("目标已在调用链中应判定为循环，实际: %v", err)
	}
}
