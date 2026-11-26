package agent

// Agent Runtime 执行引擎（阶段 A）：一次 Agent 执行的资源计数与终止判定。
//
// eino ADK 的 MaxIterations 只覆盖模型循环次数；步骤 / 工具次数 / Token / 成本
// 无法在 ADK 配置里表达，因此在 Runtime 外层通过全局 callbacks 计数，超限时
// 取消执行上下文并记录结构化错误。时间上限由 context.WithTimeout 承担。
//
// 上下文压缩（阶段 E）预留 BuildContext hook：当前直接回放完整历史。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/agentspec"
)

// 执行上限默认值（设计文档 3.10）。
const (
	DefaultWorkMaxSteps     = 12
	DefaultWorkMaxTokens    = 150_000
	DefaultWorkMaxToolCalls = 30
	DefaultWorkMaxCost      = 1.5

	DefaultMainMaxSteps     = 20
	DefaultMainMaxTokens    = 200_000
	DefaultMainMaxToolCalls = 40
	DefaultMainMaxCost      = 2.0

	DefaultMaxDuration = 600 * time.Second
	DefaultMaxRetries  = 3
)

// Limits 单次执行的资源上限。
type Limits struct {
	MaxSteps     int
	MaxTokens    int64
	MaxDuration  time.Duration
	MaxToolCalls int
	MaxCost      float64
	MaxRetries   int
}

// HardIterationLimit 计算 ADK ChatModelAgentConfig.MaxIterations 硬上限：
// Spec.MaxTurns 优先，未配置时回退 Runtime 的 max_steps。
func HardIterationLimit(maxTurns, maxSteps int) int {
	if maxTurns > 0 {
		return maxTurns
	}
	if maxSteps > 0 {
		return maxSteps
	}
	return DefaultWorkMaxSteps
}

// WorkLimits 按 work agent 的 Budget 计算上限；零值回退 work 默认值。
func WorkLimits(b agentspec.Budget) Limits {
	return limitsWithDefaults(b, Limits{
		MaxSteps: DefaultWorkMaxSteps, MaxTokens: DefaultWorkMaxTokens,
		MaxDuration: DefaultMaxDuration, MaxToolCalls: DefaultWorkMaxToolCalls,
		MaxCost: DefaultWorkMaxCost, MaxRetries: DefaultMaxRetries,
	})
}

// MainLimits 按主 Agent 的项目级 Budget 计算上限；零值回退主 Agent 默认值。
func MainLimits(b agentspec.Budget) Limits {
	return limitsWithDefaults(b, Limits{
		MaxSteps: DefaultMainMaxSteps, MaxTokens: DefaultMainMaxTokens,
		MaxDuration: DefaultMaxDuration, MaxToolCalls: DefaultMainMaxToolCalls,
		MaxCost: DefaultMainMaxCost, MaxRetries: DefaultMaxRetries,
	})
}

func limitsWithDefaults(b agentspec.Budget, def Limits) Limits {
	if b.MaxSteps > 0 {
		def.MaxSteps = b.MaxSteps
	}
	if b.MaxTokens > 0 {
		def.MaxTokens = b.MaxTokens
	}
	if b.MaxDurationSeconds > 0 {
		def.MaxDuration = time.Duration(b.MaxDurationSeconds) * time.Second
	}
	if b.MaxToolCalls > 0 {
		def.MaxToolCalls = b.MaxToolCalls
	}
	if b.MaxCost > 0 {
		def.MaxCost = b.MaxCost
	}
	if b.MaxRetries > 0 {
		def.MaxRetries = b.MaxRetries
	}
	return def
}

// LimitKind 超限类别（事件与错误信息使用）。
type LimitKind string

const (
	LimitSteps     LimitKind = "max_steps"
	LimitTokens    LimitKind = "max_tokens"
	LimitDuration  LimitKind = "max_duration"
	LimitToolCalls LimitKind = "max_tool_calls"
	LimitCost      LimitKind = "max_cost"
	LimitRetries   LimitKind = "max_retries"
)

// 结构化终止哨兵错误（errors.Is 判定）。
var (
	ErrMaxSteps       = errors.New("超过最大执行步骤")
	ErrMaxTokens      = errors.New("超过最大 Token 预算")
	ErrTimeout        = errors.New("超过最大执行时间")
	ErrMaxToolCalls   = errors.New("超过最大工具调用次数")
	ErrBudgetExceeded = errors.New("超过任务预算")
	ErrMaxRetries     = errors.New("超过模型重试次数")
)

// LimitError 一次执行超限的结构化错误，携带上限与实际用量。
type LimitError struct {
	Kind     LimitKind
	Limit    float64
	Used     float64
	Sentinel error
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s（上限 %.2f，已用 %.2f）", e.Sentinel.Error(), e.Limit, e.Used)
}

// Unwrap 支持 errors.Is(err, ErrMaxSteps) 等判断。
func (e *LimitError) Unwrap() error { return e.Sentinel }

// RetryExhaustedError 模型重试用尽的结构化错误：
// errors.Is(err, ErrMaxRetries) 可判定，并携带重试次数与最后一次错误。
type RetryExhaustedError struct {
	TotalRetries int
	LastErr      error
}

func (e *RetryExhaustedError) Error() string {
	if e.LastErr != nil {
		return fmt.Sprintf("%s（重试 %d 次后仍失败: %v）",
			ErrMaxRetries.Error(), e.TotalRetries, e.LastErr)
	}
	return fmt.Sprintf("%s（重试 %d 次）", ErrMaxRetries.Error(), e.TotalRetries)
}

