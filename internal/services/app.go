package services

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gorm.io/gorm"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
	"agentmate/internal/sandbox"
)

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Second) }

// appName/userID 镜像 ChatService 的固定本地身份（单用户应用）。
const (
	appName = "agentmate"
	userID  = "local"
)

// AppService 暴露通用应用信息。
type AppService struct {
	S           *Services
	logPath     string
	auditPath   string
	sessionsDB  string
	knowledgeDB string
	gormDB      *gorm.DB
}

// SetLogPath 将诊断日志读取器指向应用日志文件。
func (a *AppService) SetLogPath(path string) { a.logPath = path }

// NewAppService 创建该服务。存储句柄保留在构造函数中，而不是通过导出的 setter
// 传入：每个导出方法都会作为 Wails RPC 暴露，而 *gorm.DB 参数会将整个 GORM
// 类型图拖入前端绑定。
func NewAppService(s *Services, sessionsDB, knowledgeDB string, gormDB *gorm.DB) *AppService {
	return &AppService{
		S:           s,
		sessionsDB:  sessionsDB,
		knowledgeDB: knowledgeDB,
		gormDB:      gormDB,
	}
}

// AppVersion 由 main 注入（单一事实来源）。
var AppVersion = "dev"

// AppInfo 返回静态应用元数据。platform
// 为问题报告预填提供数据，使测试人员绝不需要手动输入自己的环境。
func (a *AppService) AppInfo() map[string]string {
	return map[string]string{
		"name":     "AgentMate",
		"version":  AppVersion,
		"adk":      "google adk-go v2",
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
	}
}

// backupSchemaVersion 是导出格式版本。发生破坏性变更时递增；ImportConfig
// 会拒绝来自较新应用版本的文件。
const backupSchemaVersion = 1

// maxBackupBytes 为导入载荷设置上限（10MB，远超任何真实配置）。
const maxBackupBytes = 10 << 20

// maxLogTailLines 限制 RecentLogs 的输出行数。
const maxLogTailLines = 300

// SetToolAuditPath 将诊断读取器指向工具审计 JSONL 文件。
func (a *AppService) SetToolAuditPath(path string) { a.auditPath = path }

// RecentLogs 返回应用日志的最后若干行，用于应用内诊断。
func (a *AppService) RecentLogs(tail int) ([]string, error) {
	return readTail(a.logPath, tail)
}

// RecentToolAudit 返回工具审计 JSONL 的最后若干行。
func (a *AppService) RecentToolAudit(tail int) ([]string, error) {
	return readTail(a.auditPath, tail)
}

// readTail 返回文本文件末尾的非空行。
func readTail(path string, tail int) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("路径未配置")
	}
	if tail <= 0 || tail > maxLogTailLines {
		tail = 200
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{"（文件为空或尚未创建）"}, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return lines, nil
}

// StorageInfo 报告各应用数据库在磁盘上的大小。
type StorageInfo struct {
	SessionsDBBytes  int64 `json:"sessionsDbBytes"`
	KnowledgeDBBytes int64 `json:"knowledgeDbBytes"`
}

// StorageStats 返回当前数据库文件大小（文件不存在时为 -1）。
func (a *AppService) StorageStats() StorageInfo {
	info := StorageInfo{SessionsDBBytes: -1, KnowledgeDBBytes: -1}
	if st, err := os.Stat(a.sessionsDB); err == nil {
		info.SessionsDBBytes = st.Size()
	}
	if st, err := os.Stat(a.knowledgeDB); err == nil {
		info.KnowledgeDBBytes = st.Size()
	}
	return info
}

type SandboxStatusInfo struct {
	Available  bool   `json:"available"`
	Backend    string `json:"backend"`
	Error      string `json:"error"`
	ProfileSID string `json:"profileSid"`
}

func (a *AppService) SandboxStatus() SandboxStatusInfo {
	st := sandbox.BackendStatus()
	return SandboxStatusInfo{
		Available:  st.Available,
		Backend:    st.Backend,
		Error:      st.Error,
		ProfileSID: st.ProfileSID,
	}
}

