package main

import (
	"embed"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/session/database"
	"google.golang.org/adk/v2/tool/loadmemorytool"
	"gorm.io/gorm"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
	"agentmate/internal/memory"
	"agentmate/internal/sandbox"
	"agentmate/internal/services"
)

// Wails 使用 Go 的 `embed` 包把前端文件嵌入二进制。
//
//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

// Version 是应用版本，通过 AppService.AppInfo 暴露给前端，并被更新器用作
// "currently running" 版本。
//
// Git 标签是唯一事实来源：发布构建通过 `-ldflags "-X main.Version=0.5.0"`
// 注入它（参见 Taskfile.yml 中的 VERSION 变量和
// scripts/set-version.sh，该脚本还会刷新 build/config.yml
// 以及生成的构建资源，使 exe 元数据、NSIS、Info.plist 和 deb/rpm
// 全部一致）。任何未打标签构建的产物——`go run .`、`wails3
// dev`、测试二进制——都会报告
// "dev"，更新器会将其视为比所有发布版本都旧，因此开发构建始终会被提供最新版本。
var Version = "dev"

func init() {
	// 注册聊天流式事件，使生成的绑定为其携带强类型的 JS/TS API。
	application.RegisterEvent[services.ChatStreamEvent](services.EventChatStream)
}

