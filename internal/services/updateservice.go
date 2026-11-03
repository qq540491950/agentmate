package services

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"

	"go-workbuddy/internal/config"
)

// Update channel wiring.
//
// Releases and the signed update manifest both live on GitHub Releases, so
// there is exactly one distribution channel to trust and one place to yank a
// bad release. `releases/latest/download/...` is GitHub's stable redirect to
// the newest published (non-draft, non-prerelease) release: publishing a
// hotfix moves the pointer, and turning a bad release back into a draft moves
// it back.
const (
	updateRepo        = "qq540491950/go-workbuddy"
	updateManifestURL = "https://github.com/" + updateRepo + "/releases/latest/download/manifest.json"
	updateReleasesURL = "https://github.com/" + updateRepo + "/releases"

	// firstCheckDelay keeps the startup check off the critical path: the
	// window is up and usable before anything touches the network.
	firstCheckDelay = 10 * time.Second
	// checkInterval is deliberately slow. Every check can become a prompt to
	// update, and a fast-iterating project that checked hourly would nag its
	// users daily.
	checkInterval = 24 * time.Hour
	// checkTimeout bounds one whole check (manifest fetch + redirects).
	checkTimeout = 90 * time.Second

	// Restart watchdog timings. The framework's helper waits 30 s for this
	// process to exit before it swaps the binary and relaunches; when that
	// wait lapses it gives up on the swap and relaunches the OLD binary
	// instead (exit code 17 in pkg/updater/helper.go). That is how an update
	// ends with the old window still on screen next to a freshly started app.
	// ApplyUpdate therefore does not trust the graceful shutdown alone:
	//
	//   - applyWatchdog bounds how long we wait for Restart to settle. Past
	//     it, the helper's readiness file decides: once it says "ready" the
	//     helper is blocking on this PID, so exiting now completes the swap.
	//   - applyExitGrace covers the same risk after Restart reported success
	//     (the helper is ready by definition then): a process still alive
	//     when the grace lapses exits hard.
	applyWatchdog  = 12 * time.Second
	applyPoll      = 250 * time.Millisecond
	applyExitGrace = 5 * time.Second
)

// updatePublicKeyB64 is the trust root for update signatures: the base64 of
// the raw 32-byte ed25519 public key that `wails3 updater genkey` prints.
//
// Configuring it makes signatures mandatory (fail closed): a release whose
// manifest carries no signature is rejected instead of being downgraded to a
// digest-only install. Generate the keypair once with
// `wails3 updater genkey -out ~/.wails/updater.key`, paste its base64 here,
// commit this file (a public key is public) and keep the private key in the
// CI secret store plus one offline backup — losing it means every shipped
// build can never verify another update.
var updatePublicKeyB64 = ""

// UpdateMode tells the settings page what "安装更新" can actually do here.
const (
	// UpdateModeAutoInstall: the running copy sits in a directory this user
	// can write, so the updater can stage and swap it.
	UpdateModeAutoInstall = "auto-install"
	// UpdateModeNotifyOnly: tell the user a new version exists and send them
	// to the release page. Used wherever the swap cannot work — see
	// detectUpdateMode.
	UpdateModeNotifyOnly = "notify-only"
)

// UpdateStatus is the snapshot the settings page renders.
type UpdateStatus struct {
	CurrentVersion   string `json:"currentVersion"`
	State            string `json:"state"`
	Mode             string `json:"mode"`
	Reason           string `json:"reason,omitempty"`
	AvailableVersion string `json:"availableVersion,omitempty"`
	Notes            string `json:"notes,omitempty"`
	AutoCheck        bool   `json:"autoCheck"`
	SkippedVersion   string `json:"skippedVersion,omitempty"`
	Error            string `json:"error,omitempty"`
	DownloadURL      string `json:"downloadUrl"`
	CanAutoInstall   bool   `json:"canAutoInstall"`
}

// UpdateService drives the Wails updater from the settings page.
//
// It owns the three things the framework deliberately leaves to the app: when
// to check (silently, at most daily, never auto-installing), what the UI sees
// (UpdateStatus), and the "skip this version" preference, which lives in
// config.json so it survives restarts.
type UpdateService struct {
	S   *Services
	app *application.App

	mu         sync.Mutex
	release    *updater.Release
	lastErr    string
	mode       string
	reason     string
	autoCancel context.CancelFunc
}

// NewUpdateService creates the service. Bind must run after application.New.
func NewUpdateService(s *Services) *UpdateService {
	mode, reason := detectUpdateMode(runtime.GOOS, os.Getenv("APPIMAGE"), executableDirWritable())
	return &UpdateService{S: s, mode: mode, reason: reason}
}