func (e *RetryExhaustedError) Unwrap() error { return ErrMaxRetries }

// ClassifyModelError 把框架层的重试用尽错误归一为 ErrMaxRetries。
// ADK 在 ModelRetryConfig.MaxRetries 用尽后返回 *adk.RetryExhaustedError
// （Unwrap 为 adk.ErrExceedMaxRetries）；此前它会以普通错误上报，
// 失败原因无法与其它执行错误区分。
func ClassifyModelError(err error) error {
	if err == nil {
		return nil
	}
	var exhausted *adk.RetryExhaustedError
	if errors.As(err, &exhausted) {
		return &RetryExhaustedError{TotalRetries: exhausted.TotalRetries, LastErr: exhausted.LastErr}
	}
	if errors.Is(err, adk.ErrExceedMaxRetries) {
		return &RetryExhaustedError{LastErr: err}
	}
	return err
}

// Usage 执行用量快照。
type Usage struct {
	Steps     int
	ToolCalls int
	Tokens    int64
	Cost      float64
}

// Execution 一次执行的共享计数器：Runtime 与全局 callbacks handler 共同使用。
// 所有字段受 mu 保护；callbacks 在工具/模型回调中并发调用。
type Execution struct {
	limits  Limits
	started time.Time

	mu        sync.Mutex
	steps     int
	toolCalls int
	tokens    int64
	cost      float64
	stopErr   error
	cancel    context.CancelFunc
}

// StartExecution 创建执行计数器与带超时的执行上下文。
func StartExecution(parent context.Context, limits Limits) (*Execution, context.Context, context.CancelFunc) {
	if limits.MaxDuration <= 0 {
		limits.MaxDuration = DefaultMaxDuration
	}
	e := &Execution{limits: limits, started: time.Now()}
	ctx, cancel := context.WithTimeout(parent, limits.MaxDuration)
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()
	return e, ctx, cancel
}

// Limits 返回本次执行的上限。
func (e *Execution) Limits() Limits { return e.limits }

// Run 以执行上下文运行 Agent，返回事件迭代器；事件消费由调用方负责。
func (e *Execution) Run(ctx context.Context, runner *adk.Runner, input []*schema.Message) *adk.AsyncIterator[*adk.AgentEvent] {
	return runner.Run(ctx, input)
}

// AddLLM 记录一次模型调用：步骤 +1、累计 token 与成本，并检查上限。
// usage 缺失时只计步骤（成本未知，不按 0 静默通过预算——缺失时无法判定，
// 仅在日志层提示；预算判定依赖 API usage）。
func (e *Execution) AddLLM(usage *model.TokenUsage, priceIn, priceOut float64) {
	in, out := int64(0), int64(0)
	if usage != nil {
		in, out = int64(usage.PromptTokens), int64(usage.CompletionTokens)
	}

	e.mu.Lock()
	e.steps++
	e.tokens += in + out
	e.cost += float64(in)/1e6*priceIn + float64(out)/1e6*priceOut

	var trip *LimitError
	switch {
	case e.limits.MaxSteps > 0 && e.steps > e.limits.MaxSteps:
		trip = &LimitError{Kind: LimitSteps, Limit: float64(e.limits.MaxSteps), Used: float64(e.steps), Sentinel: ErrMaxSteps}
	case e.limits.MaxTokens > 0 && e.tokens > e.limits.MaxTokens:
		trip = &LimitError{Kind: LimitTokens, Limit: float64(e.limits.MaxTokens), Used: float64(e.tokens), Sentinel: ErrMaxTokens}
	case e.limits.MaxCost > 0 && e.cost > e.limits.MaxCost:
		trip = &LimitError{Kind: LimitCost, Limit: e.limits.MaxCost, Used: e.cost, Sentinel: ErrBudgetExceeded}
	}
	e.mu.Unlock()
	if trip != nil {
		e.trip(trip)
	}
}

// AddToolCall 记录一次工具调用并检查上限。
func (e *Execution) AddToolCall() {
	e.mu.Lock()
	e.toolCalls++
	var trip *LimitError
	if e.limits.MaxToolCalls > 0 && e.toolCalls > e.limits.MaxToolCalls {
		trip = &LimitError{Kind: LimitToolCalls, Limit: float64(e.limits.MaxToolCalls), Used: float64(e.toolCalls), Sentinel: ErrMaxToolCalls}
	}
	e.mu.Unlock()
	if trip != nil {
		e.trip(trip)
	}
}

// trip 记录首个终止原因并取消执行上下文（只生效一次）。
func (e *Execution) trip(err error) {
	e.mu.Lock()
	if e.stopErr != nil {
		e.mu.Unlock()
		return
	}
	e.stopErr = err
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// StopError 返回首个超限终止原因（无则 nil）。
func (e *Execution) StopError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopErr
}

// Snapshot 返回当前用量快照。
func (e *Execution) Snapshot() Usage {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Usage{Steps: e.steps, ToolCalls: e.toolCalls, Tokens: e.tokens, Cost: e.cost}
}

// Termination 返回本次执行的终止原因：
//   - 计数超限 → LimitError；
//   - 执行上下文超时 → ErrTimeout；
//   - 用户取消 / 正常结束 → nil。
func (e *Execution) Termination(ctx context.Context) error {
	if err := e.StopError(); err != nil {
		return err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &LimitError{
			Kind: LimitDuration, Limit: e.limits.MaxDuration.Seconds(),
			Used: time.Since(e.started).Seconds(), Sentinel: ErrTimeout,
		}
	}
	return nil
}
