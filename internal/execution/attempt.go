package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nep-ai/difqo/internal/store"
)

type Options struct {
	Image, Check, ArtifactRoot, AuthHome string
	Runtime                              string
	DisableSELinuxLabel                  bool
	runtime                              containerRuntime
}

func (o *Options) Validate(ctx context.Context, repo string) error {
	o.runtime = containerRuntime{}
	if o.Image == "" || strings.HasPrefix(o.Image, "-") || strings.TrimSpace(o.Check) == "" || len(o.Check) > 4096 {
		return errors.New("Codex execution requires a local image and a check command")
	}
	for _, path := range []string{o.ArtifactRoot, o.AuthHome} {
		if err := mountPath(path); err != nil {
			return err
		}
	}
	// The credential directory must be dedicated to Difqo, with no configured tools.
	authHome, err := filepath.EvalSymlinks(o.AuthHome)
	if err != nil || mountPath(authHome) != nil {
		return errors.New("could not resolve the dedicated Codex home")
	}
	o.AuthHome = authHome
	info, err := os.Stat(authHome)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("Codex home must be an existing private directory")
	}
	for _, path := range []string{repo, os.Getenv("HOME"), filepath.Join(os.Getenv("HOME"), ".codex"), os.Getenv("CODEX_HOME")} {
		if resolved, err := filepath.EvalSymlinks(path); err == nil && authHome == resolved {
			return errors.New("use a dedicated Difqo Codex home")
		}
	}
	if rel, err := filepath.Rel(authHome, o.ArtifactRoot); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("use a dedicated Difqo Codex home")
	}
	o.runtime, err = o.resolveRuntime(ctx)
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("task was not claimed: %w", err)
	}
	return err
}

func (o Options) Run(ctx context.Context, repo string, task store.Task) store.Result {
	failure := store.Result{ExitCode: 125, Output: "could not prepare isolated execution"}
	if o.runtime.executable == "" {
		return failure
	}
	if err := os.MkdirAll(o.ArtifactRoot, 0o700); err != nil {
		return failure
	}
	dir, err := os.MkdirTemp(o.ArtifactRoot, fmt.Sprintf("attempt-%d-", task.Attempt.ID))
	if err != nil {
		return failure
	}
	workspace := filepath.Join(dir, "workspace")
	defer func() {
		_ = os.RemoveAll(workspace)
		_ = os.Remove(dir) // Only remove empty attempt directories.
	}()

	baseRepo := filepath.Join(dir, "base.git")
	keepBase := false
	defer func() {
		if !keepBase {
			_ = os.RemoveAll(baseRepo)
		}
	}()
	if err := cloneSnapshot(ctx, repo, baseRepo, "--bare"); err != nil {
		return failure
	}
	if err := cloneSnapshot(ctx, baseRepo, workspace); err != nil {
		return failure
	}
	base, err := snapshotHead(ctx, workspace)
	if err != nil {
		return failure
	}

	goal := task.Goal
	if task.Decision != nil {
		goal = fmt.Sprintf("Saved project decision (revision %d):\n%s\n\nTask goal:\n%s",
			task.Decision.ID, task.Decision.Body, goal)
	}
	c := container{name: "difqo-" + filepath.Base(dir), workspace: workspace, runtime: o.runtime}
	if diagnostic, err := c.codex(ctx, o.AuthHome, goal); err != nil {
		if errors.Is(err, ErrAuthentication) {
			return store.Result{ExitCode: 125, Output: "Codex sign-in required", Blocked: true}
		}
		result := store.Result{ExitCode: 125, Output: diagnostic}
		if os.WriteFile(filepath.Join(dir, "execution.log"), []byte(diagnostic+"\n"), 0o600) == nil {
			result.Artifacts = filepath.Base(dir)
		}
		return result
	}

	var tooLarge bool
	diffExit, err := captureFile(dir, "change.diff", &tooLarge, func(w io.Writer) (int, error) {
		return c.capture(ctx, w, io.Discard, `git -c core.hooksPath=/dev/null add -N -A &&
			git -c core.hooksPath=/dev/null diff --no-ext-diff --no-textconv --binary "$1" --`,
			base)
	})
	if err != nil || diffExit != 0 || tooLarge {
		_ = os.Remove(filepath.Join(dir, "change.diff"))
		return store.Result{ExitCode: 125, Output: "could not capture a complete diff"}
	}

	patch, err := os.ReadFile(filepath.Join(dir, "change.diff"))
	if err != nil {
		return store.Result{ExitCode: 125, Output: "could not read the captured patch"}
	}
	result := store.Result{Artifacts: filepath.Base(dir)}
	check := func(w io.Writer) (int, error) {
		return c.capture(ctx, w, w, o.Check)
	}
	if len(patch) != 0 {
		snapshot := store.Snapshot{Base: base, PatchSHA256: patchDigest(patch), Image: c.runtime.imageID, Command: o.Check}
		if !ValidSnapshot(snapshot) {
			return store.Result{ExitCode: 125, Output: "could not record the review snapshot", Artifacts: result.Artifacts}
		}
		result.Snapshot = &snapshot
		keepBase = true
		checkWorkspace := filepath.Join(dir, "check-workspace")
		defer os.RemoveAll(checkWorkspace)
		check = func(w io.Writer) (int, error) {
			checkContainer := container{name: c.name, workspace: checkWorkspace, runtime: c.runtime}
			return checkContainer.checkSnapshot(ctx, baseRepo, snapshot, patch, w)
		}
	}

	tooLarge = false
	checkExit, err := captureFile(dir, "check.log", &tooLarge, check)
	if err != nil {
		result.ExitCode = 125
		result.Output = "could not complete the original check"
		_ = os.WriteFile(filepath.Join(dir, "execution.log"), []byte(err.Error()+"\n"), 0o600)
		return result
	}
	result.ExitCode = checkExit
	result.Output = "Codex turn and check finished"
	if tooLarge {
		result.Output += " (check log truncated)"
	}
	return result
}

type cappedOutput struct {
	writer    io.Writer
	remaining int
	truncated *bool
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	kept := min(len(p), w.remaining)
	if kept != len(p) {
		*w.truncated = true
	}
	n, err := w.writer.Write(p[:kept])
	w.remaining -= n
	if err != nil {
		return n, err
	}
	return len(p), nil
}

func captureFile(dir, name string, truncated *bool, capture func(io.Writer) (int, error)) (int, error) {
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 125, err
	}
	code, err := capture(&cappedOutput{writer: f, remaining: MaxEvidenceBytes, truncated: truncated})
	return code, errors.Join(err, f.Close())
}
