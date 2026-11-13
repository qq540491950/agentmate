// Package services 实现暴露给前端的 Wails 服务。
package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentmate/internal/memory"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"agentmate/internal/agentkit"
	"agentmate/internal/config"
	"agentmate/internal/mcpmgr"
	"agentmate/internal/terminal"
)

// Services 汇总所有 Wails 服务共享的依赖。
type Services struct {
	Store  *config.Store
	Kit    *agentkit.Kit
	MCP    *mcpmgr.Manager
	Memory *memory.SQLiteService
	// A2A 设置后，配置变更可下线其 Agent 被编辑或删除的已发布服务器（未接入 A2A
	// 的测试中为 nil）。
	A2A *A2AService
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// ConfigService 向前端暴露配置的 CRUD 操作。
type ConfigService struct{ S *Services }

// NewConfigService 创建该服务。
func NewConfigService(s *Services) *ConfigService { return &ConfigService{S: s} }

// GetConfig 返回完整配置（包含 API 密钥；本地应用）。从 <root>/.agents/skills
// 发现的项目级/全局技能只合并到返回的副本中：它们是外部只读文件，绝不能写回
// config.json（这些文件属于其他 agentskills.io 工具）。
func (c *ConfigService) GetConfig() config.Config {
	cfg := c.S.Store.Get()
	cfg.Skills = config.EffectiveSkills(cfg, cfg.Settings.WorkspaceDir, config.GlobalSkillRoot())
	return cfg
}

// SaveProvider 插入或更新模型供应商。
func (c *ConfigService) SaveProvider(p config.ModelProvider) error {
	if p.Name == "" {
		return fmt.Errorf("请填写供应商名称")
	}
	if p.ID == "" {
		p.ID = newID("prov_")
	}
	if p.Models == nil {
		p.Models = []config.ModelInfo{}
	}
	return c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.Providers {
			if cfg.Providers[i].ID == p.ID {
				cfg.Providers[i] = p
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Providers = append(cfg.Providers, p)
		}
		if p.IsDefault {
			for i := range cfg.Providers {
				cfg.Providers[i].IsDefault = cfg.Providers[i].ID == p.ID
			}
		} else if len(cfg.Providers) == 1 {
			cfg.Providers[0].IsDefault = true
		}
	})
}

// DeleteProvider 按 id 删除供应商。
func (c *ConfigService) DeleteProvider(id string) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Providers[:0]
		for _, p := range cfg.Providers {
			if p.ID != id {
				out = append(out, p)
			}
		}
		cfg.Providers = out
	})
}

// ListProviderModels 获取供应商的列表端点所提供的模型
// ID，以便用户从中选择，而不是手动输入模型 ID。
func (c *ConfigService) ListProviderModels(p config.ModelProvider) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return agentkit.ListProviderModels(ctx, p)
}

// SuggestModels 为编辑器刚获取到的或用户输入的模型 ID
// 返回内置默认值（显示名称与推理等级表）。该操作离线执行，不会改动已保存的配置
// 。
func (c *ConfigService) SuggestModels(ids []string) []config.ModelInfo {
	return config.SuggestModelInfos(ids)
}