func main() {
	// 配置位于操作系统用户配置目录下。
	cfgDir, err := osUserConfigDir("agentmate")
	if err != nil {
		log.Fatal(err)
	}
	setupLogging(filepath.Join(cfgDir, "logs"))

	store, err := config.Open(cfgDir)
	if err != nil {
		log.Fatal(err)
	}

	// 用于模型/嵌入/MCP 流量的出站代理。它应用于
	// http.DefaultTransport，因此必须在任何 HTTP
	// 请求之前运行；更改该设置需要重启应用。
	if err := agentkit.ApplyGlobalProxy(store.Get().Settings.HTTPProxy); err != nil {
		log.Printf("代理配置无效（使用直连）: %v", err)
	}

	// MCP 管理器 + Agent 工具包。
	mgr := mcpmgr.New()
	mgr.SetTokenStore(store) // OAuth 令牌在重启后持久保留。
	mgr.SetAuthPendingHook(func(server, authURL string) {
		if app := application.Get(); app != nil {
			app.Event.Emit("mcp:auth-pending", map[string]string{"server": server, "url": authURL})
		}
	})
	kit := agentkit.NewKit(store, mgr)
	svc := &services.Services{Store: store, Kit: kit, MCP: mgr}
	services.AppVersion = Version

	// 持久化的 ADK 会话存储（通过纯 Go 驱动使用 SQLite）。同一个 *gorm.DB 由
	// ChatService（Regenerate）和长期记忆共享。
	gormDB, sessions, err := newSessionService(filepath.Join(cfgDir, "sessions.db"))
	if err != nil {
		log.Fatal(err)
	}

	// 长期记忆（SQLite，与会话使用同一数据库）。
	memSvc, err := memory.NewSQLiteService(gormDB)
	if err != nil {
		log.Fatal(err)
	}
	kit.SetMemoryTool(loadmemorytool.New())
	svc.Memory = memSvc

	// 知识向量索引（语义搜索升级；失败时降级为关键词搜索，因此不得阻塞启动）。
	if ki, err := agentkit.OpenKnowledgeIndex(filepath.Join(cfgDir, "knowledge.index.db")); err == nil {
		kit.SetKnowledgeIndex(ki)
	} else {
		log.Printf("knowledge index disabled: %v", err)
	}

	// 始终注册 A2A 服务（start/stop RPC）；自动启动可能已通过下面的 settings
	// 钩子开始提供服务。
	a2aRPC := services.NewA2AService(svc, sessions)
	svc.A2A = a2aRPC // 配置变更可能会使已发布的 Agent 下线

	chatSvc := services.NewChatService(svc, sessions, gormDB)
	chatSvc.SetToolAuditPath(filepath.Join(cfgDir, "logs", "tools-audit.jsonl"))
	logSvc := services.NewAppService(svc,
		filepath.Join(cfgDir, "sessions.db"),
		filepath.Join(cfgDir, "knowledge.index.db"),
		gormDB)
	logSvc.SetLogPath(filepath.Join(cfgDir, "logs", "app.log"))
	logSvc.SetToolAuditPath(filepath.Join(cfgDir, "logs", "tools-audit.jsonl"))
	// 更新检查针对 GitHub Releases 运行（见
	// updateservice.go）。该服务在应用存在之前就已注册，因此更新器本身由下方的
	// Bind 进行配置。
	updSvc := services.NewUpdateService(svc)

	// 主窗口仅在 application.New
	// 之后才存在，而第二次启动在取得锁之后的任意时刻都可能到达；原子句柄使该回调免
	// 于竞态（nil 表示"仍在启动中"）。
	var mainWindow atomic.Pointer[application.WebviewWindow]

	app := application.New(application.Options{
		Name:        "AgentMate",
		Description: "ADK-powered agent desktop app",
		Services: []application.Service{
			application.NewService(logSvc),
			application.NewService(services.NewConfigService(svc)),
			application.NewService(services.NewMCPService(svc)),
			application.NewService(services.NewArtifactService(svc)),
			application.NewService(chatSvc),
			application.NewService(a2aRPC),
			application.NewService(updSvc),
		},
		// 再次启动应用（桌面图标、开始菜单、`open`）必须唤出正在运行的窗口，而不是再
		// 打开一个完整副本。Wails 会获取跨平台单实例锁（Windows 上为命名互斥量 +
		// WM_COPYDATA，macOS/Linux 上为锁文件 +
		// 分布式通知），将本次启动交给回调，并在构建窗口或启动服务之前，以 ExitCode
		// 退出新进程。
		SingleInstance: singleInstanceOptions(func() {
			activateMainWindow(mainWindow.Load())
			log.Printf("检测到二次启动：已唤回正在运行的窗口，新进程退出")
		}),
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// 托盘 +
			// 全局快捷键是常驻功能：关闭窗口不得退出整个应用（否则会静默终止已发布的 A2A
			// 服务和唤出快捷键）。退出功能位于托盘菜单中。
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		// Windows 兜底（#77）：最后一个窗口关闭不退出进程，防止任何未拦截
		// 的关闭路径意外退出；Mac 上由上方 MacOptions 的对应字段承担。
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
		OnShutdown: func() {
			services.StopUpdates(updSvc) // 停止后台更新轮询
			// Wails 在 Quit() 内同步执行 OnShutdown。应用内更新器的辅助进程只等待
			// 本进程退出 30 秒；超时后它会放弃替换并重新启动旧二进制，导致同时运行
			// 两个应用副本。因此，可能卡住的清理流程（忽略 stdin 关闭的 MCP stdio
			// 子进程、仍在传输的 A2A 流）绝不能耗尽整个等待预算。
			shutdownBounded("A2A 服务下线", shutdownHookTimeout, a2aRPC.StopAll) // 优雅退出：退出前取消发布 Agent。
			shutdownBounded("MCP 连接关闭", shutdownHookTimeout, mgr.CloseAll)   // 然后关闭 MCP 传输（不留下孤立的 stdio 子进程）
			shutdownBounded("沙箱资源清理", 2*time.Second, sandbox.Close)
		},
	})

	// 更新绝不能阻塞启动：更新源损坏、代理不可达或固定密钥格式错误时，应用仍应
	// 完全可用，只是没有更新功能。
	if err := services.BindUpdates(updSvc, app); err != nil {
		log.Printf("更新服务初始化失败（不影响使用）: %v", err)
	}

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "AgentMate",
		Width:     1280,
		Height:    820,
		MinWidth:  960,
		MinHeight: 600,
		// Windows 启用无边框（#77）：自绘顶栏成为唯一窗口 chrome，保留
		// 无边框装饰（系统阴影/圆角/Aero Snap/边缘缩放）。macOS 不启用，
		// 继续使用隐藏标题栏 + 红绿灯配置。
		Frameless: runtime.GOOS == "windows",
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(18, 18, 20),
		URL:              "/",
	})
	mainWindow.Store(window)

	// 关闭语义统一为「隐藏到托盘」（#77）：拦截 WindowClosing（Alt+F4 /
	// 系统关闭 / Close 调用），非退出流程取消原生关闭并只隐藏窗口。隐藏后
	// 由托盘左键或全局唤起快捷键唤回；托盘「退出」先置 quitting 再 Quit，
	// 让真正的退出穿透钩子。上游测试覆盖了 Cancel 拦截路径。
	var quitting atomic.Bool
	window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		e.Cancel()
		window.Hide()
		// Alt+F4 路径前端不可见，经事件让前端弹一次性「已隐藏到托盘」提示。
		app.Event.Emit("shell:hidden-to-tray")
	})

	// A2A 服务器自动启动（为远程 A2A 客户端发布本地 Agent）。
	if svcSettings := store.Get().Settings; svcSettings.A2AAutoStart && svcSettings.A2AAgentID != "" {
		if st, err := a2aRPC.Start(svcSettings.A2AAgentID, svcSettings.A2APort); err != nil {
			log.Printf("A2A 服务自启动失败: %v", err)
		} else {
			log.Printf("A2A 服务已自启动: %s", st.URL)
		}
	}

	// MCP 自启动：后台预连接所有启用的服务器并预热工具列表。顺序执行避免
	// 一次性拉起过多子进程；失败只记日志，不阻塞启动窗口。
	go func() {
		defer func() { _ = recover() }()
		for _, s := range store.Get().MCPServers {
			if s.Disabled {
				continue
			}
			st := mgr.Connect(s)
			if st.State == "error" {
				log.Printf("MCP「%s」自启动失败: %s", st.Name, st.Error)
			} else {
				log.Printf("MCP「%s」已自启动连接，发现 %d 个工具", st.Name, len(st.Tools))
			}
		}
	}()

	setupSystemTray(app, window, &quitting)
	// 全局唤起快捷键；冲突的注册（归另一个应用所有）不得阻塞启动。
	if err := app.GlobalShortcut.Register("shift+cmd+space", func() {
		toggleMainWindow(window)
	}); err != nil {
		log.Printf("全局快捷键注册失败（不影响启动）: %v", err)
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// shutdownHookTimeout 为单个关闭钩子设定上限。这里的每个钩子都会清理网络监听器或子进程，这类工作可
// 能停滞足够久，从而产生影响：Quit() 是同步的，应用内更新器给旧进程 30
// 秒的退出窗口（参见 services.ApplyUpdate）。两个各 5 秒的钩子仍远在该窗口内。
const shutdownHookTimeout = 5 * time.Second

// shutdownBounded 在后台运行 fn，并在 fn
// 完成或超时后返回，以先发生者为准。超时的钩子会继续运行，而进程会在其仍在运行
// 时退出；替代方案是 Quit 卡死，从而把一次更新变成旧应用的两个副本。
func shutdownBounded(name string, timeout time.Duration, fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("关闭%s异常: %v", name, r)
			}
		}()
		fn()
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		log.Printf("关闭%s超时（%s），继续退出", name, timeout)
	}
}

