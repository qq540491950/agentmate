package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// maxAgentGuideBytes 是单个工程指南文件的建议性大小阈值，与 @-mention
// 单文件上限保持一致。指南绝不截断：超大文件会完整注入，并在末尾附带一条警告，
// 因此模型知道其上下文已拥挤，用户也会知道该文件值得精简。
const maxAgentGuideBytes = 48 << 10

// GlobalAgentsFile 是用户级工程指南：~/.agents/AGENTS.md（Windows 上为
// %USERPROFILE%\.agents\AGENTS.md）。无法解析主目录时为空。
func GlobalAgentsFile() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".agents", "AGENTS.md")
}

// EffectiveAgentGuides 返回注入本地 Agent
// 系统指令的工程指南块：先是用户级全局指南，然后是生效的工作区根
// AGENTS.md——冲突时以靠后的、更具体的块为准。文件会完整注入，绝不截断；单个文件
// 超过 maxAgentGuideBytes
// 时，末尾会附带中文警告。缺失、不可读或为空的文件会被跳过：指南必须绝不阻塞会
// 话。
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
