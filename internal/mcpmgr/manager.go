// Package mcpmgr 管理应用的 MCP 服务器连接： 它为每个已配置的服务器持有一个 adk
// mcptoolset，并跟踪状态。
package mcpmgr

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"
	"google.golang.org/genai"

	"agentmate/internal/config"
)

// ToolInfo 描述已连接的 MCP 服务器所暴露的一个工具。
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ServerStatus 是一个已配置 MCP 服务器的运行时状态。
type ServerStatus struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	State     string     `json:"state"` // 已停止 | 已连接 | 错误
	Tools     []ToolInfo `json:"tools"`
	Error     string     `json:"error,omitempty"`
	CheckedAt time.Time  `json:"checkedAt"`
}

// Manager 持有以服务器配置 ID 为键的 MCP 工具集。
type Manager struct {
	mu         sync.Mutex
	toolsets   map[string]tool.Toolset
	conns      map[string]*trackedTransport // 每个服务器的可关闭连接
	states     map[string]*ServerStatus
	tokenStore *config.Store // 可选：在重启之间持久化 OAuth 令牌
	// onAuthPending 在被设置后，会在 OAuth
	// 授权同意流程于浏览器中等待用户期间收到通知 （服务器显示名称 + 授权 URL）。
	onAuthPending func(server, authURL string)
}

// trackedTransport 包装一个 MCP 传输，并记住它打开的连接，这样 Disconnect
// 才能真正关闭会话（连同其 stdio 子进程）。ADK
// 工具集把自身连接保持为私有，因此这个包装器是我们唯一拥有的可关闭句柄。
type trackedTransport struct {
	inner mcp.Transport
	mu    sync.Mutex
	conn  mcp.Connection
}

func (t *trackedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	prev := t.conn
	t.conn = conn
	t.mu.Unlock()
	// 会话断开后，SDK 会再次调用 Connect
	// 重连；丢弃过期的那个连接，避免反复重连时累积会话。
	if prev != nil {
		_ = prev.Close()
	}
	return conn, nil
}

func (t *trackedTransport) Close() error {
	t.mu.Lock()
	conn := t.conn
	t.conn = nil
	t.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
}

// SetAuthPendingHook 注册一个回调，在 OAuth
// 授权开始、用户必须在浏览器中操作时触发。
func (m *Manager) SetAuthPendingHook(fn func(server, authURL string)) {
	m.onAuthPending = fn
}

// New 创建一个空的管理器。
func New() *Manager {
	return &Manager{
		toolsets: map[string]tool.Toolset{},
		conns:    map[string]*trackedTransport{},
		states:   map[string]*ServerStatus{},
	}
}

// SetTokenStore 启用 OAuth 令牌持久化（由 config
// 支持）。若不设置，令牌仅在进程生命周期内有效，且每次启动都会重新执行一次授权
// 确认。
func (m *Manager) SetTokenStore(store *config.Store) {
	m.tokenStore = store
}

func (m *Manager) onAuthPendingWith(server string) func(authURL string) {
	return func(authURL string) {
		if m.onAuthPending != nil {
			m.onAuthPending(server, authURL)
		}
	}
}

func (m *Manager) buildTransportFor(cfg config.MCPServerConfig) (mcp.Transport, error) {
	return buildTransport(cfg, m.tokenStore, m.onAuthPendingWith(cfg.Name))
}

func buildTransport(cfg config.MCPServerConfig, tokenStore *config.Store, onAuthPending func(authURL string)) (mcp.Transport, error) {
	switch cfg.Transport {
	case config.MCPStreamableHTTP:
		endpoint := strings.TrimSpace(cfg.URL)
		if endpoint == "" {
			return nil, fmt.Errorf("streamable-http 传输需要填写 URL")
		}
		switch cfg.AuthType {
		case "oauth2":
			return newOAuthTransport(cfg, tokenStore, onAuthPending)
		case "bearer", "api-key":
			return &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: authHTTPClient(cfg)}, nil
		}
		if len(cfg.Headers) > 0 {
			// 自定义 header 一直可配置，但历史上从未被使用；现在通过传输的 HTTP
			// 客户端应用它们。
			return &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: headersOnlyClient(cfg)}, nil
		}
		return &mcp.StreamableClientTransport{Endpoint: endpoint}, nil
	default: // 标准输入输出
		// 从 UI 粘贴和手工编辑的配置经常带有尾部空格（"npx "）；LookPath
		// 绝不可能匹配到这类命令，因此在执行前先去除首尾空格。
		command := strings.TrimSpace(cfg.Command)
		if command == "" {
			return nil, fmt.Errorf("stdio 传输需要填写启动命令")
		}
		args := make([]string, 0, len(cfg.Args))
		for _, a := range cfg.Args {
			if a = strings.TrimSpace(a); a != "" {
				args = append(args, a)
			}
		}
		cmd := exec.Command(command, args...)
		// 桌面应用作为没有控制台的 GUI 进程运行；若不这样处理，Windows 上的 stdio MCP
		// 子进程会分配一个可见的控制台窗口，并在整个会话期间保持打开（issue #67）。
		if attrs := stdioProcessAttrs(); attrs != nil {
			cmd.SysProcAttr = attrs
		}
		if len(cfg.Env) > 0 {
			env := os.Environ()
			for k, v := range cfg.Env {
				env = append(env, k+"="+v)
			}
			cmd.Env = env
		}
		return &mcp.CommandTransport{Command: cmd}, nil
	}
}

