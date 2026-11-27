// Package mcp 对接外部 MCP Server（stdio）：启动子进程、发现工具，
// 并把工具包装成 Eino 工具交给主 Agent。
//
// 安全策略：MCP 工具的能力对宿主是未知的，因此每次调用都经审批门
// （「批准并记住」按 server+tool 粒度生效）；无审批通道时直接拒绝执行。
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/winproc"
)

const (
	clientName    = "ai-dog-agent"
	clientVersion = "0.1.0"

	// DefaultMaxConnections MCP 连接默认上限：超出时回收最久未用的空闲连接，
	// 避免 work agent 配置过多导致 stdio 子进程/远程会话数量失控。
	DefaultMaxConnections = 16
)

// Manager 管理 MCP Server 连接（按配置 ID 复用），并向 Agent 提供包装后的工具。
type Manager struct {
	appr *approval.Manager

	maxSessions int // 连接上限；<=0 表示不限制

	mu       sync.Mutex
	sessions map[string]*serverSession
}

// serverSession 一个已连接的 MCP Server。
type serverSession struct {
	cfg     config.MCPServerConfig
	workdir string // 连接建立时的子进程 cwd；切换 workspace 后必须重连
	session *sdk.ClientSession
	tools   []*sdk.Tool

	lastUsed time.Time // 最近一次使用（装配或工具调用），LRU 回收依据
	inflight int       // 正在执行的工具调用数；>0 时不回收
}

// matches 判断已连接会话能否复用于给定配置与工作目录。
// workdir 参与比较：同一 Server 在不同 workspace 下必须有各自的 cwd。
func (ss *serverSession) matches(cfg config.MCPServerConfig, workdir string) bool {
	return ss.workdir == workdir && sameServer(ss.cfg, cfg)
}

// NewManager 创建 MCP 管理器；appr 为 nil 时所有工具调用会被拒绝。
func NewManager(appr *approval.Manager) *Manager {
	return &Manager{
		appr:        appr,
		maxSessions: DefaultMaxConnections,
		sessions:    make(map[string]*serverSession),
	}
}

// SetMaxConnections 调整连接上限（<=0 表示不限制）。
func (m *Manager) SetMaxConnections(n int) {
	m.mu.Lock()
	m.maxSessions = n
	m.mu.Unlock()
}

// Tools 确保启用的 server 已连接，并返回其全部工具的 Eino 包装。
// 单个 server 连接/列工具失败只记录日志并跳过，不影响其他 server 与会话。
func (m *Manager) Tools(ctx context.Context, servers []config.MCPServerConfig,
	workdir, sessionID string) []tool.BaseTool {
	var out []tool.BaseTool
	for _, cfg := range servers {
		if !cfg.Enabled {
			continue
		}
		ss, err := m.ensure(ctx, cfg, workdir)
		if err != nil {
			slog.Warn("MCP Server 不可用，已跳过", "server", cfg.Name, "err", err)
			continue
		}
		// 连接可能长期存活：每次装配重新拉取工具表，感知服务器增删工具。
		if err := m.refreshTools(ctx, ss); err != nil {
			slog.Warn("刷新 MCP 工具表失败，沿用上次结果", "server", cfg.Name, "err", err)
		}
		out = append(out, m.wrap(cfg, ss, sessionID, workdir)...)
	}
	return out
}

// refreshTools 重新拉取工具表并更新缓存。
func (m *Manager) refreshTools(ctx context.Context, ss *serverSession) error {
	res, err := ss.session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	m.mu.Lock()
	ss.tools = res.Tools
	m.mu.Unlock()
	return nil
}

