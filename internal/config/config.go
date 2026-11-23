// Package config 管理应用配置：模型（OpenAI 兼容自填）、审批策略等。
// 配置持久化于用户配置目录（Windows: %AppData%/ai-dog-agent/config.json）。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ModelConfig 一个 OpenAI 兼容模型配置。
// BaseURL/APIKey/Model 覆盖 DeepSeek、Qwen、GLM、OpenAI 等所有兼容供应商。
type ModelConfig struct {
	ID       string  `json:"id"`                // 唯一 ID（uuid）
	Name     string  `json:"name"`              // 显示名，如 "DeepSeek-V3"
	BaseURL  string  `json:"baseURL"`           // 如 https://api.deepseek.com/v1
	APIKey   string  `json:"apiKey"`            // 密钥
	Model    string  `json:"model"`             // 模型 ID，如 deepseek-chat
	PriceIn  float64 `json:"priceIn,omitempty"` // 每 1M 输入 token 单价（元）
	PriceOut float64 `json:"priceOut,omitempty"`
}

// SkillConfig 可复用的技能指令包（启用后追加到主 Agent 动态上下文）。
type SkillConfig struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Enabled     bool   `json:"enabled"`
}

// MCPServerConfig 外部 MCP Server 配置（stdio 启动方式）。
// Args 为命令行参数列表；Env 为 KEY=VALUE 行（每行一条）。
type MCPServerConfig struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     string   `json:"env,omitempty"`
	Enabled bool     `json:"enabled"`
}

// Config 应用配置根对象。
type Config struct {
	Models         []ModelConfig     `json:"models"`
	ActiveModelID  string            `json:"activeModelId"`
	WorkspacePath  string            `json:"workspacePath,omitempty"` // 上次使用的工作区
	Skills         []SkillConfig     `json:"skills"`
	MCPServers     []MCPServerConfig `json:"mcpServers"`
	ShellWhitelist []string          `json:"shellWhitelist,omitempty"` // run_shell 允许的程序名（未配置时回退默认）
}

// Manager 配置管理器，线程安全。
type Manager struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// Dir 返回配置目录（并确保存在）。
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("获取用户配置目录失败: %w", err)
	}
	dir := filepath.Join(base, "ai-dog-agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建配置目录失败: %w", err)
	}
	return dir, nil
}

// NewManager 加载配置；不存在时创建默认配置文件。
func NewManager() (*Manager, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	m := &Manager{path: filepath.Join(dir, "config.json")}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// load 从磁盘加载配置；文件不存在时写入默认配置。
func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &m.cfg); err != nil {
			return fmt.Errorf("解析配置文件失败(%s): %w", m.path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		m.cfg = defaultConfig()
		return m.persistLocked()
	default:
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	return nil
}

func defaultConfig() Config {
	return Config{Models: []ModelConfig{}, ActiveModelID: "", ShellWhitelist: DefaultShellWhitelist()}
}

// DefaultShellWhitelist 默认 Shell 白名单（spec 3.3：go 只读子命令、git 只读子命令、目录列表）。
// go/git 的子命令级限制在 tools.ShellRun 内部强制执行。
func DefaultShellWhitelist() []string {
	return []string{"go", "git", "dir", "ls"}
}

// Get 返回配置副本。
func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return clone(m.cfg)
}

// Save 覆盖保存配置。
func (m *Manager) Save(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = clone(cfg)
	return m.persistLocked()
}

func (m *Manager) persistLocked() error {
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return fmt.Errorf("替换配置文件失败: %w", err)
	}
	return nil
}

// ActiveModel 返回当前激活的模型配置。
func (m *Manager) ActiveModel() (ModelConfig, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mc := range m.cfg.Models {
		if mc.ID == m.cfg.ActiveModelID {
			return mc, nil
		}
	}
	return ModelConfig{}, errors.New("未配置可用模型，请先在设置页添加并选择模型")
}

// ShellWhitelist 返回生效的 Shell 白名单（未配置时回退默认值），返回副本。
func (m *Manager) ShellWhitelist() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.cfg.ShellWhitelist) == 0 {
		return DefaultShellWhitelist()
	}
	return append([]string(nil), m.cfg.ShellWhitelist...)
}

func clone(c Config) Config {
	out := c
	out.Models = make([]ModelConfig, len(c.Models))
	copy(out.Models, c.Models)
	out.Skills = make([]SkillConfig, len(c.Skills))
	copy(out.Skills, c.Skills)
	out.MCPServers = make([]MCPServerConfig, len(c.MCPServers))
	for i, s := range c.MCPServers {
		out.MCPServers[i] = s
		out.MCPServers[i].Args = append([]string(nil), s.Args...)
	}
	out.ShellWhitelist = append([]string(nil), c.ShellWhitelist...)
	return out
}
