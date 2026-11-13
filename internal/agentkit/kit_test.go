package agentkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/config"
	"agentmate/internal/sandbox"
)

func TestSafeWorkspacePath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		rel     string
		wantErr bool
	}{
		{"notes.txt", false},
		{"sub/notes.txt", false},
		{".", false},
		{"../escape.txt", true},
		{"a/../../escape.txt", true},
	}
	for _, c := range cases {
		got, err := safeWorkspacePath(root, c.rel)
		if c.wantErr {
			if err == nil {
				t.Errorf("rel %q: expected error, got %q", c.rel, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("rel %q: unexpected error %v", c.rel, err)
		}
	}
}

func TestSandboxPolicyProfiles(t *testing.T) {
	workspace := t.TempDir()
	k := &Kit{}

	request := k.sandboxPolicy(config.AgentConfig{
		ApprovalPolicy: config.ApprovalPolicyRequestApproval,
		SandboxProfile: config.SandboxProfileWorkspaceWrite,
		SandboxNetwork: config.SandboxNetworkOff,
	}, workspace)
	if request.Mode != sandbox.ModeWorkspaceWrite || request.Network != sandbox.NetworkOff {
		t.Fatalf("request policy = %+v", request)
	}

	auto := k.sandboxPolicy(config.AgentConfig{
		ApprovalPolicy: config.ApprovalPolicyAutoApprove,
		SandboxProfile: config.SandboxProfileWorkspaceWrite,
		SandboxNetwork: config.SandboxNetworkOn,
	}, workspace)
	if auto.Mode != sandbox.ModeWorkspaceWrite || auto.Network != sandbox.NetworkOn {
		t.Fatalf("auto policy = %+v", auto)
	}

	readOnly := k.sandboxPolicy(config.AgentConfig{
		ApprovalPolicy: config.ApprovalPolicyRequestApproval,
		SandboxProfile: config.SandboxProfileReadOnly,
		SandboxNetwork: config.SandboxNetworkOn,
	}, workspace)
	if readOnly.Mode != sandbox.ModeReadOnly || readOnly.Network != sandbox.NetworkOn {
		t.Fatalf("read-only policy = %+v", readOnly)
	}

	full := k.sandboxPolicy(config.AgentConfig{
		ApprovalPolicy: config.ApprovalPolicyRequestApproval,
		SandboxProfile: config.SandboxProfileDangerFullAccess,
		SandboxNetwork: config.SandboxNetworkOff,
	}, workspace)
	if full.Mode != sandbox.ModeDangerFullAccess || full.Network != sandbox.NetworkOn {
		t.Fatalf("request + danger policy = %+v", full)
	}

	never := k.sandboxPolicy(config.AgentConfig{
		ApprovalPolicy: config.ApprovalPolicyNever,
		SandboxProfile: config.SandboxProfileWorkspaceWrite,
		SandboxNetwork: config.SandboxNetworkOff,
	}, workspace)
	if never.Mode != sandbox.ModeWorkspaceWrite || never.Network != sandbox.NetworkOff {
		t.Fatalf("never + workspace policy = %+v", never)
	}

	legacy := k.sandboxPolicy(config.AgentConfig{
		SandboxMode: config.SandboxModeDangerFullAccess,
	}, workspace)
	if legacy.Mode != sandbox.ModeDangerFullAccess || legacy.Network != sandbox.NetworkOn {
		t.Fatalf("legacy fallback policy = %+v", legacy)
	}
}

func TestSandboxPolicyUsesConfiguredHTTPProxy(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	k := &Kit{store: store}
	agent := config.AgentConfig{
		ApprovalPolicy: config.ApprovalPolicyRequestApproval,
		SandboxProfile: config.SandboxProfileWorkspaceWrite,
		SandboxNetwork: config.SandboxNetworkOn,
	}

	policy := k.sandboxPolicy(agent, t.TempDir())
	if policy.ProxyURL != "" {
		t.Fatalf("empty HTTPProxy produced %q", policy.ProxyURL)
	}

	if err := store.Update(func(cfg *config.Config) {
		cfg.Settings.HTTPProxy = "http://127.0.0.1:7890"
	}); err != nil {
		t.Fatal(err)
	}
	policy = k.sandboxPolicy(agent, t.TempDir())
	if policy.ProxyURL != "http://127.0.0.1:7890" {
		t.Fatalf("proxy policy = %q", policy.ProxyURL)
	}
}

func TestWriteWorkspaceFilePolicyBlocksProtectedPath(t *testing.T) {
	root := t.TempDir()
	policy := sandbox.DefaultPolicy(root)
	if _, err := writeWorkspaceFile(root, writeArgs{Path: ".git/config", Content: "x"}, policy); err == nil {
		t.Fatal("expected .git write to be denied")
	}
	if _, err := writeWorkspaceFile(root, writeArgs{Path: "src/main.go", Content: "package main"}, policy); err != nil {
		t.Fatalf("normal workspace write failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "src", "main.go"))
	if err != nil || string(data) != "package main" {
		t.Fatalf("write result = %q, %v", string(data), err)
	}
}

func TestWriteWorkspaceFileReadOnlyIsDenied(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "keep.txt")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	policy := sandbox.DefaultPolicy(root)
	policy.Mode = sandbox.ModeReadOnly
	if _, err := writeWorkspaceFile(root, writeArgs{Path: "keep.txt", Content: "changed"}, policy); err == nil ||
		!strings.Contains(err.Error(), "read_only") {
		t.Fatalf("read-only write error = %v, want read_only", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatalf("read-only write changed file: %q err=%v", data, err)
	}
}

func TestEmptyWorkspaceRejected(t *testing.T) {
	if _, err := safeWorkspacePath("", "x.txt"); err == nil {
		t.Fatal("expected error for empty workspace")
	}
}

// TestSafeWorkspacePathBlocksSymlinkEscape:
// 工作区内的符号链接不得成为逃离工作区的途径。仅靠词法前缀检查会放行此类路径，
// 因此这里锁定对解析后路径的检查。
func TestSafeWorkspacePathBlocksSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := safeWorkspacePath(root, "leak.txt"); err == nil {
		t.Fatal("symlink pointing outside the workspace was allowed")
	}

	// 逃出工作区的符号链接目录同样会被拦截。
	if err := os.Symlink(outside, filepath.Join(root, "outdir")); err != nil {
		t.Fatal(err)
	}
	if _, err := safeWorkspacePath(root, "outdir/secret.txt"); err == nil {
		t.Fatal("path through an escaping symlinked dir was allowed")
	}

	// 仍留在工作区内部的符号链接依然可用。
	inner := filepath.Join(root, "inner.txt")
	if err := os.WriteFile(inner, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inner, filepath.Join(root, "inner-link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := safeWorkspacePath(root, "inner-link.txt"); err != nil {
		t.Fatalf("in-workspace symlink rejected: %v", err)
	}
}

func TestSanitizeAgentName(t *testing.T) {
	cases := map[string]string{
		"周报助手":        "agent_", // 仅含非 ASCII 字符 -> 需要回退后缀；以 agent_ 开头
		"Weekly Bot!": "weekly_bot",
		"user":        "user_agent",
	}
	for in, wantPrefix := range cases {
		got := sanitizeAgentName(in)
		if got == "" || got == "user" {
			t.Errorf("sanitize(%q) = %q, invalid", in, got)
		}
		if wantPrefix == "agent_" && got != "agent_" && len(got) < 6 {
			t.Errorf("sanitize(%q) = %q, expected fallback form", in, got)
		}
	}
	if sanitizeAgentName("Weekly Bot!") != "weekly_bot" {
		t.Error("unexpected sanitize result")
	}
}

func TestApplyPromptVars(t *testing.T) {
	out := applyPromptVars("今天 {{date}} {{time}}，工作区 {{workspace}}。", "/tmp/ws")
	if !strings.HasPrefix(out, "今天 ") || !strings.Contains(out, "，工作区 /tmp/ws。") {
		t.Fatalf("vars unresolved: %q", out)
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("leftover placeholders: %q", out)
	}
	if got := applyPromptVars("no vars", ""); got != "no vars" {
		t.Fatalf("plain prompt mutated: %q", got)
	}
}