// TestProvider 使用所选模型对供应商执行一次最小往返，以验证可达性和凭据。返回耗时（毫秒）供
// 展示。
func (c *ConfigService) TestProvider(p config.ModelProvider, model string) (int64, error) {
	m, err := agentkit.NewModelFromProvider(p, model)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := &adkmodel.LLMRequest{
		Contents: []*genai.Content{genai.NewContentFromText("ping", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{MaxOutputTokens: 16},
	}
	start := time.Now()
	for resp, err := range m.GenerateContent(ctx, req, false) {
		if err != nil {
			return 0, err
		}
		if resp != nil && resp.Content != nil {
			return time.Since(start).Milliseconds(), nil
		}
	}
	return 0, fmt.Errorf("模型没有返回内容")
}

// SaveAgent 插入或更新一个 Agent。
func (c *ConfigService) SaveAgent(a config.AgentConfig) error {
	if a.Name == "" {
		return fmt.Errorf("请填写 Agent 名称")
	}
	if a.ID == "" {
		a.ID = newID("agent_")
		a.CreatedAt = nowUTC()
	}
	if a.SkillIDs == nil {
		a.SkillIDs = []string{}
	}
	if a.MCPServerIDs == nil {
		a.MCPServerIDs = []string{}
	}
	if a.BuiltinTools == nil {
		a.BuiltinTools = []string{}
	}
	replaced := false
	err := c.S.Store.Update(func(cfg *config.Config) {
		for i := range cfg.Agents {
			if cfg.Agents[i].ID == a.ID {
				a.CreatedAt = cfg.Agents[i].CreatedAt
				cfg.Agents[i] = a
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Agents = append(cfg.Agents, a)
		}
	})
	if err == nil && replaced {
		// 已发布的 A2A 服务器持有在 Start 时构建的 Agent 树；
		// 编辑之后，远端仍会继续按旧配置提供服务。
		// 将其下线——用户会用新的设置重新发布。
		c.S.StopPublishedAgent(a.ID)
	}
	return err
}

// DeleteAgent 删除一个 Agent 并将其从团队中解除关联。
func (c *ConfigService) DeleteAgent(id string) error {
	err := c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Agents[:0]
		for _, a := range cfg.Agents {
			if a.ID != id {
				out = append(out, a)
			}
		}
		cfg.Agents = out
		for i := range cfg.Teams {
			ids := cfg.Teams[i].MemberAgentIDs[:0]
			for _, mid := range cfg.Teams[i].MemberAgentIDs {
				if mid != id {
					ids = append(ids, mid)
				}
			}
			cfg.Teams[i].MemberAgentIDs = ids
			if cfg.Teams[i].LeadAgentID == id {
				cfg.Teams[i].LeadAgentID = ""
				cfg.Teams[i].AutoCoordinate = true
			}
		}
	})
	if err == nil {
		// 已删除的 Agent 不得仍可通过尚处于发布状态的 A2A 服务器访问。
		c.S.StopPublishedAgent(id)
	}
	return err
}

// SaveTeam 插入或更新一个 Agent 团队。
func (c *ConfigService) SaveTeam(t config.TeamConfig) error {
	if t.Name == "" {
		return fmt.Errorf("请填写团队名称")
	}
	if t.ID == "" {
		t.ID = newID("team_")
		t.CreatedAt = nowUTC()
	}
	if t.MemberAgentIDs == nil {
		t.MemberAgentIDs = []string{}
	}
	return c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.Teams {
			if cfg.Teams[i].ID == t.ID {
				t.CreatedAt = cfg.Teams[i].CreatedAt
				cfg.Teams[i] = t
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Teams = append(cfg.Teams, t)
		}
	})
}

// DeleteTeam 按 id 删除一个团队。
func (c *ConfigService) DeleteTeam(id string) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Teams[:0]
		for _, t := range cfg.Teams {
			if t.ID != id {
				out = append(out, t)
			}
		}
		cfg.Teams = out
	})
}

// SaveSkill 插入或更新一个技能（物化为 SKILL.md）。
func (c *ConfigService) SaveSkill(s config.SkillConfig) error {
	s.Name = strings.TrimSpace(s.Name)
	s.Description = strings.TrimSpace(s.Description)
	s.SourceDir = ""
	if s.Name == "" {
		return fmt.Errorf("请填写技能名称")
	}
	if source, ok := config.DiscoveredSkillSource(s.ID); ok {
		return fmt.Errorf("该 Skill 发现自%s目录（只读），请直接修改对应的 SKILL.md", skillSourceLabel(source))
	}
	if s.ID == "" {
		s.ID = newID("skill_")
		s.CreatedAt = nowUTC()
	}
	// 共用同一规范化名称的技能会共用同一个物化目录，并会静默覆盖彼此的
	// SKILL.md——必须拒绝这种冲突。
	newKey := agentkit.MaterializedSkillName(s.Name)
	if s.Description == "" {
		s.Description = config.EffectiveSkillDescription(s.Name, "", s.Content)
	}
	if err := config.ValidateSkillMetadata(newKey, s.Description); err != nil {
		return fmt.Errorf("技能格式无效：%w", err)
	}
	for _, other := range c.S.Store.Get().Skills {
		if other.ID != s.ID && agentkit.MaterializedSkillName(other.Name) == newKey {
			return fmt.Errorf("技能名称「%s」与现有技能「%s」冲突（规范化后同名），请换个名字", s.Name, other.Name)
		}
	}
	err := c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.Skills {
			if cfg.Skills[i].ID == s.ID {
				s.CreatedAt = cfg.Skills[i].CreatedAt
				cfg.Skills[i] = s
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Skills = append(cfg.Skills, s)
		}
	})
	return err
}

