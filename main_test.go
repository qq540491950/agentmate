package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestSystemTrayNeverAttachesWindow 锁定了针对“summon jumps to the bottom-right
// corner”缺陷的修复。Wails 的 AttachWindow 会在每次显示时调用
// PositionWindow，这会将窗口停靠在任务栏托盘图标旁，而不是恢复其先前（初始居中
// ）的位置。托盘仅需要点击切换，因此显式连接该行为必须保持独立于
// AttachWindow/WindowOffset。
//
// Wails 未提供在进程内合成托盘点击的方式，因此这个 AST
// 防护是针对导致该缺陷的依赖行为的最窄回归接缝。
func TestSystemTrayNeverAttachesWindow(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "AttachWindow", "WindowOffset":
			t.Errorf("setup must not call tray.%s: Wails repositions the window at the tray corner on every show", selector.Sel.Name)
		}
		return true
	})
}

type fakeMainWindow struct {
	visible   bool
	minimised bool
	calls     []string
}

func (w *fakeMainWindow) IsVisible() bool   { return w.visible }
func (w *fakeMainWindow) IsMinimised() bool { return w.minimised }
func (w *fakeMainWindow) UnMinimise()       { w.calls = append(w.calls, "unminimise"); w.minimised = false }
func (w *fakeMainWindow) Focus()            { w.calls = append(w.calls, "focus") }
func (w *fakeMainWindow) Hide() application.Window {
	w.calls = append(w.calls, "hide")
	w.visible = false
	return nil
}
func (w *fakeMainWindow) Show() application.Window {
	w.calls = append(w.calls, "show")
	w.visible = true
	return nil
}

// TestToggleMainWindow：每个唤起路径共享同一状态机。隐藏窗口必须在不进行任何重
// 新定位的情况下取消最小化/显示/聚焦，而可见的普通窗口则被隐藏。在 Windows
// 上，最小化窗口仍被视为“可见”，因此它必须恢复而不是隐藏。
func TestToggleMainWindow(t *testing.T) {
	tests := []struct {
		name      string
		visible   bool
		minimised bool
		want      string
	}{
		{"hidden restores", false, false, "unminimise,show,focus"},
		{"visible normal hides", true, false, "hide"},
		{"minimised restores", true, true, "unminimise,show,focus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window := &fakeMainWindow{visible: tt.visible, minimised: tt.minimised}
			toggleMainWindow(window)
			if got := strings.Join(window.calls, ","); got != tt.want {
				t.Fatalf("toggle calls = %q, want %q", got, tt.want)
			}
		})
	}

	toggleMainWindow(nil)
}

// TestRotatingWriterRotates：超过 maxBytes 会将文件移动到
// .old，后续写入会落到全新的 app.log 中——长期驻留的托盘应用过去会让 app.log
// 无界增长（轮转仅在启动时运行）。
func TestRotatingWriterRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w, err := newRotatingWriter(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	// 写入超过上限：部分写入进入第一代文件，其余写入进入轮转后的文件。
	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatalf("rotation did not produce %s.old: %v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || len(data) > 64+10 {
		t.Fatalf("fresh log size = %d", len(data))
	}
	oldData, err := os.ReadFile(path + ".old")
	if err != nil || !strings.HasPrefix(string(oldData), "0123456789") {
		t.Fatalf("old log missing first generation: err=%v", err)
	}
}

// TestShutdownBounded：卡死的关闭钩子不得扣住 Quit()
// 不放，也不得连带扣住应用内更新器的 30 s
// 父进程退出窗口。错过该窗口会使更新器重新启动 OLD
// 二进制文件，即同时出现两个应用。
func TestShutdownBounded(t *testing.T) {
	wedged := make(chan struct{})
	t.Cleanup(func() { close(wedged) })

	start := time.Now()
	shutdownBounded("测试挂死钩子", 25*time.Millisecond, func() { <-wedged })
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shutdownBounded waited %s for a wedged hook", elapsed)
	}

	ran := make(chan struct{})
	shutdownBounded("测试正常钩子", time.Second, func() { close(ran) })
	select {
	case <-ran:
	default:
		t.Fatal("shutdownBounded must run the hook")
	}

	// 发生 panic 的钩子会被记录，绝不会传播到 Quit()。
	shutdownBounded("测试恐慌钩子", time.Second, func() { panic("boom") })
}

// TestSingleInstanceOptions：第二次启动（桌面图标、开始菜单）必须转发到正在运行
// 的实例并显示其窗口，而不是打开另一个完全可用的副本。UniqueID
// 是两个副本必须一致的锁名称，因此在此固定：更改它会静默重新启用与较旧运行中构
// 建并存的多实例启动。
func TestSingleInstanceOptions(t *testing.T) {
	activations := 0
	opts := singleInstanceOptions(func() { activations++ })
	if opts == nil {
		t.Fatal("singleInstanceOptions returned nil; Wails would not enforce a single instance")
	}
	if opts.UniqueID != "com.agentmate.app" {
		t.Fatalf("UniqueID = %q, want the stable product identifier", opts.UniqueID)
	}
	if opts.OnSecondInstanceLaunch == nil {
		t.Fatal("OnSecondInstanceLaunch is nil; the existing window would never be surfaced")
	}
	opts.OnSecondInstanceLaunch(application.SecondInstanceData{Args: []string{"--second"}})
	if activations != 1 {
		t.Fatalf("second launch activated the window %d times, want 1", activations)
	}

	// 一次启动可能与窗口创建发生竞态；必须容忍 nil 激活器。
	singleInstanceOptions(nil).OnSecondInstanceLaunch(application.SecondInstanceData{})

	// 并且生产激活器不得在窗口存在之前 panic。
	activateMainWindow(nil)
}
