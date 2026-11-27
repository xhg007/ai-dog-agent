//go:build windows

package winproc

import (
	"os/exec"
	"syscall"
	"testing"
)

// HideConsole 必须设置 CREATE_NO_WINDOW（0x08000000），
// 且不覆盖调用方已有的 SysProcAttr 字段。
func TestHideConsoleSetsCreateNoWindow(t *testing.T) {
	cmd := exec.Command("git", "--version")
	HideConsole(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr 未初始化")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags = %#x，缺少 CREATE_NO_WINDOW", cmd.SysProcAttr.CreationFlags)
	}

	// 已有字段应保留（例如别处设置的 HideWindow）
	cmd2 := exec.Command("git")
	cmd2.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	HideConsole(cmd2)
	if !cmd2.SysProcAttr.HideWindow {
		t.Fatal("HideConsole 覆盖了已有 HideWindow 设置")
	}
	if cmd2.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatal("已有 SysProcAttr 时未合并 CREATE_NO_WINDOW")
	}
}

func TestHideConsoleNilSafe(t *testing.T) {
	HideConsole(nil) // 不应 panic
}
