package execution

import (
	"context"
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

func (c container) codex(ctx context.Context, authHome, goal string) error {
	ctx, cancel := context.WithCancel(ctx)
	cmd := c.command(ctx, authHome,
		"timeout", "-k", "5s", "120s", "codex", "app-server", "--stdio",
		"-c", `cli_auth_credentials_store="file"`,
		"-c", `default_permissions="difqo"`,
		"-c", `permissions={difqo={extends=":workspace",filesystem={":root"="deny",":minimal"="read","/usr/local"="read","/codex-home"="deny"}}}`,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return err
	}
	defer c.remove()
	err = runCodex(stdin, stdout, goal)
	cancel()
	_ = cmd.Wait()
	return err
}

func (c container) capture(ctx context.Context, stdout, stderr io.Writer, script string, args ...string) (int, error) {
	cmd := c.command(ctx, "", append([]string{"timeout", "-k", "5s", "25s", "sh", "-c", script, "difqo"}, args...)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
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
