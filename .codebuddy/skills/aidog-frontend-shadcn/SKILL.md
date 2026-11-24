---
name: aidog-frontend-shadcn
description: ai-dog-agent 前端 UI 体系（M5 后）：Tailwind v4 + shadcn/ui（radix base）接入方式、主题令牌桥接、控件替换清单与注意事项。修改前端组件、新增 shadcn 组件、调整主题配色时使用。触发词：shadcn、tailwind、主题、样式、组件库。
---

# 前端 UI 体系（Tailwind v4 + shadcn/ui）

## 基础设施

- `frontend/vite.config.ts`：`@tailwindcss/vite` 插件 + `@` → `./src` 别名。
- `frontend/src/index.css`：`@import "tailwindcss"` → `@import "./styles.css"` → `tw-animate-css`/`shadcn/tailwind.css`/`@fontsource-variable/geist`。shadcn 令牌在 `:root`（浅色，未用）与 `.dark`（**ai-dog-agent 品牌深色**：bg #0d0d0d / sidebar #161616 / card #1e1e1e / primary #4a9eff）。`index.html` 的 `<html class="dark">` 固定深色。
- **主题单一来源在 index.css**；styles.css 的 `:root` 令牌已桥接（`--bg-canvas: var(--background)` 等），旧样式自动换肤，不要在 styles.css 里再写死颜色。
- `cn` 是独立 npm 包（shadcn v4 新用法），组件内 `import { cn } from "cn"`。
- 组件源码 `src/components/ui/*` 由 CLI 生成（`pnpm dlx shadcn@latest add <name>`，base=radix），**勿手改**。

## 控件替换清单（已完成）

| 旧实现 | 现实现 |
|---|---|
| `.btn`/`.btn.danger`/`.btn.secondary` | `Button`（variant default/secondary/destructive/ghost） |
| `.input`/`.input.textarea` | `Input` / `Textarea` |
| 原生 checkbox 启用开关 | `Switch` |
| `.model-select` | `Select`（composer 模型切换） |
| `.side-view-tab`/`.settings-tab`/`.review-tab` | `Tabs`（`variant="line"` 用于审查/设置） |
| `.status-badge`/`.task-badge` | `Badge`（配色走 `.status-*`/`.b-*` CSS 类） |
| 文字图标（⚙ ✕ ⟳） | `lucide-react` 图标 |

保留的自定义样式（styles.css）：三栏 grid 骨架、`.titlebar`（wails 无边框拖拽）、消息流 `.msg/.step-card`、`.form-grid`（两列表单）、usage 表格、审批卡片、滚动条。

## 注意点

- **层级陷阱**：styles.css 是无层叠层 CSS，优先级高于 Tailwind utilities——旧类名若还挂在 shadcn 组件上会覆盖组件样式（如 `.chat-input` 有意保留以获得无边框输入区）。
- Tooltip 需 `TooltipProvider` 包裹（App.tsx 已包）。
- 图标按钮悬停显隐用 `.thread-item:hover > button[data-slot="button"]` 选择器。
- 构建脚本 `pnpm build` = `tsc && vite build`；警告「chunk > 500 kB」可忽略（radix/markdown 依赖体积）。
