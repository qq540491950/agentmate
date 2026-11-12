//go:build !windows

package terminal

import (
	"context"
	"strings"
	"testing"

	"agentmate/internal/config"
)

func TestUnixAutoUsesLoginShell(t *testing.T) {
	host := fakeHost{
		goos: "darwin",
		env: map[string]string{
			"SHELL": "/bin/zsh",
			"PATH":  "/bin",
		},
		look: map[string]string{
			"zsh":  "/bin/zsh",
			"bash": "/bin/bash",
			"sh":   "/bin/sh",
			"pwsh": "/usr/local/bin/pwsh",
		},
		exists: map[string]bool{
			"/bin/zsh":            true,
			"/bin/bash":           true,
			"/bin/sh":             true,
			"/usr/local/bin/pwsh": true,
		},
		probe: func(context.Context, string, ...string) (string, int, error) {
			return "", 0, nil
		},
	}
	inv := Detect(context.Background(), host.host())
	if !strings.Contains(inv.Auto.DisplayName, "zsh") {
		t.Fatalf("auto = %q, want login zsh: %+v", inv.Auto.DisplayName, inv.Shells)
	}
	spec, err := Resolve(context.Background(), config.TerminalSettings{Mode: config.TerminalModeBash}, host.host())
	if err != nil {
		t.Fatal(err)
	}
	if spec.Mode != config.TerminalModeBash || spec.Path != "/bin/bash" {
		t.Fatalf("bash resolve = %+v", spec)
	}
}
