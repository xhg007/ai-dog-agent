package agent

// deltaTap 把模型流式增量实时推送给前端。
// 由 llm.WithTap 注入 ctx，只有主对话 runTurn 会注入；
// 子 Agent（explorer / dispatch / 私聊）经 withAgentIdentity → llm.WithoutTap 清除，
// work agent 的独立模型链、上下文摘要与 agent-smith 不带 Tap，因此不会误发到主线程。

import (
	"time"

	"ai-dog-agent/internal/eventbus"
)

type deltaTap struct {
	bus       *eventbus.Bus
	sessionID string
	wait      asyncWaiter
}

func newDeltaTap(bus *eventbus.Bus, sessionID string) *deltaTap {
	return &deltaTap{bus: bus, sessionID: sessionID}
}

func (t *deltaTap) Begin() { t.wait.Add() }

func (t *deltaTap) Chunk(content string) {
	if content == "" {
		return
	}
	t.bus.Emit(eventbus.Event{
		Type:      eventbus.EvChatDelta,
		SessionID: t.sessionID,
		Data:      eventbus.ChatDeltaData{Delta: content},
	})
}

func (t *deltaTap) End() { t.wait.Done() }

// Wait 等待所有旁路 goroutine 结束（不设上限，测试与内部使用）。
func (t *deltaTap) Wait() { t.wait.Wait(0) }

// WaitTimeout 等待旁路 goroutine 结束；超时返回 false。
// 流副本未关闭时不能让对话终态永久阻塞。
func (t *deltaTap) WaitTimeout(timeout time.Duration) bool { return t.wait.Wait(timeout) }
