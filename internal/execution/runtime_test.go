package execution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSelection(t *testing.T) {
	for _, tc := range []struct {
		name, choice, dockerFailure, overrideFailure, want, wantError string
	}{
		{name: "Docker wins when both work", want: "docker"},
		{name: "Docker engine unavailable", dockerFailure: "info", want: "podman"},
		{name: "Docker image missing", dockerFailure: "image", want: "podman"},
		{name: "Docker sandbox unavailable", dockerFailure: "sandbox", want: "podman"},
		{name: "explicit Podman", choice: "podman", want: "podman"},
		{name: "explicit Docker fails closed", choice: "docker", dockerFailure: "run", wantError: "isolated check container"},
		{name: "compatible executable path", choice: "my container cli", want: "my container cli"},
		{name: "compatible executable fails closed", choice: "my container cli", overrideFailure: "sandbox", wantError: "Codex sandbox"},
		{name: "executable missing", choice: "does-not-exist", wantError: "CLI executable is unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			logPath := filepath.Join(bin, "invocations")
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("DIFQO_TEST_RUNTIME_LOG", logPath)
			writeRuntimeStub(t, bin, "docker", tc.dockerFailure)
			writeRuntimeStub(t, bin, "podman", "")
			writeRuntimeStub(t, bin, "my container cli", tc.overrideFailure)
			auth := filepath.Join(t.TempDir(), "auth")
			if err := os.Mkdir(auth, 0o700); err != nil {
				t.Fatal(err)
			}
			options := Options{Runtime: tc.choice, Image: "fixture:latest", Check: "true",
				ArtifactRoot: t.TempDir(), AuthHome: auth, DisableSELinuxLabel: true}
			if tc.choice == "my container cli" {
				options.Runtime = filepath.Join(bin, tc.choice)
			}
			err := options.Validate(context.Background(), t.TempDir())
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || options.runtime.executable != "" {
					t.Fatalf("runtime = %+v, error = %v", options.runtime, err)
				}
				log, _ := os.ReadFile(logPath)
				if strings.Contains(string(log), "podman:") {
					t.Fatalf("explicit runtime fell back: %s", log)
				}
				return
			}
			if err != nil || options.runtime.executable != filepath.Join(bin, tc.want) {
				t.Fatalf("runtime = %+v, error = %v", options.runtime, err)
			}
			log, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := tc.want + ":info\n" + tc.want + ":image\n" +
				tc.want + ":run\n" + tc.want + ":rm\n" + tc.want + ":run\n" + tc.want + ":rm\n"
			if !strings.HasSuffix(string(log), wantCalls) ||
				(tc.dockerFailure == "" && string(log) != wantCalls) {
				t.Fatalf("unexpected runtime calls:\n%s", log)
			}
			if tc.want == "podman" && (!options.runtime.rootless || !options.runtime.disableSELinuxLabel) {
				t.Fatalf("expected rootless Podman with explicit SELinux setting: %+v", options.runtime)
			}
		})
	}
}

func TestCanceledRuntimeProbeCleansUp(t *testing.T) {
	bin := t.TempDir()
	logPath := filepath.Join(bin, "invocations")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DIFQO_TEST_RUNTIME_LOG", logPath)
	writeRuntimeStub(t, bin, "docker", "wait")
	writeRuntimeStub(t, bin, "podman", "")
	auth := filepath.Join(t.TempDir(), "auth")
	if err := os.Mkdir(auth, 0o700); err != nil {
		t.Fatal(err)
	}
	options := Options{Image: "fixture:latest", Check: "true", ArtifactRoot: t.TempDir(), AuthHome: auth}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	repo := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- options.Validate(ctx, repo)
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, _ := os.ReadFile(logPath)
		if strings.Contains(string(data), "sandbox-started\n") {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("sandbox probe did not start")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled preflight, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preflight did not return after cancellation")
	}
	log, err := os.ReadFile(logPath)
	if err != nil || !strings.HasSuffix(string(log), "sandbox-started\ndocker:rm\n") || strings.Contains(string(log), "podman:") {
		t.Fatalf("expected cleanup without runtime fallback, got %q (%v)", log, err)
	}
	for _, dir := range []string{options.ArtifactRoot, auth} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("preflight left files in %s: %v (%v)", dir, entries, err)
		}
	}
}

func writeRuntimeStub(t *testing.T, dir, name, failure string) {
	t.Helper()
	script := `#!/bin/sh
printf '%s:%s\n' "${0##*/}" "$1" >> "$DIFQO_TEST_RUNTIME_LOG"
test "$1" != '` + failure + `' || exit 125
case "$1" in
  info)
    if [ "${0##*/}" = podman ]; then printf 'true\n'; fi
    ;;
  image) test "$2" = inspect ;;
  rm) test "$2" = -f ;;
  run)
    marker=check-ready
    for arg in "$@"; do
      case "$arg" in
        type=bind,src=*,dst=/workspace) workspace=${arg#type=bind,src=}; workspace=${workspace%,dst=/workspace} ;;
        --env=CODEX_HOME=/codex-home) marker=sandbox-ready ;;
      esac
    done
    if [ '` + failure + `' = sandbox ] && [ "$marker" = sandbox-ready ]; then exit 125; fi
    if [ '` + failure + `' = wait ] && [ "$marker" = sandbox-ready ]; then
      printf 'sandbox-started\n' >> "$DIFQO_TEST_RUNTIME_LOG"
      exec sleep 30
    fi
    touch "$workspace/$marker"
    ;;
  *) exit 125 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestContainerCommandKeepsRuntimeIsolation(t *testing.T) {
	c := container{name: "fixture", image: "fixture:latest", workspace: "/fixture/workspace",
		runtime: containerRuntime{executable: "/fixture/podman", podman: true, rootless: true, disableSELinuxLabel: true}}
	ctx := context.Background()
	check := c.command(ctx, "", "true")
	codex := c.command(ctx, "/fixture/auth", "true")
	for _, args := range [][]string{check.Args, codex.Args} {
		for _, flag := range []string{"/fixture/podman", "--rm", "--pull=never", "--read-only",
			"--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=2g", "--cpus=2"} {
			if !slices.Contains(args, flag) {
				t.Errorf("missing %q in %q", flag, args)
			}
		}
	}
	auth := "type=bind,src=/fixture/auth,dst=/codex-home"
	labelOpt := "--security-opt=label=disable"
	if !slices.Contains(check.Args, "--network=none") || slices.Contains(check.Args, auth) || slices.Contains(check.Args, labelOpt) {
		t.Errorf("unexpected check isolation flags: %q", check.Args)
	}
	if !slices.Contains(codex.Args, auth) || !slices.Contains(codex.Args, labelOpt) {
		t.Errorf("unexpected Codex flags: %q", codex.Args)
	}
}
