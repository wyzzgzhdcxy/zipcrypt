package archive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 这组用例会真的调用本机 7z.exe 做归档/解包比对，默认跳过。
// 开启方式：
//
//	ZIPCRYPT_LIVE=1 go test ./internal/archive/ -run Live -v
//
// 只在临时目录里造数据、往临时目录解压，不碰用户任何目录。

func liveSevenZip(t *testing.T) string {
	t.Helper()
	if os.Getenv("ZIPCRYPT_LIVE") != "1" {
		t.Skip("设置 ZIPCRYPT_LIVE=1 后才运行真实 7z 用例")
	}
	info := Locate7z("")
	if !info.Found {
		t.Skipf("本机没有 7z.exe：%s", info.Message)
	}
	return info.Path
}

// livePassword 直接用程序内置的固定密码，保证用例和真实行为一致

// runLive 直接跑一次 7z，退出码非 0 直接判失败并打印输出
func runLive(t *testing.T, exe, workDir string, args ...string) string {
	t.Helper()
	full := append([]string{"-sccUTF-8", "-y"}, args...)
	res, err := runSevenZip(context.Background(), exe, full, workDir, nil)
	if err != nil {
		t.Fatalf("调用 7z 失败：%v", err)
	}
	out := strings.Join(res.Output, "\n")
	if res.ExitCode != 0 {
		t.Fatalf("7z 退出码 %d\n命令：%s\n输出：\n%s", res.ExitCode, strings.Join(full, " "), out)
	}
	return out
}

