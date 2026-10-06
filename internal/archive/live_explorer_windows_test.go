//go:build windows

package archive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveOpenOutputDirInExplorer 会真的弹出一个资源管理器窗口并选中压缩包。
// 因为会抢占桌面，默认不跑，需要显式开启：
//
//	ZIPCRYPT_LIVE_EXPLORER=1 ZIPCRYPT_LIVE=1 go test ./internal/archive/ -run OpenOutputDir -v
func TestLiveOpenOutputDirInExplorer(t *testing.T) {
	if os.Getenv("ZIPCRYPT_LIVE_EXPLORER") != "1" {
		t.Skip("会弹资源管理器窗口，需要 ZIPCRYPT_LIVE_EXPLORER=1 才运行")
	}
	liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "合集")
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
	if err := svc.OpenOutputDir(r.OutputDir, r.Archives[0]); err != nil {
		t.Fatalf("打开所在目录失败: %v", err)
	}
	t.Logf("已请求打开并选中: %s", r.Archives[0])
	time.Sleep(2 * time.Second)

	// 2) 只打开目录
	if err := svc.OpenOutputDir(r.OutputDir, ""); err != nil {
		t.Fatalf("打开目录失败: %v", err)
	}
	t.Logf("已请求打开目录: %s", r.OutputDir)
}
