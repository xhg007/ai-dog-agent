package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagerSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	m := &Manager{path: path}
	m.cfg = defaultConfig()

	cfg := m.Get()
	cfg.Models = []ModelConfig{{
		ID: "m1", Name: "DeepSeek", BaseURL: "https://api.deepseek.com/v1",
		APIKey: "sk-test", Model: "deepseek-chat", PriceIn: 1, PriceOut: 2,
	}}
	cfg.ActiveModelID = "m1"
	if err := m.Save(cfg); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	m2 := &Manager{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("配置文件未写入: %v", err)
	}
	_ = data
	loaded := m2.Get()
	_ = loaded

	// 通过重新加载验证
	m3 := &Manager{path: path}
	if err := m3.load(); err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	got, err := m3.ActiveModel()
	if err != nil {
		t.Fatalf("ActiveModel 失败: %v", err)
	}
	if got.Model != "deepseek-chat" || got.APIKey != "sk-test" {
		t.Fatalf("回读结果不符: %+v", got)
	}
}

func TestActiveModelNotConfigured(t *testing.T) {
	m := &Manager{cfg: defaultConfig()}
	if _, err := m.ActiveModel(); err == nil {
		t.Fatal("未配置模型时应返回错误")
	}
}

func TestSkillsAndMCPServersRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{path: filepath.Join(dir, "config.json")}
	m.cfg = defaultConfig()

	cfg := m.Get()
	cfg.Skills = []SkillConfig{{
		ID: "skill-1", Name: "Go 审查", Description: "Go 代码审查清单",
		Prompt: "检查错误处理与并发安全", Enabled: true,
	}}
	cfg.MCPServers = []MCPServerConfig{{
		ID: "mcp-1", Name: "本地文件系统", Command: "npx",
		Args: []string{"-y", "@modelcontextprotocol/server-filesystem"},
		Env: "ROOT=/workspace\nDEBUG=0", Enabled: true,
	}}
	if err := m.Save(cfg); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}

	m2 := &Manager{path: filepath.Join(dir, "config.json")}
	if err := m2.load(); err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	got := m2.Get()
	if len(got.Skills) != 1 || got.Skills[0].Name != "Go 审查" ||
		got.Skills[0].Prompt != "检查错误处理与并发安全" || !got.Skills[0].Enabled {
		t.Fatalf("Skills 回读不符: %+v", got.Skills)
	}
	if len(got.MCPServers) != 1 || got.MCPServers[0].Command != "npx" ||
		len(got.MCPServers[0].Args) != 2 || got.MCPServers[0].Env != "ROOT=/workspace\nDEBUG=0" {
		t.Fatalf("MCPServers 回读不符: %+v", got.MCPServers)
	}
}

func TestGetReturnsIndependentCopyOfNewSlices(t *testing.T) {
	m := &Manager{cfg: defaultConfig()}
	m.cfg.Skills = []SkillConfig{{ID: "s1", Name: "原始"}}
	m.cfg.MCPServers = []MCPServerConfig{{ID: "c1", Name: "原始"}}

	got := m.Get()
	got.Skills[0].Name = "被修改"
	got.MCPServers[0].Name = "被修改"
	if m.cfg.Skills[0].Name != "原始" || m.cfg.MCPServers[0].Name != "原始" {
		t.Fatal("Get 应返回深拷贝，修改返回值不得污染内部配置")
	}
}

// --- Shell 白名单（M4 FR-4.2） ---

func TestDefaultShellWhitelist(t *testing.T) {
	m := &Manager{path: filepath.Join(t.TempDir(), "config.json")}
	m.cfg = defaultConfig()
	got := m.ShellWhitelist()
	want := []string{"go", "git", "dir", "ls"}
	if len(got) != len(want) {
		t.Fatalf("默认白名单应为 %v，got %v", want, got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("默认白名单第 %d 项应为 %q，got %q", i, w, got[i])
		}
	}
}

func TestShellWhitelist_FallbackAndSave(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{path: filepath.Join(dir, "config.json")}
	m.cfg = defaultConfig()

	// 用户配置了空列表 → 回退默认值
	if wl := m.ShellWhitelist(); len(wl) != 4 {
		t.Fatalf("未配置时应回退默认白名单，got %v", wl)
	}

	cfg := m.Get()
	cfg.ShellWhitelist = []string{"go", "python"}
	if err := m.Save(cfg); err != nil {
		t.Fatalf("Save 失败: %v", err)
	}
	m2 := &Manager{path: filepath.Join(dir, "config.json")}
	if err := m2.load(); err != nil {
		t.Fatalf("重新加载失败: %v", err)
	}
	got := m2.ShellWhitelist()
	if len(got) != 2 || got[0] != "go" || got[1] != "python" {
		t.Fatalf("自定义白名单回读不符: %v", got)
	}
}

