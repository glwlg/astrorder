package targetfs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SnapshotWire is the canonical JSON shape of one workspace snapshot. It is
// emitted as GitResult.Stdout for GitSnapshotReadonly, so local and remote
// targets return exactly the same structure.
type SnapshotWire struct {
	Workspace         string `json:"workspace"`
	RepoRoot          string `json:"repo_root"`
	Branch            string `json:"branch"`
	Head              string `json:"head"`
	ChangeFingerprint string `json:"change_fingerprint"`
}

// ParseSnapshot decodes the canonical JSON produced by GitSnapshotReadonly.
func ParseSnapshot(data []byte) (SnapshotWire, error) {
	var snapshot SnapshotWire
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return SnapshotWire{}, errorf(502, "invalid_snapshot", "Git snapshot response is invalid", err)
	}
	if snapshot.RepoRoot == "" || snapshot.Head == "" || snapshot.ChangeFingerprint == "" {
		return SnapshotWire{}, errorf(502, "invalid_snapshot", "Git snapshot response is incomplete", nil)
	}
	return snapshot, nil
}

// SnapshotsMatch compares the structured identity of two snapshots. Workspace
// spelling is deliberately excluded: the repository root, branch, HEAD and
// change fingerprint carry the real comparison.
func SnapshotsMatch(left, right SnapshotWire) bool {
	return left.RepoRoot == right.RepoRoot &&
		left.Branch == right.Branch &&
		left.Head == right.Head &&
		left.ChangeFingerprint == right.ChangeFingerprint
}

const (
	maxSnapshotOutput      = 64 << 20
	maxUntrackedFileDigest = 512 << 20
)

// errSnapshotOutputExceeded fails one capped git output stream as soon as the
// hard limit is crossed; the same write also cancels the command context so a
// runaway child is killed instead of buffered without bound.
var errSnapshotOutputExceeded = errors.New("git snapshot output exceeded the limit")

// cappedBuffer is a hard-cap writer for one git command stream.
type cappedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	overflow bool
}

func (c *cappedBuffer) Write(data []byte) (int, error) {
	if c.buffer.Len()+len(data) > c.limit {
		c.overflow = true
		if c.cancel != nil {
			c.cancel()
		}
		return 0, errSnapshotOutputExceeded
	}
	return c.buffer.Write(data)
}

// captureLocalSnapshot runs the typed read-only snapshot for a local
// filesystem. It never mutates the repository: only rev-parse, status,
// diff --binary, ls-files and content hashing are used. Untracked paths come
// from `ls-files --full-name`, so they are repository-root-relative and are
// resolved against the repository root even when the workspace is a
// subdirectory; every content read stays inside both the repository root and
// the filesystem's allowed scope.
func captureLocalSnapshot(ctx context.Context, gitBinary, workspace string, roots []string) (GitResult, error) {
	repoRootResult, err := snapshotGit(ctx, gitBinary, workspace, maxSnapshotOutput, "rev-parse", "--show-toplevel")
	if err != nil {
		return GitResult{}, err
	}
	if repoRootResult.ExitCode != 0 {
		return GitResult{}, snapshotGitFailure(repoRootResult, "无法读取 Git 仓库根目录")
	}
	repoRoot := strings.TrimSpace(string(repoRootResult.Stdout))
	if repoRoot == "" {
		return GitResult{}, errorf(422, "git_snapshot_failed", "无法读取 Git 仓库根目录", nil)
	}
	// A detached HEAD makes symbolic-ref exit 1 with no output; fall back to
	// the short commit instead of treating the normal detached state as fatal.
	branch := ""
	branchResult, branchErr := snapshotGit(ctx, gitBinary, workspace, maxSnapshotOutput, "symbolic-ref", "--short", "-q", "HEAD")
	if branchErr != nil {
		return GitResult{}, branchErr
	}
	if branchResult.ExitCode == 0 {
		branch = strings.TrimSpace(string(branchResult.Stdout))
	} else {
		fallback, fallbackErr := snapshotGit(ctx, gitBinary, workspace, maxSnapshotOutput, "rev-parse", "--short", "HEAD")
		if fallbackErr != nil {
			return GitResult{}, fallbackErr
		}
		if fallback.ExitCode != 0 {
			return GitResult{}, snapshotGitFailure(fallback, "无法读取 Git 分支")
		}
		branch = strings.TrimSpace(string(fallback.Stdout))
	}
	headResult, err := snapshotGit(ctx, gitBinary, workspace, maxSnapshotOutput, "rev-parse", "HEAD")
	if err != nil {
		return GitResult{}, err
	}
	if headResult.ExitCode != 0 {
		return GitResult{}, snapshotGitFailure(headResult, "无法读取 Git HEAD")
	}
	head := strings.TrimSpace(string(headResult.Stdout))

	status, err := snapshotGit(ctx, gitBinary, workspace, maxSnapshotOutput, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return GitResult{}, err
	}
	if status.ExitCode != 0 {
		return GitResult{}, snapshotGitFailure(status, "无法读取 Git 工作区状态")
	}
	diff, err := snapshotGit(ctx, gitBinary, workspace, maxSnapshotOutput, "diff", "--binary", "HEAD")
	if err != nil {
		return GitResult{}, err
	}
	if diff.ExitCode != 0 {
		return GitResult{}, snapshotGitFailure(diff, "无法读取 Git 工作区变更")
	}
	untracked, err := snapshotGit(ctx, gitBinary, workspace, maxSnapshotOutput, "ls-files", "--full-name", "--others", "--exclude-standard", "-z")
	if err != nil {
		return GitResult{}, err
	}
	if untracked.ExitCode != 0 {
		return GitResult{}, snapshotGitFailure(untracked, "无法读取 Git 未跟踪文件")
	}

	digest := sha256.New()
	_, _ = digest.Write(status.Stdout)
	_, _ = digest.Write(diff.Stdout)
	_, _ = digest.Write(untracked.Stdout)
	// Untracked file contents participate in the fingerprint so a content-only
	// edit of a still-untracked file changes the snapshot. The content bytes
	// are hashed inside this operation and never leave it.
	for _, raw := range bytes.Split(untracked.Stdout, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		relative := string(raw)
		if !validSnapshotRelativePath(relative) {
			return GitResult{}, errorf(502, "invalid_snapshot", "Git returned an invalid untracked path", nil)
		}
		candidate := filepath.Clean(filepath.Join(repoRoot, filepath.FromSlash(relative)))
		if !within(candidate, repoRoot) {
			return GitResult{}, errorf(502, "invalid_snapshot", "Git returned an invalid untracked path", nil)
		}
		if !withinAny(candidate, roots) {
			return GitResult{}, errorf(502, "invalid_snapshot", "Git returned an untracked path outside the allowed workspace scope", nil)
		}
		fileDigest, err := hashUntrackedFile(ctx, candidate)
		if err != nil {
			return GitResult{}, err
		}
		_, _ = digest.Write(fileDigest)
	}

	snapshot := SnapshotWire{
		Workspace: workspace, RepoRoot: repoRoot, Branch: branch, Head: head,
		ChangeFingerprint: hex.EncodeToString(digest.Sum(nil)),
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return GitResult{}, errorf(500, "snapshot_failed", "Failed to encode Git snapshot", err)
	}
	return GitResult{ExitCode: 0, Stdout: encoded}, nil
}

