package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// 技能来源。应用技能存放在 config.json 中；项目级和全局技能在每次读取时从
// <root>/.agents/skills 以只读方式发现，且绝不会写回配置文件：该目录是
// Codex 等工具共享的项目级约定，应用内编辑不得静默遮蔽这些工具读取的文件。
// Claude Code 的官方目录是 .claude/skills，本发现层不扫描它。
const (
	SkillSourceProject = "project"
	SkillSourceGlobal  = "global"
)

const (
	discoveredProjectPrefix = "fs:project:"
	discoveredGlobalPrefix  = "fs:global:"
)

const (
	// MaxSkillNameLength / MaxSkillDescriptionLength 是 agentskills.io 规范
	// 对 frontmatter 字段长度的上限。
	MaxSkillNameLength        = 64
	MaxSkillDescriptionLength = 1024
)

// skillNamePattern 是 agentskills.io 名称语法：由连字符分隔的组构成的小写 ASCII
// 字母数字，最多 64 个字符。
var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// GlobalSkillRoot 是用户级 agentskills.io 发现根目录：~/.agents/skills（Windows
// 上为 %USERPROFILE%\.agents\skills）。当无法解析主目录时为空。
func GlobalSkillRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "skills")
}

// GlobalClaudeSkillRoot 是 Claude Code 用户级技能目录，供“导入 Skill”使用。
// 它不参与自动发现，导入后会成为可编辑的应用内技能。
func GlobalClaudeSkillRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "skills")
}

// DiscoveredSkillID 返回已发现技能的稳定标识。Agent 会存储此
// ID，因此已挂载的项目级/全局技能在重启后仍会保留；当工作区或用户根目录变化时，
// 底层目录可能会消失。未知来源返回 ""。
func DiscoveredSkillID(source, name string) string {
	switch source {
	case SkillSourceProject:
		return discoveredProjectPrefix + name
	case SkillSourceGlobal:
		return discoveredGlobalPrefix + name
	}
	return ""
}

// DiscoveredSkillSource 报告 id 是否属于已发现的技能，以及它来自哪个根目录。
func DiscoveredSkillSource(id string) (string, bool) {
	switch {
	case strings.HasPrefix(id, discoveredProjectPrefix):
		return SkillSourceProject, true
	case strings.HasPrefix(id, discoveredGlobalPrefix):
		return SkillSourceGlobal, true
	}
	return "", false
}

// DiscoveredSkillName 从已发现的 ID 中提取技能名称（对于应用配置 ID 为 ""）。
func DiscoveredSkillName(id string) string {
	switch {
	case strings.HasPrefix(id, discoveredProjectPrefix):
		return strings.TrimPrefix(id, discoveredProjectPrefix)
	case strings.HasPrefix(id, discoveredGlobalPrefix):
		return strings.TrimPrefix(id, discoveredGlobalPrefix)
	}
	return ""
}

// DiscoveredSkills 列出 <workspace>/.agents/skills（项目级）和用户级根目录中的
// agentskills.io 技能。发现严格只有一层深度：仅文件名大写的
// <root>/<name>/SKILL.md
// 才计入；游离文件和嵌套目录树会被忽略。损坏的条目会单独跳过，缺失/不可读的根目
// 录就是空——绝不是错误，因此不含 .agents/skills 的工作区会保持静默。
func DiscoveredSkills(workspace, globalRoot string) []SkillConfig {
	out := []SkillConfig{}
	if ws := strings.TrimSpace(workspace); ws != "" {
		out = append(out, scanSkillRoot(filepath.Join(ws, ".agents", "skills"), SkillSourceProject)...)
	}
	if strings.TrimSpace(globalRoot) != "" {
		out = append(out, scanSkillRoot(globalRoot, SkillSourceGlobal)...)
	}
	return out
}

// EffectiveSkills 将已发现技能与应用配置的技能合并。每个物化后的名称仅出现一次；优先级为项目级
// > 全局 > 应用配置，因此由外部维护的 SKILL.md
// 会优先于同名的应用技能，而不会导致 Agent 无法构建。
func EffectiveSkills(cfg Config, workspace, globalRoot string) []SkillConfig {
	out := make([]SkillConfig, 0, len(cfg.Skills)+4)
	seen := make(map[string]bool, len(cfg.Skills)+4)
	add := func(s SkillConfig) {
		key := NormalizeSkillName(s.Name)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, s)
	}
	for _, s := range DiscoveredSkills(workspace, globalRoot) {
		add(s)
	}
	for _, s := range cfg.Skills {
		add(s)
	}
	return out
}

func scanSkillRoot(root, source string) []SkillConfig {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil // 根目录缺失或不可读：静默为空
	}
	seen := make(map[string]bool)
	var out []SkillConfig
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue // 根目录下的散落文件会被忽略；符号链接目录按目标处理
		}
		name, desc, body, ok := readDiscoveredSkill(dir)
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		sourceDir := dir
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			sourceDir = resolved
		}
		out = append(out, SkillConfig{
			ID:          DiscoveredSkillID(source, name),
			Name:        name,
			Description: desc,
			Content:     body,
			SourceDir:   sourceDir,
		})
	}
	return out
}

