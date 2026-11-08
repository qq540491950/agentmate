package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxAgentGuideBytes is the advisory size threshold for one engineering-guide
// file, mirroring the @-mention single-file cap. Guides are never truncated:
// an oversize file is injected in full with a trailing warning, so the model
// knows its context is crowded and the user learns the file deserves a trim.
const maxAgentGuideBytes = 48 << 10

// GlobalAgentsFile is the user-level engineering guide: ~/.agents/AGENTS.md
// (%USERPROFILE%\.agents\AGENTS.md on Windows). Empty when the home directory
// cannot be resolved.
func GlobalAgentsFile() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "AGENTS.md")
}

// EffectiveAgentGuides returns the engineering-guide blocks injected into a
// local agent's system instruction: the user-level global guide first, then
// the effective workspace root AGENTS.md — conflicts resolve to the later,
// more specific block. Files are injected in full, never truncated; a single
// file over maxAgentGuideBytes carries a trailing Chinese warning. Missing,
// unreadable, or empty files are skipped: guides must never block a session.
func EffectiveAgentGuides(workspace, globalFile string) []string {
	var out []string
	if f := strings.TrimSpace(globalFile); f != "" {
		if block := readAgentGuide(f); block != "" {
			out = append(out, block)
		}
	}
	if ws := strings.TrimSpace(workspace); ws != "" {
		if block := readAgentGuide(filepath.Join(ws, "AGENTS.md")); block != "" {
			out = append(out, block)
		}
	}
	return out
}

func readAgentGuide(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := string(data)
	if strings.TrimSpace(content) == "" || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return ""
	}
	if size := len(data); size > maxAgentGuideBytes {
		content += fmt.Sprintf("\n\n（警告：此工程指南文件超过 %dKB（共 %d KB），可能挤占对话上下文，建议精简。）", maxAgentGuideBytes>>10, (size+1023)/1024)
	}
	return content
}
