package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 技能来源。应用技能存放在 config.json 中；项目级和全局技能在每次读取时从
// <root>/.agents/skills 以只读方式发现，且绝不会写回配置文件：这些目录属于
// 其他 agentskills.io 工具（Codex、Claude Code、OpenCode…），应用内编辑不得
// 静默遮蔽这些工具读取的文件。
const (
	SkillSourceProject = "project"
	SkillSourceGlobal  = "global"
)

const (
	discoveredProjectPrefix = "fs:project:"
	discoveredGlobalPrefix  = "fs:global:"
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
		if !e.IsDir() {
			continue // 根目录下的散落文件会被忽略
		}
		name, desc, body, ok := readDiscoveredSkill(filepath.Join(root, e.Name()))
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, SkillConfig{
			ID:          DiscoveredSkillID(source, name),
			Name:        name,
			Description: desc,
			Content:     body,
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
		if !validDiscoveredSkillName(name) || desc == "" {
			return "", "", "", false
		}
		return name, desc, body, true
	}
	return "", "", "", false
}

func validDiscoveredSkillName(name string) bool {
	return len(name) <= 64 && skillNamePattern.MatchString(name)
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
// name/description，并返回 markdown 正文。该解析器刻意保持精简（不依赖
// YAML）：支持顶层 `key: value` 键值对，以及 description 常用的 `|` / `>`
// 块标量形式。没有 frontmatter
// 时，由第一个标题提供回退名称（宽松处理，供工作区导入使用；发现层会拒绝
// name/description 未通过校验的条目）。
func ParseSkillMarkdown(content string) (name, desc, body string) {
	content = strings.TrimPrefix(content, "\uFEFF")
	if strings.HasPrefix(content, "---") {
		if end := strings.Index(content[3:], "\n---"); end >= 0 {
			front := content[3 : 3+end]
			body = strings.TrimSpace(content[3+end+4:])
			name, desc = parseSkillFrontmatter(front)
			return name, desc, body
		}
	}
	for _, ln := range strings.Split(content, "\n") {
		if strings.HasPrefix(ln, "# ") {
			return strings.TrimSpace(ln[2:]), "", content
		}
	}
	return "", "", content
}

func parseSkillFrontmatter(front string) (name, desc string) {
	lines := strings.Split(front, "\n")
	for i := 0; i < len(lines); i++ {
		k, v, ok := strings.Cut(lines[i], ":")
		if !ok {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(k))
		if key != "name" && key != "description" {
			continue
		}
		v = strings.TrimSpace(v)
		if key == "description" && isBlockScalarHeader(v) {
			var parts []string
			j := i + 1
			for ; j < len(lines); j++ {
				ln := lines[j]
				trimmed := strings.TrimSpace(ln)
				if trimmed == "" || strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t") {
					parts = append(parts, trimmed)
					continue
				}
				break
			}
			i = j - 1
			v = strings.Join(parts, " ")
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if key == "name" {
			name = v
		} else {
			desc = v
		}
	}
	return name, desc
}

func isBlockScalarHeader(v string) bool {
	return v != "" && (v[0] == '|' || v[0] == '>')
}
