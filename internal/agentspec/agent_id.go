package agentspec

// 项目级 Agent 稳定 ID（M5 追加）：
// 1) 每个子 Agent 配置文件（.aidog/agents/*.json）含唯一不变 agent_id
//   （UUID 去连字符），保存时自动生成、加载时对缺失项迁移固化；
// 2) 项目主 Agent ID 固化在 .aidog/agent_id——同一项目恒定，
//   供应商侧（限流归因/上下文缓存）只有相同 agent_id 才能复用。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// ErrNoWorkspace 未设置工作区。
var ErrNoWorkspace = errors.New("未设置工作区")

// NewAgentID 生成 32 位无连字符 UUID。
func NewAgentID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

// IsValidAgentID 合法 ID：32 位且无连字符。
func IsValidAgentID(id string) bool {
	return len(id) == 32 && !strings.Contains(id, "-")
}

// AgentIDFile 项目主 Agent 稳定 ID 文件路径（<root>/.aidog/agent_id）。
func AgentIDFile(root string) string {
	return filepath.Join(root, ".aidog", "agent_id")
}

// EnsureIDInFile 确保指定文件中固化一个稳定 ID：已存在且合法则复用，
// 否则生成（UUID 无连字符）并写入。同一文件多次调用返回相同 ID。
func EnsureIDInFile(path string) (string, error) {
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); IsValidAgentID(id) {
			return id, nil
		}
	}
	id := NewAgentID()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// EnsureAgentID 读取项目主 Agent 稳定 ID；缺失或非法时生成并固化到
// .aidog/agent_id。同一项目多次调用返回相同 ID（供应商缓存可复用）。
func EnsureAgentID(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", ErrNoWorkspace
	}
	return EnsureIDInFile(AgentIDFile(root))
}
