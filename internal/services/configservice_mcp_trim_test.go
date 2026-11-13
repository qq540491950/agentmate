package services

import (
	"testing"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

// TestSaveMCPServerNormalizesWhitespace: 从 UI 粘贴的空白不得进入持久化配置——对
// "npx " 执行 exec 时，即使已安装 npx，LookPath 也会失败。
func TestSaveMCPServerNormalizesWhitespace(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mgr := mcpmgr.New()
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mgr), MCP: mgr}
	cs := NewConfigService(svc)

	saved, err := cs.SaveMCPServer(config.MCPServerConfig{
		ID:        "mcp_1",
		Name:      "  tavily  ",
		Transport: config.MCPStdio,
		Command:   " npx ",
		Args:      []string{" -y ", "tavily-mcp"},
		URL:       " https://example.com ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID != "mcp_1" || saved.Name != "tavily" || saved.Command != "npx" {
		t.Fatalf("normalized server not returned: id=%q name=%q command=%q", saved.ID, saved.Name, saved.Command)
	}
	var got config.MCPServerConfig
	for _, s := range store.Get().MCPServers {
		if s.ID == "mcp_1" {
			got = s
		}
	}
	if got.Command != "npx" || got.Name != "tavily" || got.URL != "https://example.com" {
		t.Fatalf("fields not trimmed: command=%q name=%q url=%q", got.Command, got.Name, got.URL)
	}
	if len(got.Args) != 2 || got.Args[0] != "-y" || got.Args[1] != "tavily-mcp" {
		t.Fatalf("args not trimmed: %q", got.Args)
	}
}
