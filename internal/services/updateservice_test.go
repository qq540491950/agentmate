package services

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentmate/internal/config"
)

func TestDetectUpdateMode(t *testing.T) {
	cases := []struct {
		name           string
		goos           string
		appImage       string
		exeDirWritable bool
		wantMode       string
		wantReasonHas  string
	}{
		{
			name: "windows user-scope install can self-update", goos: "windows", exeDirWritable: true,
			wantMode: UpdateModeAutoInstall,
		},
		{
			// 机器范围的 NSIS 安装会落到
			// %ProgramFiles%，而更新器没有提权路径，因此替换目标不可达。
			name: "windows program files cannot self-update", goos: "windows", exeDirWritable: false,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "Program Files",
		},
		{
			name: "darwin writable bundle", goos: "darwin", exeDirWritable: true,
			wantMode: UpdateModeAutoInstall,
		},
		{
			name: "darwin read-only location", goos: "darwin", exeDirWritable: false,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "不可写",
		},
		{
			// deb/rpm 安装会把二进制文件放到 /usr/local/bin（归 root 所有）。
			name: "linux deb is notify-only", goos: "linux", appImage: "", exeDirWritable: true,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "deb/rpm",
		},
		{
			name: "linux appimage can self-update", goos: "linux", appImage: "/home/u/Apps/agentmate.AppImage", exeDirWritable: true,
			wantMode: UpdateModeAutoInstall,
		},
		{
			name: "linux appimage on read-only mount", goos: "linux", appImage: "/opt/ro/agentmate.AppImage", exeDirWritable: false,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "不可写",
		},
		{
			name: "unknown platform", goos: "freebsd", exeDirWritable: true,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "不支持",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, reason := detectUpdateMode(tc.goos, tc.appImage, tc.exeDirWritable)
			if mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q (reason %q)", mode, tc.wantMode, reason)
			}
			if tc.wantReasonHas != "" && !strings.Contains(reason, tc.wantReasonHas) {
				t.Fatalf("reason = %q, want it to mention %q", reason, tc.wantReasonHas)
			}
			if tc.wantReasonHas == "" && reason != "" {
				t.Fatalf("reason = %q, want empty", reason)
			}
		})
	}
}

func TestParseUpdatePublicKey(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	if _, err := parseUpdatePublicKey(""); err != nil {
		t.Fatalf("empty key must be accepted as 'no signature check': %v", err)
	}
	got, err := parseUpdatePublicKey(base64.StdEncoding.EncodeToString(raw))
	if err != nil || string(got) != string(raw) {
		t.Fatalf("base64 key = %x, %v", got, err)
	}
	got, err = parseUpdatePublicKey(hex.EncodeToString(raw))
	if err != nil || string(got) != string(raw) {
		t.Fatalf("hex key = %x, %v", got, err)
	}
	// 粘贴时带有一个换行，因为终端会折行较长的 base64。
	wrapped := base64.StdEncoding.EncodeToString(raw)
	wrapped = wrapped[:20] + "\n" + wrapped[20:]
	if got, err = parseUpdatePublicKey(wrapped); err != nil || string(got) != string(raw) {
		t.Fatalf("wrapped key = %x, %v", got, err)
	}
	if _, err = parseUpdatePublicKey("not-a-key"); err == nil {
		t.Fatal("expected an error for a non-key string")
	}
	if _, err = parseUpdatePublicKey(base64.StdEncoding.EncodeToString(raw[:16])); err == nil {
		t.Fatal("expected an error for a 16-byte key")
	}
}

// TestUpdateStatusBeforeBind Status 必须在 Bind
// 之前可渲染：设置页会在挂载时调用它，且更新器初始化失败不得变成空白页面或发生
// panic 的页面。
func TestUpdateStatusBeforeBind(t *testing.T) {
	t.Cleanup(func() { AppVersion = "dev" })
	AppVersion = "1.2.3"
	u := NewUpdateService(nil)
	st := u.Status()
	if st.CurrentVersion != "1.2.3" {
		t.Fatalf("currentVersion = %q", st.CurrentVersion)
	}
	if st.State != "unconfigured" {
		t.Fatalf("state = %q, want unconfigured", st.State)
	}
	if st.DownloadURL == "" {
		t.Fatal("downloadUrl must always be set so notify-only can fall back to it")
	}
	if st.CanAutoInstall != (st.Mode == UpdateModeAutoInstall) {
		t.Fatalf("canAutoInstall = %v with mode %q", st.CanAutoInstall, st.Mode)
	}
}

// TestSkipAndAutoCheckPersist Skip 和 SetAutoCheck 会持久化到
// config.json；两者都必须在重新加载后保留，且不得干扰无关设置。
func TestSkipAndAutoCheckPersist(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := store.Get().Settings.WorkspaceDir
	u := NewUpdateService(&Services{Store: store})

	if err := u.Skip("v0.6.0"); err != nil {
		t.Fatal(err)
	}
	if got := store.Get().Settings.SkippedVersion; got != "0.6.0" {
		t.Fatalf("skippedVersion = %q, want the v-prefix stripped", got)
	}
	if err := u.SetAutoCheck(false); err != nil {
		t.Fatal(err)
	}
	s := store.Get().Settings
	if s.UpdateAutoCheckEnabled() {
		t.Fatal("autoCheck must be off after SetAutoCheck(false)")
	}
	if s.WorkspaceDir != before {
		t.Fatalf("workspaceDir changed from %q to %q", before, s.WorkspaceDir)
	}
	if err := u.SetAutoCheck(true); err != nil {
		t.Fatal(err)
	}
	if !store.Get().Settings.UpdateAutoCheckEnabled() {
		t.Fatal("autoCheck must be on after SetAutoCheck(true)")
	}
	// 重新打开存储（也就是重启）时必须看到相同的值。
	reopened, err := config.Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Settings().SkippedVersion; got != "0.6.0" {
		t.Fatalf("skippedVersion after reload = %q", got)
	}
}

