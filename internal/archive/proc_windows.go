//go:build windows

package archive

import (
	"os/exec"
	"syscall"
)

// createNoWindow 隐藏子进程窗口，避免 GUI 程序里闪黑框
const createNoWindow = 0x08000000

// configureCmd 让 7z 在后台无窗口运行
func configureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
