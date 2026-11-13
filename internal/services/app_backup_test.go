package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"

	"agentmate/internal/agentkit"
)

func newBackupTestService(t *testing.T) (*AppService, *config.Store, string) {
	t.Helper()
	dir := t.TempDir()
	workspace := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(cfg *config.Config) {
		cfg.Settings.WorkspaceDir = workspace
		cfg.Providers = append(cfg.Providers, config.ModelProvider{
			ID: "prov_1", Name: "P", Protocol: config.ProtocolOpenAI,
			BaseURL: "https://api.example.com/v1", APIKey: "sk-secret-123", IsDefault: true,
		})
		cfg.Agents = append(cfg.Agents, config.AgentConfig{ID: "agent_1", Name: "A", ProviderID: "prov_1"})
		cfg.RemoteAgents = append(cfg.RemoteAgents, config.RemoteAgentConfig{ID: "ra_1", Name: "R", CardURL: "http://x"})
		cfg.Teams = append(cfg.Teams, config.TeamConfig{ID: "team_1", Name: "T"})
		cfg.Skills = append(cfg.Skills, config.SkillConfig{ID: "skill_1", Name: "S"})
		cfg.Sessions = append(cfg.Sessions, config.ChatSession{ID: "sess_1", Title: "会话"})
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Services{Store: store, Kit: agentkit.NewKit(store, mcpmgr.New()), MCP: mcpmgr.New()}
	return NewAppService(svc, "", "", nil), store, workspace
}

func TestConfigBackupRoundTrip(t *testing.T) {
	app, store, _ := newBackupTestService(t)

	path, err := app.ExportConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "sk-secret-123") {
		t.Fatal("unmasked export must contain the API key")
	}

	// 清空配置，然后重新导入备份。
	if err := store.Update(func(cfg *config.Config) {
		*cfg = config.Config{}
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.ImportConfig(string(data)); err != nil {
		t.Fatal(err)
	}
	got := store.Get()
	if len(got.Providers) != 1 || got.Providers[0].APIKey != "sk-secret-123" {
		t.Fatalf("provider/key not restored: %+v", got.Providers)
	}
	if len(got.Agents) != 1 || len(got.RemoteAgents) != 1 || len(got.Teams) != 1 {
		t.Fatalf("config not fully restored: %+v", got)
	}
	// 预置的存储也会附带默认技能；我们的技能必须也在其中。
	var haveSkill bool
	for _, sk := range got.Skills {
		if sk.ID == "skill_1" {
			haveSkill = true
		}
	}
	if !haveSkill {
		t.Fatalf("custom skill lost in round trip: %+v", got.Skills)
	}
	if got.Settings.WorkspaceDir == "" {
		t.Fatal("settings not restored")
	}
}

func TestConfigBackupMasked(t *testing.T) {
	app, store, workspace := newBackupTestService(t)

	path, err := app.ExportConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sk-secret-123") {
		t.Fatal("masked export leaked the API key")
	}
	// 回归：掩码必须应用于副本。过去，掩码后的导出会清空当前生效的供应商密钥，导致
	// 之后每次请求都失败，直到重启。
	if got := store.Get(); len(got.Providers) != 1 || got.Providers[0].APIKey != "sk-secret-123" {
		t.Fatalf("masked export mutated the live config: %+v", got.Providers)
	}
	// OAuth 令牌缓存绝不能导出（属于机密且仅限本机）。
	err = store.Update(func(cfg *config.Config) {
		cfg.MCPTokens = map[string]config.MCPOAuthToken{"m1": {AccessToken: "at-secret", TokenURL: "https://x/token"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	path2, err := app.ExportConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data2), "at-secret") {
		t.Fatal("export leaked the MCP OAuth token cache")
	}
	// 密钥文件不得对组或其他用户可读，因为它可能保存机密。
	if filepath.Dir(path) != filepath.Join(workspace, "exports") {
		t.Fatalf("unexpected export dir: %s", path)
	}
}

func TestConfigImportValidation(t *testing.T) {
	app, store, _ := newBackupTestService(t)

	if err := app.ImportConfig("not json at all {"); err == nil {
		t.Fatal("expected parse error")
	}
	if err := app.ImportConfig(`{"version":99,"config":{}}`); err == nil || !strings.Contains(err.Error(), "版本") {
		t.Fatalf("expected version rejection, got %v", err)
	}
	if err := app.ImportConfig(`{"version":1}`); err == nil {
		t.Fatal("expected missing config rejection")
	}

	// 有效备份：当前生效配置中的会话在导入后仍会保留。
	if err := app.ImportConfig(`{"version":1,"config":{"settings":{"themeMode":"light"},"providers":[],"agents":[]}}`); err != nil {
		t.Fatal(err)
	}
	got := store.Get()
	if len(got.Sessions) != 1 || got.Sessions[0].ID != "sess_1" {
		t.Fatalf("sessions must survive import: %+v", got.Sessions)
	}
	if got.Settings.ThemeMode != "light" {
		t.Fatalf("imported settings missing: %+v", got.Settings)
	}
}

// TestSettingsA2AAutoStartPersisted:
// 自动启动的三项设置在保存/加载往返（config.json 序列化）后仍会保留。
func TestSettingsA2AAutoStartPersisted(t *testing.T) {
	dir := t.TempDir()
	store, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.A2AAutoStart = true
		cfg.Settings.A2AAgentID = "agent_1"
		cfg.Settings.A2APort = 18080
	}); err != nil {
		t.Fatal(err)
	}
	reopened, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := reopened.Get().Settings
	if !st.A2AAutoStart || st.A2AAgentID != "agent_1" || st.A2APort != 18080 {
		t.Fatalf("A2A auto-start settings not persisted: %+v", st)
	}
}

// TestSaveSettingsProxyValidation: 格式错误的代理 URL
// 在保存时会被拒绝，并给出清晰的错误。
func TestSaveSettingsProxyValidation(t *testing.T) {
	app, store, _ := newBackupTestService(t)
	st := store.Get().Settings

	st.HTTPProxy = "not a url"
	if err := app.S.Store.Update(func(cfg *config.Config) {}); err != nil {
		t.Fatal(err)
	}
	svc := NewConfigService(app.S)
	if err := svc.SaveSettings(st); err == nil {
		t.Fatal("expected invalid proxy rejection")
	}

	st.HTTPProxy = "http://127.0.0.1:7890"
	if err := svc.SaveSettings(st); err != nil {
		t.Fatalf("valid proxy rejected: %v", err)
	}
	if store.Get().Settings.HTTPProxy != "http://127.0.0.1:7890" {
		t.Fatal("proxy not persisted")
	}
}

func TestSaveSettingsCompactionValidation(t *testing.T) {
	app, _, _ := newBackupTestService(t)
	svc := NewConfigService(app.S)
	st := app.S.Store.Get().Settings
	negative := -1
	st.CompactionContextPercent = &negative
	if err := svc.SaveSettings(st); err == nil {
		t.Fatal("expected negative compaction percent rejection")
	}
	tooHigh := 101
	st.CompactionContextPercent = &tooHigh
	if err := svc.SaveSettings(st); err == nil {
		t.Fatal("expected over-100 compaction percent rejection")
	}
	st.CompactionContextPercent = nil
	st.CompactionInterval = -1
	if err := svc.SaveSettings(st); err == nil {
		t.Fatal("expected negative compaction interval rejection")
	}
}

// TestConfigBackupMaskedCoversEnvAndHeaders: 一个 “safe to share”
// 的导出必须同时剥离 MCP Env/Headers（它们通常携带 API 密钥和 Authorization
// 头），而不只是供应商密钥和认证令牌。
func TestConfigBackupMaskedCoversEnvAndHeaders(t *testing.T) {
	app, store, _ := newBackupTestService(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.MCPServers = append(cfg.MCPServers, config.MCPServerConfig{
			ID: "mcp_1", Name: "M", Transport: config.MCPStdio, Command: "/bin/tool",
			Env:     map[string]string{"OPENAI_API_KEY": "env-secret-1"},
			Headers: map[string]string{"Authorization": "Bearer hdr-secret-2"},
		})
	}); err != nil {
		t.Fatal(err)
	}
	path, err := app.ExportConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"env-secret-1", "hdr-secret-2"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("masked export leaked %s", secret)
		}
	}
}

