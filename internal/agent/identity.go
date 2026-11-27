package agent

// 主 Agent 稳定 ID（随项目固化）：
// 同一项目的 agent_id 恒定不变——供应商侧（限流归因/上下文缓存）
// 只有相同 agent_id 才能复用缓存。会话可以任意新建/删除，
// ID 不再随会话轮换。
//   - workspace 已设置 → <ws>/.aidog/main.yml 的 agent_id（随项目走，随项目复制/迁移）
//   - 未设置 workspace → 应用级 %AppData%/ai-dog-agent/agent_id

import (
	"log/slog"
	"path/filepath"

	"ai-dog-agent/internal/agentspec"
	"ai-dog-agent/internal/config"
)

// agentIDFor 计算主 Agent 稳定 ID。projectRoot 非空时用项目级文件，
// 否则用应用级目录；两者都失败时返回新生成 ID（仅本次构建内稳定）。
func agentIDFor(projectRoot, appDir string) string {
	if projectRoot != "" {
		if id, err := config.EnsureProjectAgentID(projectRoot); err == nil {
			return id
		} else {
			slog.Warn("固化项目 agent_id 失败，回退应用级 ID", "root", projectRoot, "err", err)
		}
	}
	if appDir != "" {
		if id, err := agentspec.EnsureIDInFile(filepath.Join(appDir, "agent_id")); err == nil {
			return id
		} else {
			slog.Warn("固化应用级 agent_id 失败", "dir", appDir, "err", err)
		}
	}
	return agentspec.NewAgentID()
}

// agentIDForProject 当前服务上下文的主 Agent 稳定 ID。
func (s *ChatService) agentIDForProject() string {
	root := ""
	if s.ws.IsSet() {
		root = s.ws.Root()
	}
	appDir, _ := config.Dir()
	return agentIDFor(root, appDir)
}
