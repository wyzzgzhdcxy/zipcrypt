package archive

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Service 暴露给前端的全部能力
type Service struct {
	// App 由 main 注入，用于弹窗与事件推送
	App *application.App

	mu       sync.Mutex
	jobs     map[string]context.CancelFunc
	settings Settings
	sevenZip SevenZipInfo

	// sink 只给测试用：App 为 nil（无 GUI）时把事件转发到这里，
	// 便于在没有窗口的情况下断言前端真正依赖的事件负载
	sink func(name string, data any)
}

// SetEventSink 注入事件出口，返回一个还原函数。仅测试使用。
func (s *Service) SetEventSink(f func(name string, data any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink = f
}

// NewService 创建服务并加载设置、探测 7z
func NewService() *Service {
	s := &Service{
		jobs:     make(map[string]context.CancelFunc),
		settings: LoadSettings(),
	}
	s.sevenZip = Locate7z(s.settings.SevenZipPath)
	return s
}

// Bootstrap 前端启动时拉一次初始状态
type Bootstrap struct {
	Settings Settings     `json:"settings"`
	SevenZip SevenZipInfo `json:"sevenZip"`
	Platform string       `json:"platform"`
	Home     string       `json:"home"`
}

func (s *Service) Bootstrap() Bootstrap {
	platform, home := "windows", ""
	if v := os.Getenv("HOME"); v != "" {
		home = v
	} else {
		home = os.Getenv("USERPROFILE")
	}
	return Bootstrap{
		Settings: s.CurrentSettings(),
		SevenZip: s.sevenZip,
		Platform: platform,
		Home:     home,
	}
}

// CurrentSettings 返回当前设置
func (s *Service) CurrentSettings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SaveSettings 保存界面设置；填了 7z 路径会立即重新探测
func (s *Service) SaveSettings(in Settings) error {
	s.mu.Lock()
	s.settings = in
	s.mu.Unlock()
	if err := SaveSettings(in); err != nil {
		return err
	}
	if in.SevenZipPath != s.sevenZip.Path {
		s.sevenZip = Locate7z(in.SevenZipPath)
	}
	return nil
}

// DetectSevenZip 重新探测 7z（界面上"重新检测"按钮）
func (s *Service) DetectSevenZip() SevenZipInfo {
	path := ""
	s.mu.Lock()
	path = s.settings.SevenZipPath
	s.mu.Unlock()

	info := Locate7z(path)
	s.sevenZip = info
	return info
}

// SevenZipPath 返回可用的 7z 路径，没找到返回空串
func (s *Service) SevenZipPath() string {
	if s.sevenZip.Found {
		return s.sevenZip.Path
	}
	return ""
}

// SevenZipVersion 返回探测到的版本描述，没找到返回空串
func (s *Service) SevenZipVersion() string {
	if s.sevenZip.Found {
		return s.sevenZip.Version
	}
	return ""
}

// Shutdown 退出前取消所有运行中的任务，避免留下写了一半的压缩包
func (s *Service) Shutdown() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.jobs))
	for _, c := range s.jobs {
		cancels = append(cancels, c)
	}
	s.jobs = make(map[string]context.CancelFunc)
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

// PickSourceDir 弹出目录选择框选要加密的文件夹
func (s *Service) PickSourceDir(current string) (string, error) {
	if s.App == nil {
		return "", errors.New("应用尚未就绪")
	}
	dlg := s.App.Dialog.OpenFile().
		SetTitle("选择要加密的文件夹").
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(false)
	if current != "" {
		dlg = dlg.SetDirectory(current)
	}
	return dlg.PromptForSingleSelection()
}

// PickOutputDir 弹出目录选择框选输出目录
func (s *Service) PickOutputDir(current string) (string, error) {
	if s.App == nil {
		return "", errors.New("应用尚未就绪")
	}
	dlg := s.App.Dialog.OpenFile().
		SetTitle("选择压缩包输出目录").
		CanChooseFiles(false).
		CanChooseDirectories(true).
		CanCreateDirectories(true)
	if current != "" {
		dlg = dlg.SetDirectory(current)
	}
	return dlg.PromptForSingleSelection()
}

// Inspect 扫描源目录，给界面做预览
func (s *Service) Inspect(path string) (SourceInfo, error) {
	return InspectSource(path)
}

// IsRunning 是否有任务在跑
func (s *Service) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs) > 0
}