// ImportSkillsFromWorkspace 扫描 <workspace>/skills、<workspace>/.claude/skills
// 和 ~/.claude/skills 下的 <name>/SKILL.md（agentskills.io 布局），并导入所有
// 规范化名称尚未存在的技能。返回导入的技能数量。
func (c *ConfigService) ImportSkillsFromWorkspace() (int, error) {
	workspace := c.S.Store.Get().Settings.WorkspaceDir
	if workspace == "" {
		return 0, fmt.Errorf("请先在设置中配置工作区目录")
	}
	roots := []string{
		filepath.Join(workspace, "skills"),
		filepath.Join(workspace, ".claude", "skills"),
	}
	if root := config.GlobalClaudeSkillRoot(); root != "" {
		roots = append(roots, root)
	}
	imported := 0
	foundRoot := false
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		foundRoot = true
		for _, e := range entries {
			dir := filepath.Join(root, e.Name())
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue // agentskills.io 布局：skills/<name>/SKILL.md
			}
			data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
			if err != nil {
				continue
			}
			name, desc, body := config.ParseSkillMarkdown(string(data))
			name = strings.TrimSpace(name)
			desc = strings.TrimSpace(desc)
			if name == "" {
				continue
			}
			if desc == "" {
				desc = config.EffectiveSkillDescription(name, "", body)
			}
			if err := config.ValidateSkillMetadata(agentkit.MaterializedSkillName(name), desc); err != nil {
				continue
			}
			if c.addSkillIfNew(name, desc, body) {
				imported++
			}
		}
	}
	if !foundRoot {
		return 0, fmt.Errorf("工作区中没有 skills/、.claude/skills 或 ~/.claude/skills 目录")
	}
	return imported, nil
}

// addSkillIfNew 插入一个技能，除非已存在相同的规范化名称；并报告是否已添加。
func (c *ConfigService) addSkillIfNew(name, desc, body string) bool {
	added := false
	_ = c.S.Store.Update(func(cfg *config.Config) {
		// 按物化名称去重：两个规范化名称冲突的技能（CJK
		// 名称会归入同一个桶）会共用同一个目录，并会静默覆盖彼此的 SKILL.md。必须与
		// SaveSkill 的冲突规则完全一致。
		key := agentkit.MaterializedSkillName(name)
		for _, sk := range cfg.Skills {
			if agentkit.MaterializedSkillName(sk.Name) == key {
				return
			}
		}
		cfg.Skills = append(cfg.Skills, config.SkillConfig{
			ID: newID("skill_"), Name: name, Description: desc, Content: body, CreatedAt: nowUTC(),
		})
		added = true
	})
	return added
}

// skillSourceLabel 渲染发现的来源，用于面向用户的错误。
func skillSourceLabel(source string) string {
	switch source {
	case config.SkillSourceProject:
		return "\u9879\u76EE"
	case config.SkillSourceGlobal:
		return "\u5168\u5C40"
	}
	return "\u5916\u90E8"
}

// DeleteSkill 按 id 删除技能，并尽力而为地删除其物化的 SKILL.md 目录（每个
// Agent 的暂存目录以及遗留的共享布局）。 下一次 Agent
// 构建无论如何都会清理陈旧条目；这能立即保持磁盘整洁。
func (c *ConfigService) DeleteSkill(id string) error {
	if _, ok := config.DiscoveredSkillSource(id); ok {
		return fmt.Errorf("\u53D1\u73B0\u7684 Skill \u4E3A\u53EA\u8BFB\uFF08\u6765\u81EA .agents/skills\uFF09\uFF0C\u8BF7\u76F4\u63A5\u4FEE\u6539\u5BF9\u5E94\u76EE\u5F55\u4E0B\u7684 SKILL.md")
	}
	var removed config.SkillConfig
	err := c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.Skills[:0]
		for _, s := range cfg.Skills {
			if s.ID == id {
				removed = s
				continue
			}
			out = append(out, s)
		}
		cfg.Skills = out
		for i := range cfg.Agents {
			ids := cfg.Agents[i].SkillIDs[:0]
			for _, sid := range cfg.Agents[i].SkillIDs {
				if sid != id {
					ids = append(ids, sid)
				}
			}
			cfg.Agents[i].SkillIDs = ids
		}
	})
	if err != nil {
		return err
	}
	if removed.Name != "" {
		c.S.Kit.RemoveMaterializedSkill(removed.Name)
	}
	return nil
}

