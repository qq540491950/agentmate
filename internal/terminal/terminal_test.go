package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"agentmate/internal/config"
)

type fakeFileInfo struct {
	name string
	dir  bool
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0o644 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

type fakeHost struct {
	goos   string
	env    map[string]string
	look   map[string]string
	exists map[string]bool
	probe  func(context.Context, string, ...string) (string, int, error)
}

func (h fakeHost) host() Host {
	return Host{
		GOOS:   h.goos,
		Getenv: func(key string) string { return h.env[key] },
		LookPath: func(name string) (string, error) {
			if p, ok := h.look[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Stat: func(path string) (os.FileInfo, error) {
			if h.exists[strings.ToLower(filepath.Clean(path))] {
				return fakeFileInfo{name: filepath.Base(path)}, nil
			}
			return nil, os.ErrNotExist
		},
		Probe: h.probe,
	}
}

func TestParseWSLListIgnoresEmptyAndDuplicateNames(t *testing.T) {
	got := parseWSLList(utf16LE("\uFEFFUbuntu\r\n\r\nubuntu\r\nDebian\r\n"))
	if len(got) != 2 || got[0] != "Ubuntu" || got[1] != "Debian" {
		t.Fatalf("parseWSLList = %#v", got)
	}
}

func TestLegacySpecMatchesHistoricalShells(t *testing.T) {
	spec := LegacySpec()
	if runtime.GOOS == "windows" {
		if spec.Mode != config.TerminalModeCmd || spec.Path != "cmd.exe" {
			t.Fatalf("legacy windows spec = %+v", spec)
		}
		return
	}
	if spec.Mode != config.TerminalModeSh || spec.Path != "/bin/sh" {
		t.Fatalf("legacy unix spec = %+v", spec)
	}
}

func containsAll(values []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, value := range values {
			if value == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func utf16LE(s string) string {
	words := utf16.Encode([]rune(s))
	raw := make([]byte, len(words)*2)
	for i, word := range words {
		raw[i*2] = byte(word)
		raw[i*2+1] = byte(word >> 8)
	}
	return string(raw)
}
