package agentkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentmate/internal/sandbox"
)

func TestWriteWorkspaceFileRejectsStaleHash(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := sha256Hex([]byte("before"))

	if err := os.WriteFile(path, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := writeWorkspaceFile(root, writeArgs{
		Path: "notes.txt", Content: "agent", ExpectedSha256: before,
	}, sandbox.DefaultPolicy(root))
	if err == nil || !strings.Contains(err.Error(), "stale_file") {
		t.Fatalf("stale write = %v, want stale_file", err)
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "external" {
		t.Fatalf("stale write changed file: %q, %v", string(data), readErr)
	}

	if _, err := writeWorkspaceFile(root, writeArgs{
		Path: "notes.txt", Content: "agent", ExpectedSha256: sha256Hex([]byte("external")),
	}, sandbox.DefaultPolicy(root)); err != nil {
		t.Fatalf("fresh write failed: %v", err)
	}
}

func TestWriteWorkspaceFilePreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不保留 POSIX 权限位")
	}
	root := t.TempDir()
	path := filepath.Join(root, "run.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeWorkspaceFile(root, writeArgs{Path: "run.sh", Content: "#!/bin/sh\necho hi\n"}, sandbox.DefaultPolicy(root)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", info.Mode().Perm())
	}
}

func TestListWorkspaceFilesCapsAndSkipsDependencies(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"node_modules", ".git", "vendor"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < maxListEntries+20; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%03d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tl, err := ListFilesTool(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := tl.(runnableShellTool).Run(&shellToolTestContext{ctx: context.Background()}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := res["entries"].(string)
	if !strings.Contains(entries, "file-000.txt") {
		t.Fatalf("entries missing files: %q", entries[:minIntForTest(len(entries), 120)])
	}
	for _, skipped := range []string{"node_modules", ".git", "vendor"} {
		if strings.Contains(entries, skipped) {
			t.Fatalf("listing leaked %s", skipped)
		}
	}
	if res["truncated"] != true {
		t.Fatalf("truncated = %v, want true", res["truncated"])
	}
	if got := len(strings.Split(strings.TrimSpace(entries), "\n")); got > maxListEntries {
		t.Fatalf("entries = %d, want <= %d", got, maxListEntries)
	}
}

func TestReadWorkspaceFilePaginatesAndRejectsBinary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "long.txt")
	content := strings.Repeat("a", 300)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tl, err := ReadFileTool(root)
	if err != nil {
		t.Fatal(err)
	}
	run := tl.(runnableShellTool)
	ctx := &shellToolTestContext{ctx: context.Background()}
	first, err := run.Run(ctx, map[string]any{"path": "long.txt", "maxBytes": 100})
	if err != nil {
		t.Fatal(err)
	}
	if first["truncated"] != true {
		t.Fatalf("first read truncated = %v", first["truncated"])
	}
	next := int64(100)
	if got, ok := first["nextOffset"].(int64); ok {
		next = got
	}
	second, err := run.Run(ctx, map[string]any{"path": "long.txt", "offset": next, "maxBytes": 100})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(second["content"].(string), "aaa") {
		t.Fatalf("second page = %q", second["content"])
	}

	binary := filepath.Join(root, "binary.bin")
	if err := os.WriteFile(binary, []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Run(ctx, map[string]any{"path": "binary.bin"}); err == nil {
		t.Fatal("binary read should fail")
	}
}

func minIntForTest(a, b int) int {
	if a < b {
		return a
	}
	return b
}