// SaveMCPServer 插入或更新 MCP
// 服务器配置。替换设置已变更的服务器会丢弃其缓存的工具集/连接：管理器会永久按
// ID 索引工具集，因此若不这样做，下一回合会继续与 OLD
// 传输（URL、token、args、env）通信，而 UI 显示的是新服务器。
func (c *ConfigService) SaveMCPServer(s config.MCPServerConfig) (config.MCPServerConfig, error) {
	// 在验证和变更检查前进行规范化：粘贴的空白 （"npx
	// "）会被原样持久化，并在连接时破坏 stdio 执行。
	s.Name = strings.TrimSpace(s.Name)
	s.Command = strings.TrimSpace(s.Command)
	s.URL = strings.TrimSpace(s.URL)
	// 为空时保持 nil：Store.Get 的深拷贝会把空切片映射为 nil，因此空但非 nil 的
	// Args 会使每次未变更的保存看起来像 "changed"，并会无谓地丢弃缓存的 MCP 连接。
	var args []string
	for _, a := range s.Args {
		if a = strings.TrimSpace(a); a != "" {
			args = append(args, a)
		}
	}
	s.Args = args
	if s.Name == "" {
		return config.MCPServerConfig{}, fmt.Errorf("请填写服务器名称")
	}
	if s.ID == "" {
		s.ID = newID("mcp_")
	}
	changed := false
	if old := c.S.Store.Get(); s.ID != "" {
		for _, o := range old.MCPServers {
			if o.ID == s.ID {
				changed = string(mustJSON(o)) != string(mustJSON(s))
				break
			}
		}
	}
	if changed {
		c.S.MCP.Disconnect(s.ID)
	}
	if err := c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.MCPServers {
			if cfg.MCPServers[i].ID == s.ID {
				cfg.MCPServers[i] = s
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.MCPServers = append(cfg.MCPServers, s)
		}
	}); err != nil {
		return config.MCPServerConfig{}, err
	}
	return s, nil
}

// DeleteMCPServer 删除 MCP 服务器并将其从各 Agent 中解除关联。
func (c *ConfigService) DeleteMCPServer(id string) error {
	c.S.MCP.Disconnect(id)
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.MCPServers[:0]
		for _, s := range cfg.MCPServers {
			if s.ID != id {
				out = append(out, s)
			}
		}
		cfg.MCPServers = out
		for i := range cfg.Agents {
			ids := cfg.Agents[i].MCPServerIDs[:0]
			for _, mid := range cfg.Agents[i].MCPServerIDs {
				if mid != id {
					ids = append(ids, mid)
				}
			}
			cfg.Agents[i].MCPServerIDs = ids
		}
	})
}

// SaveSettings 持久化全局设置，并预先验证出站代理
// URL，使拼写错误立即暴露，而不是等到下次应用启动时才出现。
func (c *ConfigService) SaveSettings(s config.Settings) error {
	s.Terminal = config.NormalizeTerminalSettings(s.Terminal)
	if s.CompactionContextPercent == nil {
		percent := config.DefaultCompactionContextPercent
		s.CompactionContextPercent = &percent
	}
	if s.HTTPProxy != "" {
		u, err := url.Parse(s.HTTPProxy)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("代理地址无效（需 http://host:port 形式）: %q", s.HTTPProxy)
		}
	}
	if s.CompactionInterval < 0 {
		return fmt.Errorf("滑动窗口压缩间隔不能为负数")
	}
	if s.CompactionContextPercent != nil && (*s.CompactionContextPercent < 0 || *s.CompactionContextPercent > 100) {
		return fmt.Errorf("上下文占用阈值需为 0-100 的整数")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := terminal.Resolve(ctx, s.Terminal, terminal.DefaultHost(), s.SandboxProfile); err != nil {
		return fmt.Errorf("终端配置无效: %w", err)
	}
	return c.S.Store.Update(func(cfg *config.Config) {
		cfg.Settings = s
	})
}

// DetectTerminals 列出此主机上可供设置选择器使用的 shell。检测有意按需进行：WSL
// 和版本探测可能耗时几秒，且不应在应用启动期间运行。
func (c *ConfigService) DetectTerminals(sandboxProfile string) terminal.Inventory {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return terminal.Detect(ctx, terminal.DefaultHost(), config.NormalizeSandboxProfile(sandboxProfile))
}

// TestTerminal 通过所选 shell 运行一个固定命令。它绝不接受模型/用户提供的命令。
func (c *ConfigService) TestTerminal(s config.TerminalSettings, sandboxProfile string) (terminal.ProbeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return terminal.Test(ctx, s, terminal.DefaultHost(), config.NormalizeSandboxProfile(sandboxProfile))
}

// KnowledgeFile 描述工作区知识目录中的一个文件。
type KnowledgeFile struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	ModTime   string `json:"modTime"`
}

