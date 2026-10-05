package archive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// 老版本 7z 在重定向输出时会打印逐条进度： " 45% 12 - .\folder\a.mp4"
	progressLineRe = regexp.MustCompile(`^\s*(\d{1,3})%\s+(\d+)\s+-\s*(.+)$`)
	// 扫描汇总： "1 folder, 40 files, 800080 bytes (782 KiB)"
	scanSummaryRe = regexp.MustCompile(`(\d+)\s+folders?,\s+(\d+)\s+files?,\s+(\d+)\s+bytes`)
	// 只有百分比的进度（7-Zip 23+ 非 TTY 输出的形式）
	barePercentRe = regexp.MustCompile(`^\s*(\d{1,3})%\s*$`)
)

// BuildArgs 组装 7z 命令行参数。
// 核心是用户指定的：7z a -mx0 -p"xxx" -mhe=on out.7z .\dir
// 其余开关只影响输出与安全：
//
//	-bsp1   进度重定向到 stdout（非 TTY 时默认在 stderr，方便统一读取）
//	-bb0    精简日志，避免刷屏
//	-y      自动覆盖，不弹交互询问（GUI 里没人能点"是"）
//	-sccUTF-8 让中文路径的输出按 UTF-8 返回
//	-x!xx   仅当输出包落在源目录内部时使用，防止压缩包把自己打进自己
func BuildArgs(password string, overwrite bool, t target) []string {
	args := []string{
		"a",
		"-mx0",
		"-bsp1",
		"-bb0",
		"-sccUTF-8",
		"-p" + password,
		"-mhe=on",
	}
	if overwrite {
		args = append(args, "-y")
	}
	if t.Exclude != "" {
		args = append(args, "-x!"+t.Exclude)
	}
	args = append(args, t.ArchivePath)
	sep := string(filepath.Separator)
	for _, name := range t.RelNames {
		args = append(args, "."+sep+name)
	}
	return args
}

// RedactArgs 把密码替换成 ***，用于展示给用户核对命令
func RedactArgs(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if strings.HasPrefix(a, "-p") && len(a) > 2 {
			out = append(out, "-p***")
			continue
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

// parseProgressLine 解析 7z 逐条进度行，返回百分比、已处理条目数和当前文件名
func parseProgressLine(line string) (pct float64, idx int, name string, ok bool) {
	line = strings.TrimRight(line, "\r")
	m := progressLineRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, "", false
	}
	p, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, 0, "", false
	}
	i, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, "", false
	}
	return p, i, strings.TrimSpace(m[3]), true
}

// parseBarePercent 解析 "  45%" 这种只有百分比的进度（7-Zip 23+ 非 TTY）
func parseBarePercent(line string) (float64, bool) {
	m := barePercentRe.FindStringSubmatch(strings.TrimRight(line, "\r"))
	if m == nil {
		return 0, false
	}
	p, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false
	}
	return p, true
}

// parseScanSummary 解析扫描汇总行，拿到文件数和总字节
func parseScanSummary(line string) (files int, bytes int64, ok bool) {
	m := scanSummaryRe.FindStringSubmatch(line)
	if m == nil {
		return 0, 0, false
	}
	f, err1 := strconv.Atoi(m[2])
	b, err2 := strconv.ParseInt(m[3], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return f, b, true
}

// classifyLogLine 判断一行 7z 输出的级别
func classifyLogLine(line string) string {
	l := strings.ToUpper(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(l, "ERROR:"):
		return "error"
	case strings.HasPrefix(l, "WARNINGS:") || strings.HasPrefix(l, "WARNING:"):
		return "warn"
	case strings.HasPrefix(l, "FAIL"):
		return "error"
	}
	return "info"
}

// runResult 一次 7z 调用的结果
type runResult struct {
	ExitCode      int
	Output        []string
	ReportedPct   float64
	HasReported   bool
	CurrentFile   string
	ScanFiles     int
	ScanBytes     int64
	EverythingOk  bool
	KilledByError bool
}

// runSevenZip 执行 7z，把 stdout/stderr 合并成按行回调。
// 密码不出现在任何日志里。
func runSevenZip(ctx context.Context, exe string, args []string, workDir string, onLine func(level, text string)) (*runResult, error) {
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = workDir
	configureCmd(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 7z 失败：%w", err)
	}

	res := &runResult{}
	var mu sync.Mutex

	handle := func(stream string, r io.Reader) {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimRight(sc.Text(), "\r")
			if strings.TrimSpace(line) == "" {
				continue
			}
			mu.Lock()
			res.Output = append(res.Output, line)
			if pct, idx, name, ok := parseProgressLine(line); ok {
				res.ReportedPct, res.HasReported, res.CurrentFile = pct, true, name
				_ = idx
			} else if pct, ok := parseBarePercent(line); ok {
				res.ReportedPct, res.HasReported = pct, true
			} else if files, bytes, ok := parseScanSummary(line); ok {
				res.ScanFiles, res.ScanBytes = files, bytes
			}
			if strings.Contains(line, "Everything is Ok") {
				res.EverythingOk = true
			}
			mu.Unlock()

			level := classifyLogLine(line)
			if onLine != nil {
				onLine(level, line)
			}
		}
		_ = stream
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); handle("stdout", stdout) }()
	go func() { defer wg.Done(); handle("stderr", stderr) }()
	wg.Wait()

	waitErr := cmd.Wait()
	mu.Lock()
	defer mu.Unlock()
	if waitErr != nil {
		if ee, ok := waitErr.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.KilledByError = true
			return res, waitErr
		}
	}
	return res, nil
}

// watchArchiveSize 轮询压缩包体积。
// 7-Zip 23+ 在非 TTY 环境下不再逐条打印进度，但它一直在往磁盘写数据，
// 而 -mx0 是"仅存储"模式，压缩包体积几乎线性增长，用它来换算总进度最稳。
func watchArchiveSize(ctx context.Context, archivePath string, interval time.Duration, onTick func(size int64)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if fi, err := os.Stat(archivePath); err == nil {
				onTick(fi.Size())
			}
		}
	}
}
