package mcpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"agentmate/internal/config"
)

const (
	testAccessToken = "test-access-token-123"
	testAPIKey      = "test-api-key-456"
)

// TestBrowserCommandKeepsAuthorizeURLIntact 固定各平台打开浏览器的命令：URL
// 必须作为单个未经修改的参数传入。Windows 过去通过 `cmd /c start`
// 路由它，这会重新解析命令行——每个 OAuth 授权 URL 中的 `&`
// 都会截断地址，并把其余部分当作命令执行，从而彻底破坏授权同意流程。
func TestBrowserCommandKeepsAuthorizeURLIntact(t *testing.T) {
	const authorizeURL = "https://auth.example.com/authorize?response_type=code&client_id=abc&redirect_uri=http%3A%2F%2F127.0.0.1%3A0%2Fcallback"
	for _, goos := range []string{"windows", "darwin", "linux"} {
		cmd := browserCommand(goos, authorizeURL)
		if strings.Contains(strings.ToLower(cmd.Path), "cmd") {
			t.Errorf("%s: URL must not be parsed by cmd.exe (path %q)", goos, cmd.Path)
		}
		if got := cmd.Args[len(cmd.Args)-1]; got != authorizeURL {
			t.Errorf("%s: authorize URL split or modified: %q", goos, got)
		}
	}
}

// newProtectedMCPServer 在认证中间件后面托管一个真实的 go-sdk MCP
// 服务器。kind："oauth2"（由模拟 AS 签发的 Bearer）| "api-key" |
// "bearer"（静态）。
func newProtectedMCPServer(t *testing.T, kind, asBase string) *httptest.Server {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test-mcp", Version: "1.0.0"}, nil)
	server.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "answers pong",
		InputSchema: &jsonschema.Schema{Type: "object"},
	},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	check := func(r *http.Request) bool {
		switch kind {
		case "oauth2", "bearer":
			return r.Header.Get("Authorization") == "Bearer "+testAccessToken
		case "api-key":
			return r.Header.Get("X-API-Key") == testAPIKey
		}
		return true
	}

	// Mux 注册在服务器启动之后进行（与官方测试夹具相同的技巧）：PRM
	// 文档需要最终的服务器 URL。
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	resourceURL := srv.URL + "/mcp"
	mux.Handle("/.well-known/oauth-protected-resource/mcp",
		auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
			Resource:             resourceURL,
			AuthorizationServers: []string{asBase},
			ScopesSupported:      []string{"mcp:tools"},
		}))
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !check(r) {
			w.Header().Set("WWW-Authenticate", "Bearer resource_metadata="+resourceURL+"/.well-known/oauth-protected-resource/mcp")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	return srv
}

// newFakeAuthorizationServer 实现了 MCP
// 授权服务器的接口面：受保护资源元数据、AS 元数据、/authorize（PKCE 回显）和
// /token。
func newFakeAuthorizationServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                base,
			"authorization_endpoint":                base + "/authorize",
			"token_endpoint":                        base + "/token",
			"response_types_supported":              []string{"code"},
			"grant_types_supported":                 []string{"authorization_code"},
			"code_challenge_methods_supported":      []string{"S256"},
			"token_endpoint_auth_methods_supported": []string{"none"},
		})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("response_type") != "code" || q.Get("client_id") == "" ||
			q.Get("code_challenge") == "" || q.Get("redirect_uri") == "" {
			http.Error(w, "bad authorize request", http.StatusBadRequest)
			return
		}
		loc, _ := url.Parse(q.Get("redirect_uri"))
		rq := loc.Query()
		rq.Set("code", "auth-code-xyz")
		rq.Set("state", q.Get("state"))
		loc.RawQuery = rq.Encode()
		http.Redirect(w, r, loc.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") != "auth-code-xyz" {
			http.Error(w, "bad token request", http.StatusBadRequest)
			return
		}
		if r.Form.Get("code_verifier") == "" {
			http.Error(w, "PKCE verifier missing", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": testAccessToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// browserSimulator 像用户代理一样“打开”授权 URL：跟随一次 authorize
// 重定向，并落在回环回调上。
func browserSimulator() func(string) error {
	return func(raw string) error {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}, Timeout: 10 * time.Second}
		resp, err := client.Get(raw)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		loc := resp.Header.Get("Location")
		if loc == "" {
			return fmt.Errorf("authorize did not redirect: status=%d url=%s body=%.200s", resp.StatusCode, raw, body)
		}
		_, err = client.Get(loc) // 访问应用的回环回调
		return err
	}
}

func listToolsThrough(t *testing.T, tr mcp.Transport) []string {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "agentmate-test", Version: "1.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, tr, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()
	resp, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	names := []string{}
	for _, tl := range resp.Tools {
		names = append(names, tl.Name)
	}
	return names
}

