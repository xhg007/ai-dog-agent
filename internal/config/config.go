// Package config 管理应用配置：模型（OpenAI 兼容自填）、审批策略等。
// 配置持久化于用户配置目录（Windows: %AppData%/ai-dog-agent/config.yml）。
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ModelConfig 一个 OpenAI 兼容模型配置。
// BaseURL/APIKey/Model 覆盖 DeepSeek、Qwen、GLM、OpenAI 等所有兼容供应商。
// 归属供应商（ProviderID 非空）时，BaseURL/APIKey 运行时由 ProviderConfig 提供，
// 磁盘上不再重复保存，避免同一供应商的密钥散落在每个模型里。
type ModelConfig struct {
	ID         string  `json:"id" yaml:"id"`                                     // 唯一 ID（uuid）
	ProviderID string  `json:"providerId,omitempty" yaml:"providerId,omitempty"` // 所属供应商（LLM 端点）
	Name       string  `json:"name" yaml:"name"`                                 // 显示名，如 "DeepSeek-V3"
	BaseURL    string  `json:"baseURL,omitempty" yaml:"baseURL,omitempty"`       // 运行时填充；独立模型才落盘
	APIKey     string  `json:"apiKey,omitempty" yaml:"apiKey,omitempty"`         // 运行时填充；独立模型才落盘
	Model      string  `json:"model" yaml:"model"`                               // 模型 ID，如 deepseek-chat
	PriceIn    float64 `json:"priceIn,omitempty" yaml:"priceIn,omitempty"`       // 每 1M 输入 token 单价（元）
	PriceOut   float64 `json:"priceOut,omitempty" yaml:"priceOut,omitempty"`
	// TimeoutSeconds 单次模型请求超时（秒）；0 表示使用默认值 300s。
	TimeoutSeconds int `json:"timeoutSeconds,omitempty" yaml:"timeoutSeconds,omitempty"`
	// DisableExtraFields 关闭请求体的 agent_id/user_id 扩展字段；
	// 部分严格的 OpenAI 兼容端点会拒绝未知字段（400）。
	DisableExtraFields bool `json:"disableExtraFields,omitempty" yaml:"disableExtraFields,omitempty"`
	// ContextWindow 模型上下文窗口（tokens）；0 表示按 DefaultContextWindow 保守估算。
	ContextWindow int `json:"contextWindow,omitempty" yaml:"contextWindow,omitempty"`
	// IncludeUsage 流式请求注入 stream_options.include_usage（保证末尾带 usage）；
	// nil 或不开启时不注入，兼容拒绝未知字段的端点。
	IncludeUsage *bool `json:"includeUsage,omitempty" yaml:"includeUsage,omitempty"`
}

// ProviderConfig 一个 LLM 供应商（OpenAI 兼容端点）的连接配置。
// 同一供应商下可以挂多个模型，只需填写一次 BaseURL / API Key。
type ProviderConfig struct {
	ID      string `json:"id" yaml:"id"`
	Name    string `json:"name" yaml:"name"`
	BaseURL string `json:"baseURL" yaml:"baseURL"`
	APIKey  string `json:"apiKey" yaml:"apiKey"`
}

// DefaultContextWindow 未配置上下文窗口时的保守估算（tokens）。
const DefaultContextWindow = 32000

// EffectiveContextWindow 返回生效的上下文窗口（未配置时取保守默认值）。
func (mc ModelConfig) EffectiveContextWindow() int {
	if mc.ContextWindow > 0 {
		return mc.ContextWindow
	}
	return DefaultContextWindow
}

// SkillConfig 可复用的技能指令包（启用后追加到主 Agent 动态上下文）。
type SkillConfig struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	Prompt      string `json:"prompt" yaml:"prompt"`
	Enabled     bool   `json:"enabled" yaml:"enabled"`
}

// MCPServerConfig 外部 MCP Server 配置。
//   - Transport=auto（默认）：Command 非空按 stdio，否则按 http
//   - stdio：使用 Command/Args/Env
//   - http/sse：使用 URL/Headers
//
// Headers 为 KEY=VALUE 行（每行一条），值支持 ${ENV} 环境变量展开。
type MCPServerConfig struct {
	ID        string   `json:"id" yaml:"id"`
	Name      string   `json:"name" yaml:"name"`
	Transport string   `json:"transport,omitempty" yaml:"transport,omitempty"` // auto | stdio | http | sse
	Command   string   `json:"command" yaml:"command"`
	Args      []string `json:"args,omitempty" yaml:"args,omitempty"`
	Env       string   `json:"env,omitempty" yaml:"env,omitempty"`
	URL       string   `json:"url,omitempty" yaml:"url,omitempty"`
	Headers   string   `json:"headers,omitempty" yaml:"headers,omitempty"`
	Enabled   bool     `json:"enabled" yaml:"enabled"`
	// AutoApproveReadOnly 对服务器声明 readOnlyHint=true 的工具免审批。
	// 注解来自服务器、不可完全信任，默认关闭。
	AutoApproveReadOnly bool `json:"autoApproveReadOnly,omitempty" yaml:"autoApproveReadOnly,omitempty"`
}

// Config 应用配置根对象。
type Config struct {
	Providers      []ProviderConfig  `json:"providers,omitempty" yaml:"providers,omitempty"`
	Models         []ModelConfig     `json:"models" yaml:"models"`
	ActiveModelID  string            `json:"activeModelId" yaml:"activeModelId"`
	WorkspacePath  string            `json:"workspacePath,omitempty" yaml:"workspacePath,omitempty"` // 上次使用的工作区
	Skills         []SkillConfig     `json:"skills" yaml:"skills"`
	MCPServers     []MCPServerConfig `json:"mcpServers" yaml:"mcpServers"`
	ShellWhitelist []string          `json:"shellWhitelist,omitempty" yaml:"shellWhitelist,omitempty"` // run_shell 允许的程序名（未配置时回退默认）
	// AgentSmithAuto 任务失败/取消后自动运行 agent-smith（默认关闭）。
	AgentSmithAuto bool `json:"agentSmithAuto,omitempty" yaml:"agentSmithAuto,omitempty"`
	// TrustedProjects 已确认信任的项目根（绝对路径，已规范化）。
	TrustedProjects []string `json:"trustedProjects,omitempty" yaml:"trustedProjects,omitempty"`
	// BlockedProjects 审计高危自动拉黑的项目根（绝对路径）。
	BlockedProjects []string `json:"blockedProjects,omitempty" yaml:"blockedProjects,omitempty"`
	// Project 项目级配置（运行时合并视图，不写入全局 config.yml）。
	Project *ProjectConfig `json:"-" yaml:"-"`
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
	m := &Manager{path: filepath.Join(dir, "config.yml")}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// load 从磁盘加载配置；文件不存在时写入默认配置。
// 旧版 config.json 会在首次加载时自动迁移为 config.yml（原文件保留为 .bak）。
func (m *Manager) load() error {
	exists, err := readYAMLFile(m.path, &m.cfg)
	if err != nil {
		return err
	}
	if !exists {
		legacy := filepath.Join(filepath.Dir(m.path), "config.json")
		migrated, merr := migrateJSONToYAML(m.path, legacy, &m.cfg)
		if merr != nil {
			return merr
		}
		if migrated {
			slog.Info("已将 config.json 迁移为 config.yml（旧文件保留为 config.json.bak）")
		} else {
			m.cfg = defaultConfig()
			return m.persistLocked()
		}
	}
	// 旧配置迁移：把每个模型各自保存的 baseURL/apiKey 归并为供应商。
	// 写回失败不阻塞启动（本次内存中已按新结构运行，下次仍可重试）。
	if migrateProviders(&m.cfg) {
		if err := m.persistLocked(); err != nil {
			slog.Warn("配置迁移写回失败（本次仍按新结构运行）", "err", err)
		} else {
			slog.Info("已将模型配置迁移为供应商 + 多模型结构", "providers", len(m.cfg.Providers))
		}
	}
	return nil
}

// migrateProviders 把旧式「每个模型各自存 baseURL/apiKey」的配置归并为供应商：
// 相同 baseURL+apiKey 的模型共用一个 Provider，模型只保留 providerId。
// 返回是否发生变更（需要写回磁盘）。
func migrateProviders(cfg *Config) bool {
	if len(cfg.Providers) > 0 || len(cfg.Models) == 0 {
		return false
	}
	byKey := make(map[string]string) // baseURL + \x00 + apiKey -> provider id
	changed := false
	for i := range cfg.Models {
		mc := &cfg.Models[i]
		baseURL := strings.TrimSpace(mc.BaseURL)
		if baseURL == "" {
			continue
		}
		key := baseURL + "\x00" + mc.APIKey
		pid, ok := byKey[key]
		if !ok {
			pid = providerIDForKey(baseURL, mc.APIKey)
			byKey[key] = pid
			cfg.Providers = append(cfg.Providers, ProviderConfig{
				ID:      pid,
				Name:    providerDisplayName(baseURL),
				BaseURL: baseURL,
				APIKey:  mc.APIKey,
			})
		}
		mc.ProviderID = pid
		mc.BaseURL = ""
		mc.APIKey = ""
		changed = true
	}
	return changed
}

// providerIDForKey 由 baseURL+apiKey 生成稳定 ID：重复迁移得到同一 ID（幂等）。
func providerIDForKey(baseURL, apiKey string) string {
	sum := sha256.Sum256([]byte(baseURL + "\x00" + apiKey))
	return "p-" + hex.EncodeToString(sum[:6])
}

// providerDisplayName 用端点 host 作为供应商默认名（如 api.deepseek.com）。
func providerDisplayName(baseURL string) string {
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return baseURL
}

func defaultConfig() Config {
	return Config{Models: []ModelConfig{}, ActiveModelID: "", ShellWhitelist: DefaultShellWhitelist()}
}

// DefaultShellWhitelist 默认 Shell 白名单（spec 3.3：go 只读子命令、git 只读子命令、目录列表）。
// go/git 的子命令级限制在 tools.ShellRun 内部强制执行。
func DefaultShellWhitelist() []string {
	return []string{"go", "git", "dir", "ls"}
}

// Get 返回配置副本（模型已按所属供应商填充 BaseURL/APIKey）。
func (m *Manager) Get() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := clone(m.cfg)
	applyProviders(&out)
	return out
}

