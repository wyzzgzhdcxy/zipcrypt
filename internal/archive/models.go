// Package archive 实现"快速归档加密"：把一个文件夹（整体或按子目录拆分）
// 调用 7-Zip 打成 AES 加密的 .7z，并把进度实时推送给前端。
package archive

// 任务阶段常量，同时用于前端展示
const (
	PhaseScanning  = "scanning"  // 统计源目录大小
	PhasePlanning  = "planning"  // 生成任务列表
	PhaseArchiving = "archiving" // 7z 正在写压缩包
	PhaseDone      = "done"      // 全部成功
	PhaseFailed    = "failed"    // 有任务失败
	PhaseCancelled = "cancelled" // 用户中途取消
)

// ArchiveRequest 前端提交的一次加密请求。
// 输出位置和文件名都不在这里：输出固定为源目录的上一级，
// 包名固定为「源目录名.7z」（拆分模式下为「子目录名.7z」）。
type ArchiveRequest struct {
	// SourceDir 要加密的源文件夹（必填）
	SourceDir string `json:"sourceDir"`
	// SplitSubDirs true = 不加密所选文件夹本身，而是把它下面的每个子文件夹各自打成一个加密包
	SplitSubDirs bool `json:"splitSubDirs"`
	// Overwrite 已存在同名压缩包时是否覆盖（对应 7z -y）
	Overwrite bool `json:"overwrite"`
}

// Progress 进度事件负载，通过 "archive:progress" 事件推送
type Progress struct {
	JobID     string  `json:"jobId"`
	Phase     string  `json:"phase"`
	Percent   float64 `json:"percent"`   // 总进度 0-100
	Step      int     `json:"step"`      // 第几个压缩包（1 起）
	Steps     int     `json:"steps"`     // 总共要打几个包
	Archive   string  `json:"archive"`   // 当前压缩包名
	File      string  `json:"file"`      // 当前正在写入的文件（7z 有输出时才有）
	Written   int64   `json:"written"`   // 已写入字节（按压缩包体积估算）
	Total     int64   `json:"total"`     // 源数据总字节
	Speed     int64   `json:"speed"`     // 字节/秒
	Message   string  `json:"message"`   // 状态文案
	Command   string  `json:"command"`   // 脱敏后的 7z 命令，便于用户核对
	Timestamp int64   `json:"timestamp"` // 毫秒时间戳
}

// LogLine 日志事件负载，通过 "archive:log" 事件推送
type LogLine struct {
	JobID     string `json:"jobId"`
	Level     string `json:"level"` // info / warn / error
	Text      string `json:"text"`
	Timestamp int64  `json:"timestamp"`
}

// Result 任务结束事件负载，通过 "archive:done" 事件推送
type Result struct {
	JobID      string   `json:"jobId"`
	Success    bool     `json:"success"`
	Cancelled  bool     `json:"cancelled"`
	Archives   []string `json:"archives"`   // 生成的压缩包绝对路径
	Failed     []string `json:"failed"`     // 失败项描述
	OutputDir  string   `json:"outputDir"`  // 输出目录
	TotalBytes int64    `json:"totalBytes"` // 源数据总字节
	ElapsedMs  int64    `json:"elapsedMs"`
	Removed    []string `json:"removed"`    // 实际删除的源路径
	RemovedLog string   `json:"removedLog"` // 删除清单日志路径（可回溯）
	Error      string   `json:"error"`
}

// SevenZipInfo 7-Zip 可执行文件探测结果
type SevenZipInfo struct {
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
	Message string `json:"message"`
}

// SubDirInfo 源目录下的一个直接子目录
type SubDirInfo struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	FileCount int    `json:"fileCount"`
	TotalSize int64  `json:"totalSize"`
}

// SourceInfo 源目录扫描结果，用于界面预览
type SourceInfo struct {
	Path       string       `json:"path"`
	Name       string       `json:"name"`
	Parent     string       `json:"parent"`
	FileCount  int          `json:"fileCount"`
	DirCount   int          `json:"dirCount"`
	TotalSize  int64        `json:"totalSize"`
	SubDirs    []SubDirInfo `json:"subDirs"`
	LooseFiles int          `json:"looseFiles"`
	LooseSize  int64        `json:"looseSize"`
	Skipped    int          `json:"skipped"`
	// OutDir 输出目录：固定为源目录的上一级
	OutDir string `json:"outDir"`
	// OutName 输出文件名：整体模式为「文件夹名.7z」，拆分模式下为子目录名列表
	OutName      string   `json:"outName"`
	OutNames     []string `json:"outNames"`
	ScanDuration int64    `json:"scanDuration"`
}

// Settings 前端持久化的界面设置
type Settings struct {
	SourceDir    string `json:"sourceDir"`
	SevenZipPath string `json:"sevenZipPath"`
	SplitSubDirs bool   `json:"splitSubDirs"`
	Overwrite    bool   `json:"overwrite"`
}
