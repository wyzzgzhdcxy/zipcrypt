package archive

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// target 一个待生成的加密包：一次 7z 调用 = 一个 target
type target struct {
	// WorkDir 7z 的工作目录。7z 会把参数里的路径按"相对工作目录"存进包内，
	// 所以整体打包时工作目录取源目录的上一级，包内就是 "精品_日语\xxx"，
	// 不会带上整条磁盘路径。
	WorkDir string
	// RelNames 传给 7z 的相对路径列表（一个子目录，或一组散文件）
	RelNames []string
	// ArchivePath 输出的 .7z 绝对路径
	ArchivePath string
	// Label 界面展示名
	Label string
	// Exclude 需要排除的文件名（输出包落在源目录内部时防止把自己打进自己）
	Exclude string

	FileCount int
	Bytes     int64
}

// ScanTree 统计目录下的文件数、目录数和总字节数。
// 符号链接/联接点直接跳过，避免循环。
func ScanTree(root string) (files, dirs int, size int64, skipped int, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// 单个条目读不到（比如权限不足）不影响整体统计
			skipped++
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			skipped++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			dirs++
			return nil
		}
		files++
		if info, statErr := d.Info(); statErr == nil {
			size += info.Size()
		} else {
			skipped++
		}
		return nil
	})
	return
}

// stampLayout 包名里的时间戳格式：yyyymmddhhmmss
const stampLayout = "20060102150405"

// normalizeStamp 归一化包名里的时间戳：
// 留空 = 用当前时间；只保留数字，最多 14 位。
// 前端扫描目录时拿到的 Stamp 会在点"开始加密"时原样回传，
// 所以界面预览的包名和真实产物逐字一致。
// 允许重复调用：已经是纯数字的时间戳原样返回。
func normalizeStamp(stamp string) string {
	var digits []rune
	for _, r := range stamp {
		if r >= '0' && r <= '9' {
			digits = append(digits, r)
		}
	}
	if len(digits) == 0 {
		return time.Now().Format(stampLayout)
	}
	if len(digits) > len(stampLayout) {
		digits = digits[:len(stampLayout)]
	}
	return string(digits)
}

// archiveName 组压缩包名：<原名>.<yyyymmddhhmmss>.7z
func archiveName(base, stamp string) string {
	return base + "." + normalizeStamp(stamp) + ".7z"
}

