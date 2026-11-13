package mcpmgr

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"agentmate/internal/config"
)

// URLOpener 在用户的默认浏览器中打开授权 URL。可注入以便测试。
var URLOpener = defaultURLOpener

func defaultURLOpener(raw string) error {
	return browserCommand(runtime.GOOS, raw).Start()
}

// browserCommand 构建打开默认浏览器并访问 raw 的操作系统专用命令。Windows
// 不得通过 cmd.exe 路由 URL：cmd 会重新解析命令行，而每个 OAuth 授权 URL 中的
// `&` 都会截断地址，并将剩余部分作为单独命令执行。rundll32 的
// FileProtocolHandler 会将 URL 作为一个参数交给 shell。
func browserCommand(goos, raw string) *exec.Cmd {
	switch goos {
	case "darwin":
		return exec.Command("open", raw)
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	default:
		return exec.Command("xdg-open", raw)
	}
}

// authHTTPClient 构建一个
// *http.Client，应用静态配置请求头，并且对于简单凭据类型，还会应用认证请求头（B
// earer 令牌或命名 API key）。用于无需完整 OAuth 的流式 HTTP 传输。
func authHTTPClient(cfg config.MCPServerConfig) *http.Client {
	headerName := cfg.AuthHeader
	if headerName == "" {
		headerName = "X-API-Key"
	}
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		for k, v := range cfg.Headers {
			req.Header.Set(k, v)
		}
		switch cfg.AuthType {
		case "bearer":
			req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		case "api-key":
			req.Header.Set(headerName, cfg.AuthToken)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
}

// headersOnlyClient 仅应用静态配置请求头（不含认证类型）。
func headersOnlyClient(cfg config.MCPServerConfig) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		for k, v := range cfg.Headers {
			req.Header.Set(k, v)
		}
		return http.DefaultTransport.RoundTrip(req)
	})}
}

