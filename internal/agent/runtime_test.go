package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"

	"ai-dog-agent/internal/agentspec"
)

func TestWorkLimitsDefaultsAndOverride(t *testing.T) {
	def := WorkLimits(agentspec.Budget{})
	if def.MaxSteps != DefaultWorkMaxSteps || def.MaxTokens != DefaultWorkMaxTokens ||
		def.MaxToolCalls != DefaultWorkMaxToolCalls || def.MaxCost != DefaultWorkMaxCost ||
		def.MaxDuration != DefaultMaxDuration || def.MaxRetries != DefaultMaxRetries {
		t.Fatalf("work 默认上限不符: %+v", def)
	}
	custom := WorkLimits(agentspec.Budget{
		MaxSteps: 5, MaxTokens: 1000, MaxDurationSeconds: 30,
		MaxToolCalls: 3, MaxCost: 0.5, MaxRetries: 1,
	})
	if custom.MaxSteps != 5 || custom.MaxTokens != 1000 || custom.MaxDuration != 30*time.Second ||
		custom.MaxToolCalls != 3 || custom.MaxCost != 0.5 || custom.MaxRetries != 1 {
		t.Fatalf("work 覆盖上限不符: %+v", custom)
	}
	main := MainLimits(agentspec.Budget{})
	if main.MaxSteps != DefaultMainMaxSteps || main.MaxTokens != DefaultMainMaxTokens {
		t.Fatalf("主 Agent 默认上限不符: %+v", main)
	}
}

func TestHardIterationLimit(t *testing.T) {
	if got := HardIterationLimit(8, 20); got != 8 {
		t.Fatalf("Spec.MaxTurns 应优先: %d", got)
	}
	if got := HardIterationLimit(0, 20); got != 20 {
		t.Fatalf("未配置 MaxTurns 应回退 max_steps: %d", got)
	}
	if got := HardIterationLimit(0, 0); got != DefaultWorkMaxSteps {
		t.Fatalf("两者都未配置应回退默认: %d", got)
	}
}

func TestExecutionLimitSteps(t *testing.T) {
	e, _, cancel := StartExecution(context.Background(), Limits{
		MaxSteps: 2, MaxTokens: 1000, MaxToolCalls: 10, MaxCost: 10, MaxDuration: time.Second,
	})
	defer cancel()
	e.AddLLM(&model.TokenUsage{PromptTokens: 1}, 0, 0)
	e.AddLLM(&model.TokenUsage{PromptTokens: 1}, 0, 0)
	if err := e.StopError(); err != nil {
		t.Fatalf("未超限不应终止: %v", err)
	}
	e.AddLLM(&model.TokenUsage{PromptTokens: 1}, 0, 0)
	if !errors.Is(e.StopError(), ErrMaxSteps) {
		t.Fatalf("应触发 ErrMaxSteps: %v", e.StopError())
	}
	var le *LimitError
	if !errors.As(e.StopError(), &le) || le.Limit != 2 || le.Used != 3 {
		t.Fatalf("LimitError 取值不符: %+v", le)
	}
}

func TestExecutionLimitTokens(t *testing.T) {
	e, _, cancel := StartExecution(context.Background(), Limits{
		MaxSteps: 10, MaxTokens: 100, MaxToolCalls: 10, MaxCost: 10, MaxDuration: time.Second,
	})
	defer cancel()
	e.AddLLM(&model.TokenUsage{PromptTokens: 60, CompletionTokens: 60}, 0, 0)
	if !errors.Is(e.StopError(), ErrMaxTokens) {
		t.Fatalf("应触发 ErrMaxTokens: %v", e.StopError())
	}
}

func TestExecutionLimitCost(t *testing.T) {
	e, _, cancel := StartExecution(context.Background(), Limits{
		MaxSteps: 10, MaxTokens: 10_000_000, MaxToolCalls: 10, MaxCost: 1, MaxDuration: time.Second,
	})
	defer cancel()
	// 2M 输入 token × 1 元/1M = 2 元 > 1 元
	e.AddLLM(&model.TokenUsage{PromptTokens: 2_000_000}, 1, 0)
	if !errors.Is(e.StopError(), ErrBudgetExceeded) {
		t.Fatalf("应触发 ErrBudgetExceeded: %v", e.StopError())
	}
}

func TestExecutionLimitToolCalls(t *testing.T) {
	e, _, cancel := StartExecution(context.Background(), Limits{
		MaxSteps: 10, MaxTokens: 1000, MaxToolCalls: 1, MaxCost: 10, MaxDuration: time.Second,
	})
	defer cancel()
	e.AddToolCall()
	if e.StopError() != nil {
		t.Fatalf("未超限不应终止: %v", e.StopError())
	}
	e.AddToolCall()
	if !errors.Is(e.StopError(), ErrMaxToolCalls) {
		t.Fatalf("应触发 ErrMaxToolCalls: %v", e.StopError())
	}
}

func TestExecutionTimeout(t *testing.T) {
	e, ctx, cancel := StartExecution(context.Background(), Limits{
		MaxSteps: 10, MaxTokens: 1000, MaxToolCalls: 10, MaxCost: 10,
		MaxDuration: 10 * time.Millisecond,
	})
	defer cancel()
	<-ctx.Done()
	if err := e.Termination(ctx); !errors.Is(err, ErrTimeout) {
		t.Fatalf("应返回 ErrTimeout: %v", err)
	}
}

func TestExecutionTerminationNilOnCancel(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	e, ctx, cancel := StartExecution(parent, Limits{MaxDuration: time.Minute})
	defer cancel()
	cancelParent()
	<-ctx.Done()
	if err := e.Termination(ctx); err != nil {
		t.Fatalf("用户取消不应视为超限: %v", err)
	}
}