// Start 启动一次加密任务，立即返回 jobId，进度通过事件推送
func (s *Service) Start(req ArchiveRequest) (string, error) {
	exe := s.SevenZipPath()
	if exe == "" {
		if !s.sevenZip.Found {
			s.sevenZip = Locate7z("")
		}
		msg := s.sevenZip.Message
		if msg == "" {
			msg = "没有找到 7z.exe，请先安装 7-Zip"
		}
		return "", errors.New(msg)
	}
	if s.IsRunning() {
		return "", errors.New("已有任务正在运行，请先等待完成或取消")
	}

	targets, err := BuildTargets(req)
	if err != nil {
		return "", err
	}

	jobID := fmt.Sprintf("job-%d", time.Now().UnixMilli())
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.jobs[jobID] = cancel
	s.mu.Unlock()

	go s.run(ctx, jobID, req, targets, exe)
	return jobID, nil
}

// Cancel 取消任务
func (s *Service) Cancel(jobID string) error {
	s.mu.Lock()
	cancel, ok := s.jobs[jobID]
	s.mu.Unlock()
	if !ok {
		return errors.New("任务不存在或已结束")
	}
	cancel()
	return nil
}

// OpenOutputDir 打开压缩包所在目录；file 存在时顺便在资源管理器里选中它
func (s *Service) OpenOutputDir(dir string, file string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("输出目录为空")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("目录不存在：%s", dir)
	}
	log.Printf("[open] dir=%q file=%q", dir, file)
	if file != "" {
		if fi, err := os.Stat(file); err == nil && !fi.IsDir() {
			if err := openExplorerSelect(file); err != nil {
				log.Printf("[open] 打开失败: %v", err)
				return err
			}
			log.Printf("[open] 已打开并选中 %s", file)
			return nil
		}
	}
	if err := openExplorerDir(dir); err != nil {
		log.Printf("[open] 打开失败: %v", err)
		return err
	}
	log.Printf("[open] 已打开 %s", dir)
	return nil
}

// ---------------------------------------------------------------- 事件发送

// emit 统一出口：有窗口就发给前端，没有窗口就转给测试注入的 sink
func (s *Service) emit(name string, data any) {
	if s.App != nil {
		s.App.Event.Emit(name, data)
		return
	}
	s.mu.Lock()
	sink := s.sink
	s.mu.Unlock()
	if sink != nil {
		sink(name, data)
	}
}

func (s *Service) emitProgress(p Progress) {
	p.Timestamp = time.Now().UnixMilli()
	s.emit("archive:progress", p)
}

func (s *Service) emitLog(l LogLine) {
	l.Timestamp = time.Now().UnixMilli()
	s.emit("archive:log", l)
}

func (s *Service) emitResult(r Result) {
	s.emit("archive:done", r)
}

func (s *Service) logf(jobID, level, format string, a ...any) {
	text := fmt.Sprintf(format, a...)
	log.Printf("[%s][%s] %s", jobID, level, text)
	s.emitLog(LogLine{JobID: jobID, Level: level, Text: text})
}

// emitter 节流发送进度，避免大目录时前端被刷爆
type emitter struct {
	s        *Service
	jobID    string
	start    time.Time
	mu       sync.Mutex
	last     time.Time
	lastPct  float64
	finished bool
}

func newEmitter(s *Service, jobID string) *emitter {
	return &emitter{s: s, jobID: jobID, start: time.Now()}
}

func (e *emitter) send(p Progress) {
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return
	}
	p.JobID = e.jobID
	if p.Written > 0 {
		elapsed := time.Since(e.start).Seconds()
		if elapsed > 0.2 {
			p.Speed = int64(float64(p.Written) / elapsed)
		}
	}
	e.lastPct = p.Percent
	e.mu.Unlock()
	e.s.emitProgress(p)
}

// tick 按时间节流：最多 80ms 一次，或百分比变化 ≥0.5% 时立即发
func (e *emitter) tick(p Progress) {
	e.mu.Lock()
	if e.finished {
		e.mu.Unlock()
		return
	}
	diff := p.Percent - e.lastPct
	if diff < 0.5 && time.Since(e.last) < 80*time.Millisecond {
		e.mu.Unlock()
		return
	}
	e.last = time.Now()
	e.mu.Unlock()
	e.send(p)
}

func (e *emitter) finish(r Result) {
	e.mu.Lock()
	e.finished = true
	e.mu.Unlock()
	r.JobID = e.jobID
	if r.ElapsedMs == 0 {
		r.ElapsedMs = time.Since(e.start).Milliseconds()
	}
	e.s.emitResult(r)
}

// ---------------------------------------------------------------- 任务主循环

