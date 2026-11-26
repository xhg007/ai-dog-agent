package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/indexer"
	"ai-dog-agent/internal/tools"
	"ai-dog-agent/internal/workspace"
)

// BuildProjectContext 构建 explorer 的动态上下文尾部（稳定前缀之后追加）。
func BuildProjectContext(ws *workspace.Manager) (string, error) {
	root := ws.Root()
	if root == "" {
		return "", workspace.ErrNoWorkspace
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n\n# 当前项目\nworkspace: %s\n", root)

	// 语言/构建系统线索：不假设项目是 Go
	markers := projectMarkers(root)
	if len(markers) > 0 {
		fmt.Fprintf(&sb, "\n## 项目文件\n- %s\n", strings.Join(markers, "\n- "))
	}

	if mi, err := indexer.ParseGoMod(root); err == nil {
		fmt.Fprintf(&sb, "\n## Go Module\nmodule: %s\ngo: %s\n", mi.Module, mi.GoVersion)
		if len(mi.Deps) > 0 {
			sb.WriteString("依赖:\n")
			for _, d := range mi.Deps {
				fmt.Fprintf(&sb, "- %s\n", d)
			}
		}
	} else if len(markers) == 0 {
		sb.WriteString("\n（未检测到常见构建/包管理文件）\n")
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

// projectMarkers 返回项目根目录下常见的构建/包管理文件名（语言与构建系统线索）。
func projectMarkers(root string) []string {
	candidates := []string{
		"go.mod", "package.json", "pnpm-lock.yaml", "yarn.lock", "package-lock.json",
		"pyproject.toml", "requirements.txt", "Pipfile", "Cargo.toml",
		"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle",
		"composer.json", "Gemfile", "Makefile", "CMakeLists.txt", "Dockerfile",
	}
	var found []string
	for _, name := range candidates {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			found = append(found, name)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "*.csproj")); len(matches) > 0 {
		for _, m := range matches {
			found = append(found, filepath.Base(m))
		}
	}
	return found
}

// readExcerpt 读取文件前 limit 字节（不存在返回空串）。
// 按 UTF-8 边界截断，避免把中文等多字节字符切成半个。
func readExcerpt(root, name string, limit int) string {
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		return ""
	}
	if len(data) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(data[cut]) {
			cut--
		}
		data = data[:cut]
	}
	return string(data)
}

// BuiltinExplorerTool 内置 explorer 的分派工具名（与 DispatchToolName 规则一致）。
const BuiltinExplorerTool = "dispatch_explorer"

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

	projectCtx, _ := BuildProjectContext(ws)
	instruction, err := renderExplorerPrompt(PromptData{ProjectContext: projectCtx})
	if err != nil {
		return nil, err
	}

	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "dispatch_explorer", // 作为 AgentTool 时即 dispatch 工具名
		Description: "项目探索子 Agent：分析目录结构、模块依赖、入口、符号与引用关系（只读）",
		Instruction: instruction,
		Model:       cm,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: toolList},
		},
	})
}

// OrchestratorTools 主 Agent 工具：分派 explorer 子 Agent + 写操作（审批门）+ 执行类工具（M4）
// + 外部 MCP 工具（extraTools）。
// whitelist 为 Shell 白名单（run_shell 白名单内免审批；go_test/go_vet/go_build 免审批）。
func OrchestratorTools(ctx context.Context, ws *workspace.Manager, cm model.BaseChatModel,
	appr *approval.Manager, backupRoot, sessionID string, whitelist []string,
	extraTools []tool.BaseTool) ([]tool.BaseTool, error) {
	var toolList []tool.BaseTool
	// 项目配置可覆盖内置 explorer：extraTools 已含 dispatch_explorer 时不再添加内置。
	if !hasToolNamed(ctx, extraTools, BuiltinExplorerTool) {
		explorer, err := BuildExplorer(ctx, ws, cm)
		if err != nil {
			return nil, fmt.Errorf("构建 explorer 子 Agent 失败: %w", err)
		}
		toolList = append(toolList, adk.NewAgentTool(ctx, &agentWithIdentity{
			Agent: explorer, agentID: "explorer",
		}))
	}
	// 写操作工具（write_file / edit_file）：经审批门 + 自动备份
	if appr != nil {
		toolList = append(toolList, tools.WriteToolset(ws, backupRoot, appr, sessionID)...)
	}
	// Go 构建工具（go_test / go_vet / go_build）：白名单内免审批（FR-3.1/3.2）
	toolList = append(toolList, tools.GoToolset(ws)...)
	// run_shell：白名单内免审批，白名单外经审批门（FR-4.2）
	shellTool, err := tools.ShellTool(ws, whitelist, appr, sessionID)
	if err != nil {
		return nil, fmt.Errorf("注册 run_shell 工具失败: %w", err)
	}
	toolList = append(toolList, shellTool)
	// 外部 MCP 工具（已包装 + 审批门）；连接失败时调用方只得到空列表
	toolList = append(toolList, extraTools...)
	return toolList, nil
}