// VacuumSessions 压缩会话数据库（回收被裁剪/重新生成的历史所占用的空间）。在没有运行进行时是安
// 全的；调用方仅会在瞬时的 "database is locked"
// 类错误上发生竞态，我们会将这些错误上报。
func (a *AppService) VacuumSessions() error {
	if a.gormDB == nil {
		return fmt.Errorf("会话数据库不可用")
	}
	return a.gormDB.Exec("VACUUM").Error
}

// MemoryInfo 报告长期记忆存储的大小。
type MemoryInfo struct {
	Count int64 `json:"count"`
}

// MemoryStats 返回长期记忆条目的数量。
func (a *AppService) MemoryStats() (*MemoryInfo, error) {
	if a.S.Memory == nil {
		return nil, fmt.Errorf("记忆服务不可用")
	}
	n, err := a.S.Memory.Count(appName, userID)
	if err != nil {
		return nil, err
	}
	return &MemoryInfo{Count: n}, nil
}

// MemoryClear 清空本地用户的全部长期记忆条目。
func (a *AppService) MemoryClear() error {
	if a.S.Memory == nil {
		return fmt.Errorf("记忆服务不可用")
	}
	return a.S.Memory.Clear(appName, userID)
}

// agentShare 是可移植的单个 Agent 共享文档。被引用的供应商 ID
// 随其一起传递，以便导入器可以重新关联或标记该供应商 ID。
type agentShare struct {
	Version int                `json:"version"`
	Agent   config.AgentConfig `json:"agent"`
}

// ImportAgentResult 是导入结果：已存储的
// Agent，以及导入期间应用的任何本机修复（剥离悬空引用），以便 UI
// 能够展示调整了什么。
type ImportAgentResult struct {
	Agent    config.AgentConfig `json:"agent"`
	Warnings []string           `json:"warnings,omitempty"`
}

