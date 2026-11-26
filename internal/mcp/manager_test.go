package mcp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	eintool "github.com/cloudwego/eino/components/tool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/config"
	"ai-dog-agent/internal/eventbus"
)

// TestMain 兼作 MCP stdio 子进程入口：go test 以 AIDOG_MCP_HELPER=1 启动自身时
// 直接运行 server，避免测试框架向 stdout 打印内容破坏 JSON-RPC 流。
func TestMain(m *testing.M) {
	if os.Getenv("AIDOG_MCP_HELPER") == "1" {
		if err := newEchoServer().Run(context.Background(), &sdk.StdioTransport{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type echoIn struct {
	Text string `json:"text"`
}

// newEchoServer 构造带 echo 工具的测试 MCP Server。
func newEchoServer() *sdk.Server {
	srv := sdk.NewServer(&sdk.Implementation{Name: "test-server", Version: "1.0"}, nil)
	sdk.AddTool(srv, &sdk.Tool{
		Name:        "echo",
		Description: "回显文本",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []string{"text"},
		},
	}, func(_ context.Context, _ *sdk.CallToolRequest, in echoIn) (*sdk.CallToolResult, any, error) {
		return &sdk.CallToolResult{
			Content: []sdk.Content{&sdk.TextContent{Text: "echo:" + in.Text}},
		}, nil, nil
	})
	return srv
}

// connectInMemory 用 in-memory transport 建立 client/server 会话。
func connectInMemory(t *testing.T) (*sdk.ClientSession, []*sdk.Tool) {
	t.Helper()
	ctx := context.Background()
	srv := newEchoServer()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: clientName, Version: clientVersion}, nil).
		Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return cs, res.Tools
}

func waitPending(t *testing.T, appr *approval.Manager) eventbus.ApprovalRequestData {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(appr.ListPending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	pending := appr.ListPending()
	if len(pending) == 0 {
		t.Fatal("未收到审批请求")
	}
	return pending[0]
}

// TestWrapToolSchemaAndApprovedCall 工具包装、schema 转换与审批通过后的调用。
func TestWrapToolSchemaAndApprovedCall(t *testing.T) {
	cs, remoteTools := connectInMemory(t)
	if len(remoteTools) != 1 || remoteTools[0].Name != "echo" {
		t.Fatalf("应发现 echo 工具，got %+v", remoteTools)
	}
	appr := approval.New(eventbus.New())
	mgr := NewManager(appr)
	cfg := config.MCPServerConfig{ID: "s1", Name: "demo", Enabled: true}
	tools := mgr.wrap(cfg, &serverSession{cfg: cfg, session: cs, tools: remoteTools}, "sess-1", "")
	if len(tools) != 1 {
		t.Fatalf("应包装 1 个工具，got %d", len(tools))
	}
	it, ok := tools[0].(eintool.InvokableTool)
	if !ok {
		t.Fatalf("应实现 InvokableTool，got %T", tools[0])
	}
	info, err := it.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != "mcp_demo_echo" {
		t.Fatalf("工具名应加前缀，got %q", info.Name)
	}
	if info.Desc != "回显文本" {
		t.Fatalf("描述不符: %q", info.Desc)
	}

	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := it.InvokableRun(context.Background(), `{"text":"hi"}`)
		done <- result{text, err}
	}()
	p := waitPending(t, appr)
	if p.Tool != "mcp:s1" || p.Path != "echo" {
		t.Fatalf("审批 key 不符: %+v", p)
	}
	if err := appr.Decide(p.ID, true, false); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	r := <-done
	if r.err != nil {
		t.Fatalf("调用失败: %v", r.err)
	}
	if r.text != "echo:hi" {
		t.Fatalf("结果不符: %q", r.text)
	}
}

// TestMCPToolDenied 审批拒绝时不调用远端工具。
func TestMCPToolDenied(t *testing.T) {
	cs, remoteTools := connectInMemory(t)
	appr := approval.New(eventbus.New())
	mgr := NewManager(appr)
	cfg := config.MCPServerConfig{ID: "s1", Name: "demo"}
	it := mgr.wrap(cfg, &serverSession{cfg: cfg, session: cs, tools: remoteTools}, "sess-1", "")[0].
		(eintool.InvokableTool)

	errc := make(chan error, 1)
	go func() {
		_, err := it.InvokableRun(context.Background(), `{"text":"hi"}`)
		errc <- err
	}()
	p := waitPending(t, appr)
	_ = appr.Decide(p.ID, false, false)
	if err := <-errc; !errors.Is(err, approval.ErrDenied) {
		t.Fatalf("拒绝后应返回 ErrDenied，got %v", err)
	}
}

// TestMCPToolNoApprover 无审批通道时直接拒绝执行。
func TestMCPToolNoApprover(t *testing.T) {
	cs, remoteTools := connectInMemory(t)
	cfg := config.MCPServerConfig{ID: "s1", Name: "demo"}
	wrapped := NewManager(nil).
		wrap(cfg, &serverSession{cfg: cfg, session: cs, tools: remoteTools}, "s", "")
	it := wrapped[0].(eintool.InvokableTool)
	_, err := it.InvokableRun(context.Background(), `{"text":"hi"}`)
	if err == nil || !strings.Contains(err.Error(), "审批") {
		t.Fatalf("无审批通道应拒绝执行，got %v", err)
	}
}

// TestMCPToolRejectsBadArguments 非法 JSON 参数在审批前就被拒绝。
func TestMCPToolRejectsBadArguments(t *testing.T) {
	cs, remoteTools := connectInMemory(t)
	cfg := config.MCPServerConfig{ID: "s1", Name: "demo"}
	wrapped := NewManager(approval.New(eventbus.New())).
		wrap(cfg, &serverSession{cfg: cfg, session: cs, tools: remoteTools}, "s", "")
	it := wrapped[0].(eintool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{not json`); err == nil ||
		!strings.Contains(err.Error(), "JSON") {
		t.Fatalf("非法 JSON 应报错，got %v", err)
	}
}

// TestToolsSkipUnavailableServer 连接失败的 server 被跳过，不影响返回值。
func TestToolsSkipUnavailableServer(t *testing.T) {
	mgr := NewManager(approval.New(eventbus.New()))
	defer mgr.Close()
	tools := mgr.Tools(context.Background(), []config.MCPServerConfig{{
		ID: "bad", Name: "bad", Command: "ai-dog-agent-no-such-command", Enabled: true,
	}}, "", "s")
	if len(tools) != 0 {
		t.Fatalf("不可用 server 不应产出工具，got %d", len(tools))
	}
}

// TestTestConnectionFailure 连接失败返回错误。
func TestTestConnectionFailure(t *testing.T) {
	mgr := NewManager(approval.New(eventbus.New()))
	_, err := mgr.TestConnection(context.Background(), config.MCPServerConfig{
		Name: "bad", Command: "ai-dog-agent-no-such-command",
	}, "")
	if err == nil {
		t.Fatal("不存在的命令应返回错误")
	}
}

// TestStdioEndToEnd 通过真实 stdio 子进程完成握手、列工具、审批与调用。
func TestStdioEndToEnd(t *testing.T) {
	cfg := config.MCPServerConfig{
		ID: "e2e", Name: "e2e", Enabled: true,
		Command: os.Args[0],
		Args:    []string{"-test.run=^$"},
		Env:     "AIDOG_MCP_HELPER=1",
	}
	appr := approval.New(eventbus.New())
	mgr := NewManager(appr)
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tools := mgr.Tools(ctx, []config.MCPServerConfig{cfg}, "", "sess-e2e")
	if len(tools) != 1 {
		t.Fatalf("stdio server 应产出 1 个工具，got %d", len(tools))
	}
	it := tools[0].(eintool.InvokableTool)
	done := make(chan error, 1)
	var text string
	go func() {
		out, err := it.InvokableRun(ctx, `{"text":"e2e"}`)
		text = out
		done <- err
	}()
	p := waitPending(t, appr)
	_ = appr.Decide(p.ID, true, false)
	if err := <-done; err != nil {
		t.Fatalf("stdio 调用失败: %v", err)
	}
	if text != "echo:e2e" {
		t.Fatalf("结果不符: %q", text)
	}
}

// TestPureHelpers 工具名清洗、环境解析与配置比较。
func TestPureHelpers(t *testing.T) {
	if got := toolName("my server", "read file"); got != "mcp_my_server_read_file" {
		t.Fatalf("toolName = %q", got)
	}
	env := parseEnv("A=1\n\n# comment\nB=2\ninvalid")
	if len(env) != 2 || env[0] != "A=1" || env[1] != "B=2" {
		t.Fatalf("parseEnv = %v", env)
	}
	a := config.MCPServerConfig{Command: "npx", Args: []string{"-y", "x"}, Env: "A=1"}
	if !sameServer(a, a) {
		t.Fatal("相同配置应复用连接")
	}
	b := a
	b.Args = []string{"-y", "y"}
	if sameServer(a, b) {
		t.Fatal("参数变化应触发重连")
	}
}

// TestTransportFor auto 推断与非法值处理。
func TestTransportFor(t *testing.T) {
	cases := []struct {
		cfg     config.MCPServerConfig
		want    string
		wantErr bool
	}{
		{config.MCPServerConfig{Command: "npx"}, "stdio", false},
		{config.MCPServerConfig{URL: "https://x/mcp"}, "http", false},
		{config.MCPServerConfig{Transport: "sse", URL: "https://x/sse"}, "sse", false},
		{config.MCPServerConfig{Transport: "auto", Command: "npx", URL: "https://x"}, "stdio", false},
		{config.MCPServerConfig{Transport: "STDIO", Command: "npx"}, "stdio", false},
		{config.MCPServerConfig{}, "", true},
		{config.MCPServerConfig{Transport: "grpc", Command: "x"}, "", true},
	}
	for _, c := range cases {
		got, err := transportFor(c.cfg)
		if c.wantErr {
			if err == nil {
				t.Fatalf("transportFor(%+v) 应报错", c.cfg)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("transportFor(%+v) = %q, %v; want %q", c.cfg, got, err, c.want)
		}
	}
}

// TestParseHeaders KEY=VALUE 解析、${ENV} 展开与错误行。
func TestParseHeaders(t *testing.T) {
	t.Setenv("AIDOG_MCP_TEST_TOKEN", "secret")
	h, err := parseHeaders("Authorization=Bearer ${AIDOG_MCP_TEST_TOKEN}\n# comment\nX-Empty=\n")
	if err != nil {
		t.Fatalf("parseHeaders: %v", err)
	}
	if h["Authorization"] != "Bearer secret" {
		t.Fatalf("${ENV} 未展开: %q", h["Authorization"])
	}
	if v, ok := h["X-Empty"]; !ok || v != "" {
		t.Fatalf("空值 header 应保留: %v", h)
	}
	if _, err := parseHeaders("BadLine"); err == nil {
		t.Fatal("缺少 '=' 应报错")
	}
	if _, err := parseHeaders("=v"); err == nil {
		t.Fatal("空键应报错")
	}
}

// TestHeadersClient 无 headers 时返回 nil（使用 SDK 默认客户端）。
func TestHeadersClient(t *testing.T) {
	if c, err := headersClient(" \n# comment\n"); err != nil || c != nil {
		t.Fatalf("空 headers 应返回 nil, got %v, %v", c, err)
	}
	c, err := headersClient("X-Test=1")
	if err != nil || c == nil {
		t.Fatalf("有 headers 应返回客户端, got %v, %v", c, err)
	}
	if _, ok := c.Transport.(*headerRoundTripper); !ok {
		t.Fatalf("应使用 headerRoundTripper, got %T", c.Transport)
	}
}
// TestTruncateResult 截断边界与 UTF-8 安全。
func TestTruncateResult(t *testing.T) {
	short := "hello"
	if got := truncateResult(short); got != short {
		t.Fatalf("短结果应原样返回: %q", got)
	}
	long := strings.Repeat("a", MaxToolOutput+10)
	got := truncateResult(long)
	if !strings.Contains(got, "已截断") {
		t.Fatal("超长结果应附截断提示")
	}
	if len(got) <= MaxToolOutput {
		t.Fatalf("截断后应保留上限内容并附提示: %d", len(got))
	}

	// 边界落在多字节字符中间：不能产生半个 rune
	multibyte := strings.Repeat("a", MaxToolOutput-1) + "中文" + "tail"
	got = truncateResult(multibyte)
	if !utf8.ValidString(got) {
		t.Fatal("截断结果必须是合法 UTF-8")
	}
}

// TestRefreshTools 重新拉取工具表。
func TestRefreshTools(t *testing.T) {
	cs, _ := connectInMemory(t)
	ss := &serverSession{session: cs}
	mgr := NewManager(approval.New(eventbus.New()))
	if err := mgr.refreshTools(context.Background(), ss); err != nil {
		t.Fatalf("refreshTools: %v", err)
	}
	if len(ss.tools) != 1 || ss.tools[0].Name != "echo" {
		t.Fatalf("工具表刷新不符: %+v", ss.tools)
	}
}

// TestMCPToolReconnectsAfterSessionClosed 关闭缓存连接后调用应触发重连并成功。
func TestMCPToolReconnectsAfterSessionClosed(t *testing.T) {
	cfg := config.MCPServerConfig{
		ID: "e2e-reconnect", Name: "e2e", Enabled: true,
		Command: os.Args[0],
		Args:    []string{"-test.run=^$"},
		Env:     "AIDOG_MCP_HELPER=1",
	}
	appr := approval.New(eventbus.New())
	mgr := NewManager(appr)
	defer mgr.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tools := mgr.Tools(ctx, []config.MCPServerConfig{cfg}, "", "sess-reconnect")
	if len(tools) != 1 {
		t.Fatalf("应得到 1 个工具，got %d", len(tools))
	}
	it := tools[0].(eintool.InvokableTool)

	mgr.mu.Lock()
	old := mgr.sessions[cfg.ID]
	mgr.mu.Unlock()
	if old == nil {
		t.Fatal("缺少缓存会话")
	}
	_ = old.session.Close()

	done := make(chan error, 1)
	var text string
	go func() {
		out, err := it.InvokableRun(ctx, `{"text":"retry"}`)
		text = out
		done <- err
	}()
	p := waitPending(t, appr)
	_ = appr.Decide(p.ID, true, false)
	if err := <-done; err != nil {
		t.Fatalf("重连后调用应成功: %v", err)
	}
	if text != "echo:retry" {
		t.Fatalf("结果不符: %q", text)
	}
	mgr.mu.Lock()
	reconnected := mgr.sessions[cfg.ID]
	mgr.mu.Unlock()
	if reconnected == old {
		t.Fatal("应已重新建立连接")
	}
}
// TestWrapRecordsReadOnlyHint 只读注解被写入包装器。
func TestWrapRecordsReadOnlyHint(t *testing.T) {
	mgr := NewManager(approval.New(eventbus.New()))
	cfg := config.MCPServerConfig{ID: "s1", Name: "demo"}
	ss := &serverSession{cfg: cfg, tools: []*sdk.Tool{{
		Name: "ro", Description: "只读",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}}}
	wrapped := mgr.wrap(cfg, ss, "s", "")
	mt, ok := wrapped[0].(*mcpTool)
	if !ok || !mt.readOnly {
		t.Fatalf("应记录 readOnlyHint=true: %+v", wrapped[0])
	}
}

// TestReadOnlyAutoApprove 只读 + 开关开启：不产生审批，直接调用。
func TestReadOnlyAutoApprove(t *testing.T) {
	cs, remoteTools := connectInMemory(t)
	appr := approval.New(eventbus.New())
	cfg := config.MCPServerConfig{ID: "s1", Name: "demo", AutoApproveReadOnly: true}
	ss := &serverSession{cfg: cfg, session: cs, tools: remoteTools}
	wrapped := NewManager(appr).wrap(cfg, ss, "s", "")
	mt := wrapped[0].(*mcpTool)
	mt.readOnly = true
	out, err := wrapped[0].(eintool.InvokableTool).InvokableRun(context.Background(), `{"text":"ro"}`)
	if err != nil {
		t.Fatalf("只读自动放行调用失败: %v", err)
	}
	if out != "echo:ro" {
		t.Fatalf("结果不符: %q", out)
	}
	if n := len(appr.ListPending()); n != 0 {
		t.Fatalf("不应产生审批，got %d", n)
	}
}

// TestReadOnlyWithoutSwitchStillApproves 开关关闭时只读工具仍需审批。
func TestReadOnlyWithoutSwitchStillApproves(t *testing.T) {
	cs, remoteTools := connectInMemory(t)
	appr := approval.New(eventbus.New())
	cfg := config.MCPServerConfig{ID: "s1", Name: "demo"} // AutoApproveReadOnly=false
	ss := &serverSession{cfg: cfg, session: cs, tools: remoteTools}
	mt := NewManager(appr).wrap(cfg, ss, "s", "")[0].(*mcpTool)
	mt.readOnly = true

	done := make(chan error, 1)
	go func() {
		_, err := mt.InvokableRun(context.Background(), `{"text":"x"}`)
		done <- err
	}()
	p := waitPending(t, appr)
	_ = appr.Decide(p.ID, true, false)
	if err := <-done; err != nil {
		t.Fatalf("批准后应成功: %v", err)
	}
}

// TestWritableToolAlwaysApproves 非只读工具即使开关开启也需审批。
func TestWritableToolAlwaysApproves(t *testing.T) {
	cs, remoteTools := connectInMemory(t)
	appr := approval.New(eventbus.New())
	cfg := config.MCPServerConfig{ID: "s1", Name: "demo", AutoApproveReadOnly: true}
	ss := &serverSession{cfg: cfg, session: cs, tools: remoteTools}
	mt := NewManager(appr).wrap(cfg, ss, "s", "")[0].(*mcpTool)
	mt.readOnly = false

	done := make(chan error, 1)
	go func() {
		_, err := mt.InvokableRun(context.Background(), `{"text":"x"}`)
		done <- err
	}()
	p := waitPending(t, appr)
	_ = appr.Decide(p.ID, true, false)
	if err := <-done; err != nil {
		t.Fatalf("批准后应成功: %v", err)
	}
}