// Package main 调试探针：加载真实的 AgentMate 配置（只读），并完全按照 main.go
// 启动时自动连接的方式连接每个已配置的 MCP
// 服务器，以区分“旧二进制仍在运行”与“注入路径已损坏”。环境变量值和令牌绝不打印
// 。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

func main() {
	base, err := os.UserConfigDir()
	if err != nil {
		fmt.Println("user config dir:", err)
		os.Exit(1)
	}
	store, err := config.Open(filepath.Join(base, "agentmate"))
	if err != nil {
		fmt.Println("open config:", err)
		os.Exit(1)
	}
	mgr := mcpmgr.New()
	for _, s := range store.Get().MCPServers {
		st := mgr.Connect(s)
		out, _ := json.MarshalIndent(map[string]any{
			"name":     st.Name,
			"command":  s.Command,
			"args":     s.Args,
			"disabled": s.Disabled,
			"state":    st.State,
			"tools":    st.Tools,
			"error":    st.Error,
		}, "", "  ")
		fmt.Println(string(out))
	}
}