func TestSkipRejectsEmptyVersion(t *testing.T) {
	u := NewUpdateService(nil)
	if err := u.Skip("  "); err == nil {
		t.Fatal("expected an error for an empty version")
	}
}

// TestHelperAwaitingExit 校验 helperAwaitingExit：它决定强制退出是否安全，
// 因此必须只读取「此」进程的握手，并且只接受 "ready" 载荷。
func TestHelperAwaitingExit(t *testing.T) {
	const pid = 4242
	write := func(t *testing.T, dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("no readiness file", func(t *testing.T) {
		if helperAwaitingExit(t.TempDir(), pid) {
			t.Fatal("a missing handshake must not count as a helper standing by")
		}
	})
	t.Run("helper reported an error", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, fmt.Sprintf("wails-update-%d-1.ready", pid), "error: staged artifact is unavailable")
		if helperAwaitingExit(dir, pid) {
			t.Fatal("a helper that gave up must not trigger an exit")
		}
	})
	t.Run("helper is standing by", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, fmt.Sprintf("wails-update-%d-1.ready", pid), "ready\n")
		if !helperAwaitingExit(dir, pid) {
			t.Fatal("ready handshake not detected")
		}
	})
	t.Run("another process's handshake", func(t *testing.T) {
		dir := t.TempDir()
		write(t, dir, fmt.Sprintf("wails-update-%d-1.ready", pid+1), "ready")
		if helperAwaitingExit(dir, pid) {
			t.Fatal("another process's handshake must not match")
		}
	})
}

// TestApplyWithWatchdogRestartResult 返回的重启在其仍可能失败时，必须绝不强制退出：
// 成功会启用重启后的宽限退出，错误则让应用保持运行。
func TestApplyWithWatchdogRestartResult(t *testing.T) {
	t.Run("success arms the grace exit", func(t *testing.T) {
		forced := make(chan string, 4)
		err := applyWithWatchdog(
			func() error { return nil },
			func() bool { return false },
			50*time.Millisecond, 5*time.Millisecond, 20*time.Millisecond,
			func(reason string) { forced <- reason },
		)
		if err != nil {
			t.Fatalf("successful restart = %v", err)
		}
		select {
		case <-forced:
		case <-time.After(2 * time.Second):
			t.Fatal("a successful restart must arm the post-restart forced exit")
		}
	})

	t.Run("error keeps the app running", func(t *testing.T) {
		wantErr := errors.New("updater: helper did not signal readiness")
		var forced atomic.Bool
		err := applyWithWatchdog(
			func() error { return wantErr },
			func() bool { return true }, // 即使辅助进程已就绪，也不得在失败时退出
			20*time.Millisecond, 5*time.Millisecond, 20*time.Millisecond,
			func(string) { forced.Store(true) },
		)
		if !errors.Is(err, wantErr) {
			t.Fatalf("err = %v, want %v", err, wantErr)
		}
		time.Sleep(100 * time.Millisecond)
		if forced.Load() {
			t.Fatal("a failed restart must leave the app running")
		}
	})
}

// TestApplyWithWatchdogExitsWhenHelperStandsBy 所报告的缺陷：旧进程在关闭时挂起，导致辅助进程对父进程的等待超时，更新退化成
// 启动第二个应用。一旦辅助进程确认它正在等待此 PID，ApplyUpdate
// 必须退出，而不是继续等待卡死的退出流程。
func TestApplyWithWatchdogExitsWhenHelperStandsBy(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	forced := make(chan string, 1)
	readyAfter := time.Now().Add(50 * time.Millisecond)

	err := applyWithWatchdog(
		func() error { <-block; return nil }, // 应用的 Quit 绝不返回
		func() bool { return time.Now().After(readyAfter) },
		10*time.Millisecond, 5*time.Millisecond, 0,
		func(reason string) { forced <- reason },
	)
	if err != nil {
		t.Fatalf("applyWithWatchdog = %v", err)
	}
	select {
	case reason := <-forced:
		if reason == "" {
			t.Fatal("forced exit must carry a reason for the log")
		}
	default:
		t.Fatal("a standing-by helper must trigger the forced exit")
	}
}

// TestApplyWithWatchdogSlowHelperFailure 辅助进程启动缓慢并不等同于退出卡死：如果它绝不发出就绪信号，且 Restart
// 最终失败，则会返回该错误，并且不会强制执行任何操作。
func TestApplyWithWatchdogSlowHelperFailure(t *testing.T) {
	wantErr := errors.New("updater: helper did not signal readiness")
	var forced atomic.Bool
	err := applyWithWatchdog(
		func() error { time.Sleep(60 * time.Millisecond); return wantErr },
		func() bool { return false },
		10*time.Millisecond, 5*time.Millisecond, 10*time.Millisecond,
		func(string) { forced.Store(true) },
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want %v", err, wantErr)
	}
	if forced.Load() {
		t.Fatal("a helper that never became ready must not force an exit")
	}
}
