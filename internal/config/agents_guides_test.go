package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEffectiveAgentGuidesWorkspaceOnly(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("项目约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := EffectiveAgentGuides(workspace, "")
	if len(got) != 1 || got[0] != "项目约定内容" {
		t.Fatalf("workspace guide must be the only block, got %q", got)
	}
}

func TestEffectiveAgentGuidesGlobalThenWorkspace(t *testing.T) {
	workspace := t.TempDir()
	global := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(global, []byte("全局约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("工作区约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := EffectiveAgentGuides(workspace, global)
	if len(got) != 2 || got[0] != "全局约定内容" || got[1] != "工作区约定内容" {
		t.Fatalf("guides must be [global, workspace], got %q", got)
	}
}

func TestEffectiveAgentGuidesOversizeKeepsContentAndWarns(t *testing.T) {
	workspace := t.TempDir()
	content := strings.Repeat("超长约定", 9000) // 108,000 字节，高于 48KB 阈值
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got := EffectiveAgentGuides(workspace, "")
	if len(got) != 1 {
		t.Fatalf("oversize guide must stay one block, got %d", len(got))
	}
	if !strings.HasPrefix(got[0], content) {
		t.Fatal("oversize guide content must not be truncated")
	}
	if !strings.Contains(got[0], "超过 48KB") || !strings.Contains(got[0], "106 KB") || !strings.Contains(got[0], "挤占对话上下文") {
		t.Fatalf("oversize guide must carry a size warning, got tail: %q", tail(got[0], 120))
	}
}

func TestEffectiveAgentGuidesSkipsMissingUnreadableAndEmpty(t *testing.T) {
	emptyDir := t.TempDir()
	global := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(global, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := EffectiveAgentGuides(emptyDir, global); len(got) != 0 {
		t.Fatalf("empty guide file must be skipped, got %q", got)
	}

	// 预期为文件的位置实际是目录，无法读取：跳过，绝不失败。
	dir := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := EffectiveAgentGuides(emptyDir, dir); len(got) != 0 {
		t.Fatalf("unreadable guide must be skipped, got %q", got)
	}

	// 二进制或无效 UTF-8 内容无法作为指令读取：跳过它，而不是注入替换字符。
	bin := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(bin, []byte{0xff, 0xfe, 0x00, 0x01}, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := EffectiveAgentGuides(emptyDir, bin); len(got) != 0 {
		t.Fatalf("binary guide must be skipped, got %q", got)
	}
}

func TestEffectiveAgentGuidesNoWorkspaceKeepsGlobal(t *testing.T) {
	global := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(global, []byte("全局约定内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := EffectiveAgentGuides("", global)
	if len(got) != 1 || got[0] != "全局约定内容" {
		t.Fatalf("empty workspace must keep the global guide, got %q", got)
	}
}

func TestGlobalAgentsFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows 上的 os.UserHomeDir
	want := filepath.Join(home, ".agents", "AGENTS.md")
	if got := GlobalAgentsFile(); got != want {
		t.Fatalf("GlobalAgentsFile() = %q, want %q", got, want)
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
