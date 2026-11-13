package services

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"agentmate/internal/sandbox"
)

// TestAppInfoReportsInjectedVersion AppInfo 必须暴露从 main
// 注入的版本（单一事实来源）。
func TestAppInfoReportsInjectedVersion(t *testing.T) {
	AppVersion = "9.9.9-test"
	defer func() { AppVersion = "dev" }()
	a := NewAppService(&Services{}, "", "", nil)
	info := a.AppInfo()
	if info["version"] != "9.9.9-test" || info["name"] != "AgentMate" {
		t.Fatalf("AppInfo = %v", info)
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; info["platform"] != want {
		t.Fatalf("platform = %q, want %q", info["platform"], want)
	}
}

func TestSandboxStatusReportsBackend(t *testing.T) {
	a := NewAppService(&Services{}, "", "", nil)
	got := a.SandboxStatus()
	want := sandbox.BackendStatus()
	if got.Available != want.Available || got.Backend != want.Backend ||
		got.Error != want.Error || got.ProfileSID != want.ProfileSID {
		t.Fatalf("SandboxStatus = %+v, want %+v", got, want)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"available":`, `"backend":`, `"error":`, `"profileSid":`} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("SandboxStatus JSON missing %s: %s", key, raw)
		}
	}
}
