package services

import (
	"os"
	"strings"
	"testing"

	"agentmate/internal/config"
)

// TestAgentShareRoundTrip 导出一个 Agent 并（两次）重新导入它，以验证全新
// ID、名称冲突后缀处理以及供应商重新关联元数据。
func TestAgentShareRoundTrip(t *testing.T) {
	app, store, workspace := newBackupTestService(t)

	path, err := app.ExportAgent("agent_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(path, "agents") {
		t.Fatalf("unexpected export path: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	firstRes, err := app.ImportAgent(string(data))
	if err != nil {
		t.Fatal(err)
	}
	first := firstRes.Agent
	if first.ID == "agent_1" || first.ID == "" {
		t.Fatalf("imported agent must get a fresh ID: %q", first.ID)
	}
	// 原始 "A" 仍然存在，因此第一次导入就已经获得后缀。
	if first.Name != "A（2）" {
		t.Fatalf("name conflict suffix missing on first import: %q", first.Name)
	}

	secondRes, err := app.ImportAgent(string(data))
	if err != nil {
		t.Fatal(err)
	}
	second := secondRes.Agent
	if second.Name != "A（3）" {
		t.Fatalf("name conflict suffix missing: %q", second.Name)
	}
	if second.ID == first.ID {
		t.Fatal("imported agents must have distinct IDs")
	}

	// 导出未知 Agent 会报错。
	if _, err := app.ExportAgent("nope"); err == nil {
		t.Fatal("expected error exporting unknown agent")
	}
	// 错误的共享数据会被拒绝。
	if _, err := app.ImportAgent("junk{"); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := app.ImportAgent(`{"version":9,"agent":{"name":"x"}}`); err == nil {
		t.Fatal("expected version rejection")
	}
	_ = store
	_ = workspace
}

// TestImportAgentStripsDanglingRefs：在这台机器上不存在的技能 / MCP
// 引用会在导入时被剥离（并发出警告）——否则导入的 Agent
// 的每个回合都会直接构建失败。
func TestImportAgentStripsDanglingRefs(t *testing.T) {
	app, store, _ := newBackupTestService(t)
	if err := store.Update(func(cfg *config.Config) {
		for i := range cfg.Skills {
			if cfg.Skills[i].ID == "skill_1" {
				cfg.Skills[i].Description = "fixture skill"
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	data := `{"version":1,"agent":{` +
		`"id":"agent_x","name":"访客","description":"d","providerId":"prov_1",` +
		`"model":"m",` +
		`"skillIds":["skill_1","skill_missing"],` +
		`"mcpServerIds":["mcp_missing"],` +
		`"builtinTools":["time"]}}`
	res, err := app.ImportAgent(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Agent.SkillIDs) != 1 || res.Agent.SkillIDs[0] != "skill_1" {
		t.Fatalf("dangling skill ref not stripped: %v", res.Agent.SkillIDs)
	}
	if len(res.Agent.MCPServerIDs) != 0 {
		t.Fatalf("dangling MCP ref not stripped: %v", res.Agent.MCPServerIDs)
	}
	if len(res.Warnings) != 2 {
		t.Fatalf("expected 2 warnings, got %v", res.Warnings)
	}
	if _, err := app.S.Kit.BuildAgent(res.Agent); err != nil {
		t.Fatalf("imported agent must build after cleanup: %v", err)
	}
}

// TestRemoteAgentShareRoundTrip 复现了远程 A2A Agent 的 Agent 共享流程：导出 ->
// 以全新 ID 导入、冲突后缀、验证。
func TestRemoteAgentShareRoundTrip(t *testing.T) {
	app, store, _ := newBackupTestService(t)

	path, err := app.ExportRemoteAgent("ra_1")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	first, err := app.ImportRemoteAgent(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "ra_1" || first.ID == "" {
		t.Fatalf("imported remote must get a fresh ID: %q", first.ID)
	}
	if first.Name != "R（2）" {
		t.Fatalf("name conflict suffix missing: %q", first.Name)
	}
	_ = store
	if _, err := app.ExportRemoteAgent("missing"); err == nil {
		t.Fatal("expected error exporting unknown remote agent")
	}
}