// newOAuthTransport 将官方 go-sdk 授权码处理器（依据 MCP 授权规范的 OAuth 2.1 +
// PKCE）接入流式 HTTP 传输。当 tokenStore 非 nil
// 时，令牌会在应用重启后持久保留：已缓存且未过期的令牌（或可刷新的令牌）会跳过
// 浏览器同意流程。
//
// 回环重定向监听器不会在传输生命周期内保持打开：提前绑定会使端口保持占用，即使
// 缓存令牌让同意流程无法触达，并且此时在同一配置端口上执行 Disconnect+reconnect
// 会因 EADDRINUSE 失败。端口会被探测一次以构建稳定的重定向 URL，fetcher
// 会在每次同意流程期间惰性重新绑定它。
func newOAuthTransport(cfg config.MCPServerConfig, tokenStore *config.Store, onAuthPending func(authURL string)) (mcp.Transport, error) {
	if cfg.OAuthClientID == "" {
		return nil, fmt.Errorf("oauth2 认证需要预注册的 client id")
	}
	probe, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.OAuthRedirectPort))
	if err != nil {
		return nil, fmt.Errorf("本地回调端口监听失败: %w", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close() // 在实际流程启动时由 fetcher 惰性重新绑定
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	fetcher := newBrowserAuthCodeFetcher(port, onAuthPending)
	handlerCfg := &auth.AuthorizationCodeHandlerConfig{
		PreregisteredClient:      &oauthex.ClientCredentials{ClientID: cfg.OAuthClientID},
		RedirectURL:              redirectURL,
		AuthorizationCodeFetcher: fetcher,
		NewTokenSource: func(ctx context.Context, oc *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			// 持久化每个新签发/刷新的令牌，使应用重启在刷新令牌仍有效期间保持静默。
			if tokenStore != nil {
				_ = tokenStore.Update(func(c *config.Config) {
					if c.MCPTokens == nil {
						c.MCPTokens = map[string]config.MCPOAuthToken{}
					}
					c.MCPTokens[cfg.ID] = MCPOAuthTokenFromOauth2(token, oc.Endpoint.TokenURL)
				})
			}
			return oc.TokenSource(ctx, token), nil
		},
	}
	if cached := cachedTokenSource(tokenStore, cfg.ID, cfg.OAuthClientID); cached != nil {
		handlerCfg.InitialTokenSource = cached
	}
	handler, err := auth.NewAuthorizationCodeHandler(handlerCfg)
	if err != nil {
		return nil, err
	}
	return &mcp.StreamableClientTransport{Endpoint: cfg.URL, OAuthHandler: handler}, nil
}

// newBrowserAuthCodeFetcher 为同意流程提供回环回调服务：它惰性绑定重定向端口（仅在实际流程运行期间），在
// 系统浏览器中打开授权 URL，并最终返回授权服务器重定向回来的授权码（上限 5
// 分钟）。流程结束时始终释放监听器，因此同一端口上的重连仍可正常工作。
func newBrowserAuthCodeFetcher(port int, onAuthPending func(authURL string)) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return nil, fmt.Errorf("本地回调端口监听失败: %w", err)
		}
		codeCh := make(chan *auth.AuthorizationResult, 1)
		errCh := make(chan error, 1)
		mux := http.NewServeMux()
		mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if e := q.Get("error"); e != "" {
				fmt.Fprintf(w, "<html><body><h3>授权失败</h3><p>%s: %s</p></body></html>", e, q.Get("error_description"))
				errCh <- fmt.Errorf("授权服务器返回 %s: %s", e, q.Get("error_description"))
				return
			}
			fmt.Fprint(w, "<html><body><h3>已收到授权，请返回 AgentMate。</h3></body></html>")
			codeCh <- &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}
		})
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		serveDone := make(chan struct{})
		go func() {
			_ = srv.Serve(ln)
			close(serveDone)
		}()
		defer func() {
			_ = srv.Close()
			_ = ln.Close()
			<-serveDone
		}()

		if onAuthPending != nil {
			onAuthPending(args.URL)
		}
		if err := URLOpener(args.URL); err != nil {
			return nil, fmt.Errorf("无法打开浏览器完成授权: %w", err)
		}
		select {
		case res := <-codeCh:
			return res, nil
		case err := <-errCh:
			return nil, err
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Minute):
			return nil, fmt.Errorf("授权等待超时（5 分钟）")
		}
	}
}

// cachedTokenSource 从持久化缓存重建令牌源：未过期的令牌按原样使用；已过期但带有刷新令牌且令牌端
// 点已知的令牌会静默刷新（不启动浏览器）。
func cachedTokenSource(store *config.Store, serverID, clientID string) oauth2.TokenSource {
	if store == nil {
		return nil
	}
	cfgSnapshot := store.Get()
	cached, ok := cfgSnapshot.MCPTokens[serverID]
	if !ok || cached.AccessToken == "" {
		return nil
	}
	if cached.TokenURL == "" || cached.RefreshToken == "" {
		// 仅在尚未过期时可用。
		tok := oauth2TokenFromCached(cached)
		if tok.Valid() {
			return oauth2.StaticTokenSource(tok)
		}
		return nil
	}
	oc := &oauth2.Config{ClientID: clientID, Endpoint: oauth2.Endpoint{TokenURL: cached.TokenURL}}
	return oc.TokenSource(context.Background(), oauth2TokenFromCached(cached))
}

func oauth2TokenFromCached(c config.MCPOAuthToken) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  c.AccessToken,
		RefreshToken: c.RefreshToken,
		TokenType:    c.TokenType,
		Expiry:       c.Expiry,
	}
}

func MCPOAuthTokenFromOauth2(t *oauth2.Token, tokenURL string) config.MCPOAuthToken {
	return config.MCPOAuthToken{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		TokenType:    t.TokenType,
		Expiry:       t.Expiry,
		TokenURL:     tokenURL,
	}
}

// roundTripperFunc 将函数适配为 http.RoundTripper。
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
