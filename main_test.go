package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// TestRotatingWriterRotates: exceeding maxBytes moves the file to .old and
// subsequent writes land in a fresh app.log — long-resident tray apps used to
// grow app.log without bound (rotation ran only at startup).
func TestRotatingWriterRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w, err := newRotatingWriter(path, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	// Write past the cap: some writes go into the first generation, the rest
	// into the rotated one.
	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte("0123456789")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatalf("rotation did not produce %s.old: %v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || len(data) > 64+10 {
		t.Fatalf("fresh log size = %d", len(data))
	}
	oldData, err := os.ReadFile(path + ".old")
	if err != nil || !strings.HasPrefix(string(oldData), "0123456789") {
		t.Fatalf("old log missing first generation: err=%v", err)
	}
}

// TestShutdownBounded: a wedged shutdown hook must not hold Quit() — and with
// it the in-app updater's 30 s parent-exit window — hostage. Missing that
// window makes the updater relaunch the OLD binary, i.e. two apps at once.
func TestShutdownBounded(t *testing.T) {
	wedged := make(chan struct{})
	t.Cleanup(func() { close(wedged) })

	start := time.Now()
	shutdownBounded("测试挂死钩子", 25*time.Millisecond, func() { <-wedged })
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("shutdownBounded waited %s for a wedged hook", elapsed)
	}

	ran := make(chan struct{})
	shutdownBounded("测试正常钩子", time.Second, func() { close(ran) })
	select {
	case <-ran:
	default:
		t.Fatal("shutdownBounded must run the hook")
	}

	// A panicking hook is logged, never propagated into Quit().
	shutdownBounded("测试恐慌钩子", time.Second, func() { panic("boom") })
}

// TestSingleInstanceOptions: a second launch (desktop icon, Start menu) must
// be forwarded to the running instance and surface its window instead of
// opening another fully usable copy. UniqueID is the lock name both copies
// must agree on, so it is pinned here: changing it silently re-enables
// multi-instance launches alongside an older running build.
func TestSingleInstanceOptions(t *testing.T) {
	activations := 0
	opts := singleInstanceOptions(func() { activations++ })
	if opts == nil {
		t.Fatal("singleInstanceOptions returned nil; Wails would not enforce a single instance")
	}
	if opts.UniqueID != "com.workbuddy.agent" {
		t.Fatalf("UniqueID = %q, want the stable product identifier", opts.UniqueID)
	}
	if opts.OnSecondInstanceLaunch == nil {
		t.Fatal("OnSecondInstanceLaunch is nil; the existing window would never be surfaced")
	}
	opts.OnSecondInstanceLaunch(application.SecondInstanceData{Args: []string{"--second"}})
	if activations != 1 {
		t.Fatalf("second launch activated the window %d times, want 1", activations)
	}

	// A launch can race window creation; a nil activator must be tolerated.
	singleInstanceOptions(nil).OnSecondInstanceLaunch(application.SecondInstanceData{})

	// And the production activator must not panic before the window exists.
	activateMainWindow(nil)
}
