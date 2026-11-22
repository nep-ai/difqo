package execution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type container struct {
	name, image, workspace string
}

func (c container) command(ctx context.Context, authHome string, args ...string) *exec.Cmd {
	options := []string{
		"run", "--rm", "--init", "-i", "--pull=never", "--name", c.name,
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128",
		"--read-only", "--tmpfs=/tmp:rw,exec,mode=1777,size=256m",
		"--memory=2g", "--cpus=2", "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--workdir=/workspace", "--env=HOME=/tmp",
		// VM-backed bind mounts can report an owner different from the process UID.
		"--env=GIT_CONFIG_COUNT=1", "--env=GIT_CONFIG_KEY_0=safe.directory", "--env=GIT_CONFIG_VALUE_0=/workspace",
		"--mount", "type=bind,src=" + c.workspace + ",dst=/workspace",
	}
	if authHome == "" {
		options = append(options, "--network=none")
	} else {
		options = append(options, "--env=CODEX_HOME=/codex-home",
			"--mount", "type=bind,src="+authHome+",dst=/codex-home")
	}
	return exec.CommandContext(ctx, "docker", append(options, append([]string{c.image}, args...)...)...)
}

func (c container) remove() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", c.name).Run()
}

func (c container) codex(ctx context.Context, authHome, goal string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := c.command(ctx, authHome,
		"timeout", "-k", "5s", "120s", "codex", "app-server", "--stdio",
		"-c", `cli_auth_credentials_store="file"`,
		"-c", `default_permissions="difqo"`,
		"-c", `permissions={difqo={extends=":workspace",filesystem={":root"="deny",":minimal"="read","/usr/local"="read","/codex-home"="deny"}}}`,
	)
	var stderr bytes.Buffer
	var truncated bool
	cmd.Stderr = &cappedOutput{writer: &stderr, remaining: 64 << 10, truncated: &truncated}
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "could not open Docker input", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "could not open Docker output", err
	}
	if err := cmd.Start(); err != nil {
		return "could not start the Docker CLI", err
	}
	defer c.remove()
	stop := context.AfterFunc(ctx, func() {
		_ = stdin.Close()
		_ = stdout.Close()
	})
	defer stop()
	err = runCodex(stdin, stdout, goal)
	cancel()
	waitErr := cmd.Wait()
	if err == nil || errors.Is(err, ErrAuthentication) {
		return "", err
	}
	return codexDiagnostic(stderr.String(), waitErr, err), err
}

// Use fixed messages selected by known signatures. Raw stderr can contain
// credentials, repository data, or text emitted by a tool in the turn.
func codexDiagnostic(stderr string, waitErr, rpcErr error) string {
	stderr = strings.ToLower(stderr)
	switch {
	case strings.Contains(stderr, "invalid mount config"), strings.Contains(stderr, "mounts denied"),
		strings.Contains(stderr, "bind source path does not exist"):
		return "Docker rejected a bind mount. Check file sharing and directory access."
	case strings.Contains(stderr, "cannot connect to the docker daemon"), strings.Contains(stderr, "error during connect"):
		return "Docker engine is unavailable. Check the Docker context and engine status."
	case errors.Is(rpcErr, errSandbox), strings.Contains(stderr, "bwrap:"), strings.Contains(stderr, "sandbox helper"):
		return "Codex sandbox startup failed. Check runtime support for the Linux sandbox."
	case strings.Contains(stderr, "failed to load configuration"), strings.Contains(stderr, "startup configuration unavailable"):
		return "Codex could not load its startup configuration. Check the dedicated Codex home."
	}
	var exit *exec.ExitError
	if errors.As(waitErr, &exit) && exit.ExitCode() >= 0 {
		return fmt.Sprintf("Docker or Codex exited with status %d before the turn completed.", exit.ExitCode())
	}
	return "The Codex App Server connection ended before the turn completed."
}

func (c container) capture(ctx context.Context, stdout, stderr io.Writer, script string, args ...string) (int, error) {
	cmd := c.command(ctx, "", append([]string{"timeout", "-k", "5s", "25s", "sh", "-c", script, "difqo"}, args...)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	defer c.remove()
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
		return exit.ExitCode(), nil
	}
	return 125, err
}

func mountPath(path string) error {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, ",\"\r\n") {
		return fmt.Errorf("unsupported Docker bind path: %q", path)
	}
	return nil
}
