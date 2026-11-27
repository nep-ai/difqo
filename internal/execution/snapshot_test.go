package execution

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nep-ai/difqo/internal/store"
)

func TestCheckedCandidate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	run := func(repo string, args ...string) string {
		t.Helper()
		cmd := git(ctx, append([]string{"-C", repo}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	write := func(file, contents string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run(root, "init", "--template=", "--initial-branch=difqo-checked-candidate", source)
	write(filepath.Join(source, "greeting"), "before\n")
	run(source, "add", "greeting")
	run(source, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
		"-c", "commit.gpgSign=false", "commit", "-m", "Base")
	base := strings.TrimSpace(run(source, "rev-parse", "HEAD"))
	baseRepo := filepath.Join(root, "base.git")
	if err := cloneSnapshot(ctx, source, baseRepo, "--bare"); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(source, "greeting"), "after\n")
	patch := []byte(run(source, "diff", "--binary", base))
	candidate, err := recordCandidate(ctx, baseRepo, base, patch)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(run(baseRepo, "rev-parse", candidate+"^")); got != base {
		t.Fatalf("candidate parent = %q, want %q", got, base)
	}
	if got := run(baseRepo, "diff", "--binary", base, candidate); got != string(patch) {
		t.Fatalf("candidate diff = %q, want %q", got, patch)
	}
	write(filepath.Join(source, "greeting"), "unrelated source edit\n")

	for _, scenario := range []struct {
		name, candidate, head string
	}{
		{"candidate", candidate, candidate},
		{"legacy", "", base},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			workspace := filepath.Join(root, scenario.name)
			snapshot := store.Snapshot{Base: base, Candidate: scenario.candidate}
			if err := prepareCheckWorkspace(ctx, baseRepo, workspace, snapshot, patch); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(run(workspace, "rev-parse", "HEAD")); got != scenario.head {
				t.Fatalf("check HEAD = %q, want %q", got, scenario.head)
			}
			content, err := os.ReadFile(filepath.Join(workspace, "greeting"))
			if err != nil || string(content) != "after\n" {
				t.Fatalf("check file = %q, err = %v", content, err)
			}
			if scenario.candidate != "" && run(workspace, "status", "--porcelain") != "" {
				t.Fatal("candidate checkout is dirty before running the check")
			}
		})
	}

	snapshot := store.Snapshot{Base: base, Candidate: strings.Repeat("a", len(candidate))}
	if err := prepareCheckWorkspace(ctx, baseRepo, filepath.Join(root, "mismatch"), snapshot, patch); err == nil {
		t.Fatal("accepted a checkout that does not match the recorded candidate")
	}
}