func TestMCPAuthBearer(t *testing.T) {
	srv := newProtectedMCPServer(t, "bearer", "")
	cfg := config.MCPServerConfig{
		ID: "m1", Name: " secured", Transport: config.MCPStreamableHTTP,
		URL: srv.URL + "/mcp", AuthType: "bearer", AuthToken: testAccessToken,
	}
	tr, err := buildTransport(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := listToolsThrough(t, tr)
	if len(names) != 1 || names[0] != "ping" {
		t.Fatalf("unexpected tools: %v", names)
	}
}

func TestMCPAuthAPIKey(t *testing.T) {
	srv := newProtectedMCPServer(t, "api-key", "")
	cfg := config.MCPServerConfig{
		ID: "m2", Name: "secured", Transport: config.MCPStreamableHTTP,
		URL: srv.URL + "/mcp", AuthType: "api-key", AuthToken: testAPIKey, AuthHeader: "X-API-Key",
	}
	tr, err := buildTransport(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := listToolsThrough(t, tr)
	if len(names) != 1 || names[0] != "ping" {
		t.Fatalf("unexpected tools: %v", names)
	}
}

func TestMCPAuthOAuth2Flow(t *testing.T) {
	as := newFakeAuthorizationServer(t)
	resource := newProtectedMCPServer(t, "oauth2", as.URL)
	defer func() { URLOpener = defaultURLOpener }()
	URLOpener = browserSimulator()

	cfg := config.MCPServerConfig{
		ID: "m3", Name: "secured", Transport: config.MCPStreamableHTTP,
		URL: resource.URL + "/mcp", AuthType: "oauth2",
		OAuthClientID: "agentmate-desktop",
	}
	var pendingURL atomic.Value
	tr, err := buildTransport(cfg, nil, func(authURL string) {
		pendingURL.Store(authURL)
	})
	if err != nil {
		t.Fatal(err)
	}
	names := listToolsThrough(t, tr)
	if len(names) != 1 || names[0] != "ping" {
		t.Fatalf("unexpected tools after OAuth flow: %v", names)
	}
	if v := pendingURL.Load(); v == nil || !strings.Contains(v.(string), "/authorize") {
		t.Fatalf("auth-pending hook not fired with authorize URL: %v", v)
	}
	if !strings.Contains(names[0], "ping") {
		t.Fatalf("tool sanity: %v", names)
	}
}

func TestMCPAuthUnauthenticatedRejected(t *testing.T) {
	as := newFakeAuthorizationServer(t)
	srv := newProtectedMCPServer(t, "oauth2", as.URL)
	cfg := config.MCPServerConfig{
		ID: "m4", Name: "noauth", Transport: config.MCPStreamableHTTP,
		URL: srv.URL + "/mcp", // 无 AuthType
	}
	tr, err := buildTransport(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "agentmate-test", Version: "1.0.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, tr, nil)
	if err == nil {
		defer session.Close()
		_, err = session.ListTools(ctx, nil)
	}
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("expected unauthorized failure without credentials, got %v", err)
	}
}

// TestMCPAuthTokenPersistence 使用由配置支持的令牌存储运行一次 OAuth
// 流程，然后模拟应用重启：从缓存初始化的全新传输必须在不打开浏览器的情况下连接
// 。
func TestMCPAuthTokenPersistence(t *testing.T) {
	as := newFakeAuthorizationServer(t)
	resource := newProtectedMCPServer(t, "oauth2", as.URL)
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.MCPServerConfig{
		ID: "m5", Name: "secured", Transport: config.MCPStreamableHTTP,
		URL: resource.URL + "/mcp", AuthType: "oauth2",
		OAuthClientID: "agentmate-desktop",
	}

	// 首次运行：完整的同意流程；浏览器模拟器是必需的。
	defer func() { URLOpener = defaultURLOpener }()
	URLOpener = browserSimulator()
	tr1, err := buildTransport(cfg, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := listToolsThrough(t, tr1); len(names) != 1 {
		t.Fatalf("first flow tools: %v", names)
	}
	snap := store.Get()
	cached, ok := snap.MCPTokens[cfg.ID]
	if !ok || cached.AccessToken != testAccessToken || cached.TokenURL == "" {
		t.Fatalf("token not persisted: %+v (store has %+v)", cached, snap.MCPTokens)
	}

	// "Restart"：从同一存储创建全新的
	// transport。不得打开浏览器（打开浏览器会导致测试失败）。
	URLOpener = func(string) error { return fmt.Errorf("browser must not open for cached token") }
	tr2, err := buildTransport(cfg, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := listToolsThrough(t, tr2); len(names) != 1 || names[0] != "ping" {
		t.Fatalf("restart flow tools: %v", names)
	}
}