// wrap 把一个已连接 server 的工具包装成 Eino 工具。
func (m *Manager) wrap(cfg config.MCPServerConfig, ss *serverSession,
	sessionID, workdir string) []tool.BaseTool {
	// 工具表可能被 refreshTools 并发替换：取一份快照再包装
	m.mu.Lock()
	remoteTools := append([]*sdk.Tool(nil), ss.tools...)
	m.mu.Unlock()

	var out []tool.BaseTool
	for _, t := range remoteTools {
		info, err := toolInfo(cfg.Name, t)
		if err != nil {
			slog.Warn("MCP 工具 schema 解析失败，已跳过",
				"server", cfg.Name, "tool", t.Name, "err", err)
			continue
		}
		out = append(out, &mcpTool{
			ss:         ss,
			mgr:        m,
			cfg:        cfg,
			workdir:    workdir,
			info:       info,
			serverName: cfg.Name,
			serverID:   cfg.ID,
			toolName:   t.Name,
			appr:       m.appr,
			sessionID:  sessionID,
			readOnly:   t.Annotations != nil && t.Annotations.ReadOnlyHint,
		})
	}
	return out
}

// TestConnection 以一次性连接列出 server 的工具名（设置页「测试连接」用）。
func (m *Manager) TestConnection(ctx context.Context, cfg config.MCPServerConfig,
	workdir string) ([]string, error) {
	ss, err := dial(ctx, cfg, workdir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ss.session.Close() }()
	names := make([]string, 0, len(ss.tools))
	for _, t := range ss.tools {
		names = append(names, t.Name)
	}
	return names, nil
}

// Close 关闭全部 MCP 连接与子进程。
func (m *Manager) Close() {
	m.mu.Lock()
	sessions := m.sessions
	m.sessions = make(map[string]*serverSession)
	m.mu.Unlock()
	for _, ss := range sessions {
		closeSession(ss)
	}
}

// closeSession 关闭单个连接；nil 连接（测试构造/已失效）静默跳过。
func closeSession(ss *serverSession) {
	if ss == nil || ss.session == nil {
		return
	}
	if err := ss.session.Close(); err != nil {
		slog.Warn("关闭 MCP Server 失败", "server", ss.cfg.Name, "err", err)
	}
}

// beginUse / endUse 标记一次工具调用，供 LRU 回收判断连接是否空闲。
func (m *Manager) beginUse(ss *serverSession) {
	if ss == nil {
		return
	}
	m.mu.Lock()
	ss.inflight++
	ss.lastUsed = time.Now()
	m.mu.Unlock()
}

func (m *Manager) endUse(ss *serverSession) {
	if ss == nil {
		return
	}
	m.mu.Lock()
	if ss.inflight > 0 {
		ss.inflight--
	}
	m.mu.Unlock()
}

// pickVictimLocked 在连接数达到上限时选出最久未用的空闲连接并从表中移除（调用方持锁）。
// 所有连接都在执行工具时返回 nil（本次放行，下次再回收）。
func (m *Manager) pickVictimLocked(exceptID string) *serverSession {
	if m.maxSessions <= 0 || len(m.sessions) < m.maxSessions {
		return nil
	}
	var victimID string
	var victim *serverSession
	for id, ss := range m.sessions {
		if id == exceptID || ss.inflight > 0 {
			continue
		}
		if victim == nil || ss.lastUsed.Before(victim.lastUsed) {
			victimID, victim = id, ss
		}
	}
	if victim == nil {
		slog.Warn("MCP 连接数已达上限但没有空闲连接可回收，本次放行",
			"sessions", len(m.sessions), "limit", m.maxSessions)
		return nil
	}
	delete(m.sessions, victimID)
	slog.Info("回收最久未用的 MCP 连接",
		"server", victim.cfg.Name, "sessions", len(m.sessions), "limit", m.maxSessions)
	return victim
}

