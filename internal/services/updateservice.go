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

	"agentmate/internal/config"
)

// updateRepo 更新渠道接线。
//
// 发布版本和签名的更新清单都位于 GitHub Releases
// 上，因此恰好只有一个可信任的分发渠道，也只有一个可以撤下坏版本的地方。`releas
// es/latest/download/...` 是 GitHub
// 指向最新已发布（非草稿、非预发布）版本的稳定重定向：发布热修复会移动该指针，
// 而将坏版本重新改回草稿则会把它移回去。
const (
	updateRepo        = "qq540491950/agentmate"
	updateManifestURL = "https://github.com/" + updateRepo + "/releases/latest/download/manifest.json"
	updateReleasesURL = "https://github.com/" + updateRepo + "/releases"

	// firstCheckDelay 让启动检查避开关键路径：窗口先完成启动并可用，之后才会进行任何网络访问。
	firstCheckDelay = 10 * time.Second
	// checkInterval 被故意设置得较慢。每次检查都可能变成更新提示，而一个快速迭代的项目如果每小时
	// 检查一次，就会每天打扰用户。
	checkInterval = 24 * time.Hour
	// checkTimeout 为一次完整检查（清单获取 + 重定向）设定上限。
	checkTimeout = 90 * time.Second

	// applyWatchdog 重启看门狗计时。框架的 helper 会等待此进程退出 30
	// 秒，然后才替换二进制文件并重新启动；如果等待超时，它会放弃替换，改为重新启动
	// 旧的二进制文件（pkg/updater/helper.go 中的退出码
	// 17）。更新结束后旧窗口仍留在屏幕上、旁边却是一个刚启动的应用，原因就在于此。
	// 因此 ApplyUpdate 不会只依赖优雅关闭：
	//
	// - applyWatchdog 限定我们等待 Restart 稳定下来的时长。超过该时限后，由 helper
	// 的就绪文件决定：一旦它表示 "ready"，说明 helper 正阻塞等待此
	// PID，因此现在退出即可完成替换。 - applyExitGrace 覆盖 Restart
	// 报告成功之后的同一风险（此时 helper
	// 按定义已经就绪）：如果宽限时间结束时进程仍然存活，则强制退出。
	applyWatchdog  = 12 * time.Second
	applyPoll      = 250 * time.Millisecond
	applyExitGrace = 5 * time.Second
)

// updatePublicKeyB64 是更新签名的信任根：`wails3 updater genkey` 输出的原始
// 32 字节 ed25519 公钥的 base64。
//
// 配置它后签名成为强制项（fail closed）：清单未携带签名的发布会被拒绝，而不是
// 降级为仅摘要校验的安装。用
// `wails3 updater genkey -out ~/.wails/updater.key` 生成一次密钥对，把它的
// base64 粘贴到这里，提交本文件（公钥本来就是公开的），并将私钥保存在 CI 密钥库
// 以及一份离线备份中——丢失私钥意味着所有已发布构建都无法再验证后续更新。
var updatePublicKeyB64 = ""

// UpdateMode tells the settings page what "安装更新" can actually do here.
const (
	// UpdateModeAutoInstall：正在运行的副本位于此用户可写的目录中，因此更新器可以
	// 暂存并替换它。
	UpdateModeAutoInstall = "auto-install"
	// UpdateModeNotifyOnly：告知用户存在新版本，并将其引导至发布页面。用于无法执行
	// 替换的所有场景——参见 detectUpdateMode。
	UpdateModeNotifyOnly = "notify-only"
)

// UpdateStatus 是设置页面渲染的快照。
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

// UpdateService 从设置页面驱动 Wails 更新器。
//
// 它负责框架特意留给应用的三件事：何时检查（静默进行，每天最多一次，绝不自动安
// 装）、UI 看到的内容（UpdateStatus），以及 "skip this version"
// 偏好，该偏好保存在 config.json 中，因此重启后仍然存在。
type UpdateService struct {
	S   *Services
	app *application.App

	mu          sync.Mutex
	release     *updater.Release
	lastErr     string
	mode        string
	reason      string
	autoCancel  context.CancelFunc
	sweepCancel context.CancelFunc
}

// NewUpdateService 创建该服务。Bind 必须在 application.New 之后运行。
func NewUpdateService(s *Services) *UpdateService {
	mode, reason := detectUpdateMode(runtime.GOOS, os.Getenv("APPIMAGE"), executableDirWritable())
	return &UpdateService{S: s, mode: mode, reason: reason}
}

// BindUpdates 在已构建的应用上配置更新器。
//
// 这是一个包级函数，而不是 UpdateService
// 的方法，因为已注册服务的每个导出方法都会成为 webview 可以调用的 RPC，而
// "configure yourself with this *application.App" 不是页面 JavaScript
// 应能调用的操作。
func BindUpdates(u *UpdateService, app *application.App) error { return u.attach(app) }

// StopUpdates 结束后台更新轮询和遗留文件清理（应用关闭钩子）。
func StopUpdates(u *UpdateService) {
	u.stop()
	u.stopSweep()
}

