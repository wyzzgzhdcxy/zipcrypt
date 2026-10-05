//go:build !windows

package archive

import "os/exec"

// configureCmd 非 Windows 平台无需特殊处理
func configureCmd(cmd *exec.Cmd) {}