// SanitizeName 清洗压缩包名：去掉 Windows 非法字符与首尾空白
func SanitizeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32:
			// 丢弃控制字符
		case strings.ContainsRune(`\/:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.Trim(out, ".")
	if out == "" {
		return ""
	}
	// Windows 长路径限制，后缀 ".yyyymmddhhmmss.7z" 固定占 20 个字符，留足余量
	const maxRunes = 60
	if r := []rune(out); len(r) > maxRunes {
		out = string(r[:maxRunes])
	}
	return strings.TrimSpace(out)
}

// checkSourceDir 校验源路径。
// 特别拦下 "D:" 这种盘符相对路径：Go 会把它解析成"该盘上的当前目录"，
// 用户多半只是 "D:\xxx" 还没打完，不该让他一不小心扫完整个盘。
func checkSourceDir(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", fmt.Errorf("请先选择文件夹")
	}
	if len(p) == 2 && p[1] == ':' {
		return "", fmt.Errorf("请输入完整路径，例如 %s\\", p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("路径不存在：%s", abs)
		}
		return "", fmt.Errorf("无法访问：%s（%v）", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("这不是一个文件夹：%s", abs)
	}
	return abs, nil
}

// InspectSource 扫描源目录，给界面做预览（文件数、总大小、子目录清单）
func InspectSource(path string) (SourceInfo, error) {
	start := time.Now()
	abs, err := checkSourceDir(path)
	if err != nil {
		return SourceInfo{}, err
	}

	files, dirs, size, skipped, err := ScanTree(abs)
	if err != nil {
		return SourceInfo{}, err
	}

	// 时间戳在扫描时就定下来并回给前端：同一次选择的预览名和实际产物名必须一致，
	// 拆分模式下所有包也共用这一个时间戳
	stamp := normalizeStamp("")

	res := SourceInfo{
		Path:      abs,
		Name:      filepath.Base(abs),
		Parent:    filepath.Dir(abs),
		FileCount: files,
		DirCount:  dirs,
		TotalSize: size,
		Skipped:   skipped,
		SubDirs:   []SubDirInfo{},
		// 输出固定在源目录的上一级，包名固定为 文件夹名.时间戳.7z
		OutDir:       filepath.Dir(abs),
		OutName:      archiveName(SanitizeName(filepath.Base(abs)), stamp),
		OutNames:     []string{},
		Stamp:        stamp,
		ScanDuration: time.Since(start).Milliseconds(),
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return res, nil // 子项读不到时至少把总量统计出来
	}
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		full := filepath.Join(abs, e.Name())
		if e.IsDir() {
			f, _, sz, _, _ := ScanTree(full)
			res.SubDirs = append(res.SubDirs, SubDirInfo{
				Name: e.Name(), Path: full, FileCount: f, TotalSize: sz,
			})
			continue
		}
		res.LooseFiles++
		if fi, err := e.Info(); err == nil {
			res.LooseSize += fi.Size()
		}
	}
	sort.Slice(res.SubDirs, func(i, j int) bool { return res.SubDirs[i].Name < res.SubDirs[j].Name })

	// 拆分模式下每个子文件夹各出一个包，根目录散文件合成一个。
	// 这里用和 BuildTargets 完全一样的命名逻辑，好让界面预览和实际产物一致。
	used := map[string]bool{}
	for _, d := range res.SubDirs {
		res.OutNames = append(res.OutNames, archiveName(uniqueName(SanitizeName(d.Name), used), stamp))
	}
	if res.LooseFiles > 0 {
		res.OutNames = append(res.OutNames, archiveName(uniqueName("根目录文件", used), stamp))
	}
	return res, nil
}

// BuildTargets 把一次请求展开成若干个 7z 任务。
// 输出位置和文件名不是用户输入：输出固定为源目录的上一级，
// 整体模式包名为「文件夹名.时间戳.7z」，拆分模式为「子目录名.时间戳.7z」。
func BuildTargets(req ArchiveRequest) ([]target, error) {
	src, err := checkSourceDir(req.SourceDir)
	if err != nil {
		return nil, err
	}
	// 密码是硬编码常量（ArchivePassword），不来自请求；
	// 这里只做一次防御性检查，避免有人把常量改空了导致 7z 转去读控制台
	if strings.TrimSpace(ArchivePassword) == "" {
		return nil, fmt.Errorf("内置密码为空，无法加密")
	}

	// 前端把扫描时定下的时间戳回传；没传（比如直接调后端）就用当前时间。
	// 只在这里取一次时间，保证拆分出来的所有包时间戳完全相同。
	stamp := normalizeStamp(req.Stamp)

	// 压缩包和源文件夹同级
	outDir := filepath.Dir(src)
	if fi, err := os.Stat(outDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("输出目录不可用：%s", outDir)
	}

	var targets []target

	if !req.SplitSubDirs {
		name := SanitizeName(filepath.Base(src))
		if name == "" {
			return nil, fmt.Errorf("无法从 %s 推导出压缩包名", src)
		}
		files, _, size, _, _ := ScanTree(src)
		t := target{
			WorkDir:     outDir,
			RelNames:    []string{filepath.Base(src)},
			ArchivePath: filepath.Join(outDir, archiveName(name, stamp)),
			Label:       archiveName(name, stamp),
			FileCount:   files,
			Bytes:       size,
		}
		// 万一输出目录就是源目录本身（源在盘根时可能出现），
		// 必须排除掉自己，否则 7z 会把正在写的压缩包也打进压缩包
		if sameOrInside(outDir, src) {
			t.Exclude = filepath.Base(t.ArchivePath)
		}
		targets = []target{t}
	} else {
		entries, err := os.ReadDir(src)
		if err != nil {
			return nil, err
		}
		used := map[string]bool{}
		var loose []string
		for _, e := range entries {
			if e.Type()&fs.ModeSymlink != 0 {
				continue
			}
			if !e.IsDir() {
				loose = append(loose, e.Name())
				continue
			}
			files, _, size, _, _ := ScanTree(filepath.Join(src, e.Name()))
			name := uniqueName(SanitizeName(e.Name()), used)
			targets = append(targets, target{
				WorkDir:     src,
				RelNames:    []string{e.Name()},
				ArchivePath: filepath.Join(outDir, archiveName(name, stamp)),
				Label:       archiveName(name, stamp),
				FileCount:   files,
				Bytes:       size,
			})
		}

		// 源目录根下散落的文件单独打一个包
		if len(loose) > 0 {
			var size int64
			for _, n := range loose {
				if fi, err := os.Stat(filepath.Join(src, n)); err == nil {
					size += fi.Size()
				}
			}
			name := uniqueName("根目录文件", used)
			targets = append(targets, target{
				WorkDir:     src,
				RelNames:    loose,
				ArchivePath: filepath.Join(outDir, archiveName(name, stamp)),
				Label:       archiveName(name, stamp),
				FileCount:   len(loose),
				Bytes:       size,
			})
		}

		if len(targets) == 0 {
			return nil, fmt.Errorf("%s 下面没有可归档的子目录或文件", src)
		}
	}

	if !req.Overwrite {
		var clash []string
		for _, t := range targets {
			if _, err := os.Stat(t.ArchivePath); err == nil {
				clash = append(clash, t.Label)
			}
		}
		if len(clash) > 0 {
			return nil, fmt.Errorf("以下压缩包已存在：%s（可勾选“覆盖同名文件”后重试）", strings.Join(clash, "、"))
		}
	}
	return targets, nil
}

// uniqueName 避免同一个输出目录里出现重名（大小写不敏感，贴近 Windows 行为）
func uniqueName(name string, used map[string]bool) string {
	key := strings.ToLower(name)
	if !used[key] {
		used[key] = true
		return name
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d", name, i)
		if !used[strings.ToLower(cand)] {
			used[strings.ToLower(cand)] = true
			return cand
		}
	}
	used[key] = true
	return name
}

// isInside 判断 child 是否位于 parent 目录内部（不含 parent 本身）
func isInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..")
}

// sameOrInside child == parent 或 child 位于 parent 内部
func sameOrInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}
