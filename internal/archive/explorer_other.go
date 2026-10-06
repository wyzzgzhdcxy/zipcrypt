//go:build !windows

package archive

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// openExplorerDir 打开一个目录窗口
func openExplorerDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	return launch(abs)
}

// openExplorerSelect 打开文件所在目录。非 Windows 下没有等价 API，
// 退化成只打开目录——大多数文件管理器默认会顺带高亮该文件。
func openExplorerSelect(file string) error {
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	return launch(filepath.Dir(abs))
}

func launch(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("explorer.exe", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	configureCmd(cmd)
	return cmd.Start()
}
