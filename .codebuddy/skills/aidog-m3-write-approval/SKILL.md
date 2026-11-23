---
name: aidog-m3-write-approval
description: ai-dog-agent 写操作与审批（M3）：write_file/edit_file 工具的审批门（approval.Manager 阻塞 Request/Decide）、写前 diff 预览、自动备份与回滚（RestoreFromBackup/RollbackFile）、前端审批卡片（ApprovalCard）与 approval_request/approval_decided 事件流。在修改写工具、审批流程、备份回滚、审批卡片 UI 时使用。触发词：write_file、edit_file、approval、审批、Decide、RollbackFile、备份。
---

# 写操作与审批（M3）

## 审批门（internal/approval/approval.go）

`Manager.Request(ctx, sessionID, tool, path, preview)` 由工具调用方（Agent 工具实现内）**阻塞调用**，等待前端决策；前端经 RPC `ApprovalService.Decide(id, approved, remember)` → `Manager.Decide` 回填 channel。`remember=true` 时按 `sessionID → "tool:path"` 记入 `remembered`，同会话内同 tool+path 放行。`ListPending` 用于重启/重载后对账恢复卡片。拒绝返回 `ErrDenied`。

## 写工具（internal/tools/fs_write.go）

`WriteToolset(ws, backupDir, appr, sessionID)` 提供 `write_file` / `edit_file`（参数 `WriteFileArgs`/`EditFileArgs`，`edit` 的 oldText 必须唯一匹配）。流程：构建行级 diff 预览（`buildDiffPreview`）→ `appr.Request` 审批 → 原文件自动备份（`backupFile`）→ 写入（`WriteFileWithBackup`/`EditFileWithBackup`）。

装配点：`assembly.go BuildOrchestrator` 仅在 **workspace 已设置且有 approval Manager** 时把写工具集附加给主 Agent。

## 回滚 RPC（internal/service/approval_service.go）

`ApprovalService`（`NewApprovalService(mgr, ws, backupRoot)`）：
- `Decide(id, approved, remember)` — 前端决策入口
- `ListPending()` — 待审批列表
- `RollbackFile(relPath)` — 从 `%AppData%/ai-dog-agent/backups/` 经 `tools.RestoreFromBackup` 恢复（FR-4.5）

## 前端事件流

`approval_request` 事件 → `App.tsx` 按 `sessionId` 归集到 `approvalsByThread` → 当前线程渲染 `ApprovalCard.tsx`（展示 tool/path/预览 diff，按钮调 `ApprovalService.Decide`）→ `approval_decided` 事件移除对应卡片。切换线程时卡片跟随各自会话。

## 修改时的注意点

- 备份先于写入，顺序不可颠倒；回滚路径必须先过 `workspace.Resolve` 校验。
- 新增写类工具时必须接同一个 `approval.Manager`，不要绕过审批门直接写文件。
- `sessionID` 贯穿 Request→remembered→事件载荷，新增工具时从 `WriteToolset` 闭包透传，勿自造。
