package mcp

// 回归测试：MCP 连接复用必须把工作目录纳入比较，切换 workspace 后要重连。

import (
	"testing"

	"ai-dog-agent/internal/config"
)

func TestServerSessionMatchesWorkdir(t *testing.T) {
	cfg := config.MCPServerConfig{ID: "s1", Command: "npx", Args: []string{"-y", "srv"}}
	ss := &serverSession{cfg: cfg, workdir: "C:/ws/a"}

	if !ss.matches(cfg, "C:/ws/a") {
		t.Fatal("相同配置与工作目录应复用连接")
	}
	if ss.matches(cfg, "C:/ws/b") {
		t.Fatal("切换 workspace 后不应复用旧连接（子进程 cwd 仍是旧目录）")
	}

	changed := cfg
	changed.Args = []string{"-y", "other"}
	if ss.matches(changed, "C:/ws/a") {
		t.Fatal("连接参数变化应重建连接")
	}
}
