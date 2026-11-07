package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Defaults are seeded on first run.
	if len(s.Get().Skills) == 0 {
		t.Fatal("expected default skills to be seeded")
	}
	if s.Get().Settings.WorkspaceDir == "" {
		t.Fatal("expected default workspace dir")
	}

	err = s.Update(func(cfg *Config) {
		cfg.Providers = append(cfg.Providers, ModelProvider{
			ID: "p1", Name: "Test", Protocol: ProtocolOpenAI, Models: []ModelInfo{{ID: "m1"}}, IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, AgentConfig{
			ID: "a1", Name: "Agent X", ProviderID: "p1", Model: "m1",
			SkillIDs: []string{}, MCPServerIDs: []string{}, BuiltinTools: []string{"time"},
			CreatedAt: time.Now().UTC(),
		})
		cfg.Sessions = append(cfg.Sessions, ChatSession{
			ID: "s1", Title: "hello", TargetType: "agent", TargetID: "a1",
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	// Reopen: data must persist.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get()
	if len(got.Providers) != 1 || got.Providers[0].Name != "Test" {
		t.Fatalf("providers not persisted: %+v", got.Providers)
	}
	if len(got.Agents) != 1 || got.Agents[0].BuiltinTools[0] != "time" {
		t.Fatalf("agents not persisted: %+v", got.Agents)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].Title != "hello" {
		t.Fatalf("sessions not persisted: %+v", got.Sessions)
	}
}

func TestProjectsRoundTripAndCloneIndependent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(func(cfg *Config) {
		cfg.Projects = append(cfg.Projects, Project{
			ID: "proj_1", Name: "agentmate", RootDir: `D:\projects\agentmate`,
			CreatedAt: time.Now().UTC(),
		})
		cfg.Sessions = append(cfg.Sessions, ChatSession{ID: "s1", Title: "p", ProjectID: "proj_1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get()
	if len(got.Projects) != 1 || got.Projects[0].Name != "agentmate" {
		t.Fatalf("projects not persisted: %+v", got.Projects)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ProjectID != "proj_1" {
		t.Fatalf("session project binding not persisted: %+v", got.Sessions)
	}
	clone := got.Clone()
	clone.Projects[0].Name = "changed"
	if got.Projects[0].Name != "agentmate" {
		t.Fatalf("Clone shares the Projects backing array: %+v", got.Projects)
	}
}

// TestSidebarSettingsNormalizeAndPersist: older config.json files without the
// sidebar fields load with defaults; both fields persist; unknown grouping
// values fall back to "time" on the next load.
func TestSidebarSettingsNormalizeAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"settings":{"themeMode":"dark"},"providers":null,"agents":null,"teams":null,"skills":null,"mcpServers":null,"sessions":null}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get().Settings.SidebarGrouping; got != "time" {
		t.Fatalf("default sidebarGrouping = %q, want time", got)
	}
	if s.Get().Settings.SidebarCollapsed {
		t.Fatal("default sidebarCollapsed = true, want false")
	}

	if err := s.Update(func(cfg *Config) {
		cfg.Settings.SidebarGrouping = "project"
		cfg.Settings.SidebarCollapsed = true
	}); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get().Settings.SidebarGrouping; got != "project" {
		t.Fatalf("sidebarGrouping not persisted: %q", got)
	}
	if !s2.Get().Settings.SidebarCollapsed {
		t.Fatal("sidebarCollapsed not persisted")
	}

	if err := s2.Update(func(cfg *Config) { cfg.Settings.SidebarGrouping = "bogus" }); err != nil {
		t.Fatal(err)
	}
	s3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s3.Get().Settings.SidebarGrouping; got != "time" {
		t.Fatalf("invalid sidebarGrouping = %q, want time fallback", got)
	}
}

func TestStoreGetReturnsIndependentCopy(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(func(cfg *Config) {
		cfg.Providers = append(cfg.Providers, ModelProvider{
			ID: "p1", APIKey: "sk-live", Models: []ModelInfo{{ID: "m1"}},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.Get()
	cfg.Providers[0].APIKey = ""
	cfg.Providers[0].Models[0].ID = "clobbered"
	if live := s.Get(); live.Providers[0].APIKey != "sk-live" || live.Providers[0].Models[0].ID != "m1" {
		t.Fatalf("Get exposed live storage: %+v", live.Providers[0])
	}
}

func TestOpenCorruptConfigKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Get().Skills) == 0 {
		t.Fatal("expected fresh defaults after corrupt config")
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatal("expected .bak backup of corrupt config")
	}
}

func TestSkillsDir(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if s.SkillsDir() == "" || filepath.Base(s.SkillsDir()) != "skills" {
		t.Fatalf("unexpected skills dir: %q", s.SkillsDir())
	}
}

func TestLegacyModelsAndPricesMigrate(t *testing.T) {
	dir := t.TempDir()
	// Old schema: models are plain strings; prices live on the provider.
	legacy := `{
	  "settings": {"workspaceDir": "/tmp/w", "language": "zh-CN"},
	  "providers": [
	    {"id":"p1","name":"MiniMax","protocol":"anthropic",
	     "baseUrl":"https://api.minimaxi.com/anthropic","apiKey":"k",
	     "models":["MiniMax-M3",{"id":"MiniMax-M2","multimodal":true,"priceIn":2,"priceOut":8}],
	     "isDefault":true,"priceIn":1,"priceOut":4}
	  ]
	}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Get().Providers[0]
	if len(p.Models) != 2 || p.Models[0].ID != "MiniMax-M3" || p.Models[1].ID != "MiniMax-M2" {
		t.Fatalf("models not migrated: %+v", p.Models)
	}
	if p.Models[0].PriceIn != 1 || p.Models[0].PriceOut != 4 {
		t.Fatalf("provider price not copied down: %+v", p.Models[0])
	}
	if !p.Models[1].Multimodal || p.Models[1].PriceIn != 2 || p.Models[1].PriceOut != 8 {
		t.Fatalf("explicit model metadata overwritten: %+v", p.Models[1])
	}
	if p.PriceIn != 0 || p.PriceOut != 0 {
		t.Fatalf("legacy provider prices not cleared: %+v", p)
	}

	// Migration is persisted: reopening must not lose or duplicate it.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p2 := s2.Get().Providers[0]
	if p2.Models[0].PriceIn != 1 || p2.Models[0].PriceOut != 4 || p2.PriceIn != 0 {
		t.Fatalf("migration not persisted: %+v", p2)
	}
}

func TestModelInfoUnmarshalRejectsBadObject(t *testing.T) {
	var m ModelInfo
	if err := json.Unmarshal([]byte(`{"id":"x","contextWindow":128000}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "x" || m.ContextWindow != 128000 {
		t.Fatalf("got %+v", m)
	}
	// Unknown fields are ignored; the nameless entry is dropped by migrateLegacy.
	if err := json.Unmarshal([]byte(`{"nope":1}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "" {
		t.Fatalf("got %+v, want empty entry", m)
	}
}

// TestOpenCorruptConfigBackupIsPrivate: the .bak of a corrupt config holds the
// original bytes (possibly API keys), so it must not be world-readable.
func TestOpenCorruptConfigBackupIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无 POSIX 权限位语义（Chmod 0600 不生效）；文件仍受用户目录 ACL 保护")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path + ".bak")
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf(".bak mode = %o, want 600", perm)
	}
}

// TestUpdateFailedSaveDoesNotMutateMemory: a failed disk write must not leave
// the live config diverged from disk (the old in-place mutation survived in
// memory until the next restart silently reverted it).
func TestUpdateFailedSaveDoesNotMutateMemory(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	before := s.Get().Skills[0].Name
	// Make the rename target impossible: config.json becomes a directory, so
	// the atomic replace fails after the tmp write.
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "config.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(func(cfg *Config) {
		cfg.Skills[0].Name = "clobbered"
	}); err == nil {
		t.Fatal("expected the save to fail")
	}
	if got := s.Get().Skills[0].Name; got != before {
		t.Fatalf("memory mutated despite failed save: %q", got)
	}
}

// The silent update check is opt-out: a fresh config has no autoCheckUpdate
// key and must still report enabled, or every install would be stuck on the
// version it was installed with.
func TestUpdateAutoCheckDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Settings().UpdateAutoCheckEnabled() {
		t.Fatal("update auto-check must default to on when unset")
	}
	if s.Settings().SkippedVersion != "" {
		t.Fatalf("skippedVersion = %q, want empty", s.Settings().SkippedVersion)
	}
	off := false
	if err := s.Update(func(cfg *Config) { cfg.Settings.AutoCheckUpdate = &off }); err != nil {
		t.Fatal(err)
	}
	if s.Settings().UpdateAutoCheckEnabled() {
		t.Fatal("auto-check must report off when the stored pointer is false")
	}
	// A pointer is required here: the setting must survive a round trip
	// through JSON as an explicit false, not decay back to the unset default.
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Settings().AutoCheckUpdate == nil {
		t.Fatal("autoCheckUpdate=false was dropped on save")
	}
	if reopened.Settings().UpdateAutoCheckEnabled() {
		t.Fatal("auto-check must stay off after a reload")
	}
}

// TestResolveAgentModelInheritsDefault pins the #39 contract: an agent defined
// by name + description + prompt alone follows the global default provider and
// its first model, while explicit choices are never silently rewritten.
func TestResolveAgentModelInheritsDefault(t *testing.T) {
	cfg := Config{Providers: []ModelProvider{
		{ID: "p1", Name: "First", Models: []ModelInfo{{ID: "m1"}, {ID: "m2"}}},
		{ID: "p2", Name: "Default", IsDefault: true, Models: []ModelInfo{{ID: "d1"}}},
	}}

	providerID, modelID := cfg.ResolveAgentModel(AgentConfig{Name: "仅语义"})
	if providerID != "p2" || modelID != "d1" {
		t.Fatalf("blank agent should inherit default provider/model, got %q/%q", providerID, modelID)
	}

	// No provider is marked default: the first configured one is the default.
	noDefault := Config{Providers: []ModelProvider{
		{ID: "p1", Models: []ModelInfo{{ID: "m1"}}},
	}}
	providerID, modelID = noDefault.ResolveAgentModel(AgentConfig{})
	if providerID != "p1" || modelID != "m1" {
		t.Fatalf("first provider should act as default, got %q/%q", providerID, modelID)
	}

	// Explicit values win and are not rebound.
	providerID, modelID = cfg.ResolveAgentModel(AgentConfig{ProviderID: "p1", Model: "m2"})
	if providerID != "p1" || modelID != "m2" {
		t.Fatalf("explicit provider/model changed: %q/%q", providerID, modelID)
	}

	// An explicit provider with a blank model uses that provider's first model.
	providerID, modelID = cfg.ResolveAgentModel(AgentConfig{ProviderID: "p2"})
	if providerID != "p2" || modelID != "d1" {
		t.Fatalf("blank model should use the chosen provider's first model, got %q/%q", providerID, modelID)
	}

	// A dangling provider stays explicit; the build layer reports it instead
	// of silently moving the agent to another provider.
	providerID, modelID = cfg.ResolveAgentModel(AgentConfig{ProviderID: "gone"})
	if providerID != "gone" || modelID != "" {
		t.Fatalf("dangling provider must stay untouched, got %q/%q", providerID, modelID)
	}

	// Nothing configured: values stay blank so callers can show an actionable
	// "add a provider first" error.
	providerID, modelID = (Config{}).ResolveAgentModel(AgentConfig{})
	if providerID != "" || modelID != "" {
		t.Fatalf("empty config should resolve to empty, got %q/%q", providerID, modelID)
	}
}
