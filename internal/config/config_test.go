package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

func TestSettingsCompactionPercentDefaultsAndClamps(t *testing.T) {
	ptr := func(v int) *int { return &v }
	tests := []struct {
		name string
		in   *int
		want int
	}{
		{name: "missing defaults to 65", in: nil, want: 65},
		{name: "explicit zero disables", in: ptr(0), want: 0},
		{name: "negative clamps to zero", in: ptr(-3), want: 0},
		{name: "over 100 clamps", in: ptr(140), want: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Settings{CompactionContextPercent: tt.in}
			s.normalize()
			if got := s.CompactionPercent(); got != tt.want {
				t.Fatalf("CompactionPercent() = %d, want %d", got, tt.want)
			}
			if s.CompactionContextPercent == nil {
				t.Fatal("normalize left the percent pointer nil")
			}
		})
	}
}

func TestConfigCloneDoesNotShareSettingsPointers(t *testing.T) {
	percent, autoTitle, autoCheck := 65, true, false
	cfg := Config{Settings: Settings{
		CompactionContextPercent: &percent,
		AutoTitle:                &autoTitle,
		AutoCheckUpdate:          &autoCheck,
	}}
	clone := cfg.Clone()
	*clone.Settings.CompactionContextPercent = 0
	*clone.Settings.AutoTitle = false
	*clone.Settings.AutoCheckUpdate = true
	if *cfg.Settings.CompactionContextPercent != 65 || !*cfg.Settings.AutoTitle || *cfg.Settings.AutoCheckUpdate {
		t.Fatalf("clone mutated the original settings pointers: %+v", cfg.Settings)
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

func TestSandboxDefaultsAndSessionOverrides(t *testing.T) {
	cfg := Default()
	if cfg.Settings.ApprovalPolicy != ApprovalPolicyRequestApproval {
		t.Fatalf("default approval policy = %q", cfg.Settings.ApprovalPolicy)
	}
	if cfg.Settings.SandboxProfile != SandboxProfileWorkspaceWrite {
		t.Fatalf("default sandbox profile = %q", cfg.Settings.SandboxProfile)
	}
	if cfg.Settings.SandboxMode != SandboxModeRequestApproval {
		t.Fatalf("default sandbox mode = %q", cfg.Settings.SandboxMode)
	}
	if cfg.Settings.SandboxNetwork != SandboxNetworkOff {
		t.Fatalf("default sandbox network = %q", cfg.Settings.SandboxNetwork)
	}
	session := ChatSession{}
	if got := session.EffectiveSandboxMode(cfg); got != SandboxModeRequestApproval {
		t.Fatalf("session default mode = %q", got)
	}
	if got := session.EffectiveApprovalPolicy(cfg); got != ApprovalPolicyRequestApproval {
		t.Fatalf("session default approval = %q", got)
	}
	if got := session.EffectiveSandboxProfile(cfg); got != SandboxProfileWorkspaceWrite {
		t.Fatalf("session default profile = %q", got)
	}
	if got := session.EffectiveSandboxNetwork(cfg); got != SandboxNetworkOff {
		t.Fatalf("session default network = %q", got)
	}
	session.SandboxMode = SandboxModeDangerFullAccess
	session.SandboxNetwork = SandboxNetworkOn
	if got := session.EffectiveSandboxMode(cfg); got != SandboxModeDangerFullAccess {
		t.Fatalf("session override mode = %q", got)
	}
	if got := session.EffectiveSandboxNetwork(cfg); got != SandboxNetworkOn {
		t.Fatalf("session override network = %q", got)
	}

	session.ApprovalPolicy = ApprovalPolicyAutoApprove
	session.SandboxProfile = SandboxProfileWorkspaceWrite
	if got := session.EffectiveApprovalPolicy(cfg); got != ApprovalPolicyAutoApprove {
		t.Fatalf("session split approval = %q", got)
	}
	if got := session.EffectiveSandboxProfile(cfg); got != SandboxProfileWorkspaceWrite {
		t.Fatalf("session split profile = %q", got)
	}
}

func TestSandboxModeNormalizationAndProfiles(t *testing.T) {
	for input, want := range map[string]string{
		"":                     SandboxModeRequestApproval,
		"workspace-write":      SandboxModeRequestApproval,
		"request":              SandboxModeRequestApproval,
		"auto":                 SandboxModeAutoApprove,
		"auto-approve":         SandboxModeAutoApprove,
		"danger-full-access":   SandboxModeDangerFullAccess,
		"unknown":              SandboxModeRequestApproval,
		"  REQUEST-APPROVAL  ": SandboxModeRequestApproval,
	} {
		if got := NormalizeSandboxMode(input); got != want {
			t.Errorf("NormalizeSandboxMode(%q) = %q, want %q", input, got, want)
		}
	}
	for input, want := range map[string]string{
		"":                    ApprovalPolicyRequestApproval,
		"request-approval":    ApprovalPolicyRequestApproval,
		"auto-approve":        ApprovalPolicyAutoApprove,
		"auto":                ApprovalPolicyAutoApprove,
		"never":               ApprovalPolicyNever,
		"danger-full-access":  ApprovalPolicyNever,
		"workspace-write":     ApprovalPolicyRequestApproval,
		"  REQUEST-APPROVAL ": ApprovalPolicyRequestApproval,
	} {
		if got := NormalizeApprovalPolicy(input); got != want {
			t.Errorf("NormalizeApprovalPolicy(%q) = %q, want %q", input, got, want)
		}
	}
	for input, want := range map[string]string{
		"":                      SandboxProfileWorkspaceWrite,
		"workspace-write":       SandboxProfileWorkspaceWrite,
		"request-approval":      SandboxProfileWorkspaceWrite,
		"auto-approve":          SandboxProfileWorkspaceWrite,
		"read-only":             SandboxProfileReadOnly,
		"danger-full-access":    SandboxProfileDangerFullAccess,
		"  DANGER-FULL-ACCESS ": SandboxProfileDangerFullAccess,
	} {
		if got := NormalizeSandboxProfile(input); got != want {
			t.Errorf("NormalizeSandboxProfile(%q) = %q, want %q", input, got, want)
		}
	}

	agent := AgentConfig{ConfirmTools: []string{"files"}}
	agent.ApplySandboxProfile(SandboxModeRequestApproval)
	if agent.SandboxMode != SandboxModeRequestApproval || !reflect.DeepEqual(agent.ConfirmTools, []string{"files", "shell"}) {
		t.Fatalf("request profile = %+v", agent)
	}
	agent.ApplySandboxProfile(SandboxModeAutoApprove)
	if agent.SandboxMode != SandboxModeAutoApprove || !reflect.DeepEqual(agent.ConfirmTools, []string{"files", "shell"}) {
		t.Fatalf("auto profile should preserve agent prompts: %+v", agent)
	}
	agent.ApplySandboxProfile(SandboxModeDangerFullAccess)
	if agent.SandboxMode != SandboxModeDangerFullAccess || agent.ConfirmTools != nil {
		t.Fatalf("full profile = %+v", agent)
	}

	agent = AgentConfig{ConfirmTools: []string{"files"}}
	agent.ApplyExecutionPolicy(ApprovalPolicyRequestApproval, SandboxProfileWorkspaceWrite)
	if agent.ApprovalPolicy != ApprovalPolicyRequestApproval ||
		agent.SandboxProfile != SandboxProfileWorkspaceWrite ||
		!reflect.DeepEqual(agent.ConfirmTools, []string{"files", "shell"}) {
		t.Fatalf("request execution policy = %+v", agent)
	}
	agent = AgentConfig{ConfirmTools: []string{"files"}}
	agent.ApplyExecutionPolicy(ApprovalPolicyAutoApprove, SandboxProfileWorkspaceWrite)
	if agent.ApprovalPolicy != ApprovalPolicyAutoApprove ||
		!reflect.DeepEqual(agent.ConfirmTools, []string{"files"}) {
		t.Fatalf("auto execution policy must keep explicit prompts: %+v", agent)
	}
	agent.ApplyExecutionPolicy(ApprovalPolicyNever, SandboxProfileWorkspaceWrite)
	if agent.ApprovalPolicy != ApprovalPolicyNever ||
		agent.SandboxProfile != SandboxProfileWorkspaceWrite ||
		agent.ConfirmTools != nil {
		t.Fatalf("never execution policy = %+v", agent)
	}
	agent.ApplyExecutionPolicy(ApprovalPolicyRequestApproval, SandboxProfileDangerFullAccess)
	if agent.SandboxProfile != SandboxProfileDangerFullAccess ||
		!reflect.DeepEqual(agent.ConfirmTools, []string{"files", "shell"}) {
		t.Fatalf("request + danger execution policy = %+v", agent)
	}
	agent.ApplyExecutionPolicy(ApprovalPolicyRequestApproval, SandboxProfileReadOnly)
	if agent.SandboxProfile != SandboxProfileReadOnly ||
		!reflect.DeepEqual(agent.ConfirmTools, []string{"files", "shell"}) {
		t.Fatalf("request + read-only execution policy = %+v", agent)
	}
}

func TestSettingsNormalizeMigratesLegacySandboxMode(t *testing.T) {
	settings := Settings{SandboxMode: "workspace-write"}
	settings.normalize()
	if settings.SandboxMode != SandboxModeRequestApproval {
		t.Fatalf("legacy mode migrated to %q", settings.SandboxMode)
	}
	if settings.ApprovalPolicy != ApprovalPolicyRequestApproval ||
		settings.SandboxProfile != SandboxProfileWorkspaceWrite {
		t.Fatalf("legacy split migration = %q/%q", settings.ApprovalPolicy, settings.SandboxProfile)
	}
	if settings.SandboxNetwork != SandboxNetworkOff {
		t.Fatalf("empty network normalized to %q", settings.SandboxNetwork)
	}
}

func TestExecutionPolicyLegacyMigrationMatrix(t *testing.T) {
	cases := []string{
		"",
		SandboxModeWorkspaceWrite,
		SandboxModeRequestApproval,
		SandboxModeAutoApprove,
		SandboxModeDangerFullAccess,
	}
	for _, legacy := range cases {
		t.Run(legacy, func(t *testing.T) {
			settings := map[string]any{"sandboxNetwork": SandboxNetworkOn}
			if legacy != "" {
				settings["sandboxMode"] = legacy
			}
			session := map[string]any{"id": "sess_old"}
			legacySession := map[string]any{"id": "sess_legacy"}
			if legacy != "" {
				legacySession["sandboxMode"] = legacy
			}
			raw, err := json.Marshal(map[string]any{
				"settings": settings,
				"sessions": []any{session, legacySession},
			})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			store, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			cfg := store.Get()
			wantApproval, wantProfile := ExecutionPolicyFromLegacySandboxMode(legacy)
			if cfg.Settings.ApprovalPolicy != wantApproval ||
				cfg.Settings.SandboxProfile != wantProfile {
				t.Fatalf("settings policy = %q/%q, want %q/%q",
					cfg.Settings.ApprovalPolicy, cfg.Settings.SandboxProfile, wantApproval, wantProfile)
			}
			for _, got := range cfg.Sessions {
				if got.ApprovalPolicy != wantApproval ||
					got.SandboxProfile != wantProfile ||
					got.SandboxNetwork != SandboxNetworkOn {
					t.Fatalf("session %s migrated to %+v", got.ID, got)
				}
			}
		})
	}
}

func TestExecutionPolicyNewFieldsTakePrecedence(t *testing.T) {
	raw := []byte(`{
		"settings":{
			"approvalPolicy":"auto-approve",
			"sandboxProfile":"workspace-write",
			"sandboxMode":"danger-full-access",
			"sandboxNetwork":"off"
		},
		"sessions":[{
			"id":"sess_split",
			"approvalPolicy":"request-approval",
			"sandboxProfile":"danger-full-access",
			"sandboxMode":"auto-approve"
		}]
	}`)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.Get()
	if cfg.Settings.ApprovalPolicy != ApprovalPolicyAutoApprove ||
		cfg.Settings.SandboxProfile != SandboxProfileWorkspaceWrite {
		t.Fatalf("settings new fields did not win: %+v", cfg.Settings)
	}
	if cfg.Settings.SandboxMode != SandboxModeAutoApprove {
		t.Fatalf("legacy settings projection = %q", cfg.Settings.SandboxMode)
	}
	session := cfg.Sessions[0]
	if session.ApprovalPolicy != ApprovalPolicyRequestApproval ||
		session.SandboxProfile != SandboxProfileDangerFullAccess ||
		session.EffectiveSandboxNetwork(cfg) != SandboxNetworkOn ||
		session.SandboxMode != SandboxModeDangerFullAccess {
		t.Fatalf("session new fields did not win: %+v", session)
	}
}

func TestLegacyFrontendSandboxModeWritesUpdateSplitPolicy(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *Config) {
		cfg.Settings.SandboxMode = SandboxModeAutoApprove
	}); err != nil {
		t.Fatal(err)
	}
	settings := store.Get().Settings
	if settings.ApprovalPolicy != ApprovalPolicyAutoApprove ||
		settings.SandboxProfile != SandboxProfileWorkspaceWrite {
		t.Fatalf("legacy-only write = %+v", settings)
	}

	if err := store.Update(func(cfg *Config) {
		cfg.Settings.ApprovalPolicy = ApprovalPolicyNever
		cfg.Settings.SandboxProfile = SandboxProfileWorkspaceWrite
	}); err != nil {
		t.Fatal(err)
	}
	settings = store.Get().Settings
	if settings.SandboxMode != SandboxModeDangerFullAccess {
		t.Fatalf("split -> legacy projection = %q", settings.SandboxMode)
	}
	if err := store.Update(func(cfg *Config) {
		cfg.Settings.SandboxMode = SandboxModeRequestApproval
	}); err != nil {
		t.Fatal(err)
	}
	settings = store.Get().Settings
	if settings.ApprovalPolicy != ApprovalPolicyRequestApproval ||
		settings.SandboxProfile != SandboxProfileWorkspaceWrite {
		t.Fatalf("legacy switch back did not map: %+v", settings)
	}
}