// ensure 返回已连接会话；配置变化时关闭旧连接并重连。
// 连接数达到上限时先回收最久未用且空闲的连接；dial 期间不持锁，避免阻塞其他 Server。
func (m *Manager) ensure(ctx context.Context, cfg config.MCPServerConfig,
	workdir string) (*serverSession, error) {
	m.mu.Lock()
	if ss, ok := m.sessions[cfg.ID]; ok {
		if ss.matches(cfg, workdir) {
			ss.lastUsed = time.Now()
			m.mu.Unlock()
			return ss, nil
		}
		// 同一 ID 的命令/参数/环境/工作目录变化：从表中移除，锁外关闭后重建
		delete(m.sessions, cfg.ID)
		m.mu.Unlock()
		closeSession(ss)
		return m.reconnect(ctx, cfg, workdir)
	}
	victim := m.pickVictimLocked(cfg.ID)
	m.mu.Unlock()
	closeSession(victim)

	ss, err := dial(ctx, cfg, workdir)
	if err != nil {
		return nil, err
	}
	ss.lastUsed = time.Now()
	m.mu.Lock()
	// 并发 ensure 可能已建立同 ID 连接：保留先到的，关闭本次新建
	if exist, ok := m.sessions[cfg.ID]; ok && exist.matches(cfg, workdir) {
		m.mu.Unlock()
		closeSession(ss)
		return exist, nil
	}
	m.sessions[cfg.ID] = ss
	m.mu.Unlock()
	return ss, nil
}

// reconnect 失效指定 server 的缓存连接并重新建立（调用失败后的重试路径）。
func (m *Manager) reconnect(ctx context.Context, cfg config.MCPServerConfig,
	workdir string) (*serverSession, error) {
	m.mu.Lock()
	if ss, ok := m.sessions[cfg.ID]; ok {
		_ = ss.session.Close()
		delete(m.sessions, cfg.ID)
	}
	m.mu.Unlock()
	return m.ensure(ctx, cfg, workdir)
}

// transportFor 解析实际使用的 transport：auto 时 command 非空按 stdio，否则按 http。
func transportFor(cfg config.MCPServerConfig) (string, error) {
	switch t := strings.ToLower(strings.TrimSpace(cfg.Transport)); t {
	case "stdio", "http", "sse":
		return t, nil
	case "", "auto":
		if strings.TrimSpace(cfg.Command) != "" {
			return "stdio", nil
		}
		if strings.TrimSpace(cfg.URL) != "" {
			return "http", nil
		}
		return "", fmt.Errorf("MCP Server %q 缺少 command 或 url", cfg.Name)
	default:
		return "", fmt.Errorf("MCP Server %q 的 transport 非法: %q（可选 auto/stdio/http/sse）", cfg.Name, cfg.Transport)
	}
}

// dial 按 transport 建立连接（stdio 子进程 / Streamable HTTP / 旧版 SSE），
// 完成 MCP 握手并发现工具。
func dial(ctx context.Context, cfg config.MCPServerConfig, workdir string) (*serverSession, error) {
	kind, err := transportFor(cfg)
	if err != nil {
		return nil, err
	}
	client := sdk.NewClient(&sdk.Implementation{
		Name:    clientName,
		Version: clientVersion,
	}, nil)

	var transport sdk.Transport
	switch kind {
	case "stdio":
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if workdir != "" {
			cmd.Dir = workdir
		}
		// GUI 进程没有控制台：MCP stdio 启动器（npx/uvx 等）不分配可见窗口
		winproc.HideConsole(cmd)
		if env := parseEnv(cfg.Env); len(env) > 0 {
			cmd.Env = append(os.Environ(), env...)
		}
		transport = &sdk.CommandTransport{Command: cmd}
	case "http", "sse":
		hc, herr := headersClient(cfg.Headers)
		if herr != nil {
			return nil, fmt.Errorf("MCP Server %q 的 headers 无效: %w", cfg.Name, herr)
		}
		if kind == "http" {
			transport = &sdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: hc}
		} else {
			transport = &sdk.SSEClientTransport{Endpoint: cfg.URL, HTTPClient: hc}
		}
	default:
		return nil, fmt.Errorf("MCP Server %q 的 transport 非法: %q", cfg.Name, cfg.Transport)
	}

	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 MCP Server %q 失败: %w", cfg.Name, err)
	}
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		_ = cs.Close()
		return nil, fmt.Errorf("列出 MCP Server %q 工具失败: %w", cfg.Name, err)
	}
	return &serverSession{cfg: cfg, workdir: workdir, session: cs, tools: res.Tools}, nil
}

