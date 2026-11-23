package execution

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
)

func TestSnapshotResultFitsRequestLimit(t *testing.T) {
	output := strings.Repeat("🧪", 4096*2) + strings.Repeat("<", 4096) + strings.Repeat("\t", 24<<10)
	result := snapshotResult(output, true)
	payload, err := json.Marshal(result)
	if err != nil || len(payload) > 64<<10 {
		t.Fatalf("snapshot result is %d bytes, marshal error %v", len(payload), err)
	}
	if !utf8.ValidString(result.Output) || !strings.HasSuffix(result.Output, "\nSnapshot report truncated.\n") {
		t.Fatal("expected valid UTF-8 with a truncation notice")
	}
}

func TestSnapshotReportChangesOnly(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	commitFile := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte("fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "--", name)
		git("-c", "user.name=Difqo", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false",
			"-c", "core.hooksPath=/dev/null", "commit", "-qm", name)
	}

	git("init", "-q", "--template=")
	commitFile("existing.txt")
	commitFile("changed.txt")
	result := ReviewProjectSnapshot(ctx, repo, store.Task{Run: &store.SpecialistRun{Report: store.ReportChanges}})
	if result.ExitCode != 0 || !strings.Contains(result.Output, "A\tchanged.txt") {
		t.Fatalf("expected the latest commit's changed path: %+v", result)
	}
	if strings.Contains(result.Output, "Tracked files in this snapshot:") || strings.Contains(result.Output, "existing.txt") {
		t.Fatalf("changes-only report included the tracked file inventory: %s", result.Output)
	}
}