func writeLive(t *testing.T, path string, size int, fill string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := []byte(strings.Repeat(fill, size/len(fill)+1))[:size]
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

func TestLiveWholeFolder(t *testing.T) {
	exe := liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "精品_日语")
	writeLive(t, filepath.Join(src, "a.mp4"), 4096, "A")
	writeLive(t, filepath.Join(src, "sub", "b.jpg"), 8192, "B")
	writeLive(t, filepath.Join(src, "sub", "deep", "c.txt"), 1024, "C")

	req := ArchiveRequest{
		SourceDir:   src,
		OutputDir:   base,
		ArchiveName: "jp_ry",
		Overwrite:   true,
	}
	targets, err := BuildTargets(req)
	if err != nil {
		t.Fatalf("BuildTargets: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("整体打包应只有 1 个任务，实际 %d", len(targets))
	}

	args := BuildArgs(ArchivePassword, req.Overwrite, targets[0])
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := runSevenZip(ctx, exe, args, targets[0].WorkDir, nil)
	if err != nil {
		t.Fatalf("归档失败: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("7z 退出码 %d\n%s", res.ExitCode, strings.Join(res.Output, "\n"))
	}
	if !res.EverythingOk {
		t.Errorf("7z 未报告 Everything is Ok：\n%s", strings.Join(res.Output, "\n"))
	}

	archive := targets[0].ArchivePath
	if fi, err := os.Stat(archive); err != nil || fi.Size() == 0 {
		t.Fatalf("压缩包没生成：%v", err)
	}

	// 包内结构必须是 "精品_日语\..."，不能带整条磁盘路径
	out := runLive(t, exe, base, "l", "-ba", "-p"+ArchivePassword, archive)
	if !strings.Contains(out, `精品_日语\a.mp4`) {
		t.Errorf("包内应存在 精品_日语\\a.mp4，实际输出：\n%s", out)
	}
	if strings.Contains(out, base) {
		t.Errorf("包内不应带磁盘路径 %s：\n%s", base, out)
	}

	// 解压回临时目录逐字节比对
	extract := t.TempDir()
	runLive(t, exe, extract, "x", "-p"+ArchivePassword, archive)
	for rel, want := range map[string]int{
		`精品_日语\a.mp4`:          4096,
		`精品_日语\sub\b.jpg`:      8192,
		`精品_日语\sub\deep\c.txt`: 1024,
	} {
		fi, err := os.Stat(filepath.Join(extract, rel))
		if err != nil {
			t.Fatalf("解压后缺少 %s：%v", rel, err)
		}
		if int(fi.Size()) != want {
			t.Errorf("%s 大小 = %d, 期望 %d", rel, fi.Size(), want)
		}
	}
}

func TestLiveWrongPasswordRejected(t *testing.T) {
	exe := liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "secret")
	writeLive(t, filepath.Join(src, "x.txt"), 512, "S")

	targets, err := BuildTargets(ArchiveRequest{
		SourceDir: src, OutputDir: base, ArchiveName: "enc",
		Overwrite: true,
	})
	if err != nil {
		t.Fatalf("BuildTargets: %v", err)
	}
	if _, err := runSevenZip(context.Background(), exe,
		BuildArgs(ArchivePassword, true, targets[0]), targets[0].WorkDir, nil); err != nil {
		t.Fatalf("归档失败: %v", err)
	}

	// 文件头也加密了（-mhe=on）：不带密码连目录都列不出来，
	// 7z 会直接提示要密码然后以非 0 退出（GUI 场景下这里不会卡住，
	// 因为本程序只做归档，从不列目录）
	noPwd, err := runSevenZip(context.Background(), exe,
		[]string{"l", "-ba", targets[0].ArchivePath}, base, nil)
	if err != nil {
		t.Fatalf("调用 7z 失败: %v", err)
	}
	noPwdOut := strings.Join(noPwd.Output, "\n")
	if strings.Contains(noPwdOut, "x.txt") {
		t.Errorf("没有密码就能看到文件名，说明文件头没加密：\n%s", noPwdOut)
	}
	if noPwd.ExitCode == 0 {
		t.Errorf("无密码列目录居然成功退出，文件头可能没加密：\n%s", noPwdOut)
	}

	// 错误密码解压必须失败
	res, err := runSevenZip(context.Background(), exe,
		[]string{"x", "-y", "-pwrongpassword", "-o" + t.TempDir(), targets[0].ArchivePath}, base, nil)
	if err != nil {
		t.Fatalf("调用 7z 失败: %v", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("错误密码竟然解压成功了：\n%s", strings.Join(res.Output, "\n"))
	}
}

func TestLiveSplitSubDirs(t *testing.T) {
	exe := liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "合集")
	writeLive(t, filepath.Join(src, "动画", "a1.mp4"), 2048, "A")
	writeLive(t, filepath.Join(src, "动画", "a2.mp4"), 2048, "A")
	writeLive(t, filepath.Join(src, "漫画", "b1.jpg"), 1024, "B")
	writeLive(t, filepath.Join(src, "说明.txt"), 256, "C")

	out := t.TempDir()
	req := ArchiveRequest{
		SourceDir:    src,
		OutputDir:    out,
		SplitSubDirs: true,
		Overwrite:    true,
	}
	targets, err := BuildTargets(req)
	if err != nil {
		t.Fatalf("BuildTargets: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("两个子目录 + 散文件应得 3 个包，实际 %d", len(targets))
	}

	for i, tg := range targets {
		res, err := runSevenZip(context.Background(), exe,
			BuildArgs(ArchivePassword, req.Overwrite, tg), tg.WorkDir, nil)
		if err != nil {
			t.Fatalf("[%d] %s 调用失败: %v", i, tg.Label, err)
		}
		if res.ExitCode != 0 {
			t.Fatalf("[%d] %s 退出码 %d\n%s", i, tg.Label, res.ExitCode, strings.Join(res.Output, "\n"))
		}
	}

	// 每个包都只含自己那一份内容，且带原目录名
	expect := map[string][]string{
		"动画.7z":    {`动画\a1.mp4`, `动画\a2.mp4`},
		"漫画.7z":    {`漫画\b1.jpg`},
		"根目录文件.7z": {`说明.txt`},
	}
	for name, entries := range expect {
		archive := filepath.Join(out, name)
		list := runLive(t, exe, out, "l", "-ba", "-p"+ArchivePassword, archive)
		for _, e := range entries {
			if !strings.Contains(list, e) {
				t.Errorf("%s 里缺少 %s：\n%s", name, e, list)
			}
		}
		// 不能串包：动画包里不该出现漫画的内容
		if name == "动画.7z" && strings.Contains(list, `漫画\b1.jpg`) {
			t.Errorf("动画.7z 里混进了漫画内容：\n%s", list)
		}
	}
}

func TestLiveOutputInsideSourceIsExcluded(t *testing.T) {
	exe := liveSevenZip(t)

	src := t.TempDir()
	writeLive(t, filepath.Join(src, "a.txt"), 512, "A")
	writeLive(t, filepath.Join(src, "sub", "b.txt"), 512, "B")

	// 输出目录 = 源目录本身：压缩包会边写边被扫描，必须靠 -x! 排除自己
	req := ArchiveRequest{
		SourceDir: src, OutputDir: src, ArchiveName: "pack",
		Overwrite: true,
	}
	targets, err := BuildTargets(req)
	if err != nil {
		t.Fatalf("BuildTargets: %v", err)
	}
	if targets[0].Exclude != "pack.7z" {
		t.Fatalf("应自动排除自身，实际 Exclude=%q", targets[0].Exclude)
	}
	res, err := runSevenZip(context.Background(), exe,
		BuildArgs(ArchivePassword, true, targets[0]), targets[0].WorkDir, nil)
	if err != nil {
		t.Fatalf("归档失败: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("退出码 %d\n%s", res.ExitCode, strings.Join(res.Output, "\n"))
	}
	if out := runLive(t, exe, src, "l", "-ba", "-p"+ArchivePassword, targets[0].ArchivePath); strings.Contains(out, "pack.7z") {
		t.Errorf("压缩包把自己打进去了：\n%s", out)
	}
}

func TestLiveProgressByArchiveSize(t *testing.T) {
	exe := liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "big")
	// 32MB，确保有足够时间观察到体积增长
	for i := 0; i < 8; i++ {
		writeLive(t, filepath.Join(src, "f", "chunk"+string(rune('a'+i))+".bin"), 4*1024*1024, "Z")
	}
	targets, err := BuildTargets(ArchiveRequest{
		SourceDir: src, OutputDir: base, ArchiveName: "big",
		Overwrite: true,
	})
	if err != nil {
		t.Fatalf("BuildTargets: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sizes := make(chan int64, 64)
	go watchArchiveSize(ctx, targets[0].ArchivePath, 20*time.Millisecond, func(s int64) {
		select {
		case sizes <- s:
		default:
		}
	})

	if _, err := runSevenZip(context.Background(), exe,
		BuildArgs(ArchivePassword, true, targets[0]), targets[0].WorkDir, nil); err != nil {
		t.Fatalf("归档失败: %v", err)
	}
	cancel()
	close(sizes)

	var maxSeen int64
	for s := range sizes {
		if s > maxSeen {
			maxSeen = s
		}
	}
	if maxSeen == 0 {
		t.Error("全程没有观察到压缩包体积增长，进度条会一直卡在 0%")
	}
	// 体积应接近源数据总量（-mx0 只存储）
	if want := targets[0].Bytes; maxSeen < want/2 {
		t.Errorf("观测到的最大体积 %d 明显小于源数据 %d", maxSeen, want)
	}
}

// ------------------------------------------------------------ 事件链路

type capturedEvent struct {
	Name string
	Data any
}

// TestLiveServiceEventFlow 不开窗口跑完整链路：Start -> 7z -> 进度事件 -> done 事件。
// 前端就是靠这三个事件驱动界面的，这里保证负载字段和时序不会坏。
func TestLiveServiceEventFlow(t *testing.T) {
	exe := liveSevenZip(t)
	if exe == "" {
		t.Skip("没有可用的 7z")
	}

	base := t.TempDir()
	src := filepath.Join(base, "合集")
	writeLive(t, filepath.Join(src, "动画", "a.mp4"), 3*1024*1024, "A")
	writeLive(t, filepath.Join(src, "漫画", "b.jpg"), 2*1024*1024, "B")
	out := filepath.Join(base, "out")

	svc := NewService()
	if svc.SevenZipPath() == "" {
		t.Skip("Service 没探测到 7z")
	}

	events := make(chan capturedEvent, 512)
	svc.SetEventSink(func(name string, data any) {
		select {
		case events <- capturedEvent{name, data}:
		default:
		}
	})

	jobID, err := svc.Start(ArchiveRequest{
		SourceDir: src, OutputDir: out, ArchiveName: "jp_ry",
		SplitSubDirs: true, Overwrite: true,
	})
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	if jobID == "" {
		t.Fatal("Start 应返回 jobId")
	}
	if !svc.IsRunning() {
		t.Error("任务刚启动 IsRunning 应为 true")
	}

	var (
		progresses []Progress
		logs       int
		result     *Result
		deadline   = time.After(90 * time.Second)
	)
wait:
	for {
		select {
		case ev := <-events:
			switch ev.Name {
			case "archive:progress":
				p, ok := ev.Data.(Progress)
				if !ok {
					t.Fatalf("进度事件负载类型不对: %T", ev.Data)
				}
				if p.JobID != jobID {
					t.Errorf("进度事件 jobId 不匹配: %s != %s", p.JobID, jobID)
				}
				progresses = append(progresses, p)
			case "archive:log":
				logs++
			case "archive:done":
				r, ok := ev.Data.(Result)
				if !ok {
					t.Fatalf("完成事件负载类型不对: %T", ev.Data)
				}
				result = &r
				break wait
			}
		case <-deadline:
			t.Fatal("等待 archive:done 超时")
		}
	}

	if !result.Success {
		t.Fatalf("任务未成功: %+v", *result)
	}
	if result.JobID != jobID {
		t.Errorf("完成事件 jobId 不匹配: %s != %s", result.JobID, jobID)
	}
	if len(result.Archives) != 2 {
		t.Errorf("拆分模式应生成 2 个包，实际 %d: %v", len(result.Archives), result.Archives)
	}
	for _, a := range result.Archives {
		if fi, err := os.Stat(a); err != nil || fi.Size() == 0 {
			t.Errorf("压缩包不存在或为空: %s (%v)", a, err)
		}
	}
	if result.ElapsedMs <= 0 {
		t.Error("完成事件应带用时")
	}
	if result.OutputDir != out {
		t.Errorf("输出目录 = %q, 期望 %q", result.OutputDir, out)
	}

	// 进度必须单调不降，且从低到高
	if len(progresses) < 2 {
		t.Fatalf("进度事件太少: %d", len(progresses))
	}
	last := -1.0
	sawHigh := false
	for _, p := range progresses {
		if p.Percent < last-0.01 {
			t.Errorf("进度回退了: %v 之后是 %v", last, p.Percent)
		}
		if p.Percent > 100 {
			t.Errorf("进度不应超过 100，实际 %v", p.Percent)
		}
		if p.Percent > 5 {
			sawHigh = true
		}
		last = p.Percent
	}
	if !sawHigh {
		t.Errorf("进度始终接近 0，说明体积估算没生效: %v", progresses)
	}
	if last < 99 {
		t.Errorf("结束前进度应接近 100，实际 %.1f", last)
	}
	if logs == 0 {
		t.Error("没有收到任何 7z 日志事件")
	}

	// 任务结束后 IsRunning 应回到 false
	deadline2 := time.Now().Add(5 * time.Second)
	for svc.IsRunning() && time.Now().Before(deadline2) {
		time.Sleep(50 * time.Millisecond)
	}
	if svc.IsRunning() {
		t.Error("任务结束后 IsRunning 仍为 true")
	}
}

func TestLiveServiceCancel(t *testing.T) {
	liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "big")
	// 造够大的数据，确保有时间点取消
	for i := 0; i < 12; i++ {
		writeLive(t, filepath.Join(src, "f", "c"+string(rune('a'+i))+".bin"), 8*1024*1024, "Z")
	}

	svc := NewService()
	if svc.SevenZipPath() == "" {
		t.Skip("Service 没探测到 7z")
	}
	results := make(chan Result, 4)
	svc.SetEventSink(func(name string, data any) {
		if name == "archive:done" {
			if r, ok := data.(Result); ok {
				select {
				case results <- r:
				default:
				}
			}
		}
	})

	jobID, err := svc.Start(ArchiveRequest{
		SourceDir: src, OutputDir: base, ArchiveName: "cancelme",
		Overwrite: true,
	})
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	// 等到压缩包文件出现（说明 7z 已经在写）再取消
	archive := filepath.Join(base, "cancelme.7z")
	for i := 0; i < 200; i++ {
		if _, err := os.Stat(archive); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := svc.Cancel(jobID); err != nil {
		t.Fatalf("Cancel 失败: %v", err)
	}

	select {
	case r := <-results:
		if !r.Cancelled {
			t.Errorf("结果应标记为已取消: %+v", r)
		}
		if len(r.Archives) != 0 {
			t.Errorf("取消后不应记成功: %v", r.Archives)
		}
		// 本次新建的半成品必须清掉
		if _, err := os.Stat(archive); err == nil {
			t.Error("取消后仍留下了半成品压缩包")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("取消后没有收到 archive:done")
	}
}

func TestLiveServiceDeleteSource(t *testing.T) {
	liveSevenZip(t)

	base := t.TempDir()
	src := filepath.Join(base, "待清理")
	writeLive(t, filepath.Join(src, "a.bin"), 64*1024, "A")
	writeLive(t, filepath.Join(src, "sub", "b.bin"), 64*1024, "B")
	out := filepath.Join(base, "out")

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

	jobID, err := svc.Start(ArchiveRequest{
		SourceDir: src, OutputDir: out, ArchiveName: "packed",
		DeleteSource: true, Overwrite: true,
	})
	if err != nil {
		t.Fatalf("Start 失败: %v", err)
	}

	var r Result
	select {
	case r = <-results:
	case <-time.After(90 * time.Second):
		t.Fatal("等待 archive:done 超时")
	}
	if !r.Success {
		t.Fatalf("任务未成功: %+v", r)
	}
	if len(r.Archives) != 1 || !strings.HasSuffix(r.Archives[0], "packed.7z") {
		t.Fatalf("产物不对: %v", r.Archives)
	}
	if _, err := os.Stat(r.Archives[0]); err != nil {
		t.Errorf("压缩包没生成: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("源文件夹应已被删除")
	}
	if len(r.Removed) != 1 || r.Removed[0] != src {
		t.Errorf("删除清单不对: %v", r.Removed)
	}
	if r.RemovedLog == "" {
		t.Error("应写删除清单日志")
	} else if _, err := os.Stat(r.RemovedLog); err != nil {
		t.Errorf("删除清单日志不存在: %v", err)
	} else {
		t.Cleanup(func() { _ = os.Remove(r.RemovedLog) })
		body, _ := os.ReadFile(r.RemovedLog)
		if !strings.Contains(string(body), src) {
			t.Errorf("删除清单里没有记录 %s:\n%s", src, body)
		}
	}
	_ = jobID
}
