package services

import (
	"testing"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// TestSaveMCPServerInvalidatesStaleConnection:
// 编辑服务器的设置必须丢弃管理器缓存的工具集（下一回合将使用新的传输重建）；保
// 存完全相同的配置必须保留它。
func TestSaveMCPServerInvalidatesStaleConnection(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mgr := mcpmgr.New()
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mgr), MCP: mgr}
	cs := NewConfigService(svc)

	orig := config.MCPServerConfig{ID: "mcp_1", Name: "S", Transport: config.MCPStdio, Command: "/bin/cat"}
	if _, err := cs.SaveMCPServer(orig); err != nil {
		t.Fatal(err)
	}
	ts1, err := mgr.Toolset(orig)
	if err != nil {
		t.Fatal(err)
	}

	// 未更改的保存：缓存的工具集仍然保留。
	if _, err := cs.SaveMCPServer(orig); err != nil {
		t.Fatal(err)
	}
	ts2, err := mgr.Toolset(orig)
	if err != nil {
		t.Fatal(err)
	}
	if ts1 != ts2 {
		t.Fatal("unchanged config should keep the cached toolset")
	}

	// 设置已更改：必须丢弃陈旧的工具集。
	changed := orig
	changed.Args = []string{"-u"}
	if _, err := cs.SaveMCPServer(changed); err != nil {
		t.Fatal(err)
	}
	ts3, err := mgr.Toolset(changed)
	if err != nil {
		t.Fatal(err)
	}
	if ts3 == ts1 {
		t.Fatal("changed config must rebuild the toolset")
	}
}
