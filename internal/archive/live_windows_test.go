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
// 用来制造"7z 归档中途失败"的真实场景，验证源文件不会被误删。
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

// TestLiveDeleteSourceNotRunWhen7zFails 是最要命的安全性质：
// 只要有一个包没打成功，源文件夹就绝对不能被删掉。
func TestLiveDeleteSourceNotRunWhen7zFails(t *testing.T) {
	liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "重要资料")
	writeLive(t, filepath.Join(src, "ok.bin"), 4096, "A")
	locked := filepath.Join(src, "locked.bin")
	writeLive(t, locked, 4096, "B")
	out := filepath.Join(base, "out")

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
		SourceDir: src, OutputDir: out, ArchiveName: "packed",
		DeleteSource: true, Overwrite: true,
	}); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	var r Result
	select {
	case r = <-results:
	case <-time.After(90 * time.Second):
		t.Fatal("等待 archive:done 超时")
	}

	// 无论 7z 怎么报错，源文件夹都必须在
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("7z 失败时源文件夹被删了，这是严重 bug: %v", err)
	}
	if len(r.Removed) != 0 {
		t.Errorf("失败任务不应删除任何东西: %v", r.Removed)
	}
	if r.Success {
		t.Log("独占锁没能让 7z 报错（本机权限较高），源目录保留，结论一致")
	} else {
		if len(r.Failed) == 0 {
			t.Errorf("失败任务应给出失败原因: %+v", r)
		}
		if !strings.Contains(strings.Join(r.Archives, " "), "packed.7z") {
			t.Log("失败时半成品已被清理，符合预期")
		}
	}
}

// TestLiveOverwriteGuardStart 被同名包挡住时 Start 直接拒绝，源目录不受影响
func TestLiveOverwriteGuardStart(t *testing.T) {
	liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "src")
	writeLive(t, filepath.Join(src, "a.txt"), 100, "A")
	out := filepath.Join(base, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(out, "dup.7z")
	const oldBody = "OLD-CONTENT"
	if err := os.WriteFile(existing, []byte(oldBody), 0o644); err != nil {
		t.Fatal(err)
	}

	svc := NewService()
	if svc.SevenZipPath() == "" {
		t.Skip("Service 没探测到 7z")
	}
	_, err := svc.Start(ArchiveRequest{
		SourceDir: src, OutputDir: out, ArchiveName: "dup",
		Overwrite: false,
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
	if body, readErr := os.ReadFile(existing); readErr != nil || string(body) != oldBody {
		t.Errorf("原有压缩包内容不应被改动: %v %q", readErr, string(body))
	}
}
