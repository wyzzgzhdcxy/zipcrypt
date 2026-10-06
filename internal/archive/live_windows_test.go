//go:build windows

package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// lockFileExclusive 以独占方式打开文件，其他进程（包括 7z）读不到。
// 用来制造"7z 归档中途失败"的真实场景。
func lockFileExclusive(t *testing.T, path string) func() {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("路径转 UTF16 失败: %v", err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Skipf("无法独占打开 %s（可能被占用）: %v", path, err)
	}
	return func() { _ = windows.CloseHandle(h) }
}

// TestLiveFailureKeepsSourceAndCleansPartial 制造一次真实的 7z 失败（源文件被独占锁住），
// 验证两件事：源目录一个字节都不能少，本次写了一半的压缩包必须被清掉。
func TestLiveFailureKeepsSourceAndCleansPartial(t *testing.T) {
	liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "重要资料")
	writeLive(t, filepath.Join(src, "ok.bin"), 4096, "A")
	locked := filepath.Join(src, "locked.bin")
	writeLive(t, locked, 4096, "B")

	unlock := lockFileExclusive(t, locked)
	defer unlock()

	svc := NewService()
	if svc.SevenZipPath() == "" {
		t.Skip("Service 没探测到 7z")
	}
	results := make(chan Result, 2)
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
		Stamp:     testStamp,
	}); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	var r Result
	select {
	case r = <-results:
	case <-time.After(90 * time.Second):
		t.Fatal("等待 archive:done 超时")
	}

	// 源目录必须完好
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("源文件夹不见了，这是严重 bug: %v", err)
	}
	if _, err := os.Stat(locked); err != nil {
		t.Errorf("源文件不见了: %v", err)
	}

	// 半成品不能留在输出目录里
	archive := filepath.Join(base, "重要资料."+testStamp+".7z")
	if _, err := os.Stat(archive); err == nil {
		t.Error("失败的归档留下了半成品压缩包")
	}

	if r.Success {
		t.Log("独占锁没能让 7z 报错（本机权限较高），源目录与输出目录均已检查，结论一致")
		return
	}
	if len(r.Failed) == 0 {
		t.Errorf("失败任务应给出失败原因: %+v", r)
	}
	if len(r.Archives) != 0 {
		t.Errorf("失败任务不应记成功产物: %v", r.Archives)
	}
	if !strings.Contains(r.Error, "失败") {
		t.Errorf("完成提示应说明失败: %q", r.Error)
	}
}

// TestLiveOverwriteGuardStart 被同名包挡住时 Start 直接拒绝，源目录不受影响
func TestLiveOverwriteGuardStart(t *testing.T) {
	liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "src")
	writeLive(t, filepath.Join(src, "a.txt"), 100, "A")
	// 输出位置由规则决定：源目录上一级 + 文件夹名.时间戳.7z
	const oldBody = "OLD-CONTENT"
	old := filepath.Join(base, "src."+testStamp+".7z")
	if err := os.WriteFile(old, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	if svc.SevenZipPath() == "" {
		t.Skip("Service 没探测到 7z")
	}
	_, err := svc.Start(ArchiveRequest{
		SourceDir: src, Overwrite: false, Stamp: testStamp,
	})
	if err == nil {
		t.Fatal("不勾选覆盖时，遇到同名包应当直接拒绝")
	}
	if !strings.Contains(err.Error(), "已存在") {
		t.Errorf("错误信息应点名冲突文件: %v", err)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Errorf("Start 被拒后源目录不应被动过: %v", statErr)
	}
	if body, readErr := os.ReadFile(old); readErr != nil || string(body) != oldBody {
		t.Errorf("原有压缩包内容不应被改动: %v %q", readErr, string(body))
	}
}