// readDiscoveredSkill 加载单个
// <root>/<dir>/SKILL.md。文件名比较是精确且区分大小写的，因此在大小写不敏感的文
// 件系统上，小写的 skill.md 不会被误认为 agentskills.io 布局。
func readDiscoveredSkill(dir string) (name, desc, body string, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", "", false
	}
	var data []byte
	for _, e := range entries {
		if e.IsDir() || e.Name() != "SKILL.md" {
			continue
		}
		data, err = os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return "", "", "", false
		}
		name, desc, body = ParseSkillMarkdown(string(data))
		name = strings.TrimSpace(name)
		desc = strings.TrimSpace(desc)
		if name != filepath.Base(dir) {
			return "", "", "", false
		}
		if err := ValidateSkillMetadata(name, desc); err != nil {
			return "", "", "", false
		}
		return name, desc, body, true
	}
	return "", "", "", false
}

// ValidateSkillMetadata 校验 agentskills.io frontmatter 的 name / description
// 约束。发现层用它拒绝坏文件，保存层用它避免写入一个下次构建才会失败的技能。
func ValidateSkillMetadata(name, description string) error {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" {
		return fmt.Errorf("技能名称不能为空")
	}
	if utf8.RuneCountInString(name) > MaxSkillNameLength {
		return fmt.Errorf("技能名称不能超过 %d 个字符", MaxSkillNameLength)
	}
	if !skillNamePattern.MatchString(name) {
		return fmt.Errorf("技能名称只能包含小写字母、数字和单个连字符，且不能以连字符开头或结尾")
	}
	if description == "" {
		return fmt.Errorf("技能描述不能为空")
	}
	if utf8.RuneCountInString(description) > MaxSkillDescriptionLength {
		return fmt.Errorf("技能描述不能超过 %d 个字符", MaxSkillDescriptionLength)
	}
	return nil
}

// NormalizeSkillName 生成有效的 agentskills.io 技能名称：由小写 ASCII
// 字母/数字组成，连字符均为单个。空结果会归并为 "skill"，这样两个仅含 CJK
// 的名称就无法静默共享同一个物化目录。
func NormalizeSkillName(name string) string {
	var sb strings.Builder
	prevHyphen := true // 抑制前导连字符
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			sb.WriteRune(r)
			prevHyphen = false
		case (r == '-' || r == '_' || r == ' ') && !prevHyphen:
			sb.WriteRune('-')
			prevHyphen = true
		}
	}
	out := strings.TrimSuffix(sb.String(), "-")
	if out == "" {
		out = "skill"
	}
	return out
}

// ParseSkillMarkdown 从 SKILL.md 的 YAML frontmatter 块中提取
// name/description，并返回 markdown 正文。使用完整 YAML 解析，因此引号、
// 注释、折行、块标量等官方允许的写法都能识别；未知字段会被忽略，以兼容
// Claude Code / Codex 的扩展字段。没有 frontmatter 时，由第一个标题提供
// 回退名称（宽松处理，供工作区导入使用；发现层会拒绝 name/description
// 未通过校验的条目）。
func ParseSkillMarkdown(content string) (name, desc, body string) {
	content = strings.TrimPrefix(content, "\uFEFF")
	if strings.HasPrefix(content, "---") {
		front, rest, ok := splitSkillFrontmatter(content)
		if !ok {
			return "", "", strings.TrimSpace(content)
		}
		name, desc = parseSkillFrontmatter(front)
		return name, desc, strings.TrimSpace(rest)
	}
	for _, ln := range strings.Split(content, "\n") {
		if strings.HasPrefix(ln, "# ") {
			return strings.TrimSpace(ln[2:]), "", content
		}
	}
	return "", "", content
}

// skillFrontmatterPattern 只接受独占一行的 `---` 分隔符；Markdown 正文中的
// 水平线或缩进内容不会被误当成 frontmatter 结尾。
var skillFrontmatterPattern = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---(?:\r?\n|\z)`)

func splitSkillFrontmatter(content string) (front, body string, ok bool) {
	m := skillFrontmatterPattern.FindStringSubmatchIndex(content)
	if m == nil {
		return "", "", false
	}
	return content[m[2]:m[3]], content[m[1]:], true
}

func parseSkillFrontmatter(front string) (name, desc string) {
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(front), &fm); err != nil {
		return "", ""
	}
	return strings.TrimSpace(fm.Name), strings.TrimSpace(fm.Description)
}

// EffectiveSkillDescription 返回可写入 SKILL.md 的描述。应用内旧技能或导入的
// 标题型技能可能没有描述，这里回退到正文第一行（与 Claude Code 的行为一致），
// 避免挂载到 Agent 时因空 description 构建失败。
func EffectiveSkillDescription(name, description, body string) string {
	if d := strings.TrimSpace(description); d != "" {
		return truncateSkillRunes(d, MaxSkillDescriptionLength)
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimLeft(line, "#>*-`"))
		if line != "" {
			return truncateSkillRunes(line, MaxSkillDescriptionLength)
		}
	}
	if n := strings.TrimSpace(name); n != "" {
		return truncateSkillRunes(n, MaxSkillDescriptionLength)
	}
	return "技能"
}

func truncateSkillRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}
