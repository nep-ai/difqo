//go:build unix

package controlplane

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
(
  : > started
  sleep 0.6
  : > survived
) </dev/null >/dev/null 2>&1 &
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
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
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
	time.Sleep(650 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "survived")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a child kept running after setup timed out")
	}
}
