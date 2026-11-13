package mcpmgr

import (
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/tool"

	"agentmate/internal/config"
)

// TestBuildTransportTrimsStdioCommand: 粘贴或导入的配置可能带入多余的空白（"npx
// "）；exec 必须看到去除首尾空白后的命令和参数，否则 LookPath 会失败并报
// "executable file not found in %PATH%"。
func TestBuildTransportTrimsStdioCommand(t *testing.T) {
	base := "sh"
	if runtime.GOOS == "windows" {
		base = "cmd"
	}
	cfg := config.MCPServerConfig{
		ID:        "srv1",
		Name:      "S",
		Transport: config.MCPStdio,
		Command:   base + " ",
		Args:      []string{" -y ", "tavily-mcp", " "},
	}
	tr, err := buildTransport(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	cmdTr, ok := tr.(*mcp.CommandTransport)
	if !ok {
		t.Fatalf("expected *mcp.CommandTransport, got %T", tr)
	}
	cmd := cmdTr.Command
	if cmd.Err != nil {
		t.Fatalf("command %q must resolve after trim, got: %v", cfg.Command, cmd.Err)
	}
	got := strings.TrimSuffix(filepath.Base(cmd.Path), filepath.Ext(cmd.Path))
	if got != base {
		t.Fatalf("resolved command = %q, want base %q", cmd.Path, base)
	}
	wantArgs := []string{base, "-y", "tavily-mcp"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("args = %q, want %q", cmd.Args, wantArgs)
	}
	for i := range wantArgs {
		if cmd.Args[i] != wantArgs[i] {
			t.Fatalf("args = %q, want %q", cmd.Args, wantArgs)
		}
	}
}

// TestBuildTransportAppliesStdioProcessAttrs 锁定 issue #67
// 的接线方式：平台辅助函数提供的进程属性必须在 CommandTransport 启动它之前落到
// exec.Cmd 上。在 Windows 上该属性是 CREATE_NO_WINDOW；在其他平台上它保持为
// nil（没有需要隐藏的控制台）。
func TestBuildTransportAppliesStdioProcessAttrs(t *testing.T) {
	base := "sh"
	if runtime.GOOS == "windows" {
		base = "cmd"
	}
	cfg := config.MCPServerConfig{
		ID:        "srv1",
		Name:      "S",
		Transport: config.MCPStdio,
		Command:   base,
	}
	tr, err := buildTransport(cfg, nil, nil)
	if err != nil {
		t.Fatalf("buildTransport: %v", err)
	}
	cmdTr, ok := tr.(*mcp.CommandTransport)
	if !ok {
		t.Fatalf("expected *mcp.CommandTransport, got %T", tr)
	}
	if !reflect.DeepEqual(cmdTr.Command.SysProcAttr, stdioProcessAttrs()) {
		t.Fatalf("SysProcAttr = %#v, want %#v", cmdTr.Command.SysProcAttr, stdioProcessAttrs())
	}
}

// TestToolsetCachedUntilDisconnect 锁定管理器契约：工具集以服务器 ID
// 为键，并在多次调用之间复用；Disconnect
// 会丢弃缓存，使下一次构建创建全新的传输（SaveMCPServer
// 借此在配置修改后停止陈旧连接）。
func TestToolsetCachedUntilDisconnect(t *testing.T) {
	m := New()
	cfg := config.MCPServerConfig{ID: "srv1", Name: "S", Transport: config.MCPStdio, Command: "/bin/cat"}

	ts1, err := m.Toolset(cfg)
	if err != nil {
		t.Fatalf("Toolset: %v", err)
	}
	ts2, err := m.Toolset(cfg)
	if err != nil {
		t.Fatalf("Toolset (cached): %v", err)
	}
	if ts1 != ts2 {
		t.Fatal("expected the cached toolset instance to be reused")
	}

	m.Disconnect(cfg.ID)
	ts3, err := m.Toolset(cfg)
	if err != nil {
		t.Fatalf("Toolset after disconnect: %v", err)
	}
	if ts3 == ts1 {
		t.Fatal("expected a fresh toolset after Disconnect")
	}
}

// TestCloseAllClearsStateAndIsIdempotent 锁定关闭契约：一次调用会丢弃所有被跟踪的服务器（缓存 +
// 状态），重复调用是安全的。应用的关闭钩子依赖这一点，以确保 stdio
// 子进程不会作为孤儿进程比桌面应用存活更久。
func TestCloseAllClearsStateAndIsIdempotent(t *testing.T) {
	m := New()
	stdio := config.MCPServerConfig{ID: "srv1", Name: "S", Transport: config.MCPStdio, Command: "/bin/cat"}
	httpSrv := config.MCPServerConfig{ID: "srv2", Name: "H", Transport: config.MCPStreamableHTTP, URL: "http://127.0.0.1:1/mcp"}
	if _, err := m.Toolset(stdio); err != nil {
		t.Fatalf("Toolset stdio: %v", err)
	}
	if _, err := m.Toolset(httpSrv); err != nil {
		t.Fatalf("Toolset http: %v", err)
	}

	m.CloseAll()
	m.CloseAll() // 幂等：不 panic，状态不会复活

	for _, cfg := range []config.MCPServerConfig{stdio, httpSrv} {
		ts1, err := m.Toolset(cfg)
		if err != nil {
			t.Fatalf("Toolset after CloseAll: %v", err)
		}
		m.Disconnect(cfg.ID)
		ts2, err := m.Toolset(cfg)
		if err != nil {
			t.Fatalf("Toolset rebuild: %v", err)
		}
		if ts1 == ts2 {
			t.Fatalf("server %s: expected a fresh toolset after CloseAll + Disconnect cycle", cfg.ID)
		}
	}
}

// TestDisconnectClearsStaleError:
// 已停止的服务器不得继续报告导致上次断连的那个错误。
func TestDisconnectClearsStaleError(t *testing.T) {
	m := New()
	m.setState(&ServerStatus{ID: "srv1", Name: "S", State: "error", Error: "boom"})
	m.Disconnect("srv1")
	st := m.Status("srv1", "S")
	if st.State != "stopped" || st.Error != "" {
		t.Fatalf("disconnect must clear stale error, got %+v", st)
	}
}

// TestOAuthTransportPortNotHeld 锁定延迟监听契约：构建 OAuth
// 传输时不得持续占用重定向端口（缓存的 token
// 会使授权同意流程无法触达，而占用该端口会导致在同一配置的重定向端口上重连时出
// 现 EADDRINUSE）。
func TestOAuthTransportPortNotHeld(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	cfg := config.MCPServerConfig{
		ID: "srv-oauth", Name: "O", Transport: config.MCPStreamableHTTP,
		URL: "http://127.0.0.1:1/mcp", AuthType: "oauth2",
		OAuthClientID: "client-id", OAuthRedirectPort: port,
	}
	for i := 1; i <= 2; i++ {
		if _, err := buildTransport(cfg, nil, nil); err != nil {
			t.Fatalf("build %d: %v", i, err)
		}
		// 构建完成后，该端口必须立即重新可用。
		again, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("port still held after build %d: %v", i, err)
		}
		again.Close()
	}
}

