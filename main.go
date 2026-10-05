// zipcrypt 快速归档加密
//
// 一个文件夹 = 一个 AES-256 加密的 .7z（-mx0 只存储不压缩 + -mhe=on 连文件头一起加密），
// 支持把下面的子目录分别打成独立压缩包，带实时进度、结果列表和一键打开输出目录。
package main

import (
	"embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"zipcrypt/internal/archive"
)

//go:embed all:frontend/dist
var assets embed.FS

func init() {
	// 注册事件后，绑定生成器会给前端产出强类型的 TS 事件 API
	application.RegisterEvent[archive.Progress]("archive:progress")
	application.RegisterEvent[archive.LogLine]("archive:log")
	application.RegisterEvent[archive.Result]("archive:done")
}

// logFile 日志路径：%LOCALAPPDATA%\ZipCrypt\app.log
func logFile() string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "ZipCrypt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return filepath.Join(os.TempDir(), "zipcrypt.log")
	}
	return filepath.Join(dir, "app.log")
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	path := logFile()
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		// GUI 程序没有控制台，stderr 可能无效，用容忍错误的 writer 兜住
		log.SetOutput(io.MultiWriter(f, errTolerant{os.Stderr}))
		defer f.Close()
	} else {
		log.SetOutput(io.Discard)
	}
	log.Printf("=== zipcrypt 启动 %s log=%s ===", time.Now().Format("2006-01-02 15:04:05"), path)

	service := archive.NewService()
	log.Printf("7z: found=%v path=%s version=%s", service.SevenZipPath() != "", service.SevenZipPath(), service.SevenZipVersion())

	app := application.New(application.Options{
		Name:        "zipcrypt",
		Description: "快速归档加密：把文件夹打成 AES 加密的 7z",
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
		OnShutdown: func() {
			service.Shutdown()
		},
	})
	service.App = app

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "ZipCrypt · 快速归档加密",
		Width:            1040,
		Height:           820,
		MinWidth:         900,
		MinHeight:        660,
		BackgroundColour: application.NewRGB(245, 247, 250),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Printf("[ERROR] app.Run 失败: %v", err)
	}
	log.Printf("=== zipcrypt 退出 %s ===", time.Now().Format("2006-01-02 15:04:05"))
}

// errTolerant 忽略写入错误，保证 GUI 下 stderr 不可用时不会影响日志落盘
type errTolerant struct{ w io.Writer }

func (e errTolerant) Write(p []byte) (int, error) {
	_, _ = e.w.Write(p)
	return len(p), nil
}
