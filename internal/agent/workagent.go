package agent

// work agent 运行时（阶段 A 3.5）：按 agentspec.Spec 构建独立 ChatModelAgent。
//   - 模型：Spec.Model 路由，独立模型链（agent_id=Spec.AgentID, user_id=主 Agent ID）
//   - 指令：Spec.SystemPrompt（稳定前缀）+ 该 Agent 的 Skills + 项目上下文尾部
//   - 工具：Spec.Tools 从工具目录选择，空列表默认只读
//   - MCP：只使用 Spec.MCPServers 声明的 server（空 = 无 MCP）
//   - 上下文：每次 dispatch 由 AgentTool 新建 Run，独立于主 Agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
	"ai-dog-agent/internal/llm"
	"ai-dog-agent/internal/mcp"
	"ai-dog-agent/internal/tools"
	"ai-dog-agent/internal/workspace"
)

// WorkAgentDeps 构建 work agent 的依赖。
type WorkAgentDeps struct {
	Config    *config.Manager
	Workspace *workspace.Manager
	Approval  *approval.Manager
	BackupDir string
	SessionID string
	Whitelist []string
	MCP       *mcp.Manager
	Registry  *Registry     // 私聊注册表；nil 时不注入 send_message
	Bus       *eventbus.Bus // 私聊事件推送；nil 时仍可调用但无前端事件
}

// WorkAgent 构建结果：Agent 与运行配置（用量归因/上限）。
type WorkAgent struct {
	Agent    adk.Agent
	Spec     agentspec.Spec
	Model    config.ModelConfig
	Limits   Limits
	ToolName string // dispatch 工具名（adk.NewAgentTool 使用 agent.Name）
}

// BuildWorkAgent 按 Spec 构建独立 work agent。
// mainAgentID 用于 user_id；notify 上报模型故障切换（事件需带 agentId）。
func BuildWorkAgent(ctx context.Context, deps WorkAgentDeps, spec agentspec.Spec, mainAgentID string,
	notify func(from, to, reason string)) (*WorkAgent, error) {
	if strings.TrimSpace(spec.AgentID) == "" {
		return nil, fmt.Errorf("work agent %q 缺少固化 agent_id", spec.Name)
	}
	if deps.Config == nil || deps.Workspace == nil || !deps.Workspace.IsSet() {
		return nil, fmt.Errorf("构建 work agent %q 需要已设置的工作区", spec.Name)
	}

	limits := WorkLimits(spec.Budget)
	cm, _, modelCfg, err := llm.BuildWorkModelChain(ctx, deps.Config,
		spec.AgentID, mainAgentID, spec.Model, notify)
	if err != nil {
		return nil, err
	}

	effective := deps.Config.Effective(deps.Workspace.Root())
	toolList, err := tools.Catalog(ctx, deps.Workspace, tools.CatalogDeps{
		BackupDir: deps.BackupDir,
		Approval:  deps.Approval,
		SessionID: deps.SessionID,
		Whitelist: deps.Whitelist,
	}, spec.Tools)
	if err != nil {
		return nil, fmt.Errorf("构建 work agent %q 工具集失败: %w", spec.Name, err)
	}
	// 私聊工具：work agent 也可向同会话的其他 work agent 发送消息。
	if deps.Registry != nil {
		msgTool, err := SendMessageTool(deps.Registry, deps.Bus, deps.SessionID, spec.AgentID, spec.Name)
		if err != nil {
			return nil, fmt.Errorf("构建 work agent %q 的 send_message 工具失败: %w", spec.Name, err)
		}
		toolList = append(toolList, msgTool)
	}

	// MCP 集合按 Spec 声明过滤：未声明则完全不暴露外部 MCP 工具。
	servers := selectMCPServers(effective.MCPServers, spec.MCPServers)
	if len(spec.MCPServers) > 0 && deps.MCP == nil {
		slog.Warn("work agent 声明了 MCP Server，但 MCP 管理器不可用",
			"agentId", spec.AgentID, "agent", spec.Name)
	}
	if deps.MCP != nil && len(servers) > 0 {
		toolList = append(toolList, deps.MCP.Tools(ctx, servers, deps.Workspace.Root(), deps.SessionID)...)
	}

	skills := selectSkills(effective.Skills, spec.Skills)
	projectCtx, _ := BuildProjectContext(deps.Workspace)
	instruction := buildWorkInstruction(spec, skills, projectCtx)

	name := DispatchToolName(spec)
	desc := strings.TrimSpace(spec.Description)
	if desc == "" {
		desc = spec.Name + " 子 Agent"
	}
	ag, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        name,
		Description: desc,
		Instruction: instruction,
		Model:       cm,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: toolList},
		},
		MaxIterations:    HardIterationLimit(spec.MaxTurns, limits.MaxSteps),
		ModelRetryConfig: retryConfig(limits.MaxRetries),
	})
	if err != nil {
		return nil, fmt.Errorf("构建 work agent %q 失败: %w", spec.Name, err)
	}
	return &WorkAgent{Agent: ag, Spec: spec, Model: modelCfg, Limits: limits, ToolName: name}, nil
}

