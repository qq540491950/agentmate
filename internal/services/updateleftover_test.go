package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestUpdateTargetForExe 更新器在 macOS 上替换
// bundle，在其他平台上替换二进制文件；清理必须查找它实际留下文件的位置。
func TestUpdateTargetForExe(t *testing.T) {
	cases := []struct{ name, exe, goos, want string }{
		{"windows desktop exe", `C:\Users\u\Desktop\AgentMate-Agent-windows-amd64.exe`, "windows", `C:\Users\u\Desktop\AgentMate-Agent-windows-amd64.exe`},
		{"linux appimage", "/home/u/Apps/AgentMate.AppImage", "linux", "/home/u/Apps/AgentMate.AppImage"},
		{"darwin bundle", "/Users/u/Applications/AgentMate Agent.app/Contents/MacOS/AgentMate", "darwin", "/Users/u/Applications/AgentMate Agent.app"},
		{"darwin plain binary", "/usr/local/bin/AgentMate", "darwin", "/usr/local/bin/AgentMate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := updateTargetForExe(tc.exe, tc.goos); got != tc.want {
				t.Fatalf("target = %q, want %q", got, tc.want)
			}
		})
	}
}

func writeTarget(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func asideName(base string, ts time.Time) string {
	return fmt.Sprintf("%s.old.%d", base, ts.UnixNano())
}

// TestSweepUpdateLeftovers 报告的缺陷：更新后，上一版本的副本被留在应用旁边。一旦它不再被占用，正在运行
// 的构建必须将其删除，以免它永远留在用户桌面上。
func TestSweepUpdateLeftovers(t *testing.T) {
	now := time.Now()

	t.Run("removes stale aside and keeps fresh or foreign files", func(t *testing.T) {
		dir := t.TempDir()
		target := writeTarget(t, dir, "AgentMate.exe")
		stale := filepath.Join(dir, asideName("AgentMate.exe", now.Add(-time.Hour)))
		fresh := filepath.Join(dir, asideName("AgentMate.exe", now.Add(-time.Second)))
		foreign := []string{
			filepath.Join(dir, "AgentMate.exe.old.123"),        // 2020 年之前的时间戳
			filepath.Join(dir, "AgentMate.exe.old.notanumber"), // 不是我们的
			filepath.Join(dir, "Other.exe.old."+fmt.Sprint(now.UnixNano())),
			filepath.Join(dir, "AgentMate.exe.old"),
			filepath.Join(dir, "AgentMate.exe.old2."),
		}
		for _, p := range []string{stale, fresh} {
			if err := os.WriteFile(p, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range foreign {
			if err := os.WriteFile(p, []byte("keep"), 0o755); err != nil {
				t.Fatal(err)
			}
		}

		removed, remaining := sweepUpdateLeftovers(target, now, 30*time.Second)
		if len(removed) != 1 || removed[0] != stale {
			t.Fatalf("removed = %v, want [%s]", removed, stale)
		}
		if remaining != 1 {
			t.Fatalf("remaining = %d, want the fresh aside to survive until it ages", remaining)
		}
		if _, err := os.Stat(stale); !os.IsNotExist(err) {
			t.Fatalf("stale aside still present: %v", err)
		}
		for _, p := range append([]string{fresh, target}, foreign...) {
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("%s must be kept: %v", p, err)
			}
		}
	})

	t.Run("removes stale backup file", func(t *testing.T) {
		dir := t.TempDir()
		target := writeTarget(t, dir, "AgentMate.exe")
		backup := target + ".bak"
		if err := os.WriteFile(backup, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-time.Hour)
		if err := os.Chtimes(backup, old, old); err != nil {
			t.Fatal(err)
		}

		removed, remaining := sweepUpdateLeftovers(target, now, 30*time.Second)
		if remaining != 0 || len(removed) != 1 || removed[0] != backup {
			t.Fatalf("removed = %v, remaining = %d", removed, remaining)
		}
		if _, err := os.Stat(backup); !os.IsNotExist(err) {
			t.Fatalf("stale backup still present: %v", err)
		}
	})

	t.Run("removes stale bundle backup directory", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "AgentMate Agent.app")
		if err := os.MkdirAll(imageInsideBundle(t, target), 0o755); err != nil {
			t.Fatal(err)
		}
		backup := target + ".bak"
		if err := os.MkdirAll(backup, 0o755); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-time.Hour)
		if err := os.Chtimes(backup, old, old); err != nil {
			t.Fatal(err)
		}

		removed, remaining := sweepUpdateLeftovers(target, now, 30*time.Second)
		if remaining != 0 || len(removed) != 1 {
			t.Fatalf("removed = %v, remaining = %d", removed, remaining)
		}
		if _, err := os.Stat(backup); !os.IsNotExist(err) {
			t.Fatalf("stale bundle backup still present: %v", err)
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("installed bundle must be kept: %v", err)
		}
	})

	// 目标缺失意味着交换已损坏，遗留文件可能是唯一幸存的副本：在该状态下绝不删除。
	t.Run("keeps everything when the installed target is missing", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "AgentMate.exe")
		aside := filepath.Join(dir, asideName("AgentMate.exe", now.Add(-time.Hour)))
		backup := target + ".bak"
		for _, p := range []string{aside, backup} {
			if err := os.WriteFile(p, []byte("old"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		removed, remaining := sweepUpdateLeftovers(target, now, 30*time.Second)
		if len(removed) != 0 || remaining != 0 {
			t.Fatalf("removed = %v, remaining = %d; must not touch a missing install", removed, remaining)
		}
		if _, err := os.Stat(aside); err != nil {
			t.Fatalf("aside must be kept: %v", err)
		}
		if _, err := os.Stat(backup); err != nil {
			t.Fatalf("backup must be kept: %v", err)
		}
	})

	// 应用可能位于名称包含 glob 语法的文件夹中；匹配必须按字面进行，而不是按模式。
	t.Run("directory with glob metacharacters", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "apps[1]")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		target := writeTarget(t, dir, "AgentMate.exe")
		stale := filepath.Join(dir, asideName("AgentMate.exe", now.Add(-time.Hour)))
		if err := os.WriteFile(stale, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		removed, remaining := sweepUpdateLeftovers(target, now, 30*time.Second)
		if remaining != 0 || len(removed) != 1 || removed[0] != stale {
			t.Fatalf("removed = %v, remaining = %d", removed, remaining)
		}
	})
}

