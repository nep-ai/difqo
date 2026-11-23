package execution

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/nep-ai/difqo/internal/store"
)

// ReviewProjectSnapshot uses fixed Git reads against a committed tree. The
// goal, specialist instructions, and saved decision are report context.
func ReviewProjectSnapshot(ctx context.Context, repo string, task store.Task) store.Result {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "--no-optional-locks", "-C", repo}, args...)...)
		for _, variable := range cmd.Environ() {
			if !strings.HasPrefix(variable, "GIT_") {
				cmd.Env = append(cmd.Env, variable)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_TERMINAL_PROMPT=0")
		return cmd
	}
	head, err := command("rev-parse", "--verify", "HEAD^{commit}").Output()
	base := strings.TrimSpace(string(head))
	if err != nil || !commitID.MatchString(base) {
		return store.Result{ExitCode: 125, Output: "Could not resolve the selected project's committed snapshot."}
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "Snapshot %s\nGoal: %s\nSpecialist purpose: %s\nExpected output: %s\n",
		base, task.Goal, task.Run.Purpose, task.Run.Output)
	if task.Decision != nil {
		fmt.Fprintf(&output, "Saved project decision %d:\n%s\n", task.Decision.ID, task.Decision.Body)
	}
	var truncated bool
	limit := &cappedOutput{writer: &output, remaining: 24 << 10, truncated: &truncated}
	output.WriteString("\nChanges in the selected commit:\n")
	diff := command("diff-tree", "--root", "--first-parent", "-m", "--no-commit-id", "--no-ext-diff", "--no-textconv",
		"--name-status", "-r", base, "--")
	diff.Stdout = limit
	if err := diff.Run(); err != nil {
		return store.Result{ExitCode: 125, Output: "Could not inspect changes in the selected project snapshot."}
	}
	if !truncated {
		output.WriteString("\nTracked files in this snapshot:\n")
		tree := command("ls-tree", "-r", "--name-only", base, "--")
		tree.Stdout = limit
		if err := tree.Run(); err != nil {
			return store.Result{ExitCode: 125, Output: "Could not list files in the selected project snapshot."}
		}
	}
	if truncated {
		output.WriteString("\nSnapshot report truncated.\n")
	}
	return store.Result{Output: strings.ToValidUTF8(output.String(), "�")}
}