// DispatchToolName 生成 dispatch_<sanitized_name>；中文等无法转写时用 agent_id 前缀
// 保证唯一。工具名限制 64 字符。
func DispatchToolName(spec agentspec.Spec) string {
	base := sanitizeToolName(spec.Name)
	if base == "" {
		base = "agent"
		if id := strings.TrimSpace(spec.AgentID); len(id) >= 8 {
			base += "_" + id[:8]
		}
	}
	name := "dispatch_" + base
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// sanitizeToolName 保留 [a-z0-9-]，其余字符折叠为下划线并去除首尾下划线。
func sanitizeToolName(s string) string {
	var sb strings.Builder
	lastUnderscore := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-':
			sb.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				sb.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(sb.String(), "_-")
}

// selectMCPServers 按 ID/Name 引用过滤 MCP Server；缺失引用记录警告。
func selectMCPServers(all []config.MCPServerConfig, refs []string) []config.MCPServerConfig {
	if len(refs) == 0 {
		return nil
	}
	out := make([]config.MCPServerConfig, 0, len(refs))
	for _, ref := range refs {
		found := false
		for _, s := range all {
			if s.ID == ref || s.Name == ref {
				out = append(out, s)
				found = true
				break
			}
		}
		if !found {
			slog.Warn("work agent 引用的 MCP Server 不存在", "ref", ref)
		}
	}
	return out
}

// selectSkills 按 ID/Name 引用解析 Skill；缺失引用记录警告。
func selectSkills(all []config.SkillConfig, refs []string) []config.SkillConfig {
	if len(refs) == 0 {
		return nil
	}
	out := make([]config.SkillConfig, 0, len(refs))
	for _, ref := range refs {
		found := false
		for _, sk := range all {
			if sk.ID == ref || sk.Name == ref {
				out = append(out, sk)
				found = true
				break
			}
		}
		if !found {
			slog.Warn("work agent 引用的 Skill 不存在", "ref", ref)
		}
	}
	return out
}

// buildWorkInstruction 组装：Spec.SystemPrompt（稳定前缀）→ Skills 指令 → 项目上下文尾部。
func buildWorkInstruction(spec agentspec.Spec, skills []config.SkillConfig, projectCtx string) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(spec.SystemPrompt))
	if len(skills) > 0 {
		sb.WriteString("\n\n# 已启用 Skills\n")
		for _, sk := range skills {
			fmt.Fprintf(&sb, "- %s：%s", sk.Name, sk.Description)
			if strings.TrimSpace(sk.Prompt) != "" {
				fmt.Fprintf(&sb, "\n  指令：%s", strings.TrimSpace(sk.Prompt))
			}
			sb.WriteString("\n")
		}
	}
	if strings.TrimSpace(projectCtx) != "" {
		sb.WriteString("\n")
		sb.WriteString(projectCtx)
	}
	return sb.String()
}
