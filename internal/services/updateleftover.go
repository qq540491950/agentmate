package services

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Updater leftovers.
//
// Replacing a running binary is platform-specific work inside the Wails
// updater, and on Windows it cannot delete the executable at all: the helper
// process that performs the swap is itself running from that file, so the
// kernel refuses even after renaming it aside. The helper therefore renames
// the old binary to "<target>.old.<unixnano>" and its sweep only helps the
// *next* update — every Windows update leaves the previous version's copy
// sitting next to the app in the meantime. A user who keeps the app on the
// desktop sees that copy after every restart; while a still-running instance
// of the old build has it mapped (possible before single-instance locking
// existed), Windows answers their delete with "file is in use".
//
// The app is the only component that runs on every restart, so it is where
// the leftovers get cleaned up. The sweep is conservative: it only touches
// the updater's exact names, requires the installed target to still exist
// (the backup may be the only copy otherwise), and ignores anything newer
// than updateLeftoverMinAge so an update that is still in flight is never
// disturbed. Deletion can fail while the old file is mapped by a process
// that has not exited yet, hence the bounded retry loop.

const (
	// updateLeftoverMinAge protects an update that started moments ago: the
	// helper creates .bak before the swap and the aside during it, so a file
	// younger than this may still be part of a running handoff.
	updateLeftoverMinAge = 30 * time.Second
	// updateLeftoverRetry is how often a still-locked leftover is retried.
	// The helper normally exits within a second of launching the new build,
	// so the first retry almost always succeeds.
	updateLeftoverRetry = 30 * time.Second
	// updateLeftoverWindow bounds the retry loop. A file locked for longer
	// than this is held by another long-running instance; the next startup
	// (or the user, once that instance quits) finishes the job.
	updateLeftoverWindow = 10 * time.Minute
)

// updateAsideEpoch rejects numeric suffixes that cannot be the updater's
// UnixNano stamp (for example a file a user happened to name ".old.1").
var updateAsideEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()

// updateTargetForExe maps the running executable to the path the Wails
// updater replaces. Mirrors pkg/updater's bundleTarget: on macOS the app
// bundle is the replaceable unit, elsewhere it is the executable itself.
func updateTargetForExe(exe, goos string) string {
	if goos == "darwin" {
		parts := strings.Split(filepath.Clean(exe), string(os.PathSeparator))
		for i, p := range parts {
			if strings.HasSuffix(p, ".app") {
				return string(os.PathSeparator) + filepath.Join(parts[1:i+1]...)
			}
		}
	}
	return exe
}

// sweepUpdateLeftovers removes stale updater artifacts sitting next to
// target. It returns the paths it removed and how many leftovers remain
// (because they are still too young or still locked).
//
// Safety rules: never run when the installed target is missing (a leftover
// may be the only surviving copy), and only touch the updater's exact naming
// — "<target>.old.<unixnano>" and "<target>.bak".
func sweepUpdateLeftovers(target string, now time.Time, minAge time.Duration) (removed []string, remaining int) {
	if target == "" {
		return nil, 0
	}
	if _, err := os.Stat(target); err != nil {
		return nil, 0
	}

	// ReadDir rather than Glob: the app can live in a directory whose name
	// contains glob metacharacters ("[", "*", "?"), which filepath.Glob
	// would interpret instead of matching literally.
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

	// The pre-swap backup. A successful update removes it itself; a leftover
	// means that cleanup failed (or the helper is still finishing).
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

// asideTimestamp parses "<base>.old.<unixnano>" and rejects everything that
// is not an update aside: another app's file, a non-numeric suffix, a
// pre-2020 stamp.
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

// sweepUpdateLeftoversLoop retries the sweep until nothing is left, the
// window lapses, or ctx is cancelled. Tests call it with shortened
// durations; production uses the constants above.
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

// startLeftoverSweep begins the background cleanup for this launch. The
// target is derived exactly as the updater derives it, so the names match.
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

// stopSweep ends the background cleanup (app shutdown hook).
func (u *UpdateService) stopSweep() {
	u.mu.Lock()
	cancel := u.sweepCancel
	u.sweepCancel = nil
	u.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
