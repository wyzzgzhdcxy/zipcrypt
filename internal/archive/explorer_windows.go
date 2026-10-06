//go:build windows

package archive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"
)

// 打开资源管理器不靠 exec.Command("explorer.exe")，有两个问题：
//  1. explorer.exe 走 PATH 查找，GUI 程序的 PATH 未必完整；
//  2. 更要命的是 explorer 收到 DDE 请求后只负责把窗口开出来，
//     不会把它抢到前台。用户如果已经开了一堆资源管理器窗口，
//     新窗口就压在最底下，盯着屏幕看等于"点了完全没反应"。
//
// 所以改成直接调 Shell API 打开，再主动把窗口提到前台。

const (
	swShowNormal  = 1
	swRestore     = 9
	vkMenu        = 0x12
	keyEventKeyUp = 0x0002

	// SetWindowPos 用的常量
	hwndTop       = 0 // HWND_TOP
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010

	// cabinetClass 资源管理器窗口的窗口类
	cabinetClass = "CabinetWClass"

	wmSysCommand = 0x0112
	scClose      = 0xF060
)

var (
	shell32  = syscall.NewLazyDLL("shell32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procShellExecuteW = shell32.NewProc("ShellExecuteW")

	procEnumWindows            = user32.NewProc("EnumWindows")
	procGetClassNameW          = user32.NewProc("GetClassNameW")
	procGetWindowTextLengthW   = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procIsIconic               = user32.NewProc("IsIconic")
	procShowWindow             = user32.NewProc("ShowWindow")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procSwitchToThisWindow     = user32.NewProc("SwitchToThisWindow")
	procGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput      = user32.NewProc("AttachThreadInput")
	procKeybdEvent             = user32.NewProc("keybd_event")
	procSetWindowPos           = user32.NewProc("SetWindowPos")
	procGetCurrentThreadID     = kernel32.NewProc("GetCurrentThreadId")
)

// openExplorerDir 打开一个目录窗口，并把它提到前台
func openExplorerDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	before := explorerWindowSet()
	if err := shellExecute(abs, "open"); err != nil {
		return err
	}
	go raiseExplorerWindow(abs, before)
	return nil
}

// openExplorerSelect 打开文件所在目录并选中该文件。
//
// 两个坑，都踩过：
//  1. 绝不能设 SysProcAttr.HideWindow。实测带 HideWindow 启动 explorer 时，
//     窗口会被正常创建（标题都对），但 IsWindowVisible 恒为 0——
//     也就是说窗口被藏起来，用户点了完全看不见，这就是"打开目录没有反应"。
//     那个 HideWindow 原本是给 7z 加的（免得 GUI 程序里闪黑框），不能照搬过来。
//  2. 不能用 exec.Command 的 Args 传 /select,"..."，Go 会把引号转义成 \"，
//     explorer 解析不了路径，实测会跳到桌面。用 SysProcAttr.CmdLine 原样透传。
func openExplorerSelect(file string) error {
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}
	before := explorerWindowSet()

	exe := explorerExe()
	cmd := exec.Command(exe)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: syscall.EscapeArg(exe) + ` /select,"` + abs + `"`,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// explorer 是"启动即返回"的壳进程，Wait 只做回收
	go func() { _ = cmd.Wait() }()

	go raiseExplorerWindow(filepath.Dir(abs), before)
	return nil
}

// explorerExe 用绝对路径找 explorer.exe。
// GUI 程序的 PATH 未必完整，交给 exec 去 LookPath 可能直接找不到。
func explorerExe() string {
	if root := os.Getenv("SystemRoot"); root != "" {
		p := filepath.Join(root, "explorer.exe")
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("explorer.exe"); err == nil {
		return p
	}
	return "explorer.exe"
}

// ---------------------------------------------------------------- Shell API

// shellExecute 走 ShellExecuteW，不依赖 PATH 找 explorer.exe
func shellExecute(path, verb string) error {
	v, err := syscall.UTF16PtrFromString(verb)
	if err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// ShellExecuteW 返回 HINSTANCE，> 32 才是成功
	r, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(v)),
		uintptr(unsafe.Pointer(target)),
		0, 0,
		uintptr(swShowNormal),
	)
	if int32(r) <= 32 {
		return fmt.Errorf("无法打开：%s（ShellExecute 返回 %d）", path, int32(r))
	}
	return nil
}

