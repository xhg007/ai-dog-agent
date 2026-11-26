// Package config 管理应用配置：模型（OpenAI 兼容自填）、审批策略等。
// 配置持久化于用户配置目录（Windows: %AppData%/ai-dog-agent/config.json）。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// TimeoutSeconds 单次模型请求超时（秒）；0 表示使用默认值 300s。
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
	// DisableExtraFields 关闭请求体的 agent_id/user_id 扩展字段；
	// 部分严格的 OpenAI 兼容端点会拒绝未知字段（400）。
	DisableExtraFields bool `json:"disableExtraFields,omitempty"`
}

// SkillConfig 可复用的技能指令包（启用后追加到主 Agent 动态上下文）。
type SkillConfig struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	Enabled     bool   `json:"enabled"`
}

// MCPServerConfig 外部 MCP Server 配置。
//   - Transport=auto（默认）：Command 非空按 stdio，否则按 http
//   - stdio：使用 Command/Args/Env
//   - http/sse：使用 URL/Headers
//
// Headers 为 KEY=VALUE 行（每行一条），值支持 ${ENV} 环境变量展开。
type MCPServerConfig struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Transport string   `json:"transport,omitempty"` // auto | stdio | http | sse
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
	Env       string   `json:"env,omitempty"`
	URL       string   `json:"url,omitempty"`
	Headers   string   `json:"headers,omitempty"`
	Enabled   bool     `json:"enabled"`
	// AutoApproveReadOnly 对服务器声明 readOnlyHint=true 的工具免审批。
	// 注解来自服务器、不可完全信任，默认关闭。
	AutoApproveReadOnly bool `json:"autoApproveReadOnly,omitempty"`
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
	mu      sync.RWMutex
	path    string
	cfg     Config
	version uint64 // 每次成功保存递增（用于判断 Agent 是否需要重建）
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
	return NewManagerAt(dir)
}

// NewManagerAt 在指定目录创建配置管理器（测试或自定义落盘位置用）。
func NewManagerAt(dir string) (*Manager, error) {
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

// Save 覆盖保存配置。保存前校验，避免把不可用的模型链写进磁盘：
// 模型 ID/Name/Model 非空且 ID 唯一；ActiveModelID 非空时必须指向已存在的模型。
func (m *Manager) Save(cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// 先落盘成功再更新内存与版本：否则写盘失败时配置已在内存生效，
	// 前端看到「保存失败」但 Agent 实际用了未落盘的新配置，重启后又丢失。
	prev := m.cfg
	m.cfg = clone(cfg)
	if err := m.persistLocked(); err != nil {
		m.cfg = prev
		return err
	}
	m.version++
	return nil
}

// Version 返回配置版本（每次成功保存递增）。会话用它判断 Agent 是否需要
// 用新配置重建（模型 / Skills / MCP 等），使切换在当前会话下一轮即生效。
func (m *Manager) Version() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.version
}

// validateConfig 配置结构校验（只校验会影响运行时构建/切换的字段）。
func validateConfig(cfg Config) error {
	modelIDs := make(map[string]bool, len(cfg.Models))
	for i, mc := range cfg.Models {
		id := strings.TrimSpace(mc.ID)
		if id == "" {
			return fmt.Errorf("第 %d 个模型缺少 id", i+1)
		}
		if modelIDs[id] {
			return fmt.Errorf("模型 id 重复: %s", id)
		}
		modelIDs[id] = true
		if strings.TrimSpace(mc.Name) == "" {
			return fmt.Errorf("模型 %s 缺少 name（显示名）", id)
		}
		if strings.TrimSpace(mc.Model) == "" {
			return fmt.Errorf("模型 %q 缺少 model（模型 ID）", mc.Name)
		}
	}
	if cfg.ActiveModelID != "" && !modelIDs[cfg.ActiveModelID] {
		return fmt.Errorf("activeModelId 指向不存在的模型: %s", cfg.ActiveModelID)
	}

	skillIDs := make(map[string]bool, len(cfg.Skills))
	for _, sk := range cfg.Skills {
		id := strings.TrimSpace(sk.ID)
		if id == "" {
			continue // 允许未分配 ID 的草稿条目
		}
		if skillIDs[id] {
			return fmt.Errorf("Skill id 重复: %s", id)
		}
		skillIDs[id] = true
	}

	mcpIDs := make(map[string]bool, len(cfg.MCPServers))
	for _, mcp := range cfg.MCPServers {
		id := strings.TrimSpace(mcp.ID)
		if id == "" {
			continue
		}
		if mcpIDs[id] {
			return fmt.Errorf("MCP Server id 重复: %s", id)
		}
		mcpIDs[id] = true

		transport := strings.ToLower(strings.TrimSpace(mcp.Transport))
		switch transport {
		case "", "auto":
			if strings.TrimSpace(mcp.Command) == "" && strings.TrimSpace(mcp.URL) == "" {
				return fmt.Errorf("MCP Server %q 缺少 command 或 url", mcp.Name)
			}
		case "stdio":
			if strings.TrimSpace(mcp.Command) == "" {
				return fmt.Errorf("MCP Server %q（stdio）缺少 command", mcp.Name)
			}
		case "http", "sse":
			if strings.TrimSpace(mcp.URL) == "" {
				return fmt.Errorf("MCP Server %q（%s）缺少 url", mcp.Name, transport)
			}
		default:
			return fmt.Errorf("MCP Server %q 的 transport 非法: %q（可选 auto/stdio/http/sse）", mcp.Name, mcp.Transport)
		}
		for _, line := range strings.Split(mcp.Headers, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !strings.Contains(line, "=") {
				return fmt.Errorf("MCP Server %q 的 headers 行必须为 KEY=VALUE: %q", mcp.Name, line)
			}
		}
	}

	for _, w := range cfg.ShellWhitelist {
		if strings.TrimSpace(w) == "" {
			return errors.New("shellWhitelist 不能包含空项")
		}
	}
	return nil
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