// setupSystemTray 添加菜单栏托盘：点击切换窗口显隐，菜单提供显示/隐藏和退出
// （常驻托盘入口）。
func setupSystemTray(app *application.App, window *application.WebviewWindow, quitting *atomic.Bool) {
	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("AgentMate")
	// 左键直接切换主窗口显隐，右键弹出菜单。不要使用
	// AttachWindow：Wails 会在每次显示前把窗口贴到托盘所在的右下角，
	// 破坏窗口原本居中的位置。手写切换保留窗口上次位置。
	tray.OnClick(func() { toggleMainWindow(window) })

	menu := app.NewMenu()
	menu.Add("显示/隐藏 AgentMate").OnClick(func(*application.Context) {
		toggleMainWindow(window)
	})
	menu.Add("退出").OnClick(func(*application.Context) {
		// 先置退出标记再 Quit：WindowClosing 钩子据此放行真正的退出（#77）。
		quitting.Store(true)
		app.Quit()
	})
	tray.SetMenu(menu)
}

// mainWindowController 是托盘、菜单和全局快捷键唤起路径所使用的窗口操作子集。保持其精简，可让测试无
// 需运行 WebView 即可验证切换状态机。
type mainWindowController interface {
	IsVisible() bool
	IsMinimised() bool
	Hide() application.Window
	UnMinimise()
	Show() application.Window
	Focus()
}

// toggleMainWindow 会隐藏一个可见且未最小化的窗口，并恢复其他所有情况。在
// Windows 上，最小化的窗口仍会报告 IsVisible，因此在这里检查 IsMinimised
// 可防止托盘点击隐藏用户正试图重新打开的窗口。
func toggleMainWindow(window mainWindowController) {
	if window == nil {
		return
	}
	if window.IsVisible() && !window.IsMinimised() {
		window.Hide()
		return
	}
	showMainWindow(window)
}

// showMainWindow 恢复窗口而不改变其位置。初始位置已由 Wails
// 居中；唤起路径必须保留用户离开时的任意位置，而不是重新停靠到托盘旁边。
func showMainWindow(window mainWindowController) {
	if window == nil {
		return
	}
	// UnMinimise（而非 Restore）会让最大化或全屏窗口完全保持用户离开时的状态。
	window.UnMinimise()
	window.Show()
	window.Focus()
}

