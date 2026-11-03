package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The updater replaces the bundle on macOS and the binary elsewhere; the
// sweep must look where it actually leaves files.
func TestUpdateTargetForExe(t *testing.T) {
	cases := []struct{ name, exe, goos, want string }{
		{"windows desktop exe", `C:\Users\u\Desktop\WorkBuddy-Agent-windows-amd64.exe`, "windows", `C:\Users\u\Desktop\WorkBuddy-Agent-windows-amd64.exe`},
		{"linux appimage", "/home/u/Apps/workbuddy.AppImage", "linux", "/home/u/Apps/workbuddy.AppImage"},
		{"darwin bundle", "/Users/u/Applications/WorkBuddy Agent.app/Contents/MacOS/workbuddy", "darwin", "/Users/u/Applications/WorkBuddy Agent.app"},
		{"darwin plain binary", "/usr/local/bin/workbuddy", "darwin", "/usr/local/bin/workbuddy"},
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

// The reported defect: after an update the previous version's copy is left
// next to the app. Once it is no longer held, the running build must delete
// it so it does not stay on the user's desktop forever.
func TestSweepUpdateLeftovers(t *testing.T) {
	now := time.Now()

	t.Run("removes stale aside and keeps fresh or foreign files", func(t *testing.T) {
		dir := t.TempDir()
		target := writeTarget(t, dir, "WorkBuddy.exe")
		stale := filepath.Join(dir, asideName("WorkBuddy.exe", now.Add(-time.Hour)))
		fresh := filepath.Join(dir, asideName("WorkBuddy.exe", now.Add(-time.Second)))
		foreign := []string{
			filepath.Join(dir, "WorkBuddy.exe.old.123"),        // pre-2020 stamp
			filepath.Join(dir, "WorkBuddy.exe.old.notanumber"), // not ours
			filepath.Join(dir, "Other.exe.old."+fmt.Sprint(now.UnixNano())),
			filepath.Join(dir, "WorkBuddy.exe.old"),
			filepath.Join(dir, "WorkBuddy.exe.old2."),
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
		target := writeTarget(t, dir, "WorkBuddy.exe")
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
		target := filepath.Join(dir, "WorkBuddy Agent.app")
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

	// A missing target means the swap is broken and the leftover may be the
	// only surviving copy: never delete in that state.
	t.Run("keeps everything when the installed target is missing", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "WorkBuddy.exe")
		aside := filepath.Join(dir, asideName("WorkBuddy.exe", now.Add(-time.Hour)))
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

	// The app may sit in a folder whose name contains glob syntax; matching
	// must be literal, not a pattern.
	t.Run("directory with glob metacharacters", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "apps[1]")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		target := writeTarget(t, dir, "WorkBuddy.exe")
		stale := filepath.Join(dir, asideName("WorkBuddy.exe", now.Add(-time.Hour)))
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

// A leftover that is still too young (the helper may still be handing off)
// must be retried: the new build normally starts within a second of the swap,
// so the next attempt is what actually removes it.
func TestSweepUpdateLeftoversLoopRetriesUntilUnlocked(t *testing.T) {
	dir := t.TempDir()
	target := writeTarget(t, dir, "WorkBuddy.exe")
	fresh := filepath.Join(dir, asideName("WorkBuddy.exe", time.Now()))
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
	target := writeTarget(t, dir, "WorkBuddy.exe")

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
