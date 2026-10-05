package archive

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// sevenZipNames 7-Zip 命令行程序的可执行文件名，按优先级排列
var sevenZipNames = []string{"7z.exe", "7zz.exe", "7za.exe", "7zr.exe", "7z", "7zz", "7za"}

// versionRe 匹配 "7-Zip 26.03 (x64) : Copyright ..."
var versionRe = regexp.MustCompile(`7-?Zip\s+([0-9]+(?:\.[0-9]+)*)`)

// candidateDirs 返回常见安装位置
func candidateDirs() []string {
	var dirs []string
	if pf := os.Getenv("ProgramFiles"); pf != "" {
		dirs = append(dirs, filepath.Join(pf, "7-Zip"))
	}
	if pf := os.Getenv("ProgramFiles(x86)"); pf != "" {
		dirs = append(dirs, filepath.Join(pf, "7-Zip"))
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs,
			filepath.Join(local, "Programs", "7-Zip"),
			filepath.Join(local, "Microsoft", "WindowsApps"),
		)
	}
	if pd := os.Getenv("ProgramData"); pd != "" {
		dirs = append(dirs, filepath.Join(pd, "chocolatey", "bin"))
	}
	return dirs
}

// Locate7z 定位 7-Zip 可执行文件。
// override 非空时优先使用（用户手动指定），随后依次尝试 PATH 与常见安装目录。
func Locate7z(override string) SevenZipInfo {
	tried := make([]string, 0, 8)

	try := func(path string) string {
		if path == "" {
			return ""
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return ""
		}
		return path
	}

	var found string
	if override != "" {
		tried = append(tried, override+" (手动指定)")
		found = try(override)
	}
	if found == "" {
		for _, name := range sevenZipNames {
			p, err := exec.LookPath(name)
			if err != nil {
				continue
			}
			if p = try(p); p != "" {
				found = p
				break
			}
		}
	}
	if found == "" {
		for _, dir := range candidateDirs() {
			for _, name := range sevenZipNames {
				p := filepath.Join(dir, name)
				if p = try(p); p != "" {
					found = p
					break
				}
			}
			if found != "" {
				break
			}
		}
	}

	if found == "" {
		msg := "没有找到 7z.exe，请安装 7-Zip（https://www.7-zip.org）后在设置里手动指定路径"
		if len(tried) > 0 {
			msg += "；已尝试：" + strings.Join(tried, "、")
		}
		return SevenZipInfo{Message: msg}
	}

	ver := probeVersion(found)
	return SevenZipInfo{Found: true, Path: found, Version: ver, Message: "就绪"}
}

// probeVersion 执行 `7z i` 读取版本号，失败不影响主流程
func probeVersion(exe string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	run := exec.CommandContext(ctx, exe, "i")
	configureCmd(run)
	out, _ := run.Output()
	if m := versionRe.FindSubmatch(out); len(m) > 1 {
		return "7-Zip " + string(m[1])
	}
	return "7-Zip (版本未知)"
}
