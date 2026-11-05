package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Skill sources. App skills live in config.json; project and global skills are
// discovered read-only from <root>/.agents/skills on every read and are never
// written back to the config file: the directories belong to the other
// agentskills.io tools (Codex, Claude Code, OpenCode…), and an in-app edit must
// not silently shadow the file those tools read.
const (
	SkillSourceProject = "project"
	SkillSourceGlobal  = "global"
)

const (
	discoveredProjectPrefix = "fs:project:"
	discoveredGlobalPrefix  = "fs:global:"
)

// skillNamePattern is the agentskills.io name grammar: lowercase ASCII
// alphanumerics in hyphen-separated groups, at most 64 characters.
var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// GlobalSkillRoot is the user-level agentskills.io discovery root:
// ~/.agents/skills (%USERPROFILE%\.agents\skills on Windows). Empty when the
// home directory cannot be resolved.
func GlobalSkillRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "skills")
}

// DiscoveredSkillID returns the stable identity of a discovered skill. Agents
// store this ID, so a mounted project/global skill survives restarts; the
// backing directory may disappear when the workspace or the user root changes.
// Unknown sources return "".
func DiscoveredSkillID(source, name string) string {
	switch source {
	case SkillSourceProject:
		return discoveredProjectPrefix + name
	case SkillSourceGlobal:
		return discoveredGlobalPrefix + name
	}
	return ""
}

// DiscoveredSkillSource reports whether id belongs to a discovered skill and
// which root it came from.
func DiscoveredSkillSource(id string) (string, bool) {
	switch {
	case strings.HasPrefix(id, discoveredProjectPrefix):
		return SkillSourceProject, true
	case strings.HasPrefix(id, discoveredGlobalPrefix):
		return SkillSourceGlobal, true
	}
	return "", false
}

// DiscoveredSkillName extracts the skill name from a discovered ID ("" for an
// app-config ID).
func DiscoveredSkillName(id string) string {
	switch {
	case strings.HasPrefix(id, discoveredProjectPrefix):
		return strings.TrimPrefix(id, discoveredProjectPrefix)
	case strings.HasPrefix(id, discoveredGlobalPrefix):
		return strings.TrimPrefix(id, discoveredGlobalPrefix)
	}
	return ""
}

// DiscoveredSkills lists agentskills.io skills from
// <workspace>/.agents/skills (project) and the user-level root. Discovery is
// exactly one level deep: only <root>/<name>/SKILL.md with an uppercase
// filename counts; stray files and nested trees are ignored. A broken entry is
// skipped on its own, and a missing/unreadable root is simply empty — never an
// error, so a workspace without .agents/skills stays silent.
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

// EffectiveSkills merges discovered skills with the app-configured ones. Each
// materialized name exists once; precedence is project > global > app config,
// so an externally maintained SKILL.md wins over a same-named app skill rather
// than making the agent unbuildable.
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
		return nil // missing or unreadable root: silently empty
	}
	seen := make(map[string]bool)
	var out []SkillConfig
	for _, e := range entries {
		if !e.IsDir() {
			continue // stray files at the root are ignored
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

// readDiscoveredSkill loads one <root>/<dir>/SKILL.md. The filename comparison
// is exact and case-sensitive so a lower-case skill.md on a case-insensitive
// filesystem is not mistaken for the agentskills.io layout.
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

// NormalizeSkillName makes a valid agentskills.io skill name: lowercase ASCII
// letters/digits with single hyphens. Empty results collapse to "skill" so two
// CJK-only names cannot silently share one materialized directory.
func NormalizeSkillName(name string) string {
	var sb strings.Builder
	prevHyphen := true // suppress leading hyphens
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

// ParseSkillMarkdown extracts name/description from a SKILL.md YAML
// frontmatter block and returns the markdown body. The parser is deliberately
// small (no YAML dependency): top-level `key: value` pairs plus the common
// `|` / `>` block-scalar form for description. Without frontmatter the first
// heading supplies a fallback name (tolerant, used by workspace import; the
// discovery layer rejects entries whose name/description does not validate).
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