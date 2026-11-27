//go:build !windows

// Package winproc 提供 Windows 子进程创建选项的跨平台封装。
package winproc

import "os/exec"

// HideConsole 在非 Windows 平台为空实现：GUI 启动的子进程不会额外弹出控制台窗口。
func HideConsole(cmd *exec.Cmd) {}
