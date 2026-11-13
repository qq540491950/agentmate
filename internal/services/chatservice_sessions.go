package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"google.golang.org/adk/v2/session"

	"agentmate/internal/config"
)

// NewSession 创建一个带有自动隔离工作区的任务会话。
func (c *ChatService) NewSession(targetType, targetID, title string) (config.ChatSession, error) {
	return c.newSession(targetType, targetID, title, "", "")
}

// NewSessionWithWorkspace 创建一个任务会话。workspaceDir
// 为空时，会请求后端在全局工作区下创建一个唯一目录。
func (c *ChatService) NewSessionWithWorkspace(targetType, targetID, title, workspaceDir string) (config.ChatSession, error) {
	return c.newSession(targetType, targetID, title, workspaceDir, "")
}

// NewSessionWithProject 创建一个绑定到已注册项目的聊天。项目聊天共享项目根目录作为其工作区，且不拥有
// 任务目录，因此多个会话可以在同一项目中并行工作。
func (c *ChatService) NewSessionWithProject(targetType, targetID, title, projectID string) (config.ChatSession, error) {
	return c.newSession(targetType, targetID, title, "", projectID)
}

func (c *ChatService) newSession(targetType, targetID, title, workspaceDir, projectID string) (config.ChatSession, error) {
	// 空 target 表示直接聊天：回合在默认供应商的模型上运行，且在用户 @ 提及某个
	// AgentConfig 之前，没有已持久化的 AgentConfig。
	if targetType != "" && targetType != "agent" && targetType != "team" && targetType != "remote" {
		return config.ChatSession{}, fmt.Errorf("invalid target type %q", targetType)
	}
	if title == "" {
		title = "新对话"
	}
	id := newID("sess_")
	cfg := c.S.Store.Get()
	settings := cfg.Settings
	createdDir := ""
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	projectName := ""
	if strings.TrimSpace(projectID) != "" {
		for _, p := range c.S.Store.Get().Projects {
			if p.ID == projectID {
				projectName = p.Name
				workspaceDir = p.RootDir
				break
			}
		}
		if projectName == "" {
			return config.ChatSession{}, fmt.Errorf("项目不存在或已被删除")
		}
	} else if strings.TrimSpace(workspaceDir) == "" {
		global := strings.TrimSpace(settings.WorkspaceDir)
		if global == "" {
			return config.ChatSession{}, fmt.Errorf("工作区目录未配置")
		}
		workspaceDir = filepath.Join(global, "tasks", id)
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			return config.ChatSession{}, fmt.Errorf("创建任务工作区失败: %w", err)
		}
		createdDir = workspaceDir
	}
	var err error
	workspaceDir, err = validateTaskWorkspace(workspaceDir)
	if err != nil {
		cleanupCreatedDir(createdDir)
		return config.ChatSession{}, err
	}
	if projectID == "" {
		if err := projectRootConflict(cfg, workspaceDir); err != nil {
			cleanupCreatedDir(createdDir)
			return config.ChatSession{}, err
		}
		// 任务会话独占其显式目录。项目对话按设计共享项目根目录，包括在该目录注册为
		// 项目之前就绑定到该根目录的遗留任务会话——这些会话不得阻止新建项目对话
		// （issue #24：项目行的「+」曾报「工作区已被任务…使用」且没有创建任何内容）。
		if err := workspaceConflict(cfg, workspaceDir, ""); err != nil {
			cleanupCreatedDir(createdDir)
			return config.ChatSession{}, err
		}
	}
	if _, err := ensureTaskBaseline(c.S.Store, id, workspaceDir); err != nil {
		cleanupCreatedDir(createdDir)
		removeTaskBaseline(c.S.Store, id)
		return config.ChatSession{}, err
	}
	// 项目聊天会让 WorkspaceDir 保持为空：其工作区通过 ProjectID 解析（见
	// EffectiveWorkspaceFor），因此重新设置项目根目录会传播到每个聊天，而无需重写
	// 会话行。
	approvalPolicy, sandboxProfile := settings.EffectiveExecutionPolicy()
	storedWorkspace := workspaceDir
	if projectID != "" {
		storedWorkspace = ""
	}
	cs := config.ChatSession{
		ID:             id,
		Title:          title,
		TargetType:     targetType,
		TargetID:       targetID,
		CreatedAt:      nowUTC(),
		UpdatedAt:      nowUTC(),
		WorkspaceDir:   storedWorkspace,
		ProjectID:      projectID,
		ApprovalPolicy: approvalPolicy,
		SandboxProfile: sandboxProfile,
		SandboxMode:    config.LegacySandboxMode(approvalPolicy, sandboxProfile),
		SandboxNetwork: config.NormalizeSandboxNetwork(settings.SandboxNetwork),
	}
	cleanup := func() {
		cleanupCreatedDir(createdDir)
		removeTaskBaseline(c.S.Store, id)
	}
	if _, err := c.sessions.Create(context.Background(), &session.CreateRequest{
		AppName:   c.appName,
		UserID:    c.userID,
		SessionID: cs.ID,
	}); err != nil {
		cleanup()
		return config.ChatSession{}, err
	}
	err = c.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = append([]config.ChatSession{cs}, cfg.Sessions...)
	})
	if err == nil {
		auditType := targetType
		if auditType == "" {
			auditType = "direct"
		}
		c.audit(cs.ID, "session_created", "system", auditType, mustJSON(map[string]string{"targetId": targetID, "title": cs.Title, "projectId": projectID}), "")
		c.rememberSessionTerminal(cs.ID, settings.Terminal)
	} else {
		_ = c.sessions.Delete(context.Background(), &session.DeleteRequest{
			AppName: c.appName, UserID: c.userID, SessionID: cs.ID,
		})
		cleanup()
	}
	return cs, err
}

