package tools

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"ai-dog-agent/internal/approval"
	"ai-dog-agent/internal/eventbus"
)

// --- ShellRun：白名单执行内核 ---

// TestShellRun_WhitelistedExecutes 白名单内命令免审批直接执行。
func TestShellRun_WhitelistedExecutes(t *testing.T) {
	ws, _ := writeSetupWS(t)
	bus := eventbus.New()
	sub, cancel := bus.Subscribe()
	defer cancel()
	out, err := ShellRun(context.Background(), ws, []string{"go"}, nil, "s1", "go version")
	if err != nil {
		t.Fatalf("白名单内命令应直接执行: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "go") {
		t.Fatalf("输出不符: %q", out)
	}
	select {
	case ev := <-sub:
		t.Fatalf("白名单内命令不应产生审批事件: %+v", ev)
	default:
	}
}

// TestShellRun_OutsideWhitelistRequiresApproval 白名单外命令经审批后执行。
func TestShellRun_OutsideWhitelistRequiresApproval(t *testing.T) {
	ws, _ := writeSetupWS(t)
	appr := approval.New(eventbus.New())
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		// go 不在白名单，但真实存在：批准后应成功执行
		out, err := ShellRun(context.Background(), ws, []string{"git"}, appr, "s1", "go version")
		done <- result{out, err}
	}()
	mustApprove(appr)
	r := <-done
	if r.err != nil {
		t.Fatalf("批准后应执行成功: %v", r.err)
	}
	if !strings.Contains(strings.ToLower(r.out), "go") {
		t.Fatalf("输出不符: %q", r.out)
	}
}

// TestShellRun_OutsideWhitelistDenied 白名单外命令被拒绝时返回 ErrDenied。
func TestShellRun_OutsideWhitelistDenied(t *testing.T) {
	ws, _ := writeSetupWS(t)
	appr := approval.New(eventbus.New())
	done := make(chan error, 1)
	go func() {
		_, err := ShellRun(context.Background(), ws, []string{"git"}, appr, "s1", "go version")
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(appr.ListPending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	for _, p := range appr.ListPending() {
		_ = appr.Decide(p.ID, false, false)
	}
	err := <-done
	if !errors.Is(err, approval.ErrDenied) {
		t.Fatalf("拒绝后应返回 ErrDenied，got %v", err)
	}
}

// TestShellRun_OutsideWhitelistNoApprover 无审批管理器时白名单外命令直接拒绝。
func TestShellRun_OutsideWhitelistNoApprover(t *testing.T) {
	ws, _ := writeSetupWS(t)
	_, err := ShellRun(context.Background(), ws, []string{"git"}, nil, "s1", "go version")
	if err == nil {
		t.Fatal("白名单外且无审批器应拒绝执行")
	}
	if !strings.Contains(err.Error(), "审批") {
		t.Fatalf("错误应说明需审批: %v", err)
	}
}

// TestShellRun_GitWriteRequiresApproval git 在白名单但写子命令仍需审批。
func TestShellRun_GitWriteRequiresApproval(t *testing.T) {
	ws, _ := writeSetupWS(t)
	appr := approval.New(eventbus.New())
	done := make(chan error, 1)
	go func() {
		_, err := ShellRun(context.Background(), ws, []string{"git"}, appr, "s1", "git push origin main")
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for len(appr.ListPending()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	pending := appr.ListPending()
	if len(pending) == 0 {
		t.Fatal("git push 应触发审批请求而不是直接执行")
	}
	for _, p := range pending {
		_ = appr.Decide(p.ID, false, false)
	}
	if err := <-done; !errors.Is(err, approval.ErrDenied) {
		t.Fatalf("git push 拒绝后应返回 ErrDenied，got %v", err)
	}
}

// TestShellRun_Timeout 超时命令被终止并返回错误。
func TestShellRun_Timeout(t *testing.T) {
	ws, _ := writeSetupWS(t)
	old := shellTimeout
	shellTimeout = 100 * time.Millisecond
	t.Cleanup(func() { shellTimeout = old })

	var cmd string
	switch runtime.GOOS {
	case "windows":
		cmd = "ping -n 30 127.0.0.1"
	default:
		cmd = "ping -c 30 127.0.0.1"
	}
	_, err := ShellRun(context.Background(), ws, []string{"ping"}, nil, "s1", cmd)
	if err == nil {
		t.Fatal("超时命令应返回错误")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误应说明超时: %v", err)
	}
}

// TestShellRun_NoShellInterpretation 分隔符不经 shell 解释，不会拼接执行。
func TestShellRun_NoShellInterpretation(t *testing.T) {
	ws, _ := writeSetupWS(t)
	out, err := ShellRun(context.Background(), ws, []string{"git"}, nil, "s1", "git status && echo hacked")
	if err == nil {
		t.Fatalf("含分隔符的命令应被程序本身拒绝: out=%q", out)
	}
	if strings.Contains(out, "hacked") {
		t.Fatalf("输出不应包含被拼接命令的结果: %q", out)
	}
}

// --- truncateOutput ---

func TestTruncateOutput(t *testing.T) {
	short := "abc"
	if got := truncateOutput(short, 10); got != short {
		t.Fatalf("未超长应原样返回: %q", got)
	}
	long := strings.Repeat("x", 100)
	got := truncateOutput(long, 50)
	if len(got) <= 50 {
		t.Fatalf("截断后应附加提示: %q", got)
	}
	if !strings.Contains(got, "截断") {
		t.Fatalf("截断提示缺失: %q", got)
	}
	if !strings.HasPrefix(got, strings.Repeat("x", 50)) {
		t.Fatalf("截断应保留前缀内容: %q", got)
	}
}

// TestShellRun_ContextCancel 父 ctx 取消时命令被终止并返回「中断」错误。
func TestShellRun_ContextCancel(t *testing.T) {
	ws, _ := writeSetupWS(t)
	var cmd string
	switch runtime.GOOS {
	case "windows":
		cmd = "ping -n 30 127.0.0.1"
	default:
		cmd = "ping -c 30 127.0.0.1"
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	_, err := ShellRun(ctx, ws, []string{"ping"}, nil, "s1", cmd)
	if err == nil {
		t.Fatal("取消后命令应返回错误")
	}
	if !strings.Contains(err.Error(), "中断") {
		t.Fatalf("错误应说明被中断: %v", err)
	}
}
// TestSplitCommand 引号分词：支持空格路径与未闭合引号报错，反斜杠按字面保留。
func TestSplitCommand(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{`go test ./...`, []string{"go", "test", "./..."}, false},
		{`a "b c" d`, []string{"a", "b c", "d"}, false},
		{`a 'b c' d`, []string{"a", "b c", "d"}, false},
		{`"C:\my project\a.go" x`, []string{`C:\my project\a.go`, "x"}, false},
		{`a "b \"c\"" d`, []string{"a", `b "c"`, "d"}, false},
		{`"unterminated`, nil, true},
		{`   `, nil, false},
	}
	for _, c := range cases {
		got, err := splitCommand(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("splitCommand(%q) 应报错", c.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("splitCommand(%q) 失败: %v", c.in, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("splitCommand(%q) = %v, want %v", c.in, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("splitCommand(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}