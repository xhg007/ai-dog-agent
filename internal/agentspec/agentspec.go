// Package agentspec 读写项目级子 Agent 配置：workspace/.aidog/agents/*.json。
package agentspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Spec 子 Agent 定义（需求文档 §3.1 约定的 Schema）。
// AgentID 是该子 Agent 的唯一不变 ID（UUID 无连字符）：
// 保存时缺省自动生成并固化；后续实例化/归因/缓存均以此为准。
type Spec struct {
	AgentID      string        `json:"agent_id,omitempty"`
	Name         string        `json:"name"`
	Description  string        `json:"description"`
	SystemPrompt string        `json:"system_prompt"`
	Model        string        `json:"model,omitempty"`
	Tools        []string      `json:"tools,omitempty"`
	Skills       []string      `json:"skills,omitempty"`
	MCPServers   []string      `json:"mcp_servers,omitempty"`
	MaxTurns     int           `json:"max_turns,omitempty"`
	Budget       Budget        `json:"budget,omitzero"`
	Context      ContextConfig `json:"context,omitzero"`
}

// Budget 单次 Agent 执行的资源上限；零值表示使用 Runtime 默认值。
type Budget struct {
	MaxSteps           int     `json:"max_steps,omitempty"`            // 最大执行步骤
	MaxTokens          int64   `json:"max_tokens,omitempty"`           // 最大累计 token（API usage）
	MaxDurationSeconds int     `json:"max_duration_seconds,omitempty"` // 最大执行时间
	MaxToolCalls       int     `json:"max_tool_calls,omitempty"`       // 最大工具调用次数
	MaxCost            float64 `json:"max_cost,omitempty"`             // 单任务预算（元）
	MaxRetries         int     `json:"max_retries,omitempty"`          // 模型错误重试次数
}

// ContextConfig 上下文压缩策略；零值表示使用默认值。
type ContextConfig struct {
	CompressThreshold float64 `json:"compress_threshold,omitempty"` // 触发压缩的窗口占比 (0,1]
	KeepRecent        int     `json:"keep_recent,omitempty"`        // 保留最近轮数
}

// DefaultCompressThreshold / DefaultKeepRecent 上下文压缩默认值。
const (
	DefaultCompressThreshold = 0.7
	DefaultKeepRecent        = 6
)

// EffectiveThreshold 返回生效的压缩阈值（未配置时取默认值）。
func (c ContextConfig) EffectiveThreshold() float64 {
	if c.CompressThreshold > 0 {
		return c.CompressThreshold
	}
	return DefaultCompressThreshold
}

// EffectiveKeepRecent 返回生效的保留轮数（未配置时取默认值）。
func (c ContextConfig) EffectiveKeepRecent() int {
	if c.KeepRecent > 0 {
		return c.KeepRecent
	}
	return DefaultKeepRecent
}

// Dir 返回项目级 Agent 配置目录。
func Dir(root string) string {
	return filepath.Join(root, ".aidog", "agents")
}

// windowsReservedNames Windows 保留设备名（大小写不敏感，带扩展名同样非法）。
// 跨平台统一拒绝：Agent 配置随项目走，同一份 .aidog 可能在 Windows 上打开。
var windowsReservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

func validateName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" {
		return errors.New("Agent 名称不能为空")
	}
	if n == "." || n == ".." || strings.ContainsAny(n, `/\`) || strings.Contains(n, "..") {
		return fmt.Errorf("非法 Agent 名称: %q", name)
	}
	// 文件名非法字符：Windows 上这些字符会让 os.WriteFile 直接失败，
	// 提前给出可读错误，避免用户看到系统原始报错。
	if strings.ContainsAny(n, `:*?"<>|`) || strings.ContainsFunc(n, func(r rune) bool { return r < 0x20 }) {
		return fmt.Errorf("Agent 名称含非法文件名字符（不允许 : * ? \" < > | 与控制字符）: %q", name)
	}
	base := strings.ToLower(strings.TrimSuffix(n, filepath.Ext(n)))
	if windowsReservedNames[base] {
		return fmt.Errorf("Agent 名称是系统保留名: %q", name)
	}
	return nil
}

func validate(spec Spec) error {
	if err := validateName(spec.Name); err != nil {
		return err
	}
	if strings.TrimSpace(spec.SystemPrompt) == "" {
		return errors.New("system_prompt 不能为空")
	}
	b := spec.Budget
	if b.MaxSteps < 0 || b.MaxTokens < 0 || b.MaxDurationSeconds < 0 ||
		b.MaxToolCalls < 0 || b.MaxCost < 0 || b.MaxRetries < 0 {
		return errors.New("budget 各项不能为负")
	}
	c := spec.Context
	if c.CompressThreshold < 0 || c.CompressThreshold > 1 {
		return errors.New("context.compress_threshold 必须在 [0,1] 范围内（0 表示默认值）")
	}
	if c.KeepRecent < 0 {
		return errors.New("context.keep_recent 不能为负")
	}
	return nil
}