// safeKnowledgePath 解析工作区内的知识文件名， 并阻止目录遍历。
func (c *ConfigService) safeKnowledgePath(name string) (string, error) {
	workspace := c.S.Store.Get().Settings.WorkspaceDir
	if workspace == "" {
		return "", fmt.Errorf("请先在设置中配置工作区目录")
	}
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return "", fmt.Errorf("非法的知识库文件名: %q", name)
	}
	root := filepath.Join(workspace, "knowledge")
	p := filepath.Join(root, name)
	if !strings.HasPrefix(p, root+string(filepath.Separator)) && filepath.Dir(p) != root {
		return "", fmt.Errorf("非法的知识库文件名: %q", name)
	}
	return p, nil
}

// ListKnowledgeFiles 列出工作区知识目录中的文件。
func (c *ConfigService) ListKnowledgeFiles() ([]KnowledgeFile, error) {
	workspace := c.S.Store.Get().Settings.WorkspaceDir
	if workspace == "" {
		return nil, fmt.Errorf("请先在设置中配置工作区目录")
	}
	out := []KnowledgeFile{}
	root := filepath.Join(workspace, "knowledge")
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return out, nil
	}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, KnowledgeFile{
			Name:      filepath.ToSlash(rel),
			SizeBytes: st.Size(),
			ModTime:   st.ModTime().Format("2006-01-02 15:04"),
		})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime > out[j].ModTime })
	return out, nil
}

// CreateKnowledgeFile 写入新的知识文件（带目录遍历防护，拒绝 覆盖现有文件）。
func (c *ConfigService) CreateKnowledgeFile(name, content string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("文件名不能为空")
	}
	p, err := c.safeKnowledgePath(name)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err == nil {
		return "", fmt.Errorf("文件已存在: %s", name)
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("内容不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", err
	}
	return name, nil
}

// DeleteKnowledgeFile 按名称删除一个知识文件（带目录遍历防护）。
func (c *ConfigService) DeleteKnowledgeFile(name string) error {
	p, err := c.safeKnowledgePath(name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// ResetKnowledgeIndex 清空向量索引，以便下一次搜索从工作区文件重新嵌入所有内容。
func (c *ConfigService) ResetKnowledgeIndex() error { return c.S.Kit.ResetKnowledgeIndex() }

// TestKnowledgeSearch 使用当前设置运行一次性的知识搜索，
// 以便用户可以从设置页面验证语义检索质量。
func (c *ConfigService) TestKnowledgeSearch(query string) (string, error) {
	return c.S.Kit.TestKnowledgeSearch(query)
}

// KnowledgeIndexStats 为 UI 报告语义知识索引状态。
func (c *ConfigService) KnowledgeIndexStats() agentkit.KnowledgeIndexStats {
	return c.S.Kit.IndexStats()
}

// SaveRemoteAgent 插入或更新远程 A2A Agent。
func (c *ConfigService) SaveRemoteAgent(r config.RemoteAgentConfig) error {
	if r.Name == "" {
		return fmt.Errorf("远程 Agent 名称不能为空")
	}
	if r.CardURL == "" {
		return fmt.Errorf("缺少 Card URL")
	}
	if r.ID == "" {
		r.ID = newID("ra_")
		r.CreatedAt = nowUTC()
	}
	return c.S.Store.Update(func(cfg *config.Config) {
		replaced := false
		for i := range cfg.RemoteAgents {
			if cfg.RemoteAgents[i].ID == r.ID {
				r.CreatedAt = cfg.RemoteAgents[i].CreatedAt
				cfg.RemoteAgents[i] = r
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.RemoteAgents = append(cfg.RemoteAgents, r)
		}
	})
}

// DeleteRemoteAgent 移除远程 Agent，并将其从团队中分离。
func (c *ConfigService) DeleteRemoteAgent(id string) error {
	return c.S.Store.Update(func(cfg *config.Config) {
		out := cfg.RemoteAgents[:0]
		for _, r := range cfg.RemoteAgents {
			if r.ID != id {
				out = append(out, r)
			}
		}
		cfg.RemoteAgents = out
		for i := range cfg.Teams {
			ids := cfg.Teams[i].RemoteMemberIDs[:0]
			for _, rid := range cfg.Teams[i].RemoteMemberIDs {
				if rid != id {
					ids = append(ids, rid)
				}
			}
			cfg.Teams[i].RemoteMemberIDs = ids
		}
	})
}

// CheckRemoteAgent 解析在 URL 上通告的 A2A Agent 卡片，并返回用于 UI
// 连通性检查的摘要。
func (c *ConfigService) CheckRemoteAgent(cardURL string) (*agentkit.RemoteAgentInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return agentkit.CheckRemoteAgent(ctx, cardURL)
}
