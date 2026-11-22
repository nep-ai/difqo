package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nep-ai/difqo/internal/store"
)

type Options struct {
	Image, Check, ArtifactRoot, AuthHome string
}

func (o *Options) Validate(repo string) error {
	if o.Image == "" || strings.HasPrefix(o.Image, "-") || strings.TrimSpace(o.Check) == "" {
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
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("docker CLI is required for Codex execution")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, "docker", "image", "inspect", o.Image).Run(); err != nil {
		return errors.New("Docker must be running with the configured image available locally")
	}
	return nil
}

func (o Options) Run(ctx context.Context, repo string, task store.Task) store.Result {
	failure := store.Result{ExitCode: 125, Output: "could not prepare isolated execution"}
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

	localURL := (&url.URL{Scheme: "file", Path: repo}).String()
	clone := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null",
		"clone", "--depth=1", "--no-tags", "--single-branch", "--", localURL, workspace)
	clone.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if err := clone.Run(); err != nil {
		return failure
	}
	readHead := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--verify", "HEAD")
	base, err := readHead.Output()
	if err != nil {
		return failure
	}

	c := container{name: "difqo-" + filepath.Base(dir), image: o.Image, workspace: workspace}
	if diagnostic, err := c.codex(ctx, o.AuthHome, task.Goal); err != nil {
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
			strings.TrimSpace(string(base)))
	})
	if err != nil || diffExit != 0 || tooLarge {
		_ = os.Remove(filepath.Join(dir, "change.diff"))
		return store.Result{ExitCode: 125, Output: "could not capture a complete diff"}
	}

	tooLarge = false
	checkExit, err := captureFile(dir, "check.log", &tooLarge, func(w io.Writer) (int, error) {
		return c.capture(ctx, w, w, o.Check)
	})
	if err != nil {
		_ = os.Remove(filepath.Join(dir, "check.log"))
		return store.Result{ExitCode: 125, Output: "could not capture check output", Artifacts: filepath.Base(dir)}
	}
	output := "Codex turn and check finished"
	if tooLarge {
		output = "Codex turn and check finished (check log truncated)"
	}
	return store.Result{ExitCode: checkExit, Output: output, Artifacts: filepath.Base(dir)}
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
	code, err := capture(&cappedOutput{writer: f, remaining: 4 << 20, truncated: truncated})
	return code, errors.Join(err, f.Close())
}
