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
const candidateRef = "refs/tags/difqo-checked-candidate"

var (
	commitID  = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ValidSnapshot(s store.Snapshot) bool {
	digest, hasPrefix := strings.CutPrefix(s.Image, "sha256:")
	return commitID.MatchString(s.Base) && sha256Hex.MatchString(s.PatchSHA256) &&
		(s.Candidate == "" || (commitID.MatchString(s.Candidate) && s.Candidate != s.Base)) &&
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

// recordCandidate makes a commit from the captured patch in the per-attempt
// bare repository. The original checkout and saved base ref stay untouched.
func recordCandidate(ctx context.Context, baseRepo, base string, patch []byte) (string, error) {
	index := filepath.Join(filepath.Dir(baseRepo), "candidate-index")
	defer os.Remove(index)
	run := func(input []byte, args ...string) (string, error) {
		cmd := git(ctx, append([]string{"-C", baseRepo}, args...)...)
		cmd.Env = append(cmd.Env,
			"GIT_INDEX_FILE="+index,
			"GIT_AUTHOR_NAME=Difqo", "GIT_AUTHOR_EMAIL=difqo@users.noreply.github.com",
			"GIT_COMMITTER_NAME=Difqo", "GIT_COMMITTER_EMAIL=difqo@users.noreply.github.com",
		)
		cmd.Stdin = bytes.NewReader(input)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	if _, err := run(nil, "read-tree", base); err != nil {
		return "", fmt.Errorf("could not load the saved base tree: %w", err)
	}
	if _, err := run(patch, "apply", "--cached", "--whitespace=nowarn", "-"); err != nil {
		return "", fmt.Errorf("could not apply the saved patch to the base tree: %w", err)
	}
	tree, err := run(nil, "write-tree")
	if err != nil {
		return "", fmt.Errorf("could not write the candidate tree: %w", err)
	}
	candidate, err := run(nil, "-c", "commit.gpgSign=false", "commit-tree", tree, "-p", base, "-m", "Difqo candidate")
	if err != nil {
		return "", fmt.Errorf("could not commit the candidate tree: %w", err)
	}
	if _, err := run(nil, "update-ref", candidateRef, candidate, ""); err != nil {
		return "", fmt.Errorf("could not retain the candidate commit: %w", err)
	}
	return candidate, nil
}

func prepareCheckWorkspace(ctx context.Context, baseRepo, workspace string, snapshot store.Snapshot, patch []byte) error {
	if snapshot.Candidate != "" {
		if err := cloneSnapshot(ctx, baseRepo, workspace, "--no-checkout"); err != nil {
			return fmt.Errorf("could not reconstruct the saved candidate")
		}
		// Fetch by full ref so a source branch with the tag's short name
		// cannot shadow the candidate.
		if err := git(ctx, "-C", workspace, "fetch", "--depth=1", "--no-tags", "origin", candidateRef).Run(); err != nil {
			return fmt.Errorf("could not fetch the saved candidate")
		}
		fetched, err := git(ctx, "-C", workspace, "rev-parse", "--verify", "FETCH_HEAD").Output()
		if err != nil || strings.TrimSpace(string(fetched)) != snapshot.Candidate {
			return fmt.Errorf("saved candidate does not match the checked commit")
		}
		if err := git(ctx, "-C", workspace, "checkout", "--detach", snapshot.Candidate).Run(); err != nil {
			return fmt.Errorf("could not check out the saved candidate")
		}
		return nil
	}
	// Attempts saved before candidate commits retain their base-plus-patch checks.
	if err := cloneSnapshot(ctx, baseRepo, workspace); err != nil {
		return fmt.Errorf("could not reconstruct the saved base")
	}
	head, err := snapshotHead(ctx, workspace)
	if err != nil || head != snapshot.Base {
		return fmt.Errorf("saved base does not match the original commit")
	}
	apply := git(ctx, "-C", workspace, "apply", "--index", "--whitespace=nowarn", "-")
	apply.Stdin = bytes.NewReader(patch)
	if err := apply.Run(); err != nil {
		return fmt.Errorf("could not apply the saved patch to the original base")
	}
	return nil
}

func (c container) checkSnapshot(ctx context.Context, baseRepo string, snapshot store.Snapshot, patch []byte, output io.Writer) (int, error) {
	if err := prepareCheckWorkspace(ctx, baseRepo, c.workspace, snapshot, patch); err != nil {
		return 125, err
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