// BindUpdates configures the updater on an already-constructed application.
//
// This is a package-level function rather than a method on UpdateService
// because every exported method of a registered service becomes an RPC the
// webview can call, and "configure yourself with this *application.App" is not
// something page JavaScript should be able to invoke.
func BindUpdates(u *UpdateService, app *application.App) error { return u.attach(app) }

// StopUpdates ends the background update poll (app shutdown hook).
func StopUpdates(u *UpdateService) { u.stop() }

// attach wires the service to the running application and configures the
// updater. The caller decides whether a configuration error is fatal; updates
// are not worth blocking startup over.
func (u *UpdateService) attach(app *application.App) error {
	if app == nil {
		return errors.New("update: nil app")
	}
	u.app = app
	if app.Updater == nil {
		return errors.New("update: 当前 Wails 运行时没有 updater")
	}

	provider, err := endpoint.New(endpoint.Config{
		URL: updateManifestURL,
		// Share http.DefaultTransport so the global proxy setting
		// (agentkit.ApplyGlobalProxy) also applies to update traffic; a
		// private client would leave proxied users permanently "up to date".
		HTTPClient: &http.Client{Timeout: checkTimeout, Transport: http.DefaultTransport},
	})
	if err != nil {
		return err
	}

	key, err := parseUpdatePublicKey(updatePublicKeyB64)
	if err != nil {
		return err
	}
	if len(key) == 0 {
		log.Printf("更新签名未启用（updatePublicKeyB64 为空）：只校验清单内的摘要，无法发现被投毒的发布渠道")
	}

	// WindowNone: the update UI lives in the settings page, so the framework's
	// standalone window never opens and the two can never disagree.
	//
	// CheckInterval is left unset on purpose: the framework's periodic loop
	// calls CheckAndInstall, i.e. it downloads and stages an update the user
	// never asked for. We schedule Check-only rounds ourselves in
	// autoCheckLoop.
	if err := app.Updater.Init(updater.Config{
		CurrentVersion: AppVersion,
		Providers:      []updater.Provider{provider},
		PublicKey:      key,
		Window:         updater.WindowNone,
	}); err != nil {
		return err
	}
	if skipped := u.skippedVersion(); skipped != "" {
		app.Updater.SkipVersion(skipped)
	}
	u.startAutoCheck()
	return nil
}

