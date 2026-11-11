//go:build !windows

package sandbox

import (
	"context"
	"fmt"
	"os/exec"
)

func configureDirectProcess(cmd *exec.Cmd) {}

func runPlatform(ctx context.Context, req Request, policy Policy) (Result, error) {
	return Result{}, fmt.Errorf("当前平台未实现 OS 沙箱: %w", ErrUnavailable)
}

func statusPlatform() Status {
	return Status{Available: false, Backend: "unsupported", Error: "当前平台未实现 OS 沙箱"}
}

func closePlatform() {}
