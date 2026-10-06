package archive

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// testStamp 固定时间戳，让包名断言可预期
const testStamp = "20261006092610"

func TestBuildArgsMatchesUserCommand(t *testing.T) {
	tgt := target{
		WorkDir:     `D:\videos`,
		RelNames:    []string{"精品_日语"},
		ArchivePath: `D:\videos\jp_ry.7z`,
	}
	got := BuildArgs("wangchaojun", true, tgt)
	want := []string{"a", "-mx0", "-bsp1", "-bb0", "-sccUTF-8", "-pwangchaojun", "-mhe=on", "-y",
		`D:\videos\jp_ry.7z`, "." + string(filepath.Separator) + "精品_日语"}

	if len(got) != len(want) {
		t.Fatalf("参数个数 = %d, 期望 %d\ngot=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("参数[%d] = %q, 期望 %q", i, got[i], want[i])
		}
	}
	// 用户明确要求的两项不能丢
	joined := strings.Join(got, " ")
	for _, must := range []string{"-mx0", "-mhe=on", "-pwangchaojun"} {
		if !strings.Contains(joined, must) {
			t.Errorf("参数里缺少 %s：%s", must, joined)
		}
	}
}

func TestBuildArgsNoOverwriteNoExclude(t *testing.T) {
	got := BuildArgs("pw", false, target{
		RelNames:    []string{"a", "b"},
		ArchivePath: `C:\out\x.7z`,
	})
	for _, a := range got {
		if a == "-y" {
			t.Error("不覆盖时不应带 -y")
		}
		if strings.HasPrefix(a, "-x!") {
			t.Errorf("未设置 Exclude 时不应带排除开关：%q", a)
		}
	}
	if got[len(got)-1] != "."+string(filepath.Separator)+"b" {
		t.Errorf("最后应是待归档路径，实际 %q", got[len(got)-1])
	}
}

func TestBuildArgsExclude(t *testing.T) {
	got := BuildArgs("pw", true, target{
		RelNames:    []string{"dir"},
		ArchivePath: `C:\dir\dir.7z`,
		Exclude:     "dir.7z",
	})
	if !strings.Contains(strings.Join(got, " "), "-x!dir.7z") {
		t.Errorf("缺少排除自身压缩包的开关：%v", got)
	}
}

func TestRedactArgsHidesPassword(t *testing.T) {
	got := RedactArgs(BuildArgs("sup3rSecret", true, target{ArchivePath: "x.7z", RelNames: []string{"d"}}))
	if strings.Contains(got, "sup3rSecret") {
		t.Fatalf("脱敏后仍包含密码：%s", got)
	}
	if !strings.Contains(got, "-p***") {
		t.Errorf("脱敏结果缺少占位符：%s", got)
	}
}

func TestParseProgressLine(t *testing.T) {
	cases := []struct {
		line  string
		pct   float64
		name  string
		valid bool
	}{
		{` 45% 12 - .\精品_日语\a.mp4`, 45, `.\精品_日语\a.mp4`, true},
		{"100% 25 - .\\b.txt\r", 100, `.\b.txt`, true},
		{"Everything is Ok", 0, "", false},
		{"", 0, "", false},
	}
	for _, c := range cases {
		pct, _, name, ok := parseProgressLine(c.line)
		if ok != c.valid {
			t.Errorf("parseProgressLine(%q) ok=%v, 期望 %v", c.line, ok, c.valid)
			continue
		}
		if ok && (pct != c.pct || name != c.name) {
			t.Errorf("parseProgressLine(%q) = (%.0f, %q), 期望 (%.0f, %q)", c.line, pct, name, c.pct, c.name)
		}
	}
}

func TestParseBarePercent(t *testing.T) {
	if p, ok := parseBarePercent("  0%"); !ok || p != 0 {
		t.Errorf("parseBarePercent(\"  0%%\") = (%.0f, %v)", p, ok)
	}
	if p, ok := parseBarePercent("  73%"); !ok || p != 73 {
		t.Errorf("parseBarePercent(\"  73%%\") = (%.0f, %v)", p, ok)
	}
	if _, ok := parseBarePercent("Creating archive: x.7z"); ok {
		t.Error("普通日志行不应被当成百分比")
	}
}

