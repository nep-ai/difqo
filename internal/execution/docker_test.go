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
	if !strings.Contains(message, "Docker rejected a bind mount") || strings.Contains(message, "secret-token") {
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
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := (container{}).codex(ctx, "", "fixture goal")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected host deadline to interrupt the Docker stream, got %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Docker stream stayed open after the host deadline")
	}
}