// trackingToolset 是一个缓存的工具集，用于记录管理器是否关闭了它。ADK v2.7.0 使
// mcptoolset.New 返回 io.Closer，而管理器拥有该缓存实例，因此 Disconnect
// 必须释放它，而不是丢弃缓存条目并泄漏会话（对于已认证的服务器，还会泄漏池）。
type trackingToolset struct {
	tool.Toolset
	closed bool
}

func (t *trackingToolset) Close() error {
	t.closed = true
	return nil
}

// TestDisconnectClosesCachedToolset 锁定资源契约：丢弃缓存必须在传输层消失之前关闭工具集，否则 MCP
// 会话绝不会被释放。
func TestDisconnectClosesCachedToolset(t *testing.T) {
	m := New()
	ts := &trackingToolset{}
	m.mu.Lock()
	m.toolsets["srv1"] = ts
	m.mu.Unlock()

	m.Disconnect("srv1")

	if !ts.closed {
		t.Fatal("expected Disconnect to close the cached toolset")
	}
	if _, ok := m.toolsets["srv1"]; ok {
		t.Fatal("expected the cache entry to be dropped")
	}
}

// TestCloseAllClosesEveryToolset：关闭钩子依赖
// CloseAll，因此它必须恰好关闭每个工具集一次，而不只是清空映射。
func TestCloseAllClosesEveryToolset(t *testing.T) {
	m := New()
	a, b := &trackingToolset{}, &trackingToolset{}
	m.mu.Lock()
	m.toolsets["a"] = a
	m.toolsets["b"] = b
	m.conns["a"] = nil
	m.conns["b"] = nil
	m.mu.Unlock()

	m.CloseAll()

	if !a.closed || !b.closed {
		t.Fatalf("expected both toolsets closed, got a=%v b=%v", a.closed, b.closed)
	}
}