// stop ends the background check loop. Called from the app shutdown hook.
func (u *UpdateService) stop() {
	u.mu.Lock()
	cancel := u.autoCancel
	u.autoCancel = nil
	u.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Status returns the current update state for the settings page.
func (u *UpdateService) Status() UpdateStatus {
	u.mu.Lock()
	rel := u.release
	lastErr := u.lastErr
	mode, reason := u.mode, u.reason
	u.mu.Unlock()

	st := UpdateStatus{
		CurrentVersion: AppVersion,
		State:          string(updater.StateUnconfigured),
		Mode:           mode,
		Reason:         reason,
		DownloadURL:    updateReleasesURL,
		CanAutoInstall: mode == UpdateModeAutoInstall,
	}
	if u.S != nil && u.S.Store != nil {
		s := u.S.Store.Settings()
		st.AutoCheck = s.UpdateAutoCheckEnabled()
		st.SkippedVersion = strings.TrimPrefix(s.SkippedVersion, "v")
	}
	if u.app != nil && u.app.Updater != nil {
		st.State = string(u.app.Updater.State())
		if v := u.app.Updater.SkippedVersion(); v != "" && st.SkippedVersion == "" {
			st.SkippedVersion = strings.TrimPrefix(v, "v")
		}
	}
	if rel != nil {
		st.AvailableVersion = strings.TrimPrefix(rel.Version, "v")
		st.Notes = rel.Notes
	}
	st.Error = lastErr
	return st
}

// CheckNow asks the release feed for a newer version. Used by the manual
// button and by the silent loop (whose errors are logged, not surfaced).
// A nil error with State=="up-to-date" means nothing newer exists.
func (u *UpdateService) CheckNow() (UpdateStatus, error) {
	if u.app == nil || u.app.Updater == nil {
		return u.Status(), errors.New("更新服务未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	rel, err := u.app.Updater.Check(ctx)
	u.mu.Lock()
	if err != nil {
		u.lastErr = err.Error()
		if rel == nil {
			u.release = nil
		}
	} else {
		u.lastErr = ""
		u.release = rel
	}
	u.mu.Unlock()
	return u.Status(), err
}

// InstallUpdate downloads, verifies and stages the pending release. It does
// not restart: the user finishes what they are doing, then presses
// 「重启完成更新」.
func (u *UpdateService) InstallUpdate() (UpdateStatus, error) {
	if u.app == nil || u.app.Updater == nil {
		return u.Status(), errors.New("更新服务未初始化")
	}
	u.mu.Lock()
	mode, reason := u.mode, u.reason
	u.mu.Unlock()
	if mode != UpdateModeAutoInstall {
		return u.Status(), fmt.Errorf("当前安装方式不支持自动替换：%s", reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	err := u.app.Updater.DownloadAndInstall(ctx)
	u.mu.Lock()
	if err != nil {
		u.lastErr = err.Error()
	} else {
		u.lastErr = ""
	}
	u.mu.Unlock()
	return u.Status(), err
}

// ApplyUpdate hands the staged payload to the updater helper and quits; the
// helper swaps the binary/bundle and relaunches the new version.
//
// Restart blocks on this process's graceful shutdown, so it runs on its own
// goroutine under a watchdog: see the applyWatchdog comment for why a wedged
// quit must become a hard exit rather than a second copy of the old app.
func (u *UpdateService) ApplyUpdate() error {
	if u.app == nil || u.app.Updater == nil {
		return errors.New("更新服务未初始化")
	}
	return applyWithWatchdog(
		func() error { return u.app.Updater.Restart(context.Background()) },
		func() bool { return helperAwaitingExit(os.TempDir(), os.Getpid()) },
		applyWatchdog, applyPoll, applyExitGrace,
		forceExitForUpdate,
	)
}

// applyWithWatchdog runs restart and makes sure a successful update is not
// left half-applied. The forceExit callback is the last resort: production
// exits the process, tests record the call.
func applyWithWatchdog(restart func() error, helperReady func() bool, watchdog, poll, grace time.Duration, forceExit func(string)) error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("更新重启异常: %v", r)
			}
		}()
		done <- restart()
	}()

	// armExit applies only when Restart reported success: the helper is
	// standing by by definition, so the process must not outlive the handoff.
	armExit := func() {
		if grace <= 0 {
			return
		}
		go func() {
			time.Sleep(grace)
			forceExit("重启完成更新后进程未退出")
		}()
	}

	timer := time.NewTimer(watchdog)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return err
		}
		armExit()
		return nil
	case <-timer.C:
	}

	// Restart is still in flight. If the helper is ready, the only thing
	// between the user and the new version is our own shutdown.
	for {
		if helperReady() {
			forceExit("关闭流程超时，更新助手已在等待本进程退出")
			return nil // only reached when forceExit is a test recorder
		}
		select {
		case err := <-done:
			if err != nil {
				return err
			}
			armExit()
			return nil
		case <-time.After(poll):
		}
	}
}

// helperAwaitingExit reports whether the updater helper this process spawned
// has reached helper mode and is now blocking on our exit. Wails hands that
// readiness signal through <tmp>/wails-update-<pid>-<nano>.ready ("ready")
// and deletes it when Restart returns, so while Restart is still blocked the
// file proves that exiting now completes the swap rather than abandoning it.
// A file carrying an "error:" payload means the helper gave up — never exit
// on that.
func helperAwaitingExit(tmpDir string, pid int) bool {
	matches, err := filepath.Glob(filepath.Join(tmpDir, fmt.Sprintf("wails-update-%d-*.ready", pid)))
	if err != nil {
		return false
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err == nil && strings.TrimSpace(string(data)) == "ready" {
			return true
		}
	}
	return false
}

func forceExitForUpdate(reason string) {
	log.Printf("强制退出以完成更新：%s", reason)
	os.Exit(0)
}

// Skip records the version the user does not want to be reminded about. A
// newer release still surfaces: the updater compares versions, it does not
// mute the channel.
func (u *UpdateService) Skip(version string) error {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		return errors.New("缺少要跳过的版本号")
	}
	if u.app != nil && u.app.Updater != nil {
		u.app.Updater.SkipVersion(version)
	}
	return u.persistSettings(version, nil)
}

// SetAutoCheck turns the silent daily check on or off, for this session and
// for every later one.
func (u *UpdateService) SetAutoCheck(enabled bool) error {
	if err := u.persistSettings("", &enabled); err != nil {
		return err
	}
	if enabled {
		u.startAutoCheck()
	} else {
		u.stop()
	}
	return nil
}

// OpenDownloadPage opens the GitHub releases page in the system browser. It is
// the only install path where the running copy cannot replace itself.
func (u *UpdateService) OpenDownloadPage() error {
	if u.app == nil || u.app.Browser == nil {
		return errors.New("更新服务未初始化")
	}
	return u.app.Browser.OpenURL(updateReleasesURL)
}