// validSnapshotRelativePath reports whether one repository-root-relative Git
// path is safe to resolve against the repository root. Git emits
// slash-separated paths; every segment must be a real name (never "." or
// "..") so a hostile listing cannot escape the root, while file names that
// merely contain dots (for example "version..notes.txt") stay valid.
func validSnapshotRelativePath(value string) bool {
	if value == "" || filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) {
		return false
	}
	if len(value) >= 2 && value[1] == ':' {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// hashUntrackedFile hashes one untracked file's raw content. Only the digest
// is retained; content is never returned, logged, or exposed over HTTP.
func hashUntrackedFile(ctx context.Context, path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, errorf(502, "snapshot_failed", "Failed to inspect untracked file", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// A symbolic link contributes its link text and mode. The target is
		// never opened, so a link pointing outside the workspace cannot pull
		// outside content into the digest while its text changes still count.
		target, err := os.Readlink(path)
		if err != nil {
			return nil, errorf(502, "snapshot_failed", "Failed to read untracked symbolic link", err)
		}
		marker := sha256.Sum256([]byte("symlink:" + info.Mode().String() + ":" + target))
		return marker[:], nil
	}
	if !info.Mode().IsRegular() {
		// A special entry contributes its kind rather than being followed.
		marker := sha256.Sum256([]byte("non-regular:" + info.Mode().String()))
		return marker[:], nil
	}
	if info.Size() > maxUntrackedFileDigest {
		return nil, errorf(422, "snapshot_file_too_large", "An untracked file is too large to fingerprint", nil)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errorf(502, "snapshot_failed", "Failed to read untracked file", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := copyContext(ctx, digest, file); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errorf(502, "snapshot_failed", "Failed to read untracked file", err)
	}
	return digest.Sum(nil), nil
}

// snapshotGit runs one read-only git command in workspace with a hard output
// cap on both streams: the command is killed and waited on as soon as either
// stream crosses the limit, so unbounded output cannot accumulate.
func snapshotGit(ctx context.Context, gitBinary, workspace string, limit int, args ...string) (GitResult, error) {
	if err := ctx.Err(); err != nil {
		return GitResult{}, err
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(commandCtx, gitBinary, args...)
	command.Dir = workspace
	configureChildProcess(command)
	stdout := &cappedBuffer{limit: limit, cancel: cancel}
	stderr := &cappedBuffer{limit: limit, cancel: cancel}
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return GitResult{}, ctxErr
	}
	if stdout.overflow || stderr.overflow {
		return GitResult{}, errorf(502, "snapshot_failed", "Git snapshot output exceeded the limit", nil)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return GitResult{}, errorf(503, "git_unavailable", "Git executable is unavailable", err)
		}
	}
	return GitResult{ExitCode: exitCode(err), Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}, nil
}

func snapshotGitFailure(result GitResult, fallback string) error {
	detail := strings.TrimSpace(string(result.Stderr))
	if detail == "" {
		detail = fallback
	}
	return errorf(422, "git_snapshot_failed", detail, nil)
}