// attach 将服务连接到正在运行的应用并配置更新器。调用方决定配置错误是否致命；更新不值
// 得因此阻塞启动。
func (u *UpdateService) attach(app *application.App) error {
	if app == nil {
		return errors.New("update: nil app")
	}
	u.app = app
	// 清理上一次更新未能删除的任何内容（参见
	// updateleftover.go）。即使更新器本身无法配置，也会执行：否则旧的遗留文件会永
	// 远留在磁盘上。
	u.startLeftoverSweep()
	if app.Updater == nil {
		return errors.New("update: 当前 Wails 运行时没有 updater")
	}

	provider, err := endpoint.New(endpoint.Config{
		URL: updateManifestURL,
		// 共享
		// http.DefaultTransport，使全局代理设置（agentkit.ApplyGlobalProxy）也适用于
		// 更新流量；私有客户端会让使用代理的用户永久处于 "up to date" 状态。
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

	// WindowNone：更新 UI
	// 位于设置页面，因此框架的独立窗口绝不打开，两者也绝不会不一致。
	//
	// CheckInterval 有意保持未设置：框架的周期性循环会调用
	// CheckAndInstall，即下载并暂存用户从未请求的更新。我们在 autoCheckLoop
	// 中自行安排仅检查的轮次。
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

// stop 结束后台检查循环。由应用关闭钩子调用。
func (u *UpdateService) stop() {
	u.mu.Lock()
	cancel := u.autoCancel
	u.autoCancel = nil
	u.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Status 返回设置页面的当前更新状态。
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

// CheckNow 向发布源请求较新的版本。供手动按钮和静默循环使用，后者的错误会记录到日志，而
// 不会对外暴露。 返回的 error 为 nil 且 State=="up-to-date"
// 表示不存在较新的版本。
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

// InstallUpdate 下载、校验并暂存待安装的发布。它不会重启：用户先完成手头的
// 工作，然后点击「重启完成更新」。
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

// ApplyUpdate 将已暂存的载荷交给更新助手并退出；更新助手会替换二进制文件/应用包并重新启动新
// 版本。
//
// Restart 会阻塞等待本进程优雅关闭，因此它在看门狗监控下运行于独立
// goroutine：关于卡死的退出流程为何必须改为强制退出，而不是出现旧应用的第二个副
// 本，请参见 applyWatchdog 的注释。
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

// applyWithWatchdog 运行
// restart，并确保成功的更新不会停留在只应用了一半的状态。forceExit
// 回调是最后手段：生产环境会退出进程，测试中会记录该调用。
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

	// armExit 仅在 Restart
	// 报告成功时生效：此时更新助手按定义已处于待命状态，因此进程不得在交接后继续存
	// 活。
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

	// Restart 仍在进行中。如果更新助手已就绪，那么用户与新版本之间唯一剩下的障碍就是我们自
	// 己的关闭流程。
	for {
		if helperReady() {
			forceExit("关闭流程超时，更新助手已在等待本进程退出")
			return nil // 仅当 forceExit 是测试记录器时才会到达
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

// helperAwaitingExit 报告本进程启动的更新助手是否已进入助手模式，并且现在正阻塞等待我们退出。Wails
// 通过 <tmp>/wails-update-<pid>-<nano>.ready（"ready"）传递该就绪信号，并在
// Restart 返回时删除它；因此，在 Restart
// 仍被阻塞期间，该文件证明现在退出会完成替换，而不是放弃替换。带有 "error:"
// 负载的文件表示助手已放弃——绝不要因此退出。
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

// Skip 记录用户不希望再收到提醒的版本。较新的版本仍会出现：更新器会比较版本，而不会
// 屏蔽该渠道。
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

// SetAutoCheck 开启或关闭静默每日检查，该设置对本次会话及之后的每个会话生效。
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

// OpenDownloadPage 在系统浏览器中打开 GitHub releases
// 页面。它是唯一一条正在运行的副本无法自行替换自身的安装路径。
func (u *UpdateService) OpenDownloadPage() error {
	if u.app == nil || u.app.Browser == nil {
		return errors.New("更新服务未初始化")
	}
	return u.app.Browser.OpenURL(updateReleasesURL)
}

// persistSettings 在一次加锁的读取-修改-写入中应用与更新相关的设置。参数为空时，已存储的值保持
// 不变。
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

// startAutoCheck 启动（或重新启动）静默检查循环。每次调用都会取消前一个循环，因此切换该设置不
// 可能让两个循环同时运行。
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

// autoCheckOnce 执行一次静默检查。所有不检查的理由以及所有失败都是非致命的：
// 后台便利功能绝不能弹对话框、阻塞启动，或与前台流程争夺更新器的状态机。
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

// detectUpdateMode 决定此安装能否替换自身。
//
// 有两个框架事实决定了这一点： - 载荷会暂存到已安装二进制文件旁边（在 macOS
// 上，暂存到 .app bundle 旁边），因此该目录必须可由当前用户写入； -
// 完全没有提权路径。位于 %ProgramFiles% 或
// /usr/local/bin（deb/rpm）中的二进制文件绝不能被替换，也不存在可回退的
// UAC/polkit 提示。
//
// Linux 还额外需要 $APPIMAGE：仅 AppImage 是单个可替换文件，而通过 deb 安装的
// /usr/local/bin 二进制文件归 root 所有。
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

// executableDirWritable 报告存放正在运行的二进制文件的目录是否接受新文件。它通过创建一个文件来探测，
// 因为仅凭权限位在 Windows（继承的 ACL）和 macOS（隔离/应用转移）上会产生误判。
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

// parseUpdatePublicKey 接受维护者可能粘贴的几种形式：原始 32 字节密钥的
// base64（即 `wails3 updater genkey`
// 输出的内容）、十六进制，或原始字节。空输入表示“no signature
// check”，这一点会在启动时记录日志。
func parseUpdatePublicKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	s = strings.Join(strings.Fields(s), "") // 容忍粘贴产生的换行符。
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
