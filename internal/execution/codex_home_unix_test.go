//go:build darwin || linux

package execution

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCodexHomeSerializesTurns(t *testing.T) {
	dir := t.TempDir()
	release, err := lockCodexHome(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if second, err := lockCodexHome(ctx, dir); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			second()
		}
		t.Fatalf("second turn should wait for the first, got %v", err)
	}
	release()
	release = nil
	nextCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	next, err := lockCodexHome(nextCtx, dir)
	if err != nil {
		t.Fatalf("next turn could not reuse the same sign-in: %v", err)
	}
	next()
}
