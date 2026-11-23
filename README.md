# ai-dog-agent

桌面端多 Agent 编码开发助手（Windows 优先）。

- **GUI**：Wails3 + React + TypeScript
- **Agent 内核**：Eino（ADK 多 Agent 编排 / Graph 工作流）
- **模型**：OpenAI 兼容协议自填（DeepSeek / Qwen / GLM / OpenAI 通用）
- **工具协议**：MCP Go SDK（预留）
- **需求文档**：见 [docs/REQUIREMENTS.md](docs/REQUIREMENTS.md)

## 架构要点

- **主 Agent（Orchestrator）**：常驻编排，制定计划、分派子 Agent。
- **子 Agent**：配置随项目走（workspace 内 `.aidog/agents/*.json`），独立上下文 / skill / MCP / function call。
- **后台 Meta Agent**：专职优化与创造子 Agent 配置。
- **上下文前缀稳定**：系统提示词静态前缀在前、动态内容追加在后，保证提示词缓存命中。
- **会话持久化**：会话元数据与消息历史按会话落盘 `%AppData%/ai-dog-agent/sessions/*.json`，关闭软件后线程列表与对话记录可恢复，并在后续轮次回放历史。
- **配置**：设置页提供模型（OpenAI 兼容）、Skills、MCP Server 以及项目级子 Agent（`.aidog/agents/*.json`）的可视化编辑；启用项追加到主 Agent 动态上下文。
- **Markdown**：助手回复支持 GFM 表格、代码块、列表（remark-gfm）。

## 开发

```bash
wails3 dev    # 开发调试（最小参数形式）
wails3 build  # 生产构建
```

Go 侧测试：

```bash
go test ./internal/...
```

## 里程碑

| 里程碑 | 状态 |
|---|---|
| M0 骨架（module/包结构/config/日志/前端布局） | 完成 |
| M1 对话内核（llm 工厂 + Eino ADK Runner + 事件流） | 完成 |
| M2 只读工具 + 项目理解（workspace 隔离 / explorer 子 Agent / go-ast 索引） | 完成 |
| M3 写操作 + 审批（write/edit + 审批卡片 + 备份回滚 API） | 完成 |
| M4 执行与修复（run_shell + go_test/vet/build + 白名单 + 回滚 UI） | 完成 |
| M5 生产化（持久化/中断恢复/Token 统计/OTel） | 待开发 |
