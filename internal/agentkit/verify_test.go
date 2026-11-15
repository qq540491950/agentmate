package agentkit

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectVerifyCommands(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"scripts":{"test":"vitest run","build":"vite build"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"go test ./...", "npm test", "npm run build", "npx tsc --noEmit"}
	if got := detectVerifyCommands(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("detected = %#v, want %#v", got, want)
	}
}
