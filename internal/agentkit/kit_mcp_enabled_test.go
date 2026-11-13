package agentkit

import (
	"strings"
	"testing"

	"agentmate/internal/config"
)

// TestBuildAgentInjectsEnabledMCPServers: the per-server 启用 switch is the
// authoritative injection control — enabled servers reach every session
// (direct chat included) without per-agent mounting, disabled ones stay off
// unless an agent explicitly mounts them. A broken transport on an enabled
// server must surface as a build error (no silent tool loss); the same broken
// transport disabled must build cleanly.
func TestBuildAgentInjectsEnabledMCPServers(t *testing.T) {
	kit, store := newTestKit(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "p1", Name: "Fake", Protocol: config.ProtocolOpenAI, Models: []config.ModelInfo{{ID: "m1"}},
		})
		cfg.MCPServers = append(cfg.MCPServers,
			config.MCPServerConfig{ID: "mcp_on", Name: "启用服务", Transport: config.MCPStdio, Command: ""},
			config.MCPServerConfig{ID: "mcp_off", Name: "停用服务", Transport: config.MCPStdio, Disabled: true, Command: ""},
		)
	}); err != nil {
		t.Fatal(err)
	}
	ac := config.AgentConfig{ID: "a1", Name: "工具人", ProviderID: "p1", Model: "m1"}

	// 已启用的（损坏的）服务器：必须被挂载并报告。
	if _, err := kit.BuildAgent(ac); err == nil {
		t.Fatal("enabled MCP server must be injected (broken transport must surface)")
	} else if !strings.Contains(err.Error(), "启用服务") {
		t.Fatalf("error missing enabled server: %v", err)
	}

	// 仅有已停用的（损坏的）服务器：必须保持关闭并正常构建。
	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.MCPServers {
			if cfg.MCPServers[i].ID == "mcp_on" {
				cfg.MCPServers[i].Disabled = true
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := kit.BuildAgent(ac); err != nil {
		t.Fatalf("disabled MCP server must not be injected: %v", err)
	}

	// 逃生通道：Agent 显式挂载已停用的服务器即可重新选择加入。
	ac.MCPServerIDs = []string{"mcp_off"}
	if _, err := kit.BuildAgent(ac); err == nil || !strings.Contains(err.Error(), "停用服务") {
		t.Fatalf("explicit mount of a disabled server must be honored, got: %v", err)
	}
}
