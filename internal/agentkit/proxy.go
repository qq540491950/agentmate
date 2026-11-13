package agentkit

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ApplyGlobalProxy 将使用 http.DefaultTransport
// 的所有客户端（模型适配器、embeddings、MCP、gemini SDK）都通过给定的代理 URL
// 路由。必须在启动时、任何请求发出之前调用一次；传入 "" 可重置为直连。
func ApplyGlobalProxy(proxyURL string) error {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return fmt.Errorf("默认传输层类型异常（%T），无法配置代理", http.DefaultTransport)
	}
	if proxyURL == "" {
		// 回退到进程环境（HTTP_PROXY / HTTPS_PROXY /
		// NO_PROXY）而不是将其禁用：清空该函数过去会静默忽略用户在应用之外配置的代理
		// 。
		transport.Proxy = http.ProxyFromEnvironment
		return nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return fmt.Errorf("代理地址解析失败: %w", err)
	}
	transport.Proxy = func(req *http.Request) (*url.URL, error) {
		if isLoopbackHost(req.URL.Hostname()) {
			return nil, nil // 本地 Ollama / 本地 MCP / A2A 往返保持直连
		}
		return u, nil
	}
	return nil
}

// isLoopbackHost 报告 host（不含端口、不含方括号）是否为本机。
func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
