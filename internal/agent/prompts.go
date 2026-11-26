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
}

// renderOrchestratorPrompt 渲染主 Agent 系统提示词。
func renderOrchestratorPrompt(data PromptData) (string, error) {
	return prompts.Render("orchestrator.tmpl", data)
}

// renderExplorerPrompt 渲染 explorer 子 Agent 系统提示词。
func renderExplorerPrompt(data PromptData) (string, error) {
	return prompts.Render("explorer.tmpl", data)
}