func TestParseScanSummary(t *testing.T) {
	files, bytes, ok := parseScanSummary("1 folder, 40 files, 800080 bytes (782 KiB)")
	if !ok || files != 40 || bytes != 800080 {
		t.Errorf("解析扫描汇总失败：(%d, %d, %v)", files, bytes, ok)
	}
	if _, _, ok := parseScanSummary("Creating archive: a.7z"); ok {
		t.Error("普通行不应被当成扫描汇总")
	}
}

func TestClassifyLogLine(t *testing.T) {
	cases := map[string]string{
		"ERROR: Cannot open":   "error",
		"WARNING: x":           "warn",
		"WARNINGS:":            "warn",
		"Everything is Ok":     "info",
		"Files read from disk": "info",
	}
	for line, want := range cases {
		if got := classifyLogLine(line); got != want {
			t.Errorf("classifyLogLine(%q) = %q, 期望 %q", line, got, want)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"jp_ry":       "jp_ry",
		`a/b\c:d*e?f`: "a_b_c_d_e_f",
		"  精品_日语  ":   "精品_日语",
		"..":          "",
		"a<b>c|d":     "a_b_c_d",
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestScanTreeCountsFilesAndBytes(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.txt"), 10)
	mustWrite(t, filepath.Join(root, "sub", "b.txt"), 20)
	mustWrite(t, filepath.Join(root, "sub", "deep", "c.txt"), 30)

	files, dirs, size, _, err := ScanTree(root)
	if err != nil {
		t.Fatalf("ScanTree 失败: %v", err)
	}
	if files != 3 {
		t.Errorf("文件数 = %d, 期望 3", files)
	}
	if size != 60 {
		t.Errorf("总字节 = %d, 期望 60", size)
	}
	if dirs != 3 { // root + sub + deep
		t.Errorf("目录数 = %d, 期望 3", dirs)
	}
}

func TestBuildTargetsWholeFolder(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "精品_日语")
	mustWrite(t, filepath.Join(src, "a.txt"), 5)

	targets, err := BuildTargets(ArchiveRequest{
		SourceDir: src,
		Overwrite: true,
		Stamp:     testStamp,
	})
	if err != nil {
		t.Fatalf("BuildTargets 失败: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("整体打包应只有 1 个 target，实际 %d", len(targets))
	}
	got := targets[0]
	// 需求：包名固定为「文件夹名.时间戳.7z」
	if want := "精品_日语." + testStamp + ".7z"; got.Label != want {
		t.Errorf("包名 = %s, 期望 %s", got.Label, want)
	}
	// 需求：压缩包和源文件夹同级
	if got.ArchivePath != filepath.Join(parent, "精品_日语."+testStamp+".7z") {
		t.Errorf("输出路径 = %s, 期望与源目录同级", got.ArchivePath)
	}
	// 工作目录取上一级，包内才不会带整条路径
	if got.WorkDir != parent {
		t.Errorf("WorkDir = %s, 期望 %s", got.WorkDir, parent)
	}
	if got.RelNames[0] != "精品_日语" {
		t.Errorf("RelNames = %v, 期望 [精品_日语]", got.RelNames)
	}
	if got.Exclude != "" {
		t.Errorf("包落在源目录外时不应排除自身，实际 %q", got.Exclude)
	}
}

func TestBuildTargetsSplitSubDirs(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "合集")
	mustWrite(t, filepath.Join(src, "动画", "a.mp4"), 1)
	mustWrite(t, filepath.Join(src, "漫画", "b.jpg"), 1)
	mustWrite(t, filepath.Join(src, "readme.txt"), 1)

	targets, err := BuildTargets(ArchiveRequest{
		SourceDir:    src,
		SplitSubDirs: true,
		Overwrite:    true,
		Stamp:        testStamp,
	})
	if err != nil {
		t.Fatalf("BuildTargets 失败: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("两个子目录 + 一堆散文件应得 3 个包，实际 %d", len(targets))
	}

	names := map[string]target{}
	for _, tg := range targets {
		names[tg.Label] = tg
		if filepath.Dir(tg.ArchivePath) != parent {
			t.Errorf("%s 没有落在与源目录同级的地方：%s", tg.Label, tg.ArchivePath)
		}
	}
	// 需求：拆分模式下包名就是 子目录名.时间戳.7z
	for _, want := range []string{"动画." + testStamp + ".7z", "漫画." + testStamp + ".7z", "根目录文件." + testStamp + ".7z"} {
		if _, ok := names[want]; !ok {
			t.Errorf("缺少压缩包 %s，实际有 %v", want, keysOf(targets))
		}
	}
	// 子目录包的工作目录是源目录本身，包内就是 "动画\..."
	if got := names["动画."+testStamp+".7z"]; got.WorkDir != src || got.RelNames[0] != "动画" {
		t.Errorf("子目录包 WorkDir/RelNames = %s / %v", got.WorkDir, got.RelNames)
	}
	// 散文件包要带上全部根级文件
	if got := names["根目录文件."+testStamp+".7z"]; len(got.RelNames) != 1 || got.RelNames[0] != "readme.txt" {
		t.Errorf("散文件包 RelNames = %v", got.RelNames)
	}
}

func TestBuildTargetsSplitDedupNames(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "合集")
	// 子目录名和散文件包的默认名撞车
	mustWrite(t, filepath.Join(src, "根目录文件", "x.txt"), 1)
	mustWrite(t, filepath.Join(src, "readme.txt"), 1)

	targets, err := BuildTargets(ArchiveRequest{
		SourceDir:    src,
		SplitSubDirs: true,
		Overwrite:    true,
		Stamp:        testStamp,
	})
	if err != nil {
		t.Fatalf("BuildTargets 失败: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("应有 2 个包，实际 %d: %v", len(targets), labelsOf(targets))
	}
	labels := labelsOf(targets)
	if labels[0] != "根目录文件."+testStamp+".7z" || labels[1] != "根目录文件-2."+testStamp+".7z" {
		t.Errorf("散文件包应当自动错开重名，实际 %v", labels)
	}
	// 两个包都落在源目录上一级，且互不覆盖
	seen := map[string]bool{}
	for _, tg := range targets {
		if seen[tg.ArchivePath] {
			t.Errorf("输出路径冲突：%v", labels)
		}
		seen[tg.ArchivePath] = true
		if filepath.Dir(tg.ArchivePath) != parent {
			t.Errorf("%s 没有落在与源目录同级的地方：%s", tg.Label, tg.ArchivePath)
		}
	}
}

func TestArchivePasswordIsHardcoded(t *testing.T) {
	// 需求：密码固定死、硬编码在程序里，界面上不可改。
	// 请求结构里根本没有 Password 字段，任何外部输入都改不了它。
	if strings.TrimSpace(ArchivePassword) == "" {
		t.Fatal("内置密码不能为空，否则 7z 会转去读控制台")
	}
	if ArchivePassword != "fdshainewqjfnsa231!23219jh" {
		t.Errorf("内置密码被改成了 %q", ArchivePassword)
	}
	// 确认请求结构里确实没有密码入口
	rt := reflect.TypeOf(ArchiveRequest{})
	if _, ok := rt.FieldByName("Password"); ok {
		t.Error("ArchiveRequest 不应再有 Password 字段")
	}
	st := reflect.TypeOf(Settings{})
	if _, ok := st.FieldByName("Password"); ok {
		t.Error("Settings 不应再有 Password 字段（密码不落盘）")
	}
	// 固定密码必须原样出现在 -p 参数里
	args := BuildArgs(ArchivePassword, true, target{ArchivePath: "x.7z", RelNames: []string{"d"}})
	if !strings.Contains(strings.Join(args, " "), "-p"+ArchivePassword) {
		t.Errorf("固定密码没有传给 7z：%v", args)
	}
}

func TestBuildTargetsRejectsMissingSource(t *testing.T) {
	if _, err := BuildTargets(ArchiveRequest{SourceDir: filepath.Join(t.TempDir(), "nope")}); err == nil {
		t.Fatal("源目录不存在应当报错")
	}
}

func TestBuildTargetsNoOverwriteRejectsExisting(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "合集")
	mustWrite(t, filepath.Join(src, "a.txt"), 1)
	// 已有同名包的位置由规则决定：源目录上一级 + 文件夹名.时间戳.7z
	mustWrite(t, filepath.Join(parent, "合集."+testStamp+".7z"), 1)

	if _, err := BuildTargets(ArchiveRequest{
		SourceDir: src, Overwrite: false, Stamp: testStamp,
	}); err == nil {
		t.Fatal("不覆盖时遇到同名包应当报错")
	}
	if _, err := BuildTargets(ArchiveRequest{
		SourceDir: src, Overwrite: true, Stamp: testStamp,
	}); err != nil {
		t.Fatalf("勾选覆盖后应当可以继续：%v", err)
	}
}

func TestArchiveNameCarriesTimestamp(t *testing.T) {
	// 需求：包名 = <原名>.<yyyymmddhhmmss>.7z
	if got := archiveName("合集", testStamp); got != "合集."+testStamp+".7z" {
		t.Errorf("包名 = %q, 期望 合集.%s.7z", got, testStamp)
	}
	// 已经是纯数字时间戳时原样保留（前端回传的就是这个）
	if got := normalizeStamp(testStamp); got != testStamp {
		t.Errorf("合法时间戳被改成了 %q", got)
	}
	// 空值 = 当前时间，14 位
	for _, in := range []string{"", "   ", "abc-:-"} {
		got := normalizeStamp(in)
		if len(got) != len(stampLayout) {
			t.Errorf("normalizeStamp(%q) = %q, 期望 %d 位数字", in, got, len(stampLayout))
		}
		for _, r := range got {
			if r < '0' || r > '9' {
				t.Errorf("normalizeStamp(%q) 含非数字：%q", in, got)
				break
			}
		}
	}
	// 脏输入只保留数字并截到 14 位，绝不会把非法字符写进文件名
	if got := normalizeStamp("2026-10-06 09:26:10.999"); got != "20261006092610" {
		t.Errorf("脏时间戳归一化 = %q, 期望 20261006092610", got)
	}
	if got := normalizeStamp("1234567890123456789"); got != "12345678901234" {
		t.Errorf("超长时间戳应截断到 14 位，实际 %q", got)
	}
}

// TestPreviewMatchesRealOutput 是关键不变量：界面预览的包名必须和
// 真实产物逐字一致，否则用户按预览去找文件会找不到。
func TestPreviewMatchesRealOutput(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "合集")
	mustWrite(t, filepath.Join(src, "动画", "a.mp4"), 1)
	mustWrite(t, filepath.Join(src, "loose.txt"), 1)

	info, err := InspectSource(src)
	if err != nil {
		t.Fatalf("InspectSource 失败: %v", err)
	}
	if info.Stamp == "" {
		t.Fatal("扫描结果必须带上时间戳，前端要靠它对齐预览名和产物名")
	}

	// 前端的真实流程：扫描拿 stamp，点开始加密时原样回传
	whole, err := BuildTargets(ArchiveRequest{
		SourceDir: src, Overwrite: true, Stamp: info.Stamp,
	})
	if err != nil {
		t.Fatalf("BuildTargets 失败: %v", err)
	}
	if len(whole) != 1 || whole[0].Label != info.OutName {
		t.Errorf("整体模式预览 %s 与产物 %v 不一致", info.OutName, labelsOf(whole))
	}

	split, err := BuildTargets(ArchiveRequest{
		SourceDir: src, SplitSubDirs: true, Overwrite: true, Stamp: info.Stamp,
	})
	if err != nil {
		t.Fatalf("BuildTargets 失败: %v", err)
	}
	if strings.Join(labelsOf(split), ",") != strings.Join(info.OutNames, ",") {
		t.Errorf("拆分模式预览 %v 与产物 %v 不一致", info.OutNames, labelsOf(split))
	}

	// 不回传时间戳时也要能正常出包，不能把空串拼进文件名
	fallback, err := BuildTargets(ArchiveRequest{SourceDir: src, Overwrite: true})
	if err != nil {
		t.Fatalf("未带 stamp 的 BuildTargets 失败: %v", err)
	}
	if !strings.HasSuffix(fallback[0].Label, ".7z") || strings.Contains(fallback[0].Label, "..") {
		t.Errorf("兜底包名不合法：%s", fallback[0].Label)
	}
}

func TestInspectSource(t *testing.T) {
	parent := t.TempDir()
	src := filepath.Join(parent, "合集")
	mustWrite(t, filepath.Join(src, "sub1", "a.txt"), 10)
	mustWrite(t, filepath.Join(src, "sub2", "b.txt"), 20)
	mustWrite(t, filepath.Join(src, "loose.txt"), 5)

	info, err := InspectSource(src)
	if err != nil {
		t.Fatalf("InspectSource 失败: %v", err)
	}
	if info.FileCount != 3 || info.TotalSize != 35 {
		t.Errorf("文件数/大小 = %d/%d, 期望 3/35", info.FileCount, info.TotalSize)
	}
	if len(info.SubDirs) != 2 {
		t.Fatalf("子目录数 = %d, 期望 2", len(info.SubDirs))
	}
	if info.SubDirs[0].Name != "sub1" || info.SubDirs[1].Name != "sub2" {
		t.Errorf("子目录应按名称排序，实际 %s,%s", info.SubDirs[0].Name, info.SubDirs[1].Name)
	}
	if info.LooseFiles != 1 || info.LooseSize != 5 {
		t.Errorf("散文件统计 = %d/%d, 期望 1/5", info.LooseFiles, info.LooseSize)
	}
	// 界面预览必须和真实产物一致：输出目录 = 上一级
	if info.OutDir != parent {
		t.Errorf("输出目录 = %s, 期望 %s", info.OutDir, parent)
	}
	// 包名带时间戳，前缀由规则定，后缀就是本次扫描的 stamp
	if !strings.HasPrefix(info.OutName, "合集.") || !strings.HasSuffix(info.OutName, "."+info.Stamp+".7z") {
		t.Errorf("整体模式包名 = %s, 期望 合集.%s.7z", info.OutName, info.Stamp)
	}
	want := []string{"sub1." + info.Stamp + ".7z", "sub2." + info.Stamp + ".7z", "根目录文件." + info.Stamp + ".7z"}
	if strings.Join(info.OutNames, ",") != strings.Join(want, ",") {
		t.Errorf("拆分模式包名 = %v, 期望 %v", info.OutNames, want)
	}
}

func TestUniqueNameCaseInsensitive(t *testing.T) {
	used := map[string]bool{}
	if got := uniqueName("a", used); got != "a" {
		t.Errorf("首次 = %q", got)
	}
	// 大小写不同也算重名（贴近 Windows），但保留调用方给的原始大小写
	if got := uniqueName("A", used); got != "A-2" {
		t.Errorf("重名(忽略大小写) = %q, 期望 A-2", got)
	}
}

func TestIsInside(t *testing.T) {
	if !isInside(`D:\a\b`, `D:\a`) {
		t.Error("子目录应判定为在内部")
	}
	if isInside(`D:\a`, `D:\a`) {
		t.Error("自身不应算内部")
	}
	if isInside(`D:\x`, `D:\a`) {
		t.Error("无关路径不应算内部")
	}
	// sameOrInside 还要把"就是同一个目录"算进去：
	// 源目录在盘根时，压缩包会落在源目录内部，必须触发自排除
	if !sameOrInside(`D:\a`, `D:\a`) {
		t.Error("自身应被 sameOrInside 判为内部")
	}
	if sameOrInside(`D:\x`, `D:\a`) {
		t.Error("无关路径不应算内部")
	}
}

func TestLoadSaveSettingsRoundTrip(t *testing.T) {
	if _, err := configDir(); err != nil {
		t.Fatalf("配置目录不可用: %v", err)
	}
	in := Settings{SevenZipPath: `C:\Program Files\7-Zip\7z.exe`, SplitSubDirs: true, Overwrite: true}
	if err := SaveSettings(in); err != nil {
		t.Fatalf("SaveSettings 失败: %v", err)
	}
	out := LoadSettings()
	if out.SevenZipPath != in.SevenZipPath || !out.SplitSubDirs || !out.Overwrite {
		t.Errorf("读回的设置与写入不一致: %+v", out)
	}
	// 源目录不再持久化：旧配置里残留的 sourceDir 必须被忽略，
	// 否则用户上次选的路径会被下次启动悄悄带回来
	if _, ok := reflect.TypeOf(Settings{}).FieldByName("SourceDir"); ok {
		t.Error("Settings 不应再有 SourceDir 字段（源目录不再持久化）")
	}
	// 旧版本配置里残留的字段（outputDir/archiveName/password/deleteSource）
	// 必须被忽略，不能再影响行为
	if _, ok := reflect.TypeOf(Settings{}).FieldByName("DeleteSource"); ok {
		t.Error("Settings 不应再有 DeleteSource 字段（删除源文件功能已移除）")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{512: "512 B", 2048: "2.00 KiB", 5 * 1024 * 1024: "5.00 MiB"}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, 期望 %q", in, got, want)
		}
	}
}

