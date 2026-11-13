package services

import (
	"context"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// 更新器遗留文件。
//
// 替换正在运行的二进制文件是 Wails 更新器内部的平台相关工作，在 Windows
// 上它根本无法删除该可执行文件：执行替换的辅助进程自身正从该文件运行，因此即使
// 把它重命名到一旁，内核也会拒绝。因此辅助进程把旧二进制重命名为
// "<target>.old.<unixnano>"，而它的清理只对*下一次*更新有帮助——在此期间，每次
// Windows 更新都会把上一版本的副本留在应用旁边。把应用放在桌面上的用户每次重启后都会看
// 到那份副本；而当旧构建的某个仍在运行的实例映射着该文件时（在单实例锁定机制存
// 在之前这是可能的），Windows 会用 "file is in use" 回应他们的删除操作。
//
// 应用是唯一在每次重启时都会运行的组件，因此清理遗留文件的工作就放在这里。清理
// 是保守的：仅触碰更新器的精确命名，要求已安装的目标仍然存在（否则备份可能是唯
// 一的副本），并忽略比 updateLeftoverMinAge
// 更新的任何文件，从而绝不打扰仍在进行中的更新。当旧文件仍被尚未退出的进程映射
// 时，删除可能失败，因此需要带上限的重试循环。

const (
	// updateLeftoverMinAge 保护刚刚启动的更新：辅助进程在替换前创建
	// .bak、在替换过程中创建旁置文件，因此比该值更年轻的文件仍可能是正在进行的交接
	// 的一部分。
	updateLeftoverMinAge = 30 * time.Second
	// updateLeftoverRetry 是仍处于锁定状态的遗留文件的重试频率。
	// 辅助程序通常会在启动新构建后一秒内退出， 因此第一次重试几乎总是成功。
	updateLeftoverRetry = 30 * time.Second
	// updateLeftoverWindow 限定重试循环的上限。被锁定时间超过该值的文件由另一个长期运行的实例持有；下一
	// 次启动（或该实例退出后由用户）会完成这项工作。
	updateLeftoverWindow = 10 * time.Minute
)

// updateAsideEpoch 拒绝那些不可能是更新器 UnixNano
// 时间戳的数字后缀（例如用户碰巧命名为 ".old.1" 的文件）。
var updateAsideEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()

// updateTargetForExe 把正在运行的可执行文件映射为 Wails 更新器所替换的路径。与
// pkg/updater 的 bundleTarget 保持一致：在 macOS 上可替换单元是应用包（app
// bundle），其他地方则是可执行文件本身。
//
// 结果必须只取决于 goos，而不取决于运行代码的主机：macOS 上的 os.Executable
// 始终返回绝对 POSIX 路径，因此即使测试（或未来的跨平台工具）在 Windows
// 上执行，基于斜杠的 path 包也是正确的解析器。filepath
// 会在那里把输入改写成反斜杠，从而产生不可用的目标。
func updateTargetForExe(exe, goos string) string {
	if goos != "darwin" {
		return exe
	}
	clean := path.Clean(exe)
	if !strings.HasPrefix(clean, "/") {
		return exe
	}
	parts := strings.Split(clean, "/")
	for i, p := range parts {
		if strings.HasSuffix(p, ".app") {
			return "/" + path.Join(parts[1:i+1]...)
		}
	}
	return exe
}

// sweepUpdateLeftovers 删除紧邻 target
// 的过期更新器产物。它返回已删除的路径以及仍剩余多少遗留文件（因为它们还太年轻
// 或仍被锁定）。
//
// 安全规则：当已安装的目标缺失时绝不运行（否则遗留文件可能是唯一幸存的副本），
// 并且仅触碰更新器的精确命名——"<target>.old.<unixnano>" 和 "<target>.bak"。
func sweepUpdateLeftovers(target string, now time.Time, minAge time.Duration) (removed []string, remaining int) {
	if target == "" {
		return nil, 0
	}
	if _, err := os.Stat(target); err != nil {
		return nil, 0
	}

	// 使用 ReadDir 而非 Glob：应用可能位于名称包含 glob
	// 元字符（"["、"*"、"?"）的目录中，filepath.Glob
	// 会把这些字符当作通配符解释，而不是按字面匹配。
	dir := filepath.Dir(target)
	base := filepath.Base(target)
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ts, ok := asideTimestamp(e.Name(), base)
			if !ok {
				continue
			}
			if now.Sub(time.Unix(0, ts)) < minAge {
				remaining++
				continue
			}
			path := filepath.Join(dir, e.Name())
			if err := os.Remove(path); err != nil {
				remaining++
				continue
			}
			removed = append(removed, path)
		}
	}

	// 交换前备份。更新成功后会自行将其删除；若仍有遗留，
	// 则意味着清理失败（或辅助程序仍在收尾）。
	backup := target + ".bak"
	if st, err := os.Lstat(backup); err == nil {
		if now.Sub(st.ModTime()) < minAge {
			remaining++
		} else if err := os.RemoveAll(backup); err != nil {
			remaining++
		} else {
			removed = append(removed, backup)
		}
	}

	return removed, remaining
}

// asideTimestamp 解析
// "<base>.old.<unixnano>"，并拒绝所有不属于更新旁置文件的输入：其他应用的文件、
// 非数字后缀、2020 年之前的时间戳。
func asideTimestamp(name, base string) (int64, bool) {
	suffix, ok := strings.CutPrefix(name, base+".old.")
	if !ok || suffix == "" {
		return 0, false
	}
	ts, err := strconv.ParseInt(suffix, 10, 64)
	if err != nil || ts < updateAsideEpoch {
		return 0, false
	}
	return ts, true
}

// sweepUpdateLeftoversLoop 重试清理，直到没有剩余项、 时间窗口到期或 ctx
// 被取消。测试会用缩短的 时长调用它；生产环境使用上述常量。
func sweepUpdateLeftoversLoop(ctx context.Context, target string, interval, minAge, window time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("清理更新残留异常: %v", r)
		}
	}()
	deadline := time.Now().Add(window)
	for {
		removed, remaining := sweepUpdateLeftovers(target, time.Now(), minAge)
		for _, path := range removed {
			log.Printf("已清理更新残留文件: %s", path)
		}
		if remaining == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			log.Printf("更新残留文件仍被占用（%d 个），下次启动时继续清理", remaining)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// startLeftoverSweep 启动本次运行的后台清理。
// 目标完全按照更新器的推导方式得出，因此名称一致。
func (u *UpdateService) startLeftoverSweep() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	target := updateTargetForExe(exe, runtime.GOOS)

	ctx, cancel := context.WithCancel(context.Background())
	u.mu.Lock()
	prev := u.sweepCancel
	u.sweepCancel = cancel
	u.mu.Unlock()
	if prev != nil {
		prev()
	}
	go sweepUpdateLeftoversLoop(ctx, target, updateLeftoverRetry, updateLeftoverMinAge, updateLeftoverWindow)
}

// stopSweep 结束后台清理（应用关闭钩子）。
func (u *UpdateService) stopSweep() {
	u.mu.Lock()
	cancel := u.sweepCancel
	u.sweepCancel = nil
	u.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
