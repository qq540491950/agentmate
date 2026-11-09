package services

import (
	"testing"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
)

func TestSaveMCPServerReturnsNormalizedServer(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	mgr := mcpmgr.New()
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mgr), MCP: mgr}
	cs := NewConfigService(svc)

	saved, err := cs.SaveMCPServer(config.MCPServerConfig{
		Name:      "  tavily  ",
		Transport: config.MCPStdio,
		Command:   " npx ",
		Args:      []string{" -y ", "tavily-mcp"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("new server ID must be returned to the caller")
	}
	if saved.Name != "tavily" || saved.Command != "npx" {
		t.Fatalf("normalized server not returned: name=%q command=%q", saved.Name, saved.Command)
	}
	if len(saved.Args) != 2 || saved.Args[0] != "-y" {
		t.Fatalf("normalized args not returned: %q", saved.Args)
	}
}