func TestEffectiveSandboxNetworkDangerIsTransient(t *testing.T) {
	cfg := Default()
	session := ChatSession{
		ApprovalPolicy: ApprovalPolicyNever,
		SandboxProfile: SandboxProfileDangerFullAccess,
		SandboxNetwork: SandboxNetworkOff,
	}
	if got := session.EffectiveSandboxNetwork(cfg); got != SandboxNetworkOn {
		t.Fatalf("danger network = %q, want on", got)
	}
	session.SandboxProfile = SandboxProfileWorkspaceWrite
	if got := session.EffectiveSandboxNetwork(cfg); got != SandboxNetworkOff {
		t.Fatalf("workspace network = %q, want persisted off", got)
	}
}

func TestLegacySessionsSnapshotSandboxModeOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"settings":{"sandboxMode":"auto-approve","sandboxNetwork":"on"},"sessions":[{"id":"sess_old","title":"旧会话"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Get()
	if len(got.Sessions) != 1 ||
		got.Sessions[0].ApprovalPolicy != ApprovalPolicyAutoApprove ||
		got.Sessions[0].SandboxProfile != SandboxProfileWorkspaceWrite ||
		got.Sessions[0].SandboxMode != SandboxModeAutoApprove ||
		got.Sessions[0].SandboxNetwork != SandboxNetworkOn {
		t.Fatalf("legacy session snapshot = %+v", got.Sessions)
	}
	if err := store.Update(func(cfg *Config) {
		cfg.Settings.SandboxMode = SandboxModeRequestApproval
		cfg.Settings.SandboxNetwork = SandboxNetworkOff
	}); err != nil {
		t.Fatal(err)
	}
	if got := store.Get().Sessions[0]; got.ApprovalPolicy != ApprovalPolicyAutoApprove ||
		got.SandboxProfile != SandboxProfileWorkspaceWrite ||
		got.SandboxMode != SandboxModeAutoApprove ||
		got.SandboxNetwork != SandboxNetworkOn {
		t.Fatalf("settings rewrite changed legacy session: %+v", got)
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

// TestThemePresetNormalizeAndPersist: older config files default to Nord,
// unknown presets fall back to Nord, and a valid preset survives a reload
// without disturbing the other appearance preferences.
func TestThemePresetNormalizeAndPersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"settings":{"themeMode":"system","fontScale":"lg","chatWidth":"wide","accent":"violet"},"providers":null,"agents":null,"teams":null,"skills":null,"mcpServers":null,"sessions":null}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get().Settings
	if got.ThemePreset != "nord" {
		t.Fatalf("default themePreset = %q, want nord", got.ThemePreset)
	}
	if got.ThemeMode != "system" || got.FontScale != "lg" || got.ChatWidth != "wide" {
		t.Fatalf("appearance preferences changed during migration: %+v", got)
	}

	if err := s.Update(func(cfg *Config) { cfg.Settings.ThemePreset = "paper" }); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get().Settings.ThemePreset; got != "paper" {
		t.Fatalf("themePreset not persisted: %q", got)
	}

	if err := s2.Update(func(cfg *Config) { cfg.Settings.ThemePreset = "unknown" }); err != nil {
		t.Fatal(err)
	}
	s3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s3.Get().Settings.ThemePreset; got != "nord" {
		t.Fatalf("invalid themePreset = %q, want nord fallback", got)
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

// Smart session naming is opt-out: existing configs without an autoTitle key
// must refine titles instead of leaving a raw first message such as "/stats".
func TestAutoTitleDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Settings().AutoTitleEnabled() {
		t.Fatal("auto-title must default to on when unset")
	}
	off := false
	if err := s.Update(func(cfg *Config) { cfg.Settings.AutoTitle = &off }); err != nil {
		t.Fatal(err)
	}
	if s.Settings().AutoTitleEnabled() {
		t.Fatal("auto-title must report off when the stored pointer is false")
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Settings().AutoTitleEnabled() {
		t.Fatal("explicit false must survive a config round trip")
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

func TestExecutionDisplayModeNormalization(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", ExecutionDisplayAuto},
		{" AUTO ", ExecutionDisplayAuto},
		{"concise", ExecutionDisplayConcise},
		{"Detailed", ExecutionDisplayDetailed},
		{"none", ExecutionDisplayNone},
		{"stale", ExecutionDisplayAuto},
	} {
		if got := NormalizeExecutionDisplayMode(tc.in); got != tc.want {
			t.Fatalf("NormalizeExecutionDisplayMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestChatWidthNormalization(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"narrow", ChatWidthNarrow},
		{" WIDE ", ChatWidthWide},
		{"medium", ChatWidthMedium},
		{"", ChatWidthMedium},
		{"stale", ChatWidthMedium},
	} {
		if got := NormalizeChatWidth(tc.in); got != tc.want {
			t.Fatalf("NormalizeChatWidth(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if got := Default().Settings.ChatWidth; got != ChatWidthMedium {
		t.Fatalf("default chat width = %q, want %q", got, ChatWidthMedium)
	}
}