func (s *Service) run(ctx context.Context, jobID string, req ArchiveRequest, targets []target, exe string) {
	defer func() {
		s.mu.Lock()
		delete(s.jobs, jobID)
		s.mu.Unlock()
	}()

	em := newEmitter(s, jobID)
	steps := len(targets)
	var totalBytes int64
	for _, t := range targets {
		totalBytes += t.Bytes
	}

	var (
		archives []string
		failed   []string
		fatal    string
		canceled bool
	)

	em.send(Progress{
		Phase: PhaseScanning, Steps: steps, Total: totalBytes,
		Message: fmt.Sprintf("准备就绪：共 %d 个压缩包", steps),
	})

	for i, t := range targets {
		if ctx.Err() != nil {
			canceled = true
			break
		}

		existedBefore := fileExists(t.ArchivePath)
		args := BuildArgs(ArchivePassword, req.Overwrite, t)
		em.send(Progress{
			// 起始百分比是"前面几个包已经完成"的比例，
			// 填 0 会让进度条在切换到下一个包时往回跳
			Phase: PhaseArchiving, Step: i + 1, Steps: steps, Archive: t.Label,
			Percent: float64(i) / float64(steps) * 100, Total: totalBytes,
			Command: RedactArgs(args),
			Message: fmt.Sprintf("[%d/%d] 正在加密 %s（%s，%d 个文件）",
				i+1, steps, t.Label, humanBytes(t.Bytes), t.FileCount),
		})

		runCtx, runCancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		if t.Bytes > 0 {
			base := float64(i)
			wg.Add(1)
			go func(bytes int64) {
				defer wg.Done()
				watchArchiveSize(runCtx, t.ArchivePath, 150*time.Millisecond, func(size int64) {
					cur := float64(size) / float64(bytes) * 100
					// 封顶 99%，留最后 1% 给 7z 写文件头和收尾
					if cur > 99 {
						cur = 99
					}
					if cur < 0 {
						cur = 0
					}
					overall := (base + cur/100) / float64(steps) * 100
					em.tick(Progress{
						Phase: PhaseArchiving, Step: i + 1, Steps: steps,
						Archive: t.Label, Written: size, Total: totalBytes,
						Percent: overall,
						Message: fmt.Sprintf("[%d/%d] %s → %s", i+1, steps, t.Label, humanBytes(size)),
					})
				})
			}(t.Bytes)
		}

		res, runErr := runSevenZip(runCtx, exe, args, t.WorkDir, func(level, text string) {
			s.emitLog(LogLine{JobID: jobID, Level: level, Text: text})
		})
		runCancel()
		wg.Wait()

		if ctx.Err() != nil {
			canceled = true
			// 半成品包留着只会误导用户，本次新建的直接清掉
			if !existedBefore {
				_ = os.Remove(t.ArchivePath)
			}
			break
		}
		if runErr != nil {
			fatal = fmt.Sprintf("调用 7z 失败：%v", runErr)
			if !existedBefore {
				_ = os.Remove(t.ArchivePath)
			}
			break
		}
		if res.ExitCode != 0 {
			reason := strings.Join(res.Output, " | ")
			if reason == "" {
				reason = fmt.Sprintf("退出码 %d", res.ExitCode)
			}
			failed = append(failed, fmt.Sprintf("%s：%s", t.Label, truncate(reason, 200)))
			if !existedBefore {
				_ = os.Remove(t.ArchivePath)
			}
			s.logf(jobID, "error", "[%d/%d] %s 失败（退出码 %d）", i+1, steps, t.Label, res.ExitCode)
			continue
		}

		archives = append(archives, t.ArchivePath)
		em.send(Progress{
			Phase: PhaseArchiving, Step: i + 1, Steps: steps, Archive: t.Label,
			Percent: float64(i+1) / float64(steps) * 100, Total: totalBytes,
			Message: fmt.Sprintf("[%d/%d] %s 完成", i+1, steps, t.Label),
		})
		s.logf(jobID, "info", "[%d/%d] %s 完成", i+1, steps, t.Label)
	}

	success := !canceled && fatal == "" && len(failed) == 0 && len(archives) == steps
	outDir := ""
	if len(archives) > 0 {
		outDir = filepath.Dir(archives[0])
	} else if len(targets) > 0 {
		outDir = filepath.Dir(targets[0].ArchivePath)
	}

	msg := ""
	switch {
	case canceled:
		msg = "已取消"
	case fatal != "":
		msg = fatal
	case len(failed) > 0:
		msg = fmt.Sprintf("%d 个压缩包失败", len(failed))
	default:
		msg = fmt.Sprintf("完成，共生成 %d 个加密包", len(archives))
	}

	em.finish(Result{
		Success:    success,
		Cancelled:  canceled,
		Archives:   archives,
		Failed:     failed,
		OutputDir:  outDir,
		TotalBytes: totalBytes,
		Error:      msg,
	})
	s.logf(jobID, "info", "任务结束：%s", msg)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// humanBytes 人类可读的字节数
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTP"[exp])
}