// ExportAgent 将一个 Agent（自身不含机密——Agent 不携带密钥）作为可共享的 JSON
// 文件写入 <workspace>/exports/agents，并返回文件路径。
func (a *AppService) ExportAgent(agentID string) (string, error) {
	cfg := a.S.Store.Get()
	var agent *config.AgentConfig
	for i := range cfg.Agents {
		if cfg.Agents[i].ID == agentID {
			agent = &cfg.Agents[i]
			break
		}
	}
	if agent == nil {
		return "", fmt.Errorf("agent %q not found", agentID)
	}
	data, err := json.MarshalIndent(agentShare{Version: backupSchemaVersion, Agent: *agent}, "", "  ")
	if err != nil {
		return "", err
	}
	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// Agent 名称来自配置（以及导入的共享文件），因此可能包含路径分隔符。像聊天导出那样对
	// 其进行清理。
	name := sanitizeExportName(agent.Name)
	if name == "" {
		name = "agent"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.agent.json", name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ImportAgent 使用全新 ID 从共享文档添加一个 Agent。当存在同名 Agent 时，会追加
// (2)/(3)… 后缀。
func (a *AppService) ImportAgent(data string) (ImportAgentResult, error) {
	if len(data) > maxBackupBytes {
		return ImportAgentResult{}, fmt.Errorf("文件过大")
	}
	var share agentShare
	if err := json.Unmarshal([]byte(data), &share); err != nil {
		return ImportAgentResult{}, fmt.Errorf("分享文件解析失败: %w", err)
	}
	if share.Version > backupSchemaVersion {
		return ImportAgentResult{}, fmt.Errorf("分享版本 v%d 高于当前支持的 v%d，请先升级应用", share.Version, backupSchemaVersion)
	}
	imported := share.Agent
	imported.ID = ""
	imported.CreatedAt = time.Time{}
	if imported.Name == "" {
		return ImportAgentResult{}, fmt.Errorf("分享文件缺少 Agent 名称")
	}
	// 在插入之前，先剥离本机上不存在的引用：否则，严格构建契约会在导入的 Agent
	// 的每个回合中失败，而用户无法知道应分离哪些引用。
	cfg := a.S.Store.Get()
	var warnings []string
	knownSkills := make(map[string]bool, len(cfg.Skills))
	for _, s := range cfg.Skills {
		knownSkills[s.ID] = true
	}
	knownMCP := make(map[string]bool, len(cfg.MCPServers))
	for _, s := range cfg.MCPServers {
		knownMCP[s.ID] = true
	}
	var skillWarnings, mcpWarnings []string
	imported.SkillIDs, skillWarnings = stripMissingRefs(imported.SkillIDs, knownSkills, "技能")
	imported.MCPServerIDs, mcpWarnings = stripMissingRefs(imported.MCPServerIDs, knownMCP, "MCP 服务器")
	warnings = append(append(warnings, skillWarnings...), mcpWarnings...)
	err := a.S.Store.Update(func(cfg *config.Config) {
		nameExists := func(name string) bool {
			for _, ag := range cfg.Agents {
				if ag.Name == name {
					return true
				}
			}
			return false
		}
		base := imported.Name
		for n := 2; nameExists(imported.Name); n++ {
			imported.Name = fmt.Sprintf("%s（%d）", base, n)
		}
		imported.ID = newID("agent_")
		imported.CreatedAt = nowUTC()
		cfg.Agents = append(cfg.Agents, imported)
	})
	if err != nil {
		return ImportAgentResult{}, err
	}
	return ImportAgentResult{Agent: imported, Warnings: warnings}, nil
}

// stripMissingRefs 仅保留 known 中存在的 ids，并为每个被剥离的 id
// 报告一条警告。nil/空输入会原样返回。
func stripMissingRefs(ids []string, known map[string]bool, label string) (kept, warnings []string) {
	if len(ids) == 0 {
		return ids, nil
	}
	kept = make([]string, 0, len(ids))
	for _, id := range ids {
		if known[id] {
			kept = append(kept, id)
			continue
		}
		warnings = append(warnings, "已移除不存在的"+label+"引用: "+id)
	}
	return kept, warnings
}

// remoteShare 是可移植的单个远程 Agent 分享文档。
type remoteShare struct {
	Version int                       `json:"version"`
	Remote  *config.RemoteAgentConfig `json:"remote"`
}

// ExportRemoteAgent 将一个远程 A2A Agent 作为可分享的 JSON 文件写入
// <workspace>/exports/remotes，并返回该文件路径。
func (a *AppService) ExportRemoteAgent(id string) (string, error) {
	cfg := a.S.Store.Get()
	var remote *config.RemoteAgentConfig
	for i := range cfg.RemoteAgents {
		if cfg.RemoteAgents[i].ID == id {
			remote = &cfg.RemoteAgents[i]
			break
		}
	}
	if remote == nil {
		return "", fmt.Errorf("remote agent %q not found", id)
	}
	data, err := json.MarshalIndent(remoteShare{Version: backupSchemaVersion, Remote: remote}, "", "  ")
	if err != nil {
		return "", err
	}
	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports", "remotes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := sanitizeExportName(remote.Name)
	if name == "" {
		name = "remote"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.remote.json", name))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// ImportRemoteAgent 从分享文档中添加一个远程 Agent，并为其分配全新的
// ID；名称冲突时会追加数字后缀。
func (a *AppService) ImportRemoteAgent(data string) (config.RemoteAgentConfig, error) {
	if len(data) > maxBackupBytes {
		return config.RemoteAgentConfig{}, fmt.Errorf("文件过大")
	}
	var share remoteShare
	if err := json.Unmarshal([]byte(data), &share); err != nil {
		return config.RemoteAgentConfig{}, fmt.Errorf("分享文件解析失败: %w", err)
	}
	if share.Version > backupSchemaVersion {
		return config.RemoteAgentConfig{}, fmt.Errorf("分享版本 v%d 高于当前支持的 v%d，请先升级应用", share.Version, backupSchemaVersion)
	}
	if share.Remote == nil {
		return config.RemoteAgentConfig{}, fmt.Errorf("分享文件缺少 remote 字段")
	}
	imported := *share.Remote
	imported.ID = ""
	imported.CreatedAt = time.Time{}
	if imported.Name == "" {
		return config.RemoteAgentConfig{}, fmt.Errorf("分享文件缺少名称")
	}
	err := a.S.Store.Update(func(cfg *config.Config) {
		nameExists := func(name string) bool {
			for _, r := range cfg.RemoteAgents {
				if r.Name == name {
					return true
				}
			}
			return false
		}
		base := imported.Name
		for n := 2; nameExists(imported.Name); n++ {
			imported.Name = fmt.Sprintf("%s（%d）", base, n)
		}
		imported.ID = newID("ra_")
		imported.CreatedAt = nowUTC()
		cfg.RemoteAgents = append(cfg.RemoteAgents, imported)
	})
	if err != nil {
		return config.RemoteAgentConfig{}, err
	}
	return imported, nil
}

// configBackup 是可移植的备份文档。会话被有意排除：它们存放在 sessions.db
// 中，且导入操作绝不会覆盖它们。
type configBackup struct {
	Version    int            `json:"version"`
	ExportedAt time.Time      `json:"exportedAt"`
	App        string         `json:"app"`
	Config     *config.Config `json:"config"`
}

// ExportConfig 将当前配置（不含聊天会话）作为可移植的 JSON 备份写入
// <workspace>/exports，并返回文件路径。当 includeSecrets=false 时，所有供应商
// API 密钥都会被剥离，因此该文件可以安全分享。
func (a *AppService) ExportConfig(includeSecrets bool) (string, error) {
	// Get 返回独立副本；下面的脱敏绝不能作用于运行中的配置（否则会把用户内存里的
	// API 密钥清空）。
	cfg := a.S.Store.Get()
	cfg.Sessions = nil
	// OAuth 令牌属于机密信息，并且仅存在于本机：绝不导出。
	cfg.MCPTokens = nil
	if !includeSecrets {
		for i := range cfg.Providers {
			cfg.Providers[i].APIKey = ""
		}
		for i := range cfg.MCPServers {
			cfg.MCPServers[i].AuthToken = ""
			// stdio Env 和 streamable Headers 经常携带机密信息
			// （OPENAI_API_KEY、Authorization: Bearer …）——一份 "safe to share" 的
			// 备份也不得携带它们。
			cfg.MCPServers[i].Env = nil
			cfg.MCPServers[i].Headers = nil
		}
	}
	data, err := json.MarshalIndent(configBackup{
		Version:    backupSchemaVersion,
		ExportedAt: nowUTC(),
		App:        "agentmate",
		Config:     &cfg,
	}, "", "  ")
	if err != nil {
		return "", err
	}

	workspace := cfg.Settings.WorkspaceDir
	if workspace == "" {
		workspace = "."
	}
	dir := filepath.Join(workspace, "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	perm := os.FileMode(0o644)
	if includeSecrets {
		perm = 0o600 // 包含 API 密钥
	}
	// 不同的名称：脱敏（可分享）备份绝不能被误认为
	// 包含机密信息的那一份，并且同一秒内的导出不得互相覆盖。
	suffix := "redacted"
	if includeSecrets {
		suffix = "secrets"
	}
	path := filepath.Join(dir, fmt.Sprintf("agentmate-config-%s-%s.json", nowUTC().Format("20060102-150405"), suffix))
	if err := os.WriteFile(path, data, perm); err != nil {
		return "", err
	}
	return path, nil
}

// ImportConfig 用备份内容替换当前配置（供应商、Agent、远程
// Agent、团队、技能、MCP 服务器、设置）。 聊天会话会保留，API
// 密钥按文件原样导入，因此 未脱敏的备份可直接恢复连接。
func (a *AppService) ImportConfig(data string) error {
	if len(data) > maxBackupBytes {
		return fmt.Errorf("备份文件过大（%d 字节，上限 10MB）", len(data))
	}
	var bak configBackup
	if err := json.Unmarshal([]byte(data), &bak); err != nil {
		return fmt.Errorf("备份文件解析失败: %w", err)
	}
	if bak.Version > backupSchemaVersion {
		return fmt.Errorf("备份版本 v%d 高于当前支持的 v%d，请先升级应用", bak.Version, backupSchemaVersion)
	}
	if bak.Config == nil {
		return fmt.Errorf("备份文件缺少 config 字段")
	}
	imported := *bak.Config
	imported.Sessions = nil
	imported.NormalizeImported()
	// OAuth 令牌是与会话类似的本机状态：导入不得 清空本地缓存（过去这会强制在每个
	// OAuth 服务器上重新授权）。
	imported.MCPTokens = a.S.Store.Get().MCPTokens
	// 跟踪哪些 MCP 服务器消失或发生变化，以便丢弃其缓存的工具集和 连接——与
	// SaveMCPServer 的约定相同。
	oldServers := a.S.Store.Get().MCPServers
	// 绝不将过期的 ID 导入当前生效的配置：保留文件 所携带的全部内容，会话除外。
	if err := a.S.Store.Update(func(cfg *config.Config) {
		sessions := cfg.Sessions
		*cfg = imported
		cfg.Sessions = sessions
	}); err != nil {
		return err
	}
	importedByID := map[string]string{}
	for _, s := range imported.MCPServers {
		importedByID[s.ID] = string(mustJSON(s))
	}
	for _, o := range oldServers {
		if cur, ok := importedByID[o.ID]; !ok || cur != string(mustJSON(o)) {
			a.S.MCP.Disconnect(o.ID)
		}
	}
	return nil
}

// MCPService 向前端暴露 MCP 服务器管理。
type MCPService struct{ S *Services }

// NewMCPService 创建该服务。
func NewMCPService(s *Services) *MCPService { return &MCPService{S: s} }

// Status 返回所有已配置的 MCP 服务器的当前状态。
func (m *MCPService) Status() []mcpmgr.ServerStatus {
	cfg := m.S.Store.Get()
	return m.S.MCP.AllStatus(cfg.MCPServers)
}

// Connect 启动/校验一个 MCP
// 服务器的连接，并返回其状态，其中包含已发现的工具列表。
func (m *MCPService) Connect(id string) (mcpmgr.ServerStatus, error) {
	cfg := m.S.Store.Get()
	for _, s := range cfg.MCPServers {
		if s.ID == id {
			return m.S.MCP.Connect(s), nil
		}
	}
	return mcpmgr.ServerStatus{}, fmt.Errorf("MCP 服务器 %q 不存在", id)
}

// Disconnect 丢弃一个 MCP 服务器的连接状态。
func (m *MCPService) Disconnect(id string) {
	m.S.MCP.Disconnect(id)
}

// revealCommand 构建在系统文件管理器中显示 path 的操作系统命令：Finder
// 会选中该文件，Explorer 会选中该文件，其他平台则打开所在目录（没有选中 API）。
func revealCommand(goos, path string) *exec.Cmd {
	switch goos {
	case "darwin":
		return exec.Command("open", "-R", path)
	case "windows":
		return exec.Command("explorer", "/select,"+path)
	default:
		return exec.Command("xdg-open", filepath.Dir(path))
	}
}

// RevealWorkspaceFile 在系统文件管理器（Finder/Explorer）中显示一个会话工作区文件。路径规则与
// ArtifactService 预览一致：相对该会话的有效工作区解析，阻止路径穿越。
func (a *AppService) RevealWorkspaceFile(sessionID, relPath string) error {
	ws, err := sessionWorkspaceOf(a.S.Store.Get(), sessionID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ws) == "" {
		return fmt.Errorf("工作区目录未配置")
	}
	abs, err := agentkit.SafeWorkspacePath(ws, relPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("文件不存在: %w", err)
	}
	return revealCommand(runtime.GOOS, abs).Start()
}
