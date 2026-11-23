package execution

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type containerRuntime struct {
	executable          string
	podman              bool
	rootless            bool
	disableSELinuxLabel bool
}

func (o Options) resolveRuntime() (containerRuntime, error) {
	candidates := []string{o.Runtime}
	if o.Runtime == "" || o.Runtime == "auto" {
		candidates = []string{"docker", "podman"}
	}
	var failures []error
	for _, candidate := range candidates {
		runtime, err := o.probeRuntime(candidate)
		if err == nil {
			return runtime, nil
		}
		failures = append(failures, fmt.Errorf("%q: %w", candidate, err))
	}
	return containerRuntime{}, fmt.Errorf("no usable container runtime; task was not claimed: %w", errors.Join(failures...))
}

func (o Options) probeRuntime(candidate string) (containerRuntime, error) {
	executable, err := exec.LookPath(candidate)
	if err != nil {
		return containerRuntime{}, errors.New("CLI executable is unavailable")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return containerRuntime{}, errors.New("could not resolve CLI executable")
	}
	runtime := containerRuntime{executable: executable, podman: filepath.Base(candidate) == "podman"}
	info := []string{"info"}
	if runtime.podman {
		info = append(info, "--format", "{{.Host.Security.Rootless}}")
	}
	out, err := runtime.inspect(info...)
	if err != nil {
		return runtime, errors.New("engine is unavailable; check the connection and engine status")
	}
	if runtime.podman {
		switch strings.TrimSpace(out) {
		case "true":
			runtime.rootless = true
		case "false":
		default:
			return runtime, errors.New("could not determine Podman rootless mode")
		}
		runtime.disableSELinuxLabel = o.DisableSELinuxLabel
	}
	if _, err := runtime.inspect("image", "inspect", o.Image); err != nil {
		return runtime, fmt.Errorf("image %q is unavailable locally; build or load it into this engine", o.Image)
	}
	return runtime, o.probeContainers(runtime)
}

func (r containerRuntime) inspect(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.executable, args...)
	cmd.WaitDelay = time.Second
	if !r.podman || args[0] != "info" {
		return "", cmd.Run()
	}
	out, err := cmd.Output()
	return string(out), err
}

func (o Options) probeContainers(runtime containerRuntime) error {
	if err := os.MkdirAll(o.ArtifactRoot, 0o700); err != nil {
		return errors.New("could not create the artifact directory")
	}
	workspace, err := os.MkdirTemp(o.ArtifactRoot, "difqo-preflight-")
	if err != nil {
		return errors.New("could not create a test workspace under the artifact directory")
	}
	defer os.RemoveAll(workspace)
	// Use a sibling of the credentials on the same filesystem without mounting
	// the credentials or loading the operator's Codex configuration.
	auth, err := os.MkdirTemp(o.AuthHome, ".difqo-preflight-")
	if err != nil {
		return errors.New("could not write to the dedicated Codex home")
	}
	defer os.RemoveAll(auth)
	if err := os.WriteFile(filepath.Join(auth, "probe"), nil, 0o600); err != nil {
		return errors.New("could not prepare a test file in the dedicated Codex home")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := container{name: filepath.Base(workspace), image: o.Image, workspace: workspace, runtime: runtime}
	code, err := c.capture(ctx, io.Discard, io.Discard,
		`test ! -e /codex-home && git --version && touch /workspace/check-ready`)
	if err != nil || code != 0 || !probeFileExists(workspace, "check-ready") {
		return errors.New("could not start an isolated check container; check bind mounts, image tools, and runtime flags")
	}
	cmd := c.command(ctx, auth, "timeout", "-k", "2s", "10s", "sh", "-ec",
		`test -r /codex-home/probe && test -w /codex-home
		exec codex -c "$1" sandbox --permission-profile difqo -- sh -ec '
			test ! -r /codex-home/probe && touch /workspace/sandbox-ready
		'`, "difqo", codexPermissions)
	cmd.WaitDelay = time.Second
	defer c.remove()
	if err := cmd.Run(); err != nil || !probeFileExists(workspace, "sandbox-ready") {
		return errors.New("could not start the Codex sandbox with its required permissions; check Codex home sharing and engine sandbox policy (see local development guide)")
	}
	return nil
}

func probeFileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}