// headerRoundTripper 为每个请求注入静态 header（已有同名 header 时不覆盖）。
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (r *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range r.headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	return r.base.RoundTrip(req)
}

// parseHeaders 解析 KEY=VALUE 行，值支持 ${ENV} 展开。
// 错误信息只报行号，不打印可能含密钥的内容。
func parseHeaders(raw string) (map[string]string, error) {
	out := map[string]string{}
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("headers 第 %d 行缺少 '='", i+1)
		}
		k = strings.TrimSpace(k)
		if k == "" {
			return nil, fmt.Errorf("headers 第 %d 行键为空", i+1)
		}
		out[k] = os.ExpandEnv(strings.TrimSpace(v))
	}
	return out, nil
}

// headersClient 返回带静态 header 的 HTTP 客户端；无 headers 时返回 nil 使用 SDK 默认。
// 不设置 Client.Timeout：远程 MCP 的 SSE 长连接需要由 ctx 控制生命周期。
func headersClient(raw string) (*http.Client, error) {
	hdrs, err := parseHeaders(raw)
	if err != nil {
		return nil, err
	}
	if len(hdrs) == 0 {
		return nil, nil
	}
	return &http.Client{Transport: &headerRoundTripper{base: http.DefaultTransport, headers: hdrs}}, nil
}

// mcpTool 是 MCP 工具在 Eino 中的 InvokableTool 包装。
// session 可能因重连被替换，读写经 mu 保护。
type mcpTool struct {
	mu         sync.RWMutex
	ss         *serverSession // 持有连接会话：调用期间标记 inflight，重连后替换
	mgr        *Manager
	cfg        config.MCPServerConfig
	workdir    string
	info       *schema.ToolInfo
	serverName string
	serverID   string
	toolName   string
	appr       *approval.Manager
	sessionID  string
	readOnly   bool // 来自服务器声明的 readOnlyHint（不可完全信任）
}

func (t *mcpTool) serverSession() *serverSession {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ss
}

func (t *mcpTool) setSession(ss *serverSession) {
	t.mu.Lock()
	t.ss = ss
	t.mu.Unlock()
}