// ListProjects 按创建顺序返回已注册的项目。
func (c *ChatService) ListProjects() []config.Project {
	return c.S.Store.Get().Projects
}

// EffectiveSkills 返回输入框的 "/"
// 菜单在聊天上下文中可以调用的技能。工作区在服务端解析（会话 > 项目 >
// 全局），并与项目/全局 .agents/skills
// 发现结果合并，因此编辑器无需猜测聊天将在哪个目录中运行。
func (c *ChatService) EffectiveSkills(sessionID, projectID string) []config.SkillConfig {
	cfg := c.S.Store.Get()
	return effectiveSkillsFor(cfg, contextWorkspace(cfg, sessionID, projectID))
}

// SaveProject 创建项目（空
// id），或更新现有项目的名称/根目录（空白字段保留已存储的值）。根目录必须存在且
// 可写，并且不得被另一个项目占用。
func (c *ChatService) SaveProject(id, name, rootDir string) (config.Project, error) {
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	if strings.TrimSpace(id) == "" {
		root, err := validateTaskWorkspace(rootDir)
		if err != nil {
			return config.Project{}, err
		}
		if err := projectRootConflict(c.S.Store.Get(), root); err != nil {
			return config.Project{}, err
		}
		if strings.TrimSpace(name) == "" {
			name = filepath.Base(root)
		}
		p := config.Project{ID: newID("proj_"), Name: strings.TrimSpace(name), RootDir: root, CreatedAt: nowUTC()}
		if err := c.S.Store.Update(func(cfg *config.Config) {
			cfg.Projects = append(cfg.Projects, p)
		}); err != nil {
			return config.Project{}, err
		}
		c.audit("projects", "project_saved", "system", "project", mustJSON(map[string]string{"id": p.ID, "name": p.Name, "root": p.RootDir}), "")
		return p, nil
	}

	var updated config.Project
	var saveErr error
	err := c.S.Store.Update(func(cfg *config.Config) {
		idx := -1
		for i := range cfg.Projects {
			if cfg.Projects[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			saveErr = fmt.Errorf("项目 %q 不存在", id)
			return
		}
		p := &cfg.Projects[idx]
		root := p.RootDir
		if strings.TrimSpace(rootDir) != "" {
			canonical, err := validateTaskWorkspace(rootDir)
			if err != nil {
				saveErr = err
				return
			}
			for j := range cfg.Projects {
				if j != idx && samePath(cfg.Projects[j].RootDir, canonical) {
					saveErr = fmt.Errorf("目录已被项目「%s」使用", cfg.Projects[j].Name)
					return
				}
			}
			root = canonical
		}
		p.RootDir = root
		if strings.TrimSpace(name) != "" {
			p.Name = strings.TrimSpace(name)
		}
		updated = *p
	})
	if err != nil {
		return config.Project{}, err
	}
	if saveErr != nil {
		return config.Project{}, saveErr
	}
	c.audit("projects", "project_saved", "system", "project", mustJSON(map[string]string{"id": updated.ID, "name": updated.Name, "root": updated.RootDir}), "")
	return updated, nil
}

// DeleteProject 注销项目。其会话回退到默认工作区，其基线重置为新目录；任务文件绝不会被触碰。
// 存在运行中会话的项目会被拒绝。
func (c *ChatService) DeleteProject(id string) error {
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	cfg := c.S.Store.Get()
	found := false
	for _, p := range cfg.Projects {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("项目 %q 不存在", id)
	}
	for _, s := range cfg.Sessions {
		if s.ProjectID == id && c.sessionRunning(s.ID) {
			return fmt.Errorf("会话「%s」正在运行，不能删除项目", s.Title)
		}
	}
	if err := c.S.Store.Update(func(cfg *config.Config) {
		projects := make([]config.Project, 0, len(cfg.Projects))
		for _, p := range cfg.Projects {
			if p.ID != id {
				projects = append(projects, p)
			}
		}
		cfg.Projects = projects
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ProjectID == id {
				cfg.Sessions[i].ProjectID = ""
			}
		}
	}); err != nil {
		return err
	}
	// 基线从该会话新生效的（全局）工作区重新开始，因此任务面板不会将项目文件报告为
	// 大规模删除。
	for _, s := range cfg.Sessions {
		if s.ProjectID != id {
			continue
		}
		s.ProjectID = ""
		_, _ = resetTaskBaseline(c.S.Store, s.ID, s.EffectiveWorkspaceFor(c.S.Store.Get()))
	}
	c.audit("projects", "project_deleted", "system", "project", mustJSON(map[string]string{"id": id}), "")
	return nil
}