// TestConfigImportPreservesLocalState: 固化导入语义：OAuth
// 令牌缓存是机器本地的且会保留，旧 schema 的备份会以规范化形式输出（设置默认值
// + 遗留价格迁移），并且已更改/已删除的 MCP 服务器会丢弃其缓存的连接。
func TestConfigImportPreservesLocalState(t *testing.T) {
	app, store, _ := newBackupTestService(t)
	if err := store.Update(func(cfg *config.Config) {
		cfg.MCPTokens = map[string]config.MCPOAuthToken{"m1": {AccessToken: "at-local", TokenURL: "https://x/token"}}
		cfg.Settings.ThemeMode = ""
		cfg.Providers[0].PriceIn = 2.0 // 遗留的供应商级价格
	}); err != nil {
		t.Fatal(err)
	}

	// 来自旧 schema 的备份：没有令牌、没有按模型价格、主题为空。
	backup := `{"version":1,"config":{
		"settings":{"workspaceDir":"/tmp/ws"},
		"providers":[{"id":"prov_1","name":"P","protocol":"openai","baseUrl":"https://api.example.com/v1","apiKey":"k","priceIn":2.0,"priceOut":8.0,"models":[{"id":"m1"}]}],
		"mcpServers":[{"id":"mcp_1","name":"M","transport":"stdio","command":"/bin/tool"}],
		"agents":[]}}`
	if err := app.ImportConfig(backup); err != nil {
		t.Fatal(err)
	}
	got := store.Get()
	if got.MCPTokens["m1"].AccessToken != "at-local" {
		t.Fatalf("local OAuth token cache wiped by import: %+v", got.MCPTokens)
	}
	if got.Settings.ThemeMode == "" {
		t.Fatalf("imported settings not normalized: %+v", got.Settings)
	}
	if len(got.Providers) != 1 || len(got.Providers[0].Models) != 1 {
		t.Fatalf("imported provider unexpected: %+v", got.Providers)
	}
	mi := got.Providers[0].Models[0]
	if mi.PriceIn != 2.0 || mi.PriceOut != 8.0 {
		t.Fatalf("legacy provider price not migrated down: %+v", mi)
	}
	if got.Providers[0].PriceIn != 0 {
		t.Fatalf("legacy provider-level price not cleared: %+v", got.Providers[0])
	}

	// 配置已更改（或已消失）的服务器必须丢弃其缓存的工具集：第二次导入会将
	// /bin/tool 替换为 /bin/tool2。
	cfg := `{"version":1,"config":{
		"settings":{},
		"providers":[],
		"mcpServers":[{"id":"mcp_1","name":"M","transport":"stdio","command":"/bin/tool2"}],
		"agents":[]}}`
	if err := app.ImportConfig(cfg); err != nil {
		t.Fatal(err)
	}
}