// BuildOrchestrator 构建主 Agent（含 explorer 分派工具 + 写操作审批工具 + 执行工具 + MCP 工具）。
// prompt 为动态提示词数据（项目上下文 / Skills / MCP / 项目 Agent）；extraTools 为外部 MCP 工具。
// limits 为执行上限；零值时回退主 Agent 默认值。
func BuildOrchestrator(ctx context.Context, ws *workspace.Manager, cm model.BaseChatModel,
	appr *approval.Manager, backupRoot, sessionID string, whitelist []string,
	prompt PromptData, extraTools []tool.BaseTool, limits Limits) (adk.Agent, error) {
	if limits.MaxSteps <= 0 {
		limits = MainLimits(agentspec.Budget{})
	}
	orchTools, err := OrchestratorTools(ctx, ws, cm, appr, backupRoot, sessionID, whitelist, extraTools)
	if err != nil {
		return nil, err
	}
	// 项目上下文由 ws 生成；Skills / MCP / 项目 Agent 由调用方提供（稳定前缀之后追加）
	if prompt.ProjectContext == "" {
		if dyn, perr := BuildProjectContext(ws); perr == nil {
			prompt.ProjectContext = dyn
		}
	}
	instruction, err := renderOrchestratorPrompt(prompt)
	if err != nil {
		return nil, err
	}
	return adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "orchestrator",
		Description: "ai-dog-agent 主 Agent，负责理解意图、制定计划、编排子 Agent",
		Instruction: instruction,
		Model:       cm,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig:    compose.ToolsNodeConfig{Tools: orchTools},
			EmitInternalEvents: true, // explorer 的内部事件流转发到主 Agent 事件流
		},
		MaxIterations:    limits.MaxSteps, // ADK 硬上限；步骤/工具/Token/成本由 Runtime 外层计数
		ModelRetryConfig: retryConfig(limits.MaxRetries),
	})
}

// newPlainOrchestrator 未设置 workspace 时的纯对话主 Agent（仍可携带 MCP 工具）。
func newPlainOrchestrator(ctx context.Context, cm model.BaseChatModel,
	prompt PromptData, extraTools []tool.BaseTool, limits Limits) (adk.Agent, error) {
	if limits.MaxSteps <= 0 {
		limits = MainLimits(agentspec.Budget{})
	}
	instruction, err := renderOrchestratorPrompt(prompt)
	if err != nil {
		return nil, err
	}
	cfg := &adk.ChatModelAgentConfig{
		Name:             "orchestrator",
		Description:      "ai-dog-agent 主 Agent",
		Instruction:      instruction,
		Model:            cm,
		MaxIterations:    limits.MaxSteps,
		ModelRetryConfig: retryConfig(limits.MaxRetries),
	}
	if len(extraTools) > 0 {
		cfg.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: extraTools},
		}
	}
	return adk.NewChatModelAgent(ctx, cfg)
}

// hasToolNamed 判断工具列表中是否已有指定名称（避免同名分派工具重复注册）。
func hasToolNamed(ctx context.Context, list []tool.BaseTool, name string) bool {
	for _, t := range list {
		if info, err := t.Info(ctx); err == nil && info.Name == name {
			return true
		}
	}
	return false
}

// retryConfig 模型调用限流重试（参考 DeepSeek 限流：429 并发超限，
// 指数退避重试；用户主动取消不重试）。maxRetries<=0 时使用默认值。
func retryConfig(maxRetries int) *adk.ModelRetryConfig {
	if maxRetries <= 0 {
		maxRetries = DefaultMaxRetries
	}
	return &adk.ModelRetryConfig{
		MaxRetries: maxRetries,
		IsRetryAble: func(ctx context.Context, err error) bool {
			return err != nil && ctx.Err() == nil
		},
	}
}