func (c *ChatService) sessionRunning(sessionID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.active[sessionID]) > 0
}

// ListSessions 返回所有聊天会话，置顶的在前，然后最新的在前。
func (c *ChatService) ListSessions() []config.ChatSession {
	sessions := c.S.Store.Get().Sessions
	sorted := make([]config.ChatSession, len(sessions))
	copy(sorted, sessions)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Pinned != sorted[j].Pinned {
			return sorted[i].Pinned
		}
		return sorted[i].UpdatedAt.After(sorted[j].UpdatedAt)
	})
	return sorted
}

// SearchSessions 按标题或消息内容筛选会话（对已存储的事件负载进行 LIKE
// 探测）。空查询 = 所有会话（置顶的在前，然后最近活跃的在前）。
func (c *ChatService) SearchSessions(query string) []config.ChatSession {
	sessions := c.ListSessions()
	q := strings.TrimSpace(query)
	if q == "" {
		return sessions
	}
	matched := map[string]bool{}
	for i := range sessions {
		if strings.Contains(sessions[i].Title, q) {
			matched[sessions[i].ID] = true
		}
	}
	if c.db != nil {
		var ids []string
		// 转义 LIKE 通配符，以便搜索 "50%" 时匹配字面文本，而不是匹配所有包含 "50"
		// 的内容。
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
		pattern := "%" + escaped + "%"
		if err := c.db.Raw("SELECT DISTINCT session_id FROM events WHERE content LIKE ? ESCAPE '\\' AND app_name = ? AND user_id = ?",
			pattern, c.appName, c.userID).Pluck("session_id", &ids); err == nil {
			for _, id := range ids {
				matched[id] = true
			}
		}
	}
	out := make([]config.ChatSession, 0, len(matched))
	for _, s := range sessions {
		if matched[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// SetSessionModel 将会话固定到目标 Agent
// 的供应商下的特定模型（按会话的模型覆盖）；空值会清除该覆盖。先前的思考级别也
// 会被清除：新模型可能不提供该级别。
func (c *ChatService) SetSessionModel(sessionID, modelID string) error {
	err := c.mutateSession(sessionID, func(s *config.ChatSession) {
		s.ModelOverride = strings.TrimSpace(modelID)
		s.ReasoningLevel = ""
	})
	if err == nil {
		c.audit(sessionID, "session_model_set", "system", "session", mustJSON(map[string]string{"model": modelID}), "")
	}
	return err
}

// SetSessionReasoningLevel 记住此会话的思考级别（级别名称来自实际生效模型的表；空值会恢复模型默认值）。
// 这是按会话做出的选择，绝不写回供应商配置。
func (c *ChatService) SetSessionReasoningLevel(sessionID, level string) error {
	err := c.mutateSession(sessionID, func(s *config.ChatSession) { s.ReasoningLevel = strings.TrimSpace(level) })
	if err == nil {
		c.audit(sessionID, "session_reasoning_set", "system", "session", mustJSON(map[string]string{"level": level}), "")
	}
	return err
}

// SessionExecutionPolicy 是会话在下一回合将使用的独立审批维度和操作系统边界维度。
type SessionExecutionPolicy struct {
	ApprovalPolicy string `json:"approvalPolicy"`
	SandboxProfile string `json:"sandboxProfile"`
}

// SetSessionExecutionPolicy 独立更改此聊天的审批与沙箱维度。这对设置会被持久化，因此切换聊天或重启后仍会
// 保留；其他会话不受影响。若已有回合正在运行，则从下一个回合起生效。
func (c *ChatService) SetSessionExecutionPolicy(sessionID, approvalPolicy, sandboxProfile, source string) error {
	approval, err := validateSessionApprovalPolicy(approvalPolicy)
	if err != nil {
		return err
	}
	profile, err := validateSessionSandboxProfile(sandboxProfile)
	if err != nil {
		return err
	}
	return c.setSessionExecutionPolicy(sessionID, approval, profile, source, "session_execution_policy_set", "")
}

// SetSessionSandboxMode 是遗留的三配置档包装器。它将每个旧的组合模式映射到独立的一对设置上，同时保留
// 旧的审计阶段。
func (c *ChatService) SetSessionSandboxMode(sessionID, mode, source string) error {
	normalized, err := validateSessionSandboxMode(mode)
	if err != nil {
		return err
	}
	approval, profile := config.ExecutionPolicyFromLegacySandboxMode(normalized)
	return c.setSessionExecutionPolicy(sessionID, approval, profile, source, "session_sandbox_mode_set", normalized)
}

func (c *ChatService) setSessionExecutionPolicy(sessionID, approvalPolicy, sandboxProfile, source, phase, legacyMode string) error {
	err := c.mutateSession(sessionID, func(s *config.ChatSession) {
		s.ApprovalPolicy = approvalPolicy
		s.SandboxProfile = sandboxProfile
		s.SandboxMode = config.LegacySandboxMode(approvalPolicy, sandboxProfile)
		// SandboxNetwork 是用户持久化的偏好设置。Danger 会通过
		// EffectiveSandboxNetwork 报告为 "on"，但不会覆盖它，因此之后切回
		// workspace-write 时会恢复原来的值。
	})
	if err == nil {
		payload := map[string]any{
			"approval": approvalPolicy,
			"sandbox":  sandboxProfile,
			"source":   normalizeSandboxModeSource(source),
		}
		if legacyMode != "" {
			payload["mode"] = legacyMode
		}
		c.audit(sessionID, phase, "user", "session", mustJSON(payload), "")
	}
	return err
}

// SessionExecutionPolicy 报告此聊天在下一个回合将使用的拆分策略。
func (c *ChatService) SessionExecutionPolicy(sessionID string) (*SessionExecutionPolicy, error) {
	cfg := c.S.Store.Get()
	for _, s := range cfg.Sessions {
		if s.ID == sessionID {
			approval, profile := s.EffectiveExecutionPolicy(cfg)
			return &SessionExecutionPolicy{ApprovalPolicy: approval, SandboxProfile: profile}, nil
		}
	}
	return nil, fmt.Errorf("会话 %q 不存在", sessionID)
}

// SessionSandboxMode 报告此聊天为旧前端投影出的遗留组合模式。 未知 ID
// 会报错，与其他会话设置 RPC 一样。
func (c *ChatService) SessionSandboxMode(sessionID string) (string, error) {
	cfg := c.S.Store.Get()
	for _, s := range cfg.Sessions {
		if s.ID == sessionID {
			return s.EffectiveSandboxMode(cfg), nil
		}
	}
	return "", fmt.Errorf("会话 %q 不存在", sessionID)
}

func validateSessionApprovalPolicy(policy string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case config.ApprovalPolicyRequestApproval, "request", config.SandboxProfileWorkspaceWrite:
		return config.ApprovalPolicyRequestApproval, nil
	case config.ApprovalPolicyAutoApprove, "auto":
		return config.ApprovalPolicyAutoApprove, nil
	case config.ApprovalPolicyNever, config.SandboxModeDangerFullAccess:
		return config.ApprovalPolicyNever, nil
	default:
		return "", fmt.Errorf("未知审批策略 %q", policy)
	}
}

func validateSessionSandboxProfile(profile string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case config.SandboxProfileReadOnly:
		return config.SandboxProfileReadOnly, nil
	case config.SandboxProfileWorkspaceWrite:
		return config.SandboxProfileWorkspaceWrite, nil
	case config.SandboxProfileDangerFullAccess:
		return config.SandboxProfileDangerFullAccess, nil
	default:
		return "", fmt.Errorf("未知沙箱 profile %q", profile)
	}
}

func validateSessionSandboxMode(mode string) (string, error) {
	raw := strings.ToLower(strings.TrimSpace(mode))
	switch raw {
	case config.SandboxModeRequestApproval, "request", "workspace-write":
		return config.SandboxModeRequestApproval, nil
	case config.SandboxModeAutoApprove, "auto":
		return config.SandboxModeAutoApprove, nil
	case config.SandboxModeDangerFullAccess:
		return config.SandboxModeDangerFullAccess, nil
	default:
		return "", fmt.Errorf("未知审批模式 %q", mode)
	}
}

func normalizeSandboxModeSource(source string) string {
	if strings.TrimSpace(source) == "composer" {
		return "composer"
	}
	return "unknown"
}

// ArchiveSession 切换会话的归档标志（已归档会话会从默认侧边栏列表中隐藏，但绝不会被删除）。
func (c *ChatService) ArchiveSession(id string, archived bool) error {
	if !archived {
		c.workspaceMu.Lock()
		defer c.workspaceMu.Unlock()
	}
	err := c.mutateSessionGuarded(id, func(s *config.ChatSession, cfg *config.Config) error {
		if !archived {
			if ws := strings.TrimSpace(s.WorkspaceDir); ws != "" {
				if err := workspaceConflictForUnarchive(*cfg, *s); err != nil {
					return err
				}
			}
			s.UpdatedAt = nowUTC()
		}
		s.Archived = archived
		return nil
	})
	if err == nil {
		phase := "session_archived"
		if !archived {
			phase = "session_unarchived"
		}
		c.audit(id, phase, "system", "session", mustJSON(map[string]bool{"archived": archived}), "")
	}
	return err
}

// PinSession 切换会话的置顶标志。
func (c *ChatService) PinSession(id string, pinned bool) error {
	return c.mutateSession(id, func(s *config.ChatSession) { s.Pinned = pinned })
}

// mutateSession 将 fn 应用于一个已存储的会话，并将未知 ID
// 报告为错误——这是重命名、模型覆盖、置顶和归档背后共享的 found/notFound 形态。
func (c *ChatService) mutateSession(id string, fn func(*config.ChatSession)) error {
	return c.mutateSessionGuarded(id, func(s *config.ChatSession, _ *config.Config) error {
		fn(s)
		return nil
	})
}

// mutateSessionGuarded 是 mutateSession，带有一个可中止变更的验证钩子（由
// unarchive 的工作区归属检查使用）。
func (c *ChatService) mutateSessionGuarded(id string, fn func(*config.ChatSession, *config.Config) error) error {
	var notFound error
	var guardErr error
	err := c.S.Store.Update(func(cfg *config.Config) {
		found := false
		for i := range cfg.Sessions {
			if cfg.Sessions[i].ID == id {
				found = true
				if guardErr = fn(&cfg.Sessions[i], cfg); guardErr != nil {
					return
				}
			}
		}
		if !found {
			notFound = fmt.Errorf("会话 %q 不存在", id)
		}
	})
	if err == nil {
		err = notFound
	}
	if err == nil {
		err = guardErr
	}
	return err
}

// RenameSession 更新会话标题。未知 ID
// 视为错误，且审计记录仅在成功时写入，因此失败的改名不会留下虚假的 "renamed"
// 条目。
func (c *ChatService) RenameSession(id, title string) error {
	err := c.mutateSession(id, func(s *config.ChatSession) { s.Title = title })
	if err == nil {
		c.audit(id, "session_renamed", "system", "session", mustJSON(map[string]string{"title": title}), "")
	}
	return err
}

// DeleteSession 删除聊天及其存储的事件。仅在数据库删除成功后才会移除配置条目；否则会话会从侧
// 边栏消失，而它的行仍留在原处（还可能被 fork/search
// 复活）。先取消进行中的运行：孤立的回合会持续消耗
// token，并为已删除的会话重新创建事件，直到其超时。
func (c *ChatService) DeleteSession(id string) error {
	return c.deleteSessionInternal(id)
}

// DeleteArchivedSessions 删除所有已归档会话（先取消，包含事件），并返回删除了多少条。
func (c *ChatService) DeleteArchivedSessions() (int, error) {
	var ids []string
	for _, s := range c.S.Store.Get().Sessions {
		if s.Archived {
			ids = append(ids, s.ID)
		}
	}
	deleted := 0
	for _, id := range ids {
		if err := c.deleteSessionInternal(id); err != nil {
			return deleted, fmt.Errorf("删除会话 %q 失败: %w", id, err)
		}
		deleted++
	}
	return deleted, nil
}

func (c *ChatService) deleteSessionInternal(id string) error {
	c.cancelAndWaitBounded(id)
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	if err := c.sessions.Delete(context.Background(), &session.DeleteRequest{
		AppName: c.appName, UserID: c.userID, SessionID: id,
	}); err != nil {
		return fmt.Errorf("delete session events: %w", err)
	}
	// ADK 会话服务只删除 sessions
	// 行——事件行会作为孤儿残留，全文会话搜索会一直匹配到它们。在进行中的运行回退之
	// 后（这样之后就没有任何东西能写入新行）显式删除它们。
	if c.db != nil {
		if err := c.db.WithContext(context.Background()).Exec(
			"DELETE FROM events WHERE app_name = ? AND user_id = ? AND session_id = ?",
			c.appName, c.userID, id,
		).Error; err != nil {
			return fmt.Errorf("delete session event rows: %w", err)
		}
	}
	var notFound error
	if err := c.S.Store.Update(func(cfg *config.Config) {
		found := false
		out := cfg.Sessions[:0]
		for _, s := range cfg.Sessions {
			if s.ID != id {
				out = append(out, s)
			} else {
				found = true
			}
		}
		cfg.Sessions = out
		if !found {
			notFound = fmt.Errorf("会话 %q 不存在", id)
		}
	}); err != nil {
		return err
	}
	if notFound != nil {
		return notFound
	}
	removeTaskBaseline(c.S.Store, id)
	delete(c.workspaceLocks, id)
	c.forgetSessionTerminal(id)
	c.audit(id, "session_deleted", "system", "session", mustJSON(map[string]string{"sessionId": id}), "")
	return nil
}

// GetSessionMessages 渲染会话已存储的 ADK 事件。
func (c *ChatService) GetSessionMessages(sessionID string) ([]ChatMessage, error) {
	resp, err := c.sessions.Get(context.Background(), &session.GetRequest{
		AppName: c.appName, UserID: c.userID, SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	if resp.Session == nil {
		return nil, nil
	}
	var out []ChatMessage
	i := 0
	cfg := c.S.Store.Get()
	for ev := range resp.Session.Events().All() {
		if ev == nil {
			continue
		}
		i++
		msgs := renderEvent(cfg, ev, i)
		out = append(out, msgs...)
	}
	return out, nil
}

// ForkSession 将会话的完整 ADK
// 事件历史复制到新聊天（分支）：用户可以在分支中重新生成/编辑/确认，而不触碰原
// 始时间线。
func (c *ChatService) ForkSession(sessionID string) (config.ChatSession, error) {
	if c.db == nil {
		return config.ChatSession{}, fmt.Errorf("会话数据库不可用")
	}
	c.workspaceMu.Lock()
	defer c.workspaceMu.Unlock()
	cfg := c.S.Store.Get()
	src := c.resolveSessionTarget(cfg, sessionID)
	if src == nil {
		return config.ChatSession{}, fmt.Errorf("会话 %q 不存在", sessionID)
	}
	id := newID("sess_")
	workspace := src.EffectiveWorkspaceFor(cfg)
	if _, err := resetTaskBaseline(c.S.Store, id, workspace); err != nil {
		return config.ChatSession{}, err
	}
	cleanupBranch := func() {
		removeTaskBaseline(c.S.Store, id)
		_ = c.sessions.Delete(context.Background(), &session.DeleteRequest{
			AppName: c.appName, UserID: c.userID, SessionID: id,
		})
		if c.db != nil {
			_ = c.db.WithContext(context.Background()).Exec(
				"DELETE FROM events WHERE app_name = ? AND user_id = ? AND session_id = ?",
				c.appName, c.userID, id,
			).Error
		}
	}
	if _, err := c.sessions.Create(context.Background(), &session.CreateRequest{
		AppName: c.appName, UserID: c.userID, SessionID: id,
	}); err != nil {
		removeTaskBaseline(c.S.Store, id)
		return config.ChatSession{}, err
	}

	// 将每个已存储的事件行复制到新会话。events 表的键包含 session_id，因此 id
	// 可以在不同会话间重复。列清单取自 schema 本身，以便在 ADK schema
	// 更新后仍能工作。
	var cols []string
	if err := c.db.Raw("SELECT name FROM pragma_table_info('events')").Pluck("name", &cols).Error; err != nil {
		cleanupBranch()
		return config.ChatSession{}, fmt.Errorf("读取事件表结构失败: %w", err)
	}
	if len(cols) == 0 {
		cleanupBranch()
		return config.ChatSession{}, fmt.Errorf("事件表不存在")
	}
	selects := make([]string, len(cols))
	for i, col := range cols {
		if col == "session_id" {
			selects[i] = "? AS session_id"
		} else {
			selects[i] = col
		}
	}
	if err := c.db.Exec(
		fmt.Sprintf("INSERT INTO events (%s) SELECT %s FROM events WHERE app_name = ? AND user_id = ? AND session_id = ?",
			strings.Join(cols, ","), strings.Join(selects, ",")),
		id, c.appName, c.userID, sessionID,
	).Error; err != nil {
		cleanupBranch()
		return config.ChatSession{}, fmt.Errorf("复制会话事件失败: %w", err)
	}

	branch := config.ChatSession{
		ID:             id,
		Title:          strings.TrimSpace(src.Title + "（分支）"),
		TargetType:     src.TargetType,
		TargetID:       src.TargetID,
		CreatedAt:      nowUTC(),
		UpdatedAt:      nowUTC(),
		ForkedFrom:     sessionID,
		WorkspaceDir:   src.WorkspaceDir,
		ProjectID:      src.ProjectID,
		ApprovalPolicy: src.EffectiveApprovalPolicy(cfg),
		SandboxProfile: src.EffectiveSandboxProfile(cfg),
		SandboxMode:    src.EffectiveSandboxMode(cfg),
		SandboxNetwork: src.PersistedSandboxNetwork(cfg),
	}
	err := c.S.Store.Update(func(cfg *config.Config) {
		cfg.Sessions = append([]config.ChatSession{branch}, cfg.Sessions...)
	})
	if err == nil {
		c.audit(branch.ID, "session_forked", "system", "session", mustJSON(map[string]string{"forkedFrom": sessionID}), "")
	} else {
		cleanupBranch()
	}
	return branch, err
}