// Save 覆盖保存配置。保存前校验，避免把不可用的模型链写进磁盘：
// 模型 ID/Name/Model 非空且 ID 唯一；ActiveModelID 非空时必须指向已存在的模型。
func (m *Manager) Save(cfg Config) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}
	// 供应商模型的连接字段运行时由 Provider 提供，磁盘上不重复保存
	stripProviderFields(&cfg)
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
	providers := make(map[string]bool, len(cfg.Providers))
	for i, p := range cfg.Providers {
		id := strings.TrimSpace(p.ID)
		if id == "" {
			return fmt.Errorf("第 %d 个供应商缺少 id", i+1)
		}
		if providers[id] {
			return fmt.Errorf("供应商 id 重复: %s", id)
		}
		providers[id] = true
		if strings.TrimSpace(p.Name) == "" {
			return fmt.Errorf("供应商 %s 缺少 name（显示名）", id)
		}
		if strings.TrimSpace(p.BaseURL) == "" {
			return fmt.Errorf("供应商 %q 缺少 baseURL", p.Name)
		}
	}

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
		if pid := strings.TrimSpace(mc.ProviderID); pid != "" && !providers[pid] {
			return fmt.Errorf("模型 %q 引用了不存在的供应商: %s", mc.Name, pid)
		}
		if mc.ContextWindow < 0 {
			return fmt.Errorf("模型 %q 的 contextWindow 不能为负", mc.Name)
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
	return writeYAMLAtomic(m.path, m.cfg)
}

// Effective 返回全局配置与项目级配置（<projectRoot>/.aidog）合并后的副本。
// 项目配置只在内存合并，不写入全局 config.yml；加载失败时记录警告并回退全局配置，
// 不阻塞会话（项目配置坏了不应导致应用不可用）。
func (m *Manager) Effective(projectRoot string) Config {
	global := m.Get()
	if strings.TrimSpace(projectRoot) == "" {
		return global
	}
	st, err := m.ProjectStatus(projectRoot)
	if err != nil {
		slog.Warn("项目配置审计失败，回退全局配置", "root", projectRoot, "err", err)
		return global
	}
	if st.Status != ProjectTrusted {
		slog.Warn("项目配置未信任或已拉黑，回退全局配置",
			"root", projectRoot, "status", st.Status)
		return global
	}
	pc, err := LoadProject(projectRoot)
	if err != nil {
		slog.Warn("加载项目级配置失败，回退全局配置", "root", projectRoot, "err", err)
		return global
	}
	merged, warnings := MergeProject(global, pc)
	for _, w := range warnings {
		slog.Warn("项目级配置引用无效", "root", projectRoot, "warning", w)
	}
	applyProviders(&merged)
	return merged
}

// ActiveModel 返回当前激活的模型配置（已填充所属供应商的连接信息）。
func (m *Manager) ActiveModel() (ModelConfig, error) {
	cfg := m.Get()
	for _, mc := range cfg.Models {
		if mc.ID == cfg.ActiveModelID {
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

// applyProviders 把供应商的连接信息填充到引用它的模型上（只作用于副本）。
func applyProviders(cfg *Config) {
	if len(cfg.Providers) == 0 {
		return
	}
	byID := make(map[string]ProviderConfig, len(cfg.Providers))
	for _, p := range cfg.Providers {
		byID[p.ID] = p
	}
	for i := range cfg.Models {
		mc := &cfg.Models[i]
		if mc.ProviderID == "" {
			continue
		}
		if p, ok := byID[mc.ProviderID]; ok {
			mc.BaseURL = p.BaseURL
			mc.APIKey = p.APIKey
		}
	}
}

// stripProviderFields 落盘前清空供应商模型的连接字段，避免同一密钥在磁盘上重复存储。
func stripProviderFields(cfg *Config) {
	for i := range cfg.Models {
		if cfg.Models[i].ProviderID != "" {
			cfg.Models[i].BaseURL = ""
			cfg.Models[i].APIKey = ""
		}
	}
}

func clone(c Config) Config {
	out := c
	out.Providers = append([]ProviderConfig(nil), c.Providers...)
	out.Models = make([]ModelConfig, len(c.Models))
	for i, mc := range c.Models {
		out.Models[i] = mc
		if mc.IncludeUsage != nil {
			v := *mc.IncludeUsage
			out.Models[i].IncludeUsage = &v
		}
	}
	out.Skills = make([]SkillConfig, len(c.Skills))
	copy(out.Skills, c.Skills)
	out.MCPServers = make([]MCPServerConfig, len(c.MCPServers))
	for i, s := range c.MCPServers {
		out.MCPServers[i] = s
		out.MCPServers[i].Args = append([]string(nil), s.Args...)
	}
	out.ShellWhitelist = append([]string(nil), c.ShellWhitelist...)
	out.TrustedProjects = append([]string(nil), c.TrustedProjects...)
	out.BlockedProjects = append([]string(nil), c.BlockedProjects...)
	return out
}
