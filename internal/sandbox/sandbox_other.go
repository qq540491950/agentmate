//go:build !windows

package sandbox

import (
	"context"
	"os"
)

func runPlatform(ctx context.Context, req Request, policy Policy) (Result, error) {
	// The P0 backend is Windows-only. Non-Windows development builds keep the
	// historical direct-exec behavior so the existing test suite and local
	// development remain usable; Status reports that no OS sandbox is active.
	return runDirect(ctx, req, append(os.Environ(), policy.ExtraEnv...))
}

func statusPlatform() Status {
	return Status{Available: false, Backend: "unsupported"}
}

func closePlatform() {}