// ---------------------------------------------------------------- 前台提升

// explorerWindowSet 记下当前已存在的资源管理器窗口，用来识别"新开的那一个"
func explorerWindowSet() map[uintptr]bool {
	res := make(map[uintptr]bool)
	enumWindows(func(hwnd uintptr) bool {
		if className(hwnd) == cabinetClass {
			res[hwnd] = true
		}
		return true
	})
	return res
}

// raiseExplorerWindow 等资源管理器把窗口开出来，然后把它提到前台。
//
// 分两个阶段，因为这里有两个坑：
//  1. explorer 是异步的，窗口可能几十毫秒后才出现；
//  2. explorer 的 DDE 激活是异步落地的，可能在我们置前之后才把焦点抢回去，
//     所以置前不是一次性的，要盯着前台一小段时间。
func raiseExplorerWindow(dir string, before map[uintptr]bool) {
	want := filepath.Base(filepath.Clean(dir))

	// 第一阶段：等窗口出现
	var hwnd uintptr
	for i := 0; i < 40 && hwnd == 0; i++ {
		time.Sleep(80 * time.Millisecond)
		if h := findNewExplorerWindow(before); h != 0 {
			hwnd = h
		} else if i >= 5 {
			// 目录本来就开着时不会开新窗口，退回按标题找唯一的那个
			if h := findExplorerWindowByTitle(want); h != 0 {
				hwnd = h
			}
		}
	}
	if hwnd == 0 {
		return
	}

	// 第二阶段：守住前台。焦点还在资源管理器内部说明是 explorer 自己在抢，
	// 那就继续拉回来；用户自己切到别的程序了就立刻收手，别跟用户抢。
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		fg, _, _ := procGetForegroundWindow.Call()
		if fg == hwnd {
			time.Sleep(120 * time.Millisecond)
			continue
		}
		if className(fg) != cabinetClass {
			return
		}
		focusWindow(hwnd)
		time.Sleep(120 * time.Millisecond)
	}
}

// findNewExplorerWindow 找出这次新出现的资源管理器窗口。
// EnumWindows 本身就是按 Z 序从上到下枚举的，第一个命中的就是最靠前的那个，
// 所以不需要拿标题去猜——用户桌面上可能同时开着十几个同名窗口，标题并不可靠。
func findNewExplorerWindow(before map[uintptr]bool) uintptr {
	var found uintptr
	enumWindows(func(hwnd uintptr) bool {
		if before[hwnd] {
			return true
		}
		if className(hwnd) != cabinetClass {
			return true
		}
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return true
		}
		found = hwnd
		return false // 返回 false 中断枚举，保留 Z 序最靠前的那个
	})
	return found
}

// findExplorerWindowByTitle 复用已有窗口时的兜底：按目录名找。
// 只有全屏范围内"有且仅有一个"同名窗口才认——桌面上同名文件夹一多
// （测试目录、备份目录都很容易撞名），认错窗口比不认更糟。
func findExplorerWindowByTitle(want string) uintptr {
	var found uintptr
	count := 0
	enumWindows(func(hwnd uintptr) bool {
		if className(hwnd) != cabinetClass {
			return true
		}
		if v, _, _ := procIsWindowVisible.Call(hwnd); v == 0 {
			return true
		}
		if !titleMatches(windowText(hwnd), want) {
			return true
		}
		count++
		if count > 1 {
			return false
		}
		found = hwnd
		return true
	})
	if count != 1 {
		return 0
	}
	return found
}