// persistSettings applies the update-related settings in one locked
// read-modify-write. Empty arguments leave the stored value alone.
func (u *UpdateService) persistSettings(skipped string, autoCheck *bool) error {
	if u.S == nil || u.S.Store == nil {
		return errors.New("配置存储不可用")
	}
	return u.S.Store.Update(func(c *config.Config) {
		if autoCheck != nil {
			c.Settings.AutoCheckUpdate = autoCheck
		}
		if skipped != "" {
			c.Settings.SkippedVersion = skipped
		}
	})
}

func (u *UpdateService) skippedVersion() string {
	if u.S == nil || u.S.Store == nil {
		return ""
	}
	return strings.TrimPrefix(u.S.Store.Settings().SkippedVersion, "v")
}

// startAutoCheck (re)starts the silent check loop. Each call cancels the
// previous loop, so toggling the setting cannot leave two loops running.
func (u *UpdateService) startAutoCheck() {
	ctx, cancel := context.WithCancel(context.Background())
	u.mu.Lock()
	prev := u.autoCancel
	u.autoCancel = cancel
	u.mu.Unlock()
	if prev != nil {
		prev()
	}
	go u.autoCheckLoop(ctx)
}

func (u *UpdateService) autoCheckLoop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("自动检查更新异常退出: %v", r)
		}
	}()
	select {
	case <-ctx.Done():
		return
	case <-time.After(firstCheckDelay):
	}
	for {
		u.autoCheckOnce()
		select {
		case <-ctx.Done():
			return
		case <-time.After(checkInterval):
		}
	}
}

// autoCheckOnce runs one silent check. Every reason not to check, and every
// failure, is non-fatal: a background convenience must never raise a dialog,
// block startup, or fight a foreground flow over the updater's state machine.
func (u *UpdateService) autoCheckOnce() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("自动检查更新异常: %v", r)
		}
	}()
	if u.app == nil || u.app.Updater == nil {
		return
	}
	if u.S == nil || u.S.Store == nil || !u.S.Store.Settings().UpdateAutoCheckEnabled() {
		return
	}
	switch u.app.Updater.State() {
	case updater.StateChecking, updater.StateDownloading, updater.StateVerifying,
		updater.StateInstalling, updater.StateReady:
		return
	}
	if _, err := u.CheckNow(); err != nil {
		log.Printf("自动检查更新失败（已忽略）: %v", err)
	}
}

// detectUpdateMode decides whether this install can replace itself.
//
// Two framework facts drive it:
//   - the payload is staged beside the installed binary (on macOS, beside the
//     .app bundle), so that directory must be writable by the current user;
//   - there is no elevation path at all. A binary in %ProgramFiles% or in
//     /usr/local/bin (deb/rpm) can never be swapped, and no UAC/polkit prompt
//     exists to fall back on.
//
// Linux additionally needs $APPIMAGE: only an AppImage is a single
// replaceable file, and a deb-installed /usr/local/bin binary is root-owned.
func detectUpdateMode(goos, appImage string, exeDirWritable bool) (mode, reason string) {
	switch goos {
	case "windows":
		if !exeDirWritable {
			return UpdateModeNotifyOnly, "exe 所在目录不可写（例如放在 Program Files 或只读介质）。把 exe 移到可写目录（如 D:\\Apps）后即可自动更新"
		}
		return UpdateModeAutoInstall, ""
	case "darwin":
		if !exeDirWritable {
			return UpdateModeNotifyOnly, "应用所在目录不可写，无法原地替换，请下载新版安装包"
		}
		return UpdateModeAutoInstall, ""
	case "linux":
		if appImage == "" {
			return UpdateModeNotifyOnly, "当前是 deb/rpm 安装，程序在系统目录（需 root 权限），请下载新版安装包手动升级"
		}
		if !exeDirWritable {
			return UpdateModeNotifyOnly, "AppImage 所在目录不可写，无法原地替换，请下载新版"
		}
		return UpdateModeAutoInstall, ""
	default:
		return UpdateModeNotifyOnly, "当前平台不支持自动更新，请下载新版安装包"
	}
}

// executableDirWritable reports whether the directory holding the running
// binary accepts a new file. It probes by creating one, because permission
// bits alone lie on Windows (inherited ACLs) and macOS (quarantine/app translocation).
func executableDirWritable() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	f, err := os.CreateTemp(dir, ".update-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// parseUpdatePublicKey accepts the shapes a maintainer might paste: base64 of
// the raw 32-byte key (what `wails3 updater genkey` prints), hex, or the raw
// bytes. Empty input means "no signature check", which is logged at startup.
func parseUpdatePublicKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	s = strings.Join(strings.Fields(s), "") // tolerate pasted line breaks
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("更新公钥格式无法识别（需要 32 字节 ed25519 公钥的 base64 或 hex）")
}
