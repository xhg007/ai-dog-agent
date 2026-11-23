package agent

// 全局 eino callbacks.Handler（M5 FR-5.1/5.3/5.4）：
// - ChatModel 节点结束：采集 TokenUsage → token_usage 落库 + llm_usage 事件（按调用粒度）
// - Tool 节点前后：before/after checkpoint 落库 + tool_call 事件
// 通过 callbacks.AppendGlobalHandlers 在服务装配时注册一次（非线程安全，仅启动期）。

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/store"
	"ai-dog-agent/internal/task"
)

// obsInfo 经 ctx 从 ChatService 传递到 handler：会话/任务标识 + 消息快照。
type obsInfo struct {
	SessionID string
	TaskID    string
	Snapshot  string // runTurn 初始消息历史 JSON（checkpoint 用）
}

type obsKey struct{}

func withObsInfo(ctx context.Context, oi *obsInfo) context.Context {
	return context.WithValue(ctx, obsKey{}, oi)
}

func obsFromCtx(ctx context.Context) *obsInfo {
	oi, _ := ctx.Value(obsKey{}).(*obsInfo)
	return oi
}

// PriceFunc 查询当前激活模型的名称与单价（元/1M tokens）。
type PriceFunc func() (model string, priceIn, priceOut float64)

// obsPriceFn 由 RegisterObsHandler 注入，handler 在 LLM 调用结束时查询。
var obsPriceFn atomic.Value // PriceFunc

// RegisterObsHandler 注册全局观测 handler（应用启动时调用一次；非线程安全）。
func RegisterObsHandler(tasks *task.Manager, price PriceFunc) {
	if price != nil {
		obsPriceFn.Store(price)
	}
	callbacks.AppendGlobalHandlers(newObsHandler(tasks))
	slog.Info("已注册 eino 全局观测 handler")
}

func newObsHandler(tasks *task.Manager) callbacks.Handler {
	b := callbacks.NewHandlerBuilder()

	// ---- OnEnd：ChatModel（非流式 usage 采集）与 Tool（after checkpoint）共用一个 fn
	//（HandlerBuilder 的 OnEndFn 后设覆盖先设，必须合并）----
	b.OnEndFn(func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
		if info == nil {
			return ctx
		}
		switch info.Component {
		case components.ComponentOfChatModel:
			if mo := model.ConvCallbackOutput(output); mo != nil {
				recordModelUsage(tasks, obsFromCtx(ctx), info, mo.TokenUsage)
			}
		case components.ComponentOfTool:
			oi := obsFromCtx(ctx)
			if oi == nil || tasks == nil || oi.TaskID == "" {
				return ctx
			}
			result := ""
			if tout := tool.ConvCallbackOutput(output); tout != nil {
				result = tout.Response
			}
			seq := tasks.NextCheckpointSeq(oi.TaskID)
			tasks.AppendCheckpoint(store.Checkpoint{
				TaskID: oi.TaskID, Seq: seq, Phase: "after",
				ToolName: info.Name, ToolResult: result,
			})
		}
		return ctx
	})

	// ---- ChatModel：流式结束 ----
	// 框架已为每个 handler 复制独立流副本（inject.go OnWithStreamHandle），
	// 本 handler 可同步消费到底，不影响主转发链路。
	b.OnEndWithStreamOutputFn(func(ctx context.Context, info *callbacks.RunInfo,
		output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
		if info == nil || info.Component != components.ComponentOfChatModel {
			return ctx
		}
		var usage *model.TokenUsage
		for {
			chunk, err := output.Recv()
			if err != nil {
				break // EOF 或错误：以最后拿到的 usage 为准
			}
			if mo := model.ConvCallbackOutput(chunk); mo != nil && mo.TokenUsage != nil {
				usage = mo.TokenUsage
			}
		}
		recordModelUsage(tasks, obsFromCtx(ctx), info, usage)
		return ctx
	})

	// ---- Tool：执行前（before checkpoint + tool_call 事件）----
	b.OnStartFn(func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
		if info == nil || info.Component != components.ComponentOfTool {
			return ctx
		}
		oi := obsFromCtx(ctx)
		if oi == nil || tasks == nil || oi.TaskID == "" {
			return ctx
		}
		tin := tool.ConvCallbackInput(input)
		args := ""
		if tin != nil {
			args = tin.ArgumentsInJSON
		}
		seq := tasks.NextCheckpointSeq(oi.TaskID)
		tasks.Emit(oi.SessionID, oi.TaskID, eventbus.EvToolCall, eventbus.ToolCallData{
			TaskID: oi.TaskID, Tool: info.Name, Args: args,
		})
		tasks.AppendCheckpoint(store.Checkpoint{
			TaskID: oi.TaskID, Seq: seq, Phase: "before",
			ToolName: info.Name, ToolArgs: args, Messages: oi.Snapshot,
		})
		return ctx
	})

	return b.Build()
}

// recordModelUsage 记录一次 LLM 调用用量：成本按激活模型单价折算（元/1M tokens）。
func recordModelUsage(tasks *task.Manager, oi *obsInfo, info *callbacks.RunInfo, usage *model.TokenUsage) {
	if tasks == nil || usage == nil || oi == nil || oi.TaskID == "" {
		return
	}
	var modelName string
	var pIn, pOut float64
	if fn, ok := obsPriceFn.Load().(PriceFunc); ok && fn != nil {
		modelName, pIn, pOut = fn()
	}
	in, out := int64(usage.PromptTokens), int64(usage.CompletionTokens)
	data := eventbus.LLMUsageData{
		TaskID: oi.TaskID, Model: modelName,
		InputTokens: in, OutputTokens: out,
		CostIn:  float64(in) / 1e6 * pIn,
		CostOut: float64(out) / 1e6 * pOut,
	}
	tasks.Emit(oi.SessionID, oi.TaskID, eventbus.EvLLMUsage, data)
	tasks.AppendTokenUsage(store.TokenUsage{
		TaskID: oi.TaskID, Model: modelName,
		InputTokens: in, OutputTokens: out,
		CostIn: data.CostIn, CostOut: data.CostOut,
		CreatedAt: time.Now(),
	})
}
