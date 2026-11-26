package agent

// 上下文压缩（阶段 E）：
//   - 保守 token 估算（rune/3 + 每条开销），超过 模型窗口 × 阈值 时触发；
//   - 保留最近 keep_recent 轮，更早消息由 Agent 自身模型生成摘要；
//   - 摘要失败降级为规则截断（丢弃早期消息 + 固定提示），不阻断对话。

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/llm"
)

const (
	compressCharsPerToken   = 3
	compressMessageOverhead = 4
	compressMaxSummaryChars = 4000
	compressMaxInputChars   = 30000
	compressCooldown        = 30 * time.Second
)

// EstimateTokens 保守估算消息 token（rune/3 + 每条开销）。
func EstimateTokens(msgs []*schema.Message) int {
	total := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		total += utf8.RuneCountInString(m.Content)/compressCharsPerToken + compressMessageOverhead
		total += 16 * len(m.ToolCalls)
	}
	return total
}

// ShouldCompress 判断是否超过窗口占比阈值。
func ShouldCompress(msgs []*schema.Message, window int, threshold float64) bool {
	if window <= 0 {
		window = config.DefaultContextWindow
	}
	if threshold <= 0 {
		threshold = agentspec.DefaultCompressThreshold
	}
	if threshold > 1 {
		threshold = 1
	}
	return float64(EstimateTokens(msgs)) > float64(window)*threshold
}

// CompressOptions 压缩参数；零值回退默认（0.7 阈值 / 6 轮 / 32k 窗口）。
type CompressOptions struct {
	ContextWindow int
	Threshold     float64
	KeepRecent    int
}

// CompressionResult 一次压缩的结果（事件与用量统计用）。
type CompressionResult struct {
	Compressed   bool
	BeforeTokens int
	AfterTokens  int
	Strategy     string // summary | truncate
	Summarized   int
	Usage        *schema.TokenUsage
	Err          error // 摘要失败原因（已降级，不阻断）
}

// CompressHistory 超过阈值时压缩历史：保留最近 keep_recent 轮，早期消息摘要。
func CompressHistory(ctx context.Context, cm model.BaseChatModel,
	msgs []*schema.Message, opts CompressOptions) ([]*schema.Message, CompressionResult) {
	window := opts.ContextWindow
	if window <= 0 {
		window = config.DefaultContextWindow
	}
	threshold := opts.Threshold
	if threshold <= 0 {
		threshold = agentspec.DefaultCompressThreshold
	}
	if threshold > 1 {
		threshold = 1
	}
	keepRecent := opts.KeepRecent
	if keepRecent <= 0 {
		keepRecent = agentspec.DefaultKeepRecent
	}

	before := EstimateTokens(msgs)
	result := CompressionResult{BeforeTokens: before}
	if len(msgs) == 0 || !ShouldCompress(msgs, window, threshold) {
		result.AfterTokens = before
		return msgs, result
	}

	keep := keepRecent * 2 // 一轮≈user+assistant
	if keep > len(msgs) {
		keep = len(msgs)
	}
	older := msgs[:len(msgs)-keep]
	recent := append([]*schema.Message(nil), msgs[len(msgs)-keep:]...)
	result.Compressed = true
	result.Summarized = len(older)

	var summary string
	if len(older) == 0 {
		result.Strategy = "truncate"
		summary = "（无早期消息可摘要）"
	} else {
		s, usage, err := summarizeHistory(ctx, cm, older)
		if err != nil {
			result.Strategy = "truncate"
			result.Err = err
			summary = "早期对话已省略（摘要生成失败）。"
		} else {
			result.Strategy = "summary"
			summary = s
			result.Usage = usage
		}
	}
	if utf8.RuneCountInString(summary) > compressMaxSummaryChars {
		summary = truncateRunes(summary, compressMaxSummaryChars) + "…"
	}

	out := make([]*schema.Message, 0, len(recent)+1)
	if strings.TrimSpace(summary) != "" {
		out = append(out, schema.AssistantMessage("[历史摘要] "+summary, nil))
	}
	out = append(out, recent...)
	result.AfterTokens = EstimateTokens(out)
	return out, result
}

// summarizeHistory 用 Agent 自身模型压缩早期消息。
func summarizeHistory(ctx context.Context, cm model.BaseChatModel,
	msgs []*schema.Message) (string, *schema.TokenUsage, error) {
	if cm == nil {
		return "", nil, fmt.Errorf("缺少摘要模型")
	}
	var sb strings.Builder
	for _, m := range msgs {
		if m == nil {
			continue
		}
		role := "user"
		if m.Role == schema.Assistant {
			role = "assistant"
		}
		fmt.Fprintf(&sb, "%s: %s\n", role, truncateRunes(m.Content, 1000))
		if utf8.RuneCountInString(sb.String()) > compressMaxInputChars {
			break
		}
	}
	input := truncateRunes(sb.String(), compressMaxInputChars)

	resp, err := cm.Generate(ctx, []*schema.Message{
		schema.SystemMessage("你是上下文压缩助手。把早期对话压缩成简洁摘要，保留：用户目标、约束、已完成的操作与结果、未解决的问题、关键文件/命令。不要编造。"),
		schema.UserMessage("请压缩以下早期对话：\n\n" + input),
	})
	if err != nil {
		return "", nil, err
	}
	if resp == nil {
		return "", nil, fmt.Errorf("摘要模型返回空响应")
	}
	var usage *schema.TokenUsage
	if resp.ResponseMeta != nil {
		usage = resp.ResponseMeta.Usage
	}
	return strings.TrimSpace(resp.Content), usage, nil
}

// compressAgentHistory 压缩 work agent 私聊历史；模型不可用时保持原样。
func compressAgentHistory(ctx context.Context, as *agentSession) ([]*schema.Message, *CompressionResult) {
	if time.Since(as.lastCompress) < compressCooldown {
		return as.history, nil
	}
	cm, err := llm.BuildModel(ctx, as.model, as.spec.AgentID, as.spec.AgentID)
	if err != nil {
		slog.Warn("构建 work agent 压缩模型失败，跳过压缩",
			"agentId", as.spec.AgentID, "err", err)
		return as.history, nil
	}
	compressed, res := CompressHistory(ctx, cm, as.history, CompressOptions{
		ContextWindow: as.model.EffectiveContextWindow(),
		Threshold:     as.spec.Context.EffectiveThreshold(),
		KeepRecent:    as.spec.Context.EffectiveKeepRecent(),
	})
	if !res.Compressed {
		return as.history, nil
	}
	as.lastCompress = time.Now()
	slog.Info("work agent 私聊历史已压缩", "agentId", as.spec.AgentID,
		"before", res.BeforeTokens, "after", res.AfterTokens,
		"strategy", res.Strategy, "summarized", res.Summarized)
	return compressed, &res
}

// truncateRunes 按 rune 截断，避免切断多字节字符。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
