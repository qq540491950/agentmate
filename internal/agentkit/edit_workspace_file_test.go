package agentkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentmate/internal/sandbox"
)

func writeEditFixture(t *testing.T, root, rel, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEditWorkspaceFileUniqueMatch(t *testing.T) {
	root := t.TempDir()
	path := writeEditFixture(t, root, "notes.txt", "alpha\nbeta\ngamma\n")
	res, err := editWorkspaceFile(root, editArgs{
		Path:    "notes.txt",
		OldText: "beta",
		NewText: "BETA",
	}, sandbox.DefaultPolicy(root))
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Bytes != len("alpha\nBETA\ngamma\n") {
		t.Fatalf("result = %+v", res)
	}
	if res.AddedLines != 1 || res.RemovedLines != 1 {
		t.Fatalf("added/removed = %d/%d, want 1/1", res.AddedLines, res.RemovedLines)
	}
	if !strings.Contains(res.Diff, "-beta") || !strings.Contains(res.Diff, "+BETA") {
		t.Fatalf("diff missing changed line:\n%s", res.Diff)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("file = %q, err = %v", data, err)
	}
}

func TestEditWorkspaceFileNoMatch(t *testing.T) {
	root := t.TempDir()
	path := writeEditFixture(t, root, "notes.txt", "alpha\n")
	_, err := editWorkspaceFile(root, editArgs{
		Path:    "notes.txt",
		OldText: "missing",
		NewText: "x",
	}, sandbox.DefaultPolicy(root))
	if err == nil || !strings.Contains(err.Error(), "未找到匹配内容") {
		t.Fatalf("error = %v, want 未找到匹配内容", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\n" {
		t.Fatalf("file changed on failed edit: %q", data)
	}
}

func TestEditWorkspaceFileMultipleMatches(t *testing.T) {
	root := t.TempDir()
	path := writeEditFixture(t, root, "notes.txt", "foo foo")
	_, err := editWorkspaceFile(root, editArgs{
		Path:    "notes.txt",
		OldText: "foo",
		NewText: "bar",
	}, sandbox.DefaultPolicy(root))
	if err == nil || !strings.Contains(err.Error(), "匹配到 2 处") ||
		!strings.Contains(err.Error(), "replaceAll=true") {
		t.Fatalf("error = %v, want a multi-match hint", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "foo foo" {
		t.Fatalf("file changed on ambiguous edit: %q", data)
	}

	res, err := editWorkspaceFile(root, editArgs{
		Path:       "notes.txt",
		OldText:    "foo",
		NewText:    "bar",
		ReplaceAll: true,
	}, sandbox.DefaultPolicy(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.AddedLines != 1 || res.RemovedLines != 1 {
		t.Fatalf("replace-all line counts = %d/%d", res.AddedLines, res.RemovedLines)
	}
	if data, _ := os.ReadFile(path); string(data) != "bar bar" {
		t.Fatalf("replace-all file = %q", data)
	}
}

func TestEditWorkspaceFileRejectsOutsideProtectedAndSymlink(t *testing.T) {
	root := t.TempDir()
	policy := sandbox.DefaultPolicy(root)

	outside := filepath.Join(filepath.Dir(root), "outside-edit.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	if _, err := editWorkspaceFile(root, editArgs{
		Path: "../outside-edit.txt", OldText: "outside", NewText: "changed",
	}, policy); err == nil || !strings.Contains(err.Error(), "路径越出工作区范围") {
		t.Fatalf("outside error = %v", err)
	}
	if data, _ := os.ReadFile(outside); string(data) != "outside" {
		t.Fatalf("outside file changed: %q", data)
	}

	protected := writeEditFixture(t, root, ".git/config", "original")
	if _, err := editWorkspaceFile(root, editArgs{
		Path: ".git/config", OldText: "original", NewText: "changed",
	}, policy); err == nil || !strings.Contains(err.Error(), "protected_path") {
		t.Fatalf("protected error = %v", err)
	}
	if data, _ := os.ReadFile(protected); string(data) != "original" {
		t.Fatalf("protected file changed: %q", data)
	}

	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "leak.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := editWorkspaceFile(root, editArgs{
		Path: "leak.txt", OldText: "secret", NewText: "changed",
	}, policy); err == nil || !strings.Contains(err.Error(), "路径越出工作区范围") {
		t.Fatalf("symlink error = %v", err)
	}
	if data, _ := os.ReadFile(secret); string(data) != "secret" {
		t.Fatalf("symlink target changed: %q", data)
	}
}

func TestEditWorkspaceFileReadOnlyIsDenied(t *testing.T) {
	root := t.TempDir()
	path := writeEditFixture(t, root, "keep.txt", "original")
	policy := sandbox.DefaultPolicy(root)
	policy.Mode = sandbox.ModeReadOnly
	if _, err := editWorkspaceFile(root, editArgs{
		Path: "keep.txt", OldText: "original", NewText: "changed",
	}, policy); err == nil || !strings.Contains(err.Error(), "read_only") {
		t.Fatalf("read-only error = %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "original" {
		t.Fatalf("read-only edit changed file: %q", data)
	}
}

func TestEditWorkspaceFilePreservesCRLFBytes(t *testing.T) {
	root := t.TempDir()
	path := writeEditFixture(t, root, "windows.txt", "alpha\r\nbeta\r\ngamma\r\n")
	if _, err := editWorkspaceFile(root, editArgs{
		Path: "windows.txt", OldText: "beta", NewText: "BETA",
	}, sandbox.DefaultPolicy(root)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "alpha\r\nBETA\r\ngamma\r\n" {
		t.Fatalf("CRLF bytes not preserved: %q", data)
	}
}

func TestEditWorkspaceFilePreservesPermissions(t *testing.T) {
	root := t.TempDir()
	path := writeEditFixture(t, root, "mode.txt", "before")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editWorkspaceFile(root, editArgs{
		Path: "mode.txt", OldText: "before", NewText: "after",
	}, sandbox.DefaultPolicy(root)); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("mode changed from %o to %o", before.Mode().Perm(), after.Mode().Perm())
	}
}

func TestEditWorkspaceFileDiffIsTruncated(t *testing.T) {
	root := t.TempDir()
	var oldContent, newContent strings.Builder
	for i := 0; i < 260; i++ {
		fmt.Fprintf(&oldContent, "old-%03d\n", i)
		fmt.Fprintf(&newContent, "new-%03d\n", i)
	}
	writeEditFixture(t, root, "large.txt", oldContent.String())
	res, err := editWorkspaceFile(root, editArgs{
		Path:    "large.txt",
		OldText: oldContent.String(),
		NewText: newContent.String(),
	}, sandbox.DefaultPolicy(root))
	if err != nil {
		t.Fatal(err)
	}
	if res.AddedLines != 260 || res.RemovedLines != 260 {
		t.Fatalf("full line counts = %d/%d, want 260/260", res.AddedLines, res.RemovedLines)
	}
	if !strings.Contains(res.Diff, "已截断") {
		t.Fatalf("large diff was not truncated:\n%s", res.Diff)
	}
	if lines := strings.Count(res.Diff, "\n"); lines > maxDiffLines {
		t.Fatalf("diff has %d lines, want <= %d", lines, maxDiffLines)
	}
	if len(res.Diff) > maxDiffBytes {
		t.Fatalf("diff has %d bytes, want <= %d", len(res.Diff), maxDiffBytes)
	}
}

func TestWorkspaceFileToolsRegistersEdit(t *testing.T) {
	root := t.TempDir()
	tools, err := workspaceFileTools(root, false, sandbox.DefaultPolicy(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Name() != "edit_workspace_file" {
			continue
		}
		if !strings.Contains(tl.Description(), "修改已有文件优先用本工具") ||
			!strings.Contains(tl.Description(), "write_workspace_file") {
			t.Fatalf("unexpected edit description: %q", tl.Description())
		}
		return
	}
	t.Fatal("edit_workspace_file was not registered")
}
