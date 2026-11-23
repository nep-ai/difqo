package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
)

const MaxEvidenceBytes = 4 << 20

var (
	commitID  = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ValidSnapshot(s store.Snapshot) bool {
	digest, hasPrefix := strings.CutPrefix(s.Image, "sha256:")
	return commitID.MatchString(s.Base) && sha256Hex.MatchString(s.PatchSHA256) &&
		hasPrefix && sha256Hex.MatchString(digest) && strings.TrimSpace(s.Command) != "" &&
		len(s.Command) <= 4096 && utf8.ValidString(s.Command) && !strings.ContainsRune(s.Command, 0)
}

func patchDigest(patch []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(patch))
}

func ReadPatch(root *os.Root, ref, digest string) ([]byte, error) {
	file, err := root.Open(filepath.Join(ref, "change.diff"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxEvidenceBytes {
		return nil, fmt.Errorf("invalid patch file")
	}
	patch, err := io.ReadAll(io.LimitReader(file, MaxEvidenceBytes+1))
	if err != nil || len(patch) == 0 || len(patch) > MaxEvidenceBytes || patchDigest(patch) != digest {
		return nil, fmt.Errorf("saved patch is unavailable or has changed")
	}
	return patch, nil
}

func git(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_COUNT=0")
	return cmd
}

func cloneSnapshot(ctx context.Context, repo, destination string, options ...string) error {
	localURL := (&url.URL{Scheme: "file", Path: repo}).String()
	args := []string{"clone", "--depth=1", "--no-tags", "--single-branch", "--template="}
	args = append(args, options...)
	return git(ctx, append(args, "--", localURL, destination)...).Run()
}

func snapshotHead(ctx context.Context, repo string) (string, error) {
	head, err := git(ctx, "-C", repo, "rev-parse", "--verify", "HEAD").Output()
	return strings.TrimSpace(string(head)), err
}

func (c container) checkSnapshot(ctx context.Context, baseRepo string, snapshot store.Snapshot, patch []byte, output io.Writer) (int, error) {
	if err := cloneSnapshot(ctx, baseRepo, c.workspace); err != nil {
		return 125, fmt.Errorf("could not reconstruct the saved base")
	}
	head, err := snapshotHead(ctx, c.workspace)
	if err != nil || head != snapshot.Base {
		return 125, fmt.Errorf("saved base does not match the original commit")
	}
	apply := git(ctx, "-C", c.workspace, "apply", "--index", "--whitespace=nowarn", "-")
	apply.Stdin = bytes.NewReader(patch)
	if err := apply.Run(); err != nil {
		return 125, fmt.Errorf("could not apply the saved patch to the original base")
	}
	code, err := c.capture(ctx, output, output, snapshot.Command)
	if err != nil || ctx.Err() != nil {
		return 125, fmt.Errorf("check was interrupted")
	}
	return code, nil
}

func Rerun(ctx context.Context, root *os.Root, ref string, snapshot store.Snapshot, patch []byte, runtimeChoice string) (int, string) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	base, err := root.OpenRoot(filepath.Join(ref, "base.git"))
	if err != nil {
		return 125, "Saved base is unavailable."
	}
	defer base.Close()
	dir, err := os.MkdirTemp(root.Name(), "review-")
	if err != nil {
		return 125, "Could not create an isolated check workspace."
	}
	defer os.RemoveAll(dir)
	if err := mountPath(dir); err != nil {
		return 125, "The container runtime cannot mount the isolated check workspace."
	}
	options := Options{Runtime: runtimeChoice, Image: snapshot.Image, ArtifactRoot: root.Name()}
	runtime, err := options.resolveRuntime(ctx)
	if err != nil {
		return 125, "Could not initialize a container runtime for the saved image. Check the server's runtime configuration and local image."
	}

	var buffer bytes.Buffer
	var truncated bool
	log := &cappedOutput{writer: &buffer, remaining: 64 << 10, truncated: &truncated}
	c := container{name: "difqo-" + filepath.Base(dir), workspace: filepath.Join(dir, "workspace"), runtime: runtime}
	code, err := c.checkSnapshot(ctx, base.Name(), snapshot, patch, log)
	if err != nil {
		return code, err.Error()
	}
	output := strings.ReplaceAll(strings.ToValidUTF8(buffer.String(), "�"), "\x00", "�")
	if truncated {
		output += "\n[Check output truncated]"
	}
	return code, output
}
