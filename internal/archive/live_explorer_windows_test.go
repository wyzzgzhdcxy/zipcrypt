//go:build windows

package archive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// explorerSnapshot 记下当前所有资源管理器窗口句柄
func explorerSnapshot() map[uintptr]bool {
	res := map[uintptr]bool{}
	enumWindows(func(hwnd uintptr) bool {
		if className(hwnd) == cabinetClass {
			res[hwnd] = true
		}
		return true
	})
	return res
}

func foregroundInfo() (uintptr, string, string) {
	fg, _, _ := procGetForegroundWindow.Call()
	return fg, className(fg), windowText(fg)
}

// assertExplorerInForeground 断言"打开目录"真的让用户看得见目标目录。
//
// 硬性要求只有一条：新窗口必须可见，且标题是目标目录。
// 带 HideWindow 启动 explorer 时，窗口会被正常创建、标题也对，
// 但 IsWindowVisible 恒为 0——用户点了完全看不见，
// 这才是"打开目录没有反应"的真正根因，必须由测试守住。
//
// 置前只能尽力断言：explorer 自己从不抢前台，是我们拉上去的；
// 而测试机上随时可能有别的程序或对话框占着前台（用户自己的窗口、
// 其他程序的弹窗），那种情况无法断言前台归属，放行并记录，
// 否则失败的就只是环境噪音，不是这个功能的 bug。
func assertExplorerInForeground(t *testing.T, before map[uintptr]bool, dir, what string) {
	t.Helper()
	want := filepath.Base(filepath.Clean(dir))

	// findNewExplorerWindow 内部已过滤不可见窗口，能找到就说明是显示出来的
	var hwnd uintptr
	deadline := time.Now().Add(8 * time.Second)
	for hwnd == 0 && time.Now().Before(deadline) {
		hwnd = findNewExplorerWindow(before)
		if hwnd == 0 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if hwnd == 0 {
		after := explorerSnapshot()
		fresh := 0
		for h := range after {
			if !before[h] {
				fresh++
			}
		}
		_, cls, title := foregroundInfo()
		t.Fatalf("%s：没等到 %s 的可见窗口。before=%d after=%d 新窗口=%d；前台 class=%q title=%q",
			what, want, len(before), len(after), fresh, cls, title)
	}
	if title := windowText(hwnd); !titleMatches(title, want) {
		t.Fatalf("%s：窗口标题是 %q，期望是 %q", what, title, want)
	}

	// 置前：只和资源管理器抢
	fgDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(fgDeadline) {
		fg, _, _ := procGetForegroundWindow.Call()
		if fg == hwnd {
			return
		}
		if className(fg) != cabinetClass {
			t.Logf("%s：窗口已创建且可见，但前台被非资源管理器窗口占用（class=%q title=%q），跳过置前断言",
				what, className(fg), windowText(fg))
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, cls, title := foregroundInfo()
	t.Fatalf("%s：%s 的窗口可见但没能置到前台。前台 class=%q title=%q", what, want, cls, title)
}

var procPostMessageW = user32.NewProc("PostMessageW")

// closeWindowsOpenedAfter 关掉本用例自己弹出来的窗口。
// 反复跑这些用例会在桌面积压一堆资源管理器窗口，
// 既干扰后续用例（老窗口会抢前台），也污染用户桌面。
//
// 注意：Explorer 窗口对 WM_CLOSE 经常没反应，要发 WM_SYSCOMMAND/SC_CLOSE。
func closeWindowsOpenedAfter(before map[uintptr]bool) {
	enumWindows(func(hwnd uintptr) bool {
		if before[hwnd] || className(hwnd) != cabinetClass {
			return true
		}
		procPostMessageW.Call(hwnd, wmSysCommand, scClose, 0)
		return true
	})
	time.Sleep(700 * time.Millisecond)
}

// TestTitleMatches 窗口标题匹配。新版 Windows 会带 " - 文件资源管理器" 后缀，
// 早期版本只有目录名，两种都得认。
func TestTitleMatches(t *testing.T) {
	cases := []struct {
		title, want string
		ok          bool
	}{
		{"001", "001", true},
		{"001 - 文件资源管理器", "001", true},
		{"001 - File Explorer", "001", true},
		{"001 \u2013 文件资源管理器", "001", true}, // 短横线是多字节
		{"video", "VIDEO", true},            // 大小写无关
		{"00123", "001", false},             // 不能误匹配前缀
		{"0012 - 文件资源管理器", "001", false},
		{"video", "other", false},
		{"", "001", false},
		{"001", "", false},
	}
	for _, c := range cases {
		if got := titleMatches(c.title, c.want); got != c.ok {
			t.Errorf("titleMatches(%q, %q) = %v, 期望 %v", c.title, c.want, got, c.ok)
		}
	}
}

// TestLiveOpenDirComesToForeground 是"打开目录没有反应"的回归测试。
// 因为会抢占桌面，默认不跑：
//
//	ZIPCRYPT_LIVE_EXPLORER=1 go test ./internal/archive/ -run ComesToForeground -v
func TestLiveOpenDirComesToForeground(t *testing.T) {
	if os.Getenv("ZIPCRYPT_LIVE_EXPLORER") != "1" {
		t.Skip("会弹资源管理器窗口，需要 ZIPCRYPT_LIVE_EXPLORER=1 才运行")
	}

	dir := filepath.Join(t.TempDir(), "zc-开目录")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	before := explorerSnapshot()
	defer closeWindowsOpenedAfter(before)
	if err := openExplorerDir(dir); err != nil {
		t.Fatalf("openExplorerDir 失败: %v", err)
	}
	assertExplorerInForeground(t, before, dir, "打开目录")
	t.Log("打开目录：新窗口已置前")
}

// TestLiveOpenSelectComesToForeground 覆盖"打开加密文件所在目录"（带选中）
func TestLiveOpenSelectComesToForeground(t *testing.T) {
	if os.Getenv("ZIPCRYPT_LIVE_EXPLORER") != "1" {
		t.Skip("会弹资源管理器窗口，需要 ZIPCRYPT_LIVE_EXPLORER=1 才运行")
	}

	// 目录名要唯一：t.TempDir() 的末级永远叫 "001"，会跟历史残留窗口撞名
	dir := filepath.Join(t.TempDir(), "zc-sel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "选中测试.7z")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	before := explorerSnapshot()
	defer closeWindowsOpenedAfter(before)
	if err := openExplorerSelect(file); err != nil {
		t.Fatalf("openExplorerSelect 失败: %v", err)
	}
	assertExplorerInForeground(t, before, dir, "打开并选中")
	t.Log("打开并选中：新窗口已置前")
}

// TestLiveOpenOutputDirInExplorer 端到端跑一次真实归档，再打开结果目录。
// 需要显式开启：
//
//	ZIPCRYPT_LIVE_EXPLORER=1 ZIPCRYPT_LIVE=1 go test ./internal/archive/ -run OpenOutputDir -v
func TestLiveOpenOutputDirInExplorer(t *testing.T) {
	if os.Getenv("ZIPCRYPT_LIVE_EXPLORER") != "1" {
		t.Skip("会弹资源管理器窗口，需要 ZIPCRYPT_LIVE_EXPLORER=1 才运行")
	}
	liveSevenZip(t)

	// 输出目录固定为源目录的上一级，所以要用一个唯一命名的中间层：
	// t.TempDir() 的末级目录永远叫 "001"，会和历史测试残留的窗口撞名。
	work := filepath.Join(t.TempDir(), "zc-e2e")
	src := filepath.Join(work, "合集")
	writeLive(t, filepath.Join(src, "a.mp4"), 1024*1024, "A")

	svc := NewService()
	if svc.SevenZipPath() == "" {
		t.Skip("Service 没探测到 7z")
	}
	results := make(chan Result, 1)
	svc.SetEventSink(func(name string, data any) {
		if name == "archive:done" {
			if r, ok := data.(Result); ok {
				results <- r
			}
		}
	})
	if _, err := svc.Start(ArchiveRequest{
		SourceDir: src,
		Overwrite: true,
	}); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	var r Result
	select {
	case r = <-results:
	case <-time.After(60 * time.Second):
		t.Fatal("等待 archive:done 超时")
	}
	if !r.Success {
		t.Fatalf("归档失败: %+v", r)
	}

	// 1) 打开所在目录并选中压缩包
	before := explorerSnapshot()
	defer closeWindowsOpenedAfter(before)
	if err := svc.OpenOutputDir(r.OutputDir, r.Archives[0]); err != nil {
		t.Fatalf("打开所在目录失败: %v", err)
	}
	assertExplorerInForeground(t, before, r.OutputDir, "打开并选中")

	// 2) 只打开目录
	before = explorerSnapshot()
	if err := svc.OpenOutputDir(r.OutputDir, ""); err != nil {
		t.Fatalf("打开目录失败: %v", err)
	}
	assertExplorerInForeground(t, before, r.OutputDir, "只打开目录")
}
