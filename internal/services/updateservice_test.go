package services

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"go-workbuddy/internal/config"
)

func TestDetectUpdateMode(t *testing.T) {
	cases := []struct {
		name           string
		goos           string
		appImage       string
		exeDirWritable bool
		wantMode       string
		wantReasonHas  string
	}{
		{
			name: "windows user-scope install can self-update", goos: "windows", exeDirWritable: true,
			wantMode: UpdateModeAutoInstall,
		},
		{
			// The machine-scope NSIS install lands in %ProgramFiles% and the
			// updater has no elevation path, so the swap target is unreachable.
			name: "windows program files cannot self-update", goos: "windows", exeDirWritable: false,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "Program Files",
		},
		{
			name: "darwin writable bundle", goos: "darwin", exeDirWritable: true,
			wantMode: UpdateModeAutoInstall,
		},
		{
			name: "darwin read-only location", goos: "darwin", exeDirWritable: false,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "不可写",
		},
		{
			// deb/rpm installs put the binary in /usr/local/bin (root-owned).
			name: "linux deb is notify-only", goos: "linux", appImage: "", exeDirWritable: true,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "deb/rpm",
		},
		{
			name: "linux appimage can self-update", goos: "linux", appImage: "/home/u/Apps/workbuddy.AppImage", exeDirWritable: true,
			wantMode: UpdateModeAutoInstall,
		},
		{
			name: "linux appimage on read-only mount", goos: "linux", appImage: "/opt/ro/workbuddy.AppImage", exeDirWritable: false,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "不可写",
		},
		{
			name: "unknown platform", goos: "freebsd", exeDirWritable: true,
			wantMode: UpdateModeNotifyOnly, wantReasonHas: "不支持",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, reason := detectUpdateMode(tc.goos, tc.appImage, tc.exeDirWritable)
			if mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q (reason %q)", mode, tc.wantMode, reason)
			}
			if tc.wantReasonHas != "" && !strings.Contains(reason, tc.wantReasonHas) {
				t.Fatalf("reason = %q, want it to mention %q", reason, tc.wantReasonHas)
			}
			if tc.wantReasonHas == "" && reason != "" {
				t.Fatalf("reason = %q, want empty", reason)
			}
		})
	}
}

func TestParseUpdatePublicKey(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	if _, err := parseUpdatePublicKey(""); err != nil {
		t.Fatalf("empty key must be accepted as 'no signature check': %v", err)
	}
	got, err := parseUpdatePublicKey(base64.StdEncoding.EncodeToString(raw))
	if err != nil || string(got) != string(raw) {
		t.Fatalf("base64 key = %x, %v", got, err)
	}
	got, err = parseUpdatePublicKey(hex.EncodeToString(raw))
	if err != nil || string(got) != string(raw) {
		t.Fatalf("hex key = %x, %v", got, err)
	}
	// Pasted with a line break, as terminals wrap long base64.
	wrapped := base64.StdEncoding.EncodeToString(raw)
	wrapped = wrapped[:20] + "\n" + wrapped[20:]
	if got, err = parseUpdatePublicKey(wrapped); err != nil || string(got) != string(raw) {
		t.Fatalf("wrapped key = %x, %v", got, err)
	}
	if _, err = parseUpdatePublicKey("not-a-key"); err == nil {
		t.Fatal("expected an error for a non-key string")
	}
	if _, err = parseUpdatePublicKey(base64.StdEncoding.EncodeToString(raw[:16])); err == nil {
		t.Fatal("expected an error for a 16-byte key")
	}
}

// Status must be renderable before Bind: the settings page calls it on mount,
// and a failed updater init must not turn into a blank or panicking page.
func TestUpdateStatusBeforeBind(t *testing.T) {
	t.Cleanup(func() { AppVersion = "dev" })
	AppVersion = "1.2.3"
	u := NewUpdateService(nil)
	st := u.Status()
	if st.CurrentVersion != "1.2.3" {
		t.Fatalf("currentVersion = %q", st.CurrentVersion)
	}
	if st.State != "unconfigured" {
		t.Fatalf("state = %q, want unconfigured", st.State)
	}
	if st.DownloadURL == "" {
		t.Fatal("downloadUrl must always be set so notify-only can fall back to it")
	}
	if st.CanAutoInstall != (st.Mode == UpdateModeAutoInstall) {
		t.Fatalf("canAutoInstall = %v with mode %q", st.CanAutoInstall, st.Mode)
	}
}

// Skip and SetAutoCheck persist into config.json; both must survive a reload
// and must not disturb unrelated settings.
func TestSkipAndAutoCheckPersist(t *testing.T) {
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	before := store.Get().Settings.WorkspaceDir
	u := NewUpdateService(&Services{Store: store})

	if err := u.Skip("v0.6.0"); err != nil {
		t.Fatal(err)
	}
	if got := store.Get().Settings.SkippedVersion; got != "0.6.0" {
		t.Fatalf("skippedVersion = %q, want the v-prefix stripped", got)
	}
	if err := u.SetAutoCheck(false); err != nil {
		t.Fatal(err)
	}
	s := store.Get().Settings
	if s.UpdateAutoCheckEnabled() {
		t.Fatal("autoCheck must be off after SetAutoCheck(false)")
	}
	if s.WorkspaceDir != before {
		t.Fatalf("workspaceDir changed from %q to %q", before, s.WorkspaceDir)
	}
	if err := u.SetAutoCheck(true); err != nil {
		t.Fatal(err)
	}
	if !store.Get().Settings.UpdateAutoCheckEnabled() {
		t.Fatal("autoCheck must be on after SetAutoCheck(true)")
	}
	// Reopening the store (a restart) must see the same values.
	reopened, err := config.Open(store.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Settings().SkippedVersion; got != "0.6.0" {
		t.Fatalf("skippedVersion after reload = %q", got)
	}
}

func TestSkipRejectsEmptyVersion(t *testing.T) {
	u := NewUpdateService(nil)
	if err := u.Skip("  "); err == nil {
		t.Fatal("expected an error for an empty version")
	}
}