func imageInsideBundle(t *testing.T, bundle string) string {
	t.Helper()
	return filepath.Join(bundle, "Contents", "MacOS")
}

// TestSweepUpdateLeftoversLoopRetriesUntilUnlocked 仍然太新的遗留文件（辅助程序可能仍在交接）必须重试：新构建通常会在交换后一秒
// 内启动，因此下一次尝试才会真正将其删除。
func TestSweepUpdateLeftoversLoopRetriesUntilUnlocked(t *testing.T) {
	dir := t.TempDir()
	target := writeTarget(t, dir, "AgentMate.exe")
	fresh := filepath.Join(dir, asideName("AgentMate.exe", time.Now()))
	if err := os.WriteFile(fresh, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sweepUpdateLeftoversLoop(ctx, target, 10*time.Millisecond, 20*time.Millisecond, 2*time.Second)
	if ctx.Err() != nil {
		t.Fatal("the sweep did not finish before the test timeout")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("fresh leftover must be removed once it ages: %v", err)
	}
}

func TestSweepUpdateLeftoversLoopExitsWhenClean(t *testing.T) {
	dir := t.TempDir()
	target := writeTarget(t, dir, "AgentMate.exe")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	sweepUpdateLeftoversLoop(ctx, target, time.Hour, 30*time.Second, time.Hour)
	if ctx.Err() != nil {
		t.Fatal("an empty directory must end the loop immediately")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("loop waited %s with nothing to clean", elapsed)
	}
}
