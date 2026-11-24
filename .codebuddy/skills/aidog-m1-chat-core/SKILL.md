---
name: aidog-m1-chat-core
description: ai-dog-agent 对话内核（M1）：LLM 模型工厂、ChatService 会话服务（Eino ADK Runner 流式）、事件总线 7 种事件、会话 JSON 持久化。在修改流式对话、会话/线程管理、事件流类型、模型构建、消息历史回放、Token 统计时使用。触发词：chat_started/chat_delta/chat_done/chat_error、ChatService、SendMessage、adk.Runner、eventbus、会话持久化。
---

# 对话内核（M1）

## 模型工厂（internal/llm/factory.go）

`BuildModel(ctx, mc, agentID)` 按 OpenAI 兼容 `ModelConfig` 构建 Eino ChatModel；请求体注入自定义字段 `agent_id` / `user_id`（常量 `BodyAgentIDField`、`BodyUserIDField`，用于限流归因与追踪）。

**Agent ID 语义（M5 起改为随项目固化，internal/agent/identity.go）**：主 Agent ID 不再随会话轮换——`initAgentLocked` 每次构建时经 `agentIDForProject()` 取稳定 ID：workspace 已设置 → `<ws>/.aidog/agent_id`；否则 → 应用级 `%AppData%/ai-dog-agent/agent_id`。同一项目恒定，供应商侧只有相同 agent_id 才能复用缓存；旧会话记录在下次构建 Agent 时自动迁移。ID 格式为 32 位无连字符 UUID（`agentspec.NewAgentID`）。

## 会话服务（internal/agent/chat.go）

`ChatService` 是 Wails Service：cfg/ws/bus/appr/backupRoot/store。每个会话一个 `chatSession`（agent/runner/cancel/running/title/messages），`adk.ChatModelAgent` + `adk.Runner`（`EnableStreaming: true`），惰性构建（`initAgentLocked`）。

RPC：`NewSession` / `ListSessions` / `DeleteSession` / `EnsureSession` / `SendMessage` / `Cancel` / `GetMessages` / `RestoreSessions`。

执行循环 `runTurn`：消费 `iter.Next()`，流式增量 → `chat_delta`，Role==Tool → `tool_result`，结束 → `chat_done`（含 Token 用量）。

提示词结构（保证缓存命中）：稳定前缀（`internal/agent/instruction.go` 的 `OrchestratorInstruction`）在前，动态内容在后——`buildDynamicExtras` 把启用的 Skills/MCP/项目子 Agent（agentspec）摘要追加到尾部。

有 workspace 时走 `BuildOrchestrator`，否则 `newPlainOrchestrator`。

## 事件总线（internal/eventbus/eventbus.go）

进程内 Subscribe + Wails 事件推送，统一事件名 `EventName = "agent:event"`（前端 `Events.On("agent:event")` 消费，载荷在 `ev.data`）。

事件常量与载荷：
- `EvChatStarted="chat_started"`（含 agentId）
- `EvChatDelta="chat_delta"`（增量文本）
- `EvToolResult="tool_result"`（tool/content → 前端渲染工具卡片）
- `EvChatDone="chat_done"`（最终 content + promptTokens/completionTokens/totalTokens）
- `EvChatError="chat_error"`（message）
- `EvApprovalRequest="approval_request"` / `EvApprovalDecided="approval_decided"`（见 `aidog-m3-write-approval`）

main.go 的 `application.RegisterEvent[eventbus.Event]` 使 `frontend/bindings/events/index.ts` 生成强类型 `AgentEvent`。

## 会话持久化（internal/agent/store.go）

一个会话一个 JSON：`%AppData%/ai-dog-agent/sessions/<id>.json`，tmp+rename 原子写。`StoredMessage.Role`：user|assistant|tool|error；历史回放只取 user/assistant。启动时 `RestoreSessions` 恢复线程列表。

## 修改时的注意点

- 新增事件：先在 eventbus 定义常量与 Data 结构 → 事件走 `Bus.Emit` → 前端 `App.tsx`/`ThreadPanel.tsx` 的 switch 分发 → 重新生成 bindings。
- `SendMessage` 异步执行，前端以 `running` 状态防重入；中断走 `Cancel`（context 取消）。
- 会话文件是唯一持久化层，M5 的中断恢复/Token 统计将在此扩展。