// titleMatches 判断资源管理器窗口标题是不是目标目录。
// 新版 Windows 的标题带后缀，形如 "001 - 文件资源管理器"，
// 老版只有目录名，所以两者都要认；分隔符要求是空白/短横线，
// 免得 "001" 误匹配上 "00123"。
func titleMatches(title, want string) bool {
	if title == "" || want == "" {
		return false
	}
	if strings.EqualFold(title, want) {
		return true
	}
	rest, ok := trimPrefixFold(title, want)
	if !ok {
		return false
	}
	// 短横线类字符是多字节 UTF-8，必须按 rune 判断
	r, _ := utf8.DecodeRuneInString(rest)
	switch r {
	case ' ', '-', '\u2013', '\u2014', ':', '\t':
		return true
	}
	return false
}

// trimPrefixFold 按 UTF-8 字节切前缀（want 本身是完整字符串，边界必然对齐），
// 返回剥掉前缀后的剩余部分
func trimPrefixFold(s, prefix string) (string, bool) {
	if len(s) <= len(prefix) {
		return "", false
	}
	if !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}

// focusWindow 把窗口恢复并置前，返回是否真的成了。
// SetForegroundWindow 有严格的前台锁，后台进程直接调经常无效，
// 所以先用 SwitchToThisWindow（它不受该限制），再逐级兜底。
func focusWindow(hwnd uintptr) bool {
	if v, _, _ := procIsIconic.Call(hwnd); v != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	if fg, _, _ := procGetForegroundWindow.Call(); fg == hwnd {
		return true
	}

	// 主力方案：不受前台锁限制
	procSwitchToThisWindow.Call(hwnd, 1)
	if fg, _, _ := procGetForegroundWindow.Call(); fg == hwnd {
		return true
	}

	// 兜底一：挂到当前前台线程上再 SetForegroundWindow
	if fg, _, _ := procGetForegroundWindow.Call(); fg != hwnd {
		fgThread, _, _ := procGetWindowThreadProcess.Call(fg, 0)
		curThread, _, _ := procGetCurrentThreadID.Call()
		if fgThread != 0 && fgThread != curThread {
			if r, _, _ := procAttachThreadInput.Call(curThread, fgThread, 1); r != 0 {
				procSetForegroundWindow.Call(hwnd)
				procAttachThreadInput.Call(curThread, fgThread, 0)
			}
		}
	}
	if fg, _, _ := procGetForegroundWindow.Call(); fg == hwnd {
		return true
	}

	// 兜底二：模拟一次 Alt 松开，解除部分系统残留的前台锁
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procKeybdEvent.Call(vkMenu, 0, keyEventKeyUp, 0)
	procSetForegroundWindow.Call(hwnd)
	if fg, _, _ := procGetForegroundWindow.Call(); fg == hwnd {
		return true
	}

	// 最后一道：抢不到激活权时，至少把它挪到 Z 序最上层。
	// 这样窗口会盖在其他窗口上面，用户扫一眼就能看到，不至于"像没反应"。
	procSetWindowPos.Call(hwnd, hwndTop, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	return false
}

// ---------------------------------------------------------------- EnumWindows

// EnumWindows 的回调只能有一个，这里全局复用一份
var (
	enumMu     sync.Mutex
	enumTarget func(hwnd uintptr) bool
	enumProc   uintptr
)

func init() {
	enumProc = syscall.NewCallback(enumWindowsTrampoline)
}

// EnumWindows 的回调只能有一个，这里全局复用一份。
// 锁必须持有整个枚举过程：否则两个 goroutine 同时枚举会互相把对方的
// 回调覆盖掉，拿到对方的窗口列表。
func enumWindows(fn func(hwnd uintptr) bool) {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumTarget = fn
	procEnumWindows.Call(enumProc, 0)
	enumTarget = nil
}

func enumWindowsTrampoline(hwnd uintptr, _ uintptr) uintptr {
	fn := enumTarget
	if fn == nil {
		return 1
	}
	if fn(hwnd) {
		return 1
	}
	return 0 // 返回 FALSE 才会真正中断枚举
}

func className(hwnd uintptr) string {
	buf := make([]uint16, 64)
	procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	runtime.KeepAlive(buf)
	return syscall.UTF16ToString(buf)
}

func windowText(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	runtime.KeepAlive(buf)
	return syscall.UTF16ToString(buf)
}
