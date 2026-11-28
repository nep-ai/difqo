//go:build !unix

package controlplane

import (
	"context"
	"os/exec"
	"time"
)

// The installed helper is only configured on macOS.
func setupCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/bash", args...)
	cmd.WaitDelay = 2 * time.Second
	return cmd
}
