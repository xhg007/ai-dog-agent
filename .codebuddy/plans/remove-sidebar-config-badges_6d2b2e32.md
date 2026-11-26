---
name: remove-sidebar-config-badges
overview: 删除侧栏线程列表下方的「Skills 0 MCP 0 Agents 0」徽标行及其支撑代码（configCounts 状态链），保持侧栏仅含项目/线程/任务列表与设置入口；保留任务面板空态复用的样式类，MCP 验证后提交。
design:
  architecture:
    framework: react
    component: shadcn
  styleKeywords:
    - 数据密集
    - 状态徽标
    - 紧凑行距
  fontSystem:
    fontFamily: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif
    heading:
      size: 14px
      weight: 600
    subheading:
      size: 12px
      weight: 600
    body:
      size: 11px
      weight: 400
  colorSystem:
    primary:
      - "#4A9EFF"
      - "#2DD4A7"
    background:
      - "#1A1D23"
      - "#22262E"
    text:
      - "#E8EAED"
      - "#8A919D"
    functional:
      - "#2DD4A7"
      - "#E8B23A"
      - "#E5566D"
todos:
  - id: remove-badges-sidebar
    content: 删除 Sidebar.tsx 徽标行 JSX 及 configCounts Props/Badge import
    status: completed
  - id: cleanup-app-counts
    content: 清理 App.tsx 中 configCounts state、useEffect 与传参
    status: completed
    dependencies:
      - remove-badges-sidebar
  - id: verify-commit
    content: pnpm build + [mcp:mcp-wails3-dev] DOM 验证后 git commit
    status: completed
    dependencies:
      - cleanup-app-counts
---

## 产品概述

按用户确认的处理方式：直接删除侧栏线程列表下方的「Skills 0 MCP 0 Agents 0」徽标行，侧栏保持干净，仅保留项目入口、线程/任务列表与设置按钮。

## 核心功能

- 移除侧栏底部三个配置计数徽标（Skills/MCP/Agents）及其 JSX 容器
- 清理不再被消费的 configCounts 状态链（App 中的 state、计算 useEffect、传参与 Sidebar 的 Props 定义）
- 保留 TaskPanel 空态「暂无任务」复用的 .side-placeholder 样式，不影响任务面板
- 侧栏视觉：线程列表区域自然占满剩余空间，无其他布局变化

## 技术方案

- 沿用现有 React 18 + TypeScript + shadcn/ui 架构，纯前端删减改动，无新增依赖
- 修改点：
- `frontend/src/components/Sidebar.tsx`：删除 L112-116 徽标 JSX；从 Props 接口与函数参数中移除 configCounts；Badge import 若无其他使用一并删除（删除后检查文件内是否还有 Badge 引用）
- `frontend/src/App.tsx`：删除 L31 configCounts state、L116-127 计算 useEffect（含 ConfigService.GetConfig/GetProjectAgents 调用）、L151 传参；若 ConfigService import 不再被使用则同步清理
- 保留项：
- `styles.css` 的 .side-placeholder 规则保留（TaskPanel 空态仍在使用），不做删除以免误伤
- 性能收益：减少一次设置弹层关闭/工作区切换时的 ConfigService 双调用
- 验证与交付：pnpm build 通过后，使用 [mcp:mcp-wails3-dev] 对运行中的应用做 DOM 验证（js_eval 确认徽标行不存在、任务面板空态正常、侧栏布局无塌陷），最后 git commit

## Agent Extensions

### MCP

- **mcp-wails3-dev**
- Purpose: 对运行中的 Wails 应用做 UI 验证（js_eval 检查徽标行已移除、任务面板空态与线程列表正常）
- Expected outcome: DOM 中不再出现 Skills/MCP/Agents 徽标，侧栏布局无回归，验证通过后提交