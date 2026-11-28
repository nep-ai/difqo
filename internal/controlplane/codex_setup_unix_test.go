//go:build unix

package controlplane

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCodexSetupTimeoutStopsChildBeforeCleanup(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	if err := os.WriteFile(helper, []byte(`
cd "$(dirname "$0")"
if [ "$1" = cleanup-login ]; then
  : > cleaned
  exit
fi
sleep 60 </dev/null >/dev/null 2>&1 &
printf '%s\n' "$!" > child
wait
`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	setup := &codexSetup{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		setup.runHelper(ctx, helper, "prepare", 0)
	}()
	var pid int
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if raw, err := os.ReadFile(filepath.Join(dir, "child")); err == nil {
			if parsed, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && parsed > 0 {
				pid = parsed
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper did not start its child")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("setup did not finish after cancellation")
	}
	if _, err := os.Stat(filepath.Join(dir, "cleaned")); err != nil {
		t.Fatalf("setup did not wait for login cleanup: %v", err)
	}
	if setup.view.State != "attention" || !strings.Contains(setup.view.Message, "timed out") {
		t.Fatalf("unexpected timeout status: %+v", setup.view)
	}
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		// A killed orphan can remain visible until its new parent reaps it.
		status, _ := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
		if strings.HasPrefix(strings.TrimSpace(string(status)), "Z") {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child %d kept running after setup timed out", pid)
		}
	}
}