// Info 返回工具元数据（名称已加 mcp_<server>_ 前缀）。
func (t *mcpTool) Info(_ context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun 审批后调用 MCP 工具，返回文本化结果。
func (t *mcpTool) InvokableRun(ctx context.Context, argumentsInJSON string,
	_ ...tool.Option) (string, error) {
	var args any
	if strings.TrimSpace(argumentsInJSON) != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
			return "", fmt.Errorf("MCP 工具参数不是合法 JSON: %w", err)
		}
	}
	// 只读工具仅在 server 显式开启 AutoApproveReadOnly 时免审批；
	// 注解来自服务器、不可完全信任，默认仍走审批。
	autoApprove := t.readOnly && t.cfg.AutoApproveReadOnly
	if !autoApprove {
		if t.appr == nil {
			return "", errors.New("MCP 工具缺少审批通道，已拒绝执行")
		}
		preview := fmt.Sprintf("MCP Server: %s\n工具: %s\n参数: %s",
			t.serverName, t.toolName, argumentsInJSON)
		if _, err := t.appr.RequestContext(ctx, t.sessionID,
			"mcp:"+t.serverID, t.toolName, preview); err != nil {
			return "", err
		}
	}
	ss := t.serverSession()
	if ss == nil || ss.session == nil {
		return "", fmt.Errorf("MCP 工具 %s 的连接已失效", t.toolName)
	}
	// 调用期间标记连接在用（LRU 不回收）；重连后切换到新连接
	var used *serverSession
	if t.mgr != nil {
		used = ss
		t.mgr.beginUse(used)
	}
	defer func() {
		if used != nil && t.mgr != nil {
			t.mgr.endUse(used)
		}
	}()
	res, err := ss.session.CallTool(ctx, &sdk.CallToolParams{
		Name:      t.toolName,
		Arguments: args,
	})
	if err != nil && t.mgr != nil {
		// 连接可能已失效（子进程退出/远程断开）：失效缓存并重连后重试一次
		slog.Warn("MCP 工具调用失败，尝试重连后重试",
			"server", t.serverName, "tool", t.toolName, "err", err)
		// 先退出旧连接的使用计数，再重连（否则旧连接会被 LRU 视为在用）
		t.mgr.endUse(used)
		used = nil
		if newSS, rerr := t.mgr.reconnect(ctx, t.cfg, t.workdir); rerr == nil {
			t.setSession(newSS)
			used = newSS
			t.mgr.beginUse(newSS)
			res, err = newSS.session.CallTool(ctx, &sdk.CallToolParams{
				Name:      t.toolName,
				Arguments: args,
			})
		}
	}
	if err != nil {
		return "", fmt.Errorf("MCP 工具 %s 调用失败: %w", t.toolName, err)
	}
	text := truncateResult(renderResult(res))
	if res.IsError {
		return "", fmt.Errorf("MCP 工具 %s 返回错误:\n%s", t.toolName, text)
	}
	return text, nil
}

// MaxToolOutput MCP 工具结果截断上限（与 shell 的 64KB 一致）。
const MaxToolOutput = 64 << 10

// truncateResult 截断超长结果并附提示；按 UTF-8 边界切断，避免半个字符。
func truncateResult(s string) string {
	if len(s) <= MaxToolOutput {
		return s
	}
	cut := MaxToolOutput
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…（输出超长，已截断至 %d 字节）", MaxToolOutput)
}

// renderResult 把 MCP 结果转成模型可读文本：文本内容直出，其余类型 JSON 化。
func renderResult(res *sdk.CallToolResult) string {
	if res == nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			sb.WriteString(tc.Text)
			sb.WriteString("\n")
			continue
		}
		if b, err := json.Marshal(c); err == nil {
			sb.Write(b)
			sb.WriteString("\n")
		}
	}
	if sb.Len() == 0 && res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			return string(b)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// toolInfo 把 MCP 工具元数据转换为 Eino ToolInfo（参数用其 JSON Schema）。
func toolInfo(serverName string, t *sdk.Tool) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{
		Name: toolName(serverName, t.Name),
		Desc: t.Description,
	}
	if t.InputSchema != nil {
		b, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, err
		}
		var s jsonschema.Schema
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, err
		}
		info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&s)
	}
	return info, nil
}

// toolName 生成不会与内置工具冲突的名字：mcp_<server>_<tool>。
func toolName(server, tool string) string {
	return sanitize("mcp_" + server + "_" + tool)
}

// sanitize 只保留 [A-Za-z0-9_-]，其余字符替换为下划线。
func sanitize(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	return sb.String()
}

// parseEnv 解析 "KEY=VALUE" 行（跳过空行与 # 注释）。
func parseEnv(env string) []string {
	var out []string
	for _, line := range strings.Split(env, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// sameServer 判断配置中的连接参数是否一致（一致则复用连接）。
// AutoApproveReadOnly 只影响审批、不影响连接，不参与比较。
func sameServer(a, b config.MCPServerConfig) bool {
	return strings.EqualFold(strings.TrimSpace(a.Transport), strings.TrimSpace(b.Transport)) &&
		a.Command == b.Command &&
		strings.Join(a.Args, "\x00") == strings.Join(b.Args, "\x00") &&
		a.Env == b.Env &&
		a.URL == b.URL &&
		a.Headers == b.Headers
}