// ---------------------------------------------------------------- 辅助

func mustWrite(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll 失败: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("写文件失败: %v", err)
	}
}

func keysOf(targets []target) []string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.Label)
	}
	return out
}

func labelsOf(targets []target) []string { return keysOf(targets) }

func TestOpenOutputDirValidatesPaths(t *testing.T) {
	svc := NewService()
	dir := t.TempDir()
	file := filepath.Join(dir, "x.7z")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 目录为空要报错
	if err := svc.OpenOutputDir("", ""); err == nil {
		t.Error("空目录应当报错")
	}
	// 目录不存在要报错（不启动 explorer）
	if err := svc.OpenOutputDir(filepath.Join(dir, "nope"), ""); err == nil {
		t.Error("不存在的目录应当报错")
	}
	// 路径存在但不是目录也要拦住
	if err := svc.OpenOutputDir(file, ""); err == nil {
		t.Error("指向文件的路径应当报错")
	}
	// 注意：这里只覆盖"会报错"的分支。file 缺失时会真的去打开目录，
	// 会弹资源管理器窗口，不属于单元测试该有的副作用，
	// 真正的打开行为由 ZIPCRYPT_LIVE_EXPLORER=1 的用例覆盖。
}

func TestCheckSourceDir(t *testing.T) {
	// 不存在的路径要给出人话错误，而不是 GetFileAttributesEx 这种原生串
	if _, err := checkSourceDir(`D:\绝对不存在的目录_zzz`); err == nil {
		t.Fatal("不存在的路径应当报错")
	} else if !strings.Contains(err.Error(), "路径不存在") {
		t.Errorf("错误信息应说明路径不存在: %v", err)
	}

	// 只输到盘符就停手时，别让它去扫整个盘
	if _, err := checkSourceDir("D:"); err == nil {
		t.Error(`只输入 "D:" 应当报错`)
	} else if !strings.Contains(err.Error(), "完整路径") {
		t.Errorf(`"D:" 的错误信息应提示补全路径: %v`, err)
	}

	if _, err := checkSourceDir("   "); err == nil {
		t.Error("空路径应当报错")
	}

	// 文件不是目录
	f := filepath.Join(t.TempDir(), "a.txt")
	mustWrite(t, f, 1)
	if _, err := checkSourceDir(f); err == nil {
		t.Error("指向文件的路径应当报错")
	} else if !strings.Contains(err.Error(), "不是一个文件夹") {
		t.Errorf("错误信息应说明不是文件夹: %v", err)
	}

	// 正常路径
	dir := t.TempDir()
	got, err := checkSourceDir(dir)
	if err != nil {
		t.Fatalf("合法路径不应报错: %v", err)
	}
	if got != filepath.Clean(dir) {
		t.Errorf("返回路径 = %q, 期望 %q", got, filepath.Clean(dir))
	}
}
