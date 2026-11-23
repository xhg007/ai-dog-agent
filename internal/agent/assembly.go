package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/indexer"
	"ai-dog-agent/internal/tools"
	"ai-dog-agent/internal/workspace"
)

// ExplorerInstruction explorer 子 Agent 系统提示词（稳定前缀）。
// 动态项目上下文通过 BuildProjectContext 追加到尾部，保证前缀缓存命中。
const ExplorerInstruction = `你是 ai-dog-agent 的 explorer（项目探索）子 Agent，拥有独立上下文，由主 Agent 分派任务。

# 职责
- 理解目标项目的结构与代码：目录、模块依赖、入口、符号与引用关系。
- 优先读取 README.md / AGENTS.md / CLAUDE.md 作为项目背景。
- 只读操作：你只能使用只读工具，不得修改任何文件。

# 输出规范
- 用简体中文输出结构化项目概览：模块名与依赖、入口、目录组织、关键类型与函数、值得注意的设计点。
- 引用代码位置时使用「文件:行号」格式。`

// BuildProjectContext 构建 explorer 的动态上下文尾部（稳定前缀之后追加）。
func BuildProjectContext(ws *workspace.Manager) (string, error) {
	root := ws.Root()
	if root == "" {
		return "", workspace.ErrNoWorkspace
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n\n# 当前项目\nworkspace: %s\n", root)

	if mi, err := indexer.ParseGoMod(root); err == nil {
		fmt.Fprintf(&sb, "\n## Go Module\nmodule: %s\ngo: %s\n", mi.Module, mi.GoVersion)
		if len(mi.Deps) > 0 {
			sb.WriteString("依赖:\n")
			for _, d := range mi.Deps {
				fmt.Fprintf(&sb, "- %s\n", d)
			}
		}
	} else {
		sb.WriteString("\n（非 Go Module 项目或未找到 go.mod）\n")
	}

	if eps, err := indexer.FindEntryPoints(root); err == nil && len(eps) > 0 {
		sb.WriteString("\n## 入口\n")
		for _, ep := range eps {
			fmt.Fprintf(&sb, "- %s:%d\n", ep.File, ep.Line)
		}
	}

	readmeExcerpt := readExcerpt(root, "README.md", 1500)
	if readmeExcerpt == "" {
		readmeExcerpt = readExcerpt(root, "AGENTS.md", 1500)
	}
	if readmeExcerpt != "" {
		fmt.Fprintf(&sb, "\n## README 摘要\n%s\n", readmeExcerpt)
	}

	if syms, err := indexer.ScanSymbols(root); err == nil && len(syms) > 0 {
		fmt.Fprintf(&sb, "\n## 符号概览（共 %d 个，前 80 个）\n", len(syms))
		n := len(syms)
		if n > 80 {
			n = 80
		}
		for _, s := range syms[:n] {
			if s.Recv != "" {
				fmt.Fprintf(&sb, "- %s %s.%s (%s:%d)\n", s.Kind, s.Recv, s.Name, s.File, s.Line)
			} else {
				fmt.Fprintf(&sb, "- %s %s (%s:%d)\n", s.Kind, s.Name, s.File, s.Line)
			}
		}
	}
	return sb.String(), nil
}

// readExcerpt 读取文件前 limit 字节（不存在返回空串）。
func readExcerpt(root, name string, limit int) string {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return ""
	}
	if len(data) > limit {
		data = data[:limit]
	}
	return string(data)
}

// BuildExplorer 构建 explorer 子 Agent（独立上下文，只读工具集 + 项目摘要工具）。
func BuildExplorer(ctx context.Context, ws *workspace.Manager, cm model.BaseChatModel) (adk.Agent, error) {
	toolList := tools.ReadOnlyToolset(ws)

	summaryTool, err := utils.InferTool("project_summary",
		"生成项目摘要：go.mod 信息、入口、符号统计。",
		func(inner context.Context, _ tools.NoArgs) (string, error) {
			return BuildProjectContext(ws)
		})
	if err != nil {
		return nil, err
	}
	toolList = append(toolList, summaryTool)

	instruction := ExplorerInstruction
	if dyn, err := BuildProjectContext(ws); err == nil {
		instruction += dyn
	}

	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "explorer",
		Description: "项目探索子 Agent：分析目录结构、模块依赖、入口、符号与引用关系（只读）",
		Instruction: instruction,
		Model:       cm,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: toolList},
		},
	})
}

// OrchestratorTools 主 Agent 工具：分派 explorer 子 Agent + 写操作（审批门）。
func OrchestratorTools(ctx context.Context, ws *workspace.Manager, cm model.BaseChatModel,
	appr *approval.Manager, backupRoot, sessionID string) ([]tool.BaseTool, error) {
	explorer, err := BuildExplorer(ctx, ws, cm)
	if err != nil {
		return nil, fmt.Errorf("构建 explorer 子 Agent 失败: %w", err)
	}
	dispatchTool := adk.NewAgentTool(ctx, explorer)
	toolList := []tool.BaseTool{dispatchTool}
	// 写操作工具（write_file / edit_file）：经审批门 + 自动备份
	if appr != nil {
		toolList = append(toolList, tools.WriteToolset(ws, backupRoot, appr, sessionID)...)
	}
	return toolList, nil
}

// BuildOrchestrator 构建主 Agent（含 explorer 分派工具 + 写操作审批工具）。
func BuildOrchestrator(ctx context.Context, ws *workspace.Manager, cm model.BaseChatModel,
	appr *approval.Manager, backupRoot, sessionID string, extras ...string) (adk.Agent, error) {
	orchTools, err := OrchestratorTools(ctx, ws, cm, appr, backupRoot, sessionID)
	if err != nil {
		return nil, err
	}
	instruction := OrchestratorInstruction
	if dyn, err := BuildProjectContext(ws); err == nil {
		instruction += dyn
	}
	// 动态尾部：Skills / MCP / 项目自定义 Agent 配置（稳定前缀之后追加）
	instruction += strings.Join(extras, "")
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "orchestrator",
		Description: "ai-dog-agent 主 Agent，负责理解意图、制定计划、编排子 Agent",
		Instruction: instruction,
		Model:       cm,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig:    compose.ToolsNodeConfig{Tools: orchTools},
			EmitInternalEvents: true, // explorer 的内部事件流转发到主 Agent 事件流
		},
		ModelRetryConfig: retryConfig(),
	})
}

// newPlainOrchestrator 未设置 workspace 时的纯对话主 Agent。
func newPlainOrchestrator(ctx context.Context, cm model.BaseChatModel, extras ...string) (adk.Agent, error) {
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:             "orchestrator",
		Description:      "ai-dog-agent 主 Agent",
		Instruction:      OrchestratorInstruction + strings.Join(extras, ""),
		Model:            cm,
		ModelRetryConfig: retryConfig(),
	})
}

// retryConfig 模型调用限流重试（参考 DeepSeek 限流：429 并发超限，
// 指数退避重试；用户主动取消不重试）。
func retryConfig() *adk.ModelRetryConfig {
	return &adk.ModelRetryConfig{
		MaxRetries: 3,
		IsRetryAble: func(ctx context.Context, err error) bool {
			return err != nil && ctx.Err() == nil
		},
	}
}