// singleInstanceOptions 构建 Wails 单实例配置。
//
// UniqueID 是正在运行的副本与新启动副本用于相互发现的锁名称，因此它必须在各版本间保持稳
// 定：更改它会让新构建与旧构建并排运行（这正是它要防止的缺陷）。它与
// build/config.yml 中的 productIdentifier 匹配。
func singleInstanceOptions(activate func()) *application.SingleInstanceOptions {
	return &application.SingleInstanceOptions{
		UniqueID: "com.agentmate.app",
		OnSecondInstanceLaunch: func(application.SecondInstanceData) {
			if activate != nil {
				activate()
			}
		},
	}
}

// activateMainWindow 将已在运行的窗口带回前台。
//
// 检测到第二次启动时，回调会在第一个实例上运行。nil
// 窗口仅在第二次启动抢在窗口创建之前、且应用无论如何都即将显示该窗口时才会出现
// 。
func activateMainWindow(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	showMainWindow(window)
}

// setupLogging 除 stderr
// 外，还会将标准日志记录器写入一个有大小上限的轮转文件。app.log 一旦超过 5 MB
// 就会移动到 app.log.old。
func setupLogging(logDir string) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	rw, err := newRotatingWriter(filepath.Join(logDir, "app.log"), 5<<20)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, rw))
	log.SetFlags(log.LstdFlags | log.LUTC)
}

// rotatingWriter 将其文件大小上限设为
// maxBytes：托盘应用会驻留数周，而过去仅在启动时执行的轮转检查会让 app.log
// 在单次会话内无限增长。每次写入都会检查大小，并在追加前轮转到 <path>.old。
type rotatingWriter struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	f        *os.File
}

func newRotatingWriter(path string, maxBytes int64) (*rotatingWriter, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > maxBytes {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &rotatingWriter{path: path, maxBytes: maxBytes, f: f}, nil
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// 重新打开失败会使 w.f 保持为 nil；后续每次写入都会重试打开，因此短暂的
	// EMFILE/权限错误不会导致文件日志在进程剩余生命周期内失效。
	if w.f == nil {
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		w.f = f
	}
	st, err := w.f.Stat()
	if err == nil && st.Size()+int64(len(p)) > w.maxBytes {
		_ = w.f.Close()
		_ = os.Rename(w.path, w.path+".old")
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			w.f = nil // 会在下一次写入时重试
			return 0, err
		}
		w.f = f
	}
	return w.f.Write(p)
}

// Close 释放当前日志文件句柄（供测试使用，以免在 Windows
// 上因打开的句柄阻止删除而导致 TempDir 清理失败）。
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// newSessionService 打开由 SQLite 支持的 ADK 会话服务，并同时返回共享的 gorm
// 句柄。损坏的数据库会被移到一旁（备份）并重新创建，以确保应用始终能够启动。
func newSessionService(path string) (*gorm.DB, session.Service, error) {
	open := func() (*gorm.DB, session.Service, error) {
		dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
		db, err := gorm.Open(sqlite.Open(dsn))
		if err != nil {
			return nil, nil, err
		}
		// 将所有访问串行化：并行会话会并发写入此数据库，而仅靠 WAL
		// 无法帮助遇到快照冲突的延迟事务（SQLITE_BUSY 会绕过
		// busy_timeout）。改为由单个连接将每条语句排队——对桌面应用而言已足够。
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.SetMaxOpenConns(1)
		}
		svc, err := database.NewSessionServiceFromDB(db)
		if err != nil {
			return nil, nil, err
		}
		if err := database.AutoMigrate(svc); err != nil {
			return nil, nil, err
		}
		// 探测：损坏的文件可能通过 Open，但在首次查询时失败。
		if err := db.Exec("SELECT count(*) FROM sessions").Error; err != nil {
			closeDB(db) // 在对损坏文件重命名之前释放连接池
			return nil, nil, err
		}
		return db, svc, nil
	}
	db, svc, err := open()
	if err != nil {
		backup := path + ".corrupt-" + time.Now().Format("20060102-150405")
		// 先关闭任何泄漏的句柄：Windows
		// 会拒绝重命名仍处于打开状态的文件（共享冲突），进而使自愈路径变成硬性启动失
		// 败。
		closeDB(db)
		if rerr := os.Rename(path, backup); rerr != nil {
			log.Printf("sessions database corrupt but backup rename failed: %v", rerr)
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Rename(path+suffix, backup+suffix)
		}
		log.Printf("sessions database was corrupt, moved to %s and recreated", backup)
		db, svc, err = open()
		if err != nil {
			return nil, nil, err
		}
	}
	return db, svc, nil
}

// closeDB 释放 gorm 句柄底层的 SQL 连接池（尽力而为）。
func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
