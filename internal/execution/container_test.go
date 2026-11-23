package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexDiagnosticOmitsRawOutput(t *testing.T) {
	message := codexDiagnostic("invalid mount config: /private/secret-token", nil, nil)
	if !strings.Contains(message, "rejected a bind mount") || strings.Contains(message, "secret-token") {
		t.Fatalf("unexpected diagnostic: %q", message)
	}
	message = codexDiagnostic("", nil, errSandbox)
	if !strings.Contains(message, "Codex sandbox startup failed") {
		t.Fatalf("unexpected diagnostic: %q", message)
	}
}

func TestCodexStopsReadingOnHostDeadline(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nif [ \"$1\" = run ]; then exec sleep 30; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		c := container{runtime: containerRuntime{executable: filepath.Join(bin, "docker")}}
		_, err := c.codex(ctx, "", "fixture goal")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected host deadline to interrupt the container stream, got %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("container stream stayed open after the host deadline")
	}
}
