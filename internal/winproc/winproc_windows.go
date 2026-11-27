//go:build windows

// Package winproc 提供 Windows 子进程创建选项的跨平台封装。
package winproc

import (
	"os/exec"
	"syscall"
)

// createNoWindow（CREATE_NO_WINDOW）让子进程不分配可见控制台窗口。
// 只设 HideWindow 会先创建窗口再隐藏，仍可能闪出黑框；该标志从源头阻止创建。
const createNoWindow = 0x08000000

// HideConsole 让命令及其子进程在无可见控制台窗口的情况下运行。
// GUI 进程（Wails）本身没有控制台，不设置该标志时 git.exe 等会新开黑框。
// 对已有 SysProcAttr 的调用方只合并 CreationFlags，不覆盖其它字段。
func HideConsole(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}