// Save 写入（覆盖）一个子 Agent 配置；agent_id 缺省时自动生成并固化。
// 已有同名配置则沿用其 ID（编辑保存不换 ID），仅全新配置生成新 ID。
func Save(root string, spec Spec) error {
	if err := validate(spec); err != nil {
		return err
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("未设置工作区，无法保存项目 Agent 配置")
	}
	dir := Dir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	if !IsValidAgentID(spec.AgentID) {
		if old := readSpecFile(filepath.Join(dir, strings.TrimSpace(spec.Name)+".json")); old != nil && IsValidAgentID(old.AgentID) {
			spec.AgentID = old.AgentID // 编辑已有配置：ID 保持不变
		} else {
			spec.AgentID = NewAgentID()
		}
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	target := filepath.Join(dir, strings.TrimSpace(spec.Name)+".json")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("替换配置失败: %w", err)
	}
	return nil
}

// Validate 校验 Spec 结构（smith 预校验与外部调用）。
func Validate(spec Spec) error { return validate(spec) }

// backupDir 返回 Agent 配置备份目录（.aidog/agents/.backup）。
func backupDir(root string) string { return filepath.Join(Dir(root), ".backup") }

// backupLimit 每个 Agent 保留的备份份数。
const backupLimit = 10

// Backup 在覆盖前备份现有配置；配置不存在时返回空路径（新建 Agent 无需备份）。
// 备份文件位于 .aidog/agents/.backup/（.aidog 已被 .gitignore 忽略）。
func Backup(root, name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	if strings.TrimSpace(root) == "" {
		return "", errors.New("未设置工作区，无法备份 Agent 配置")
	}
	name = strings.TrimSpace(name)
	src := filepath.Join(Dir(root), name+".json")
	data, err := os.ReadFile(src)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取 Agent 配置失败: %w", err)
	}
	dir := backupDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建备份目录失败: %w", err)
	}
	stamp := time.Now().Format("20060102-150405.000000000")
	dst := filepath.Join(dir, name+"-"+stamp+".json")
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return "", fmt.Errorf("写入备份失败: %w", err)
	}
	pruneBackups(dir, name, backupLimit)
	return dst, nil
}

// pruneBackups 删除同名 Agent 超出 limit 的最旧备份（文件名含时间戳，字典序即时间序）。
func pruneBackups(dir, name string, limit int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := name + "-"
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		files = append(files, e.Name())
	}
	if len(files) <= limit {
		return
	}
	sort.Strings(files)
	for _, f := range files[:len(files)-limit] {
		_ = os.Remove(filepath.Join(dir, f))
	}
}

// BackupInfo 一份 Agent 配置备份。
type BackupInfo struct {
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"createdAt"`
}

// ListBackups 返回指定 Agent 的备份，按时间倒序（最新在前）。
func ListBackups(root, name string) ([]BackupInfo, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("未设置工作区")
	}
	name = strings.TrimSpace(name)
	dir := backupDir(root)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []BackupInfo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取备份目录失败: %w", err)
	}
	prefix := name + "-"
	out := make([]BackupInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupInfo{Path: filepath.Join(dir, e.Name()), CreatedAt: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Restore 用指定备份覆盖 Agent 配置；backupPath 必须位于 .backup 目录内。
// 覆盖前会先备份当前配置（撤销本身也可回溯）。
func Restore(root, name, backupPath string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("未设置工作区")
	}
	name = strings.TrimSpace(name)
	dir := backupDir(root)
	cleanDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	cleanPath, err := filepath.Abs(backupPath)
	if err != nil {
		return fmt.Errorf("非法备份路径: %w", err)
	}
	rel, err := filepath.Rel(cleanDir, cleanPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("非法备份路径: %s", backupPath)
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return fmt.Errorf("读取备份失败: %w", err)
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return fmt.Errorf("解析备份失败: %w", err)
	}
	if spec.Name != name {
		return fmt.Errorf("备份不属于该 Agent: %s", spec.Name)
	}
	if err := validate(spec); err != nil {
		return fmt.Errorf("备份内容无效: %w", err)
	}
	if _, err := Backup(root, name); err != nil {
		return err
	}
	target := filepath.Join(Dir(root), name+".json")
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("替换配置失败: %w", err)
	}
	return nil
}

// readSpecFile 读取单个配置文件；损坏或不存在返回 nil。
func readSpecFile(path string) *Spec {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil || strings.TrimSpace(spec.Name) == "" {
		return nil
	}
	return &spec
}

// List 返回全部子 Agent 配置，按名称排序；目录不存在时返回空列表。
// 手工配置缺 agent_id 时自动生成并写回固化（迁移）。
func List(root string) ([]Spec, error) {
	if strings.TrimSpace(root) == "" {
		return []Spec{}, nil
	}
	entries, err := os.ReadDir(Dir(root))
	if errors.Is(err, os.ErrNotExist) {
		return []Spec{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Spec, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		spec := readSpecFile(filepath.Join(Dir(root), e.Name()))
		if spec == nil {
			continue // 跳过损坏文件，不影响其余配置
		}
		if !IsValidAgentID(spec.AgentID) {
			spec.AgentID = NewAgentID()
			if fixed, err := json.MarshalIndent(spec, "", "  "); err == nil {
				_ = os.WriteFile(filepath.Join(Dir(root), e.Name()), fixed, 0o600)
			}
		}
		out = append(out, *spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete 删除指定名称的子 Agent 配置。
func Delete(root, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if strings.TrimSpace(root) == "" {
		return errors.New("未设置工作区")
	}
	if err := os.Remove(filepath.Join(Dir(root), strings.TrimSpace(name)+".json")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("配置不存在: %s", name)
		}
		return err
	}
	return nil
}