// Toolset 返回服务器的工具集（必要时创建）。它不会建立连接；连接在首次使用工具时惰性发
// 生。创建过程在锁内进行：否则两个并发连接会构建两个传输层，并泄漏其中一个（以
// 及其 stdio 子进程）。
func (m *Manager) Toolset(cfg config.MCPServerConfig) (tool.Toolset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ts, ok := m.toolsets[cfg.ID]; ok {
		return ts, nil
	}
	tr, err := m.buildTransportFor(cfg)
	if err != nil {
		return nil, err
	}
	tracked := &trackedTransport{inner: tr}
	ts, err := mcptoolset.New(mcptoolset.Config{Transport: tracked})
	if err != nil {
		return nil, fmt.Errorf("创建 MCP 工具集「%s」失败: %w", cfg.Name, err)
	}
	m.toolsets[cfg.ID] = ts
	m.conns[cfg.ID] = tracked
	return ts, nil
}

// Connect 验证服务器是否可达并缓存其工具列表。
func (m *Manager) Connect(cfg config.MCPServerConfig) ServerStatus {
	st := &ServerStatus{ID: cfg.ID, Name: cfg.Name, State: "connecting"}
	ts, err := m.Toolset(cfg)
	if err != nil {
		st.State = "error"
		st.Error = err.Error()
		m.setState(st)
		return *st
	}
	// 首次 OAuth 授权同意会为浏览器流程最多等待 5 分钟（auth.go 的 fetcher
	// 上限）；30s 的状态检查超时会始终在用户完成登录之前终止授权。
	limit := 30 * time.Second
	if cfg.AuthType == "oauth2" {
		limit = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	adkTools, err := ts.Tools(statusCtx{ctx})
	if err != nil {
		st.State = "error"
		st.Error = err.Error()
	} else {
		st.State = "connected"
		for _, t := range adkTools {
			st.Tools = append(st.Tools, ToolInfo{Name: t.Name(), Description: t.Description()})
		}
	}
	st.CheckedAt = time.Now()
	m.setState(st)
	return *st
}

// Disconnect 丢弃服务器的缓存工具集。
func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	tr := m.conns[id]
	ts := m.toolsets[id]
	delete(m.toolsets, id)
	delete(m.conns, id)
	if st, ok := m.states[id]; ok {
		st.State = "stopped"
		st.Tools = nil
		st.Error = "" // 已停止的服务器不得保留过期的错误。
		st.CheckedAt = time.Now()
	}
	m.mu.Unlock()
	// 在锁外关闭：关闭 stdio 会话需要等待子进程。工具集也会被关闭：自 ADK v2.7.0
	// 起，mcptoolset 持有一个必须释放的会话客户端（v2.4.0 的工具集没有 Close，因此
	// 当时只有传输层值得关闭）。先关闭工具集再关闭传输层，可以让会话通过仍然存活的
	// 连接完成关闭；从未打开会话的工具集关闭时没有副作用。
	if closer, ok := ts.(io.Closer); ok {
		_ = closer.Close()
	}
	if tr != nil {
		_ = tr.Close()
	}
}

// CloseAll 断开所有已跟踪的服务器。应用关闭钩子会调用它，以免 stdio
// 子进程成为孤儿进程并比桌面应用存活更久。幂等：可以安全地多次调用。
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.conns))
	for id := range m.conns {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Disconnect(id)
	}
}

func (m *Manager) setState(st *ServerStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[st.ID] = st
}

// Status 返回某个服务器已缓存的状态（或一个已停止的占位符）。
func (m *Manager) Status(id, name string) ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.states[id]; ok {
		return *st
	}
	return ServerStatus{ID: id, Name: name, State: "stopped"}
}

// AllStatus 返回给定配置对应的各个状态。
func (m *Manager) AllStatus(cfgs []config.MCPServerConfig) []ServerStatus {
	out := make([]ServerStatus, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, m.Status(c.ID, c.Name))
	}
	return out
}

// statusCtx 将普通 context 适配为
// agent.ReadonlyContext，供在运行中的调用之外发生的工具集查询使用。
type statusCtx struct{ context.Context }

func (statusCtx) UserContent() *genai.Content          { return nil }
func (statusCtx) InvocationID() string                 { return "" }
func (statusCtx) AgentName() string                    { return "" }
func (statusCtx) ReadonlyState() session.ReadonlyState { return nil }
func (statusCtx) UserID() string                       { return "" }
func (statusCtx) AppName() string                      { return "" }
func (statusCtx) SessionID() string                    { return "" }
func (statusCtx) Branch() string                       { return "" }
