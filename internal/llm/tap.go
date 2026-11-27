package llm

// 流式 token 旁路（tap）：
// Eino ADK 的 ReAct 流程会先同步聚合模型流（用于判断工具调用），聚合完成后才把
// 原始 chunk 交给上层消费，导致 chat_delta 全部堆积到生成结束后一次性到达。
// 这里在模型外层复制一份流，由旁路 goroutine 实时读取并转发给 Tap，
// 主副本仍按原样交给 ADK，不改变其聚合行为。

import (
	"context"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Tap 实时接收模型流式增量。Begin/End 成对出现，供调用方等待所有旁路 goroutine。
type Tap interface {
	Begin()
	Chunk(content string)
	End()
}

type tapKey struct{}
type noTapKey struct{}

// WithoutTap 禁用 ctx 中的流式旁路。
// 子 Agent（explorer / dispatch / 私聊）与主 Agent 可能共用同一个 tap 包装模型，
// 它们的内部推理不应写入主对话的 chat_delta。
func WithoutTap(ctx context.Context) context.Context {
	return context.WithValue(ctx, noTapKey{}, true)
}

// WithTap 把 Tap 注入 ctx；未注入 Tap 的调用（work agent / 摘要 / smith 等）不转发。
func WithTap(ctx context.Context, t Tap) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, tapKey{}, t)
}

func tapFromCtx(ctx context.Context) Tap {
	if ctx.Value(noTapKey{}) != nil {
		return nil
	}
	t, _ := ctx.Value(tapKey{}).(Tap)
	return t
}

// WithTapModel 包装模型：Stream 时复制流并实时转发增量，Generate 原样透传。
// 可重复包装（内层无 Tap 时等价于空操作）。
func WithTapModel(cm model.BaseChatModel) model.BaseChatModel {
	if cm == nil {
		return nil
	}
	return &tapModel{inner: cm}
}

type tapModel struct {
	inner model.BaseChatModel
}

func (m *tapModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.inner.Generate(ctx, in, opts...)
}

func (m *tapModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	sr, err := m.inner.Stream(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	tap := tapFromCtx(ctx)
	if tap == nil {
		return sr, nil
	}
	copies := sr.Copy(2)
	tap.Begin()
	go func() {
		defer tap.End()
		defer copies[1].Close()
		for {
			chunk, rerr := copies[1].Recv()
			if rerr != nil {
				return
			}
			if chunk != nil && chunk.Content != "" {
				tap.Chunk(chunk.Content)
			}
		}
	}()
	return copies[0], nil
}
