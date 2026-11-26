package agent

// 系统提示词模板的数据与渲染入口。模板文件在 internal/prompts/*.tmpl，
// 通过 go:embed 打包；这里只负责把业务数据映射成模板变量。

import (
	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/prompts"
)

// PromptData 追加在稳定静态前缀之后的动态提示词数据。
type PromptData struct {
	ProjectContext string
	Skills         []config.SkillConfig
	MCPServers     []config.MCPServerConfig
	Agents         []agentspec.Spec
	Dispatches     []DispatchInfo // 实际可用的 dispatch 工具（与工具集保持一致）
}

// DispatchInfo 一个可用的分派工具（名称与描述用于提示词）。
type DispatchInfo struct {
	Name        string
	Description string
}

// ensureBuiltinExplorer 把内置 explorer 列入可用分派工具；项目配置已覆盖同名工具时不重复。
func ensureBuiltinExplorer(list []DispatchInfo) []DispatchInfo {
	for _, d := range list {
		if d.Name == BuiltinExplorerTool {
			return list
		}
	}
	return append([]DispatchInfo{{
		Name:        BuiltinExplorerTool,
		Description: "项目探索子 Agent（内置，只读）：分析目录结构、依赖、入口与符号",
	}}, list...)
}

// renderOrchestratorPrompt 渲染主 Agent 系统提示词。
func renderOrchestratorPrompt(data PromptData) (string, error) {
	return prompts.Render("orchestrator.tmpl", data)
}

// renderExplorerPrompt 渲染 explorer 子 Agent 系统提示词。
func renderExplorerPrompt(data PromptData) (string, error) {
	return prompts.Render("explorer.tmpl", data)
}