func TestCloneShellWhitelistIndependent(t *testing.T) {
	m := &Manager{cfg: defaultConfig()}
	got := m.Get()
	got.ShellWhitelist[0] = "被修改"
	if m.cfg.ShellWhitelist[0] == "被修改" {
		t.Fatal("clone 应深拷贝 ShellWhitelist，修改返回值不得污染内部配置")
	}
}

// TestSaveValidation 保存前校验：模型字段缺失、ID 重复、ActiveModelID 悬空均被拒。
func TestSaveValidation(t *testing.T) {
	newMgr := func() *Manager {
		return &Manager{path: filepath.Join(t.TempDir(), "config.json"), cfg: defaultConfig()}
	}
	cases := []struct {
		name string
		cfg  Config
	}{
		{"模型缺少 id", Config{Models: []ModelConfig{{Name: "A", Model: "m"}}}},
		{"模型缺少 name", Config{Models: []ModelConfig{{ID: "1", Model: "m"}}}},
		{"模型缺少 model", Config{Models: []ModelConfig{{ID: "1", Name: "A"}}}},
		{"模型 id 重复", Config{Models: []ModelConfig{
			{ID: "1", Name: "A", Model: "m"},
			{ID: "1", Name: "B", Model: "n"},
		}}},
		{"activeModelId 悬空", Config{ActiveModelID: "missing"}},
		{"Skill id 重复", Config{Skills: []SkillConfig{{ID: "s"}, {ID: "s"}}}},
		{"MCP id 重复", Config{MCPServers: []MCPServerConfig{{ID: "c"}, {ID: "c"}}}},
		{"白名单含空项", Config{ShellWhitelist: []string{"go", " "}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := newMgr().Save(tc.cfg); err == nil {
				t.Fatalf("应拒绝非法配置: %+v", tc.cfg)
			}
		})
	}

	// 合法的空配置（尚未添加模型）应允许保存
	if err := newMgr().Save(Config{}); err != nil {
		t.Fatalf("空配置应允许保存: %v", err)
	}
}
// TestVersionIncrementsOnSave 配置版本随成功保存递增，非法保存不递增。
func TestVersionIncrementsOnSave(t *testing.T) {
	m := &Manager{path: filepath.Join(t.TempDir(), "config.json"), cfg: defaultConfig()}
	if v := m.Version(); v != 0 {
		t.Fatalf("初始版本应为 0，got %d", v)
	}
	if err := m.Save(Config{}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if v := m.Version(); v != 1 {
		t.Fatalf("保存后版本应为 1，got %d", v)
	}
	if err := m.Save(Config{ActiveModelID: "missing"}); err == nil {
		t.Fatal("非法配置应被拒绝")
	}
	if v := m.Version(); v != 1 {
		t.Fatalf("非法保存不应递增版本，got %d", v)
	}
	if err := m.Save(Config{}); err != nil {
		t.Fatal(err)
	}
	if v := m.Version(); v != 2 {
		t.Fatalf("再次保存后版本应为 2，got %d", v)
	}
}
// TestMCPValidation MCP 连接字段校验：transport/command/url/headers。
func TestMCPValidation(t *testing.T) {
	newMgr := func() *Manager {
		return &Manager{path: filepath.Join(t.TempDir(), "config.json"), cfg: defaultConfig()}
	}
	bad := []struct {
		name string
		cfg  Config
	}{
		{"auto 缺 command/url", Config{MCPServers: []MCPServerConfig{{ID: "m1", Name: "m"}}}},
		{"stdio 缺 command", Config{MCPServers: []MCPServerConfig{{ID: "m1", Name: "m", Transport: "stdio"}}}},
		{"http 缺 url", Config{MCPServers: []MCPServerConfig{{ID: "m1", Name: "m", Transport: "http"}}}},
		{"sse 缺 url", Config{MCPServers: []MCPServerConfig{{ID: "m1", Name: "m", Transport: "sse"}}}},
		{"非法 transport", Config{MCPServers: []MCPServerConfig{{ID: "m1", Name: "m", Transport: "grpc", Command: "x"}}}},
		{"headers 无等号", Config{MCPServers: []MCPServerConfig{
			{ID: "m1", Name: "m", Transport: "http", URL: "https://x/mcp", Headers: "Authorization"},
		}}},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if err := newMgr().Save(tc.cfg); err == nil {
				t.Fatalf("应拒绝非法 MCP 配置: %+v", tc.cfg)
			}
		})
	}

	ok := []Config{
		{MCPServers: []MCPServerConfig{{ID: "m1", Name: "m", Command: "npx"}}},
		{MCPServers: []MCPServerConfig{{ID: "m2", Name: "m", Transport: "http",
			URL: "https://x/mcp", Headers: "Authorization=Bearer ${TOKEN}\n# comment"}}},
		{MCPServers: []MCPServerConfig{{ID: "m3", Name: "m", Transport: "sse", URL: "https://x/sse"}}},
	}
	for i, cfg := range ok {
		if err := newMgr().Save(cfg); err != nil {
			t.Fatalf("合法配置应允许保存(%d): %v", i, err)
		}
	}
}