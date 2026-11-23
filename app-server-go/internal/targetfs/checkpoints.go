package targetfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	checkpointRefPrefix = "refs/astrorder/checkpoints/"
	maxCheckpointDiff   = 8 << 20
)

var (
	validGitSHA40  = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	validRefSuffix = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,160}$`)
)

// CheckpointFileStat describes one file's delta between two checkpoint trees.
type CheckpointFileStat struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// CheckpointCaptureWire is emitted as GitResult.Stdout for GitCheckpointCapture.
type CheckpointCaptureWire struct {
	Workspace    string               `json:"workspace"`
	RepoRoot     string               `json:"repo_root"`
	Branch       string               `json:"branch"`
	Head         string               `json:"head"`
	TreeSHA      string               `json:"tree_sha"`
	CommitSHA    string               `json:"commit_sha"`
	RefName      string               `json:"ref_name"`
	PrevTreeSHA  string               `json:"prev_tree_sha,omitempty"`
	FilesChanged int                  `json:"files_changed"`
	Insertions   int                  `json:"insertions"`
	Deletions    int                  `json:"deletions"`
	Files        []CheckpointFileStat `json:"files"`
}

// CheckpointDiffWire is emitted as GitResult.Stdout for GitCheckpointDiff.
type CheckpointDiffWire struct {
	FromTree     string               `json:"from_tree"`
	ToTree       string               `json:"to_tree"`
	FilesChanged int                  `json:"files_changed"`
	Insertions   int                  `json:"insertions"`
	Deletions    int                  `json:"deletions"`
	Files        []CheckpointFileStat `json:"files"`
	Diff         string               `json:"diff"`
	Truncated    bool                 `json:"truncated,omitempty"`
}

// CheckpointRestoreWire is emitted as GitResult.Stdout for GitCheckpointRestore.
type CheckpointRestoreWire struct {
	RestoredTree string                 `json:"restored_tree"`
	RemovedFiles int                    `json:"removed_files"`
	Backup       *CheckpointCaptureWire `json:"backup,omitempty"`
}

func validateCheckpointRef(refName string) (string, error) {
	trimmed := strings.TrimSpace(refName)
	if !strings.HasPrefix(trimmed, checkpointRefPrefix) {
		return "", errorf(400, "invalid_checkpoint_ref", "Checkpoint ref must stay under refs/astrorder/checkpoints/", nil)
	}
	suffix := strings.TrimPrefix(trimmed, checkpointRefPrefix)
	if !validRefSuffix.MatchString(suffix) || strings.Contains(suffix, "..") || strings.Contains(suffix, "//") || strings.HasSuffix(suffix, "/") || strings.HasSuffix(suffix, ".lock") {
		return "", errorf(400, "invalid_checkpoint_ref", "Checkpoint ref name is invalid", nil)
	}
	return trimmed, nil
}

func checkpointGitEnv(indexFile string) []string {
	env := make([]string, 0, len(os.Environ())+7)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_INDEX_FILE=") {
			continue
		}
		env = append(env, entry)
	}
	if indexFile != "" {
		env = append(env, "GIT_INDEX_FILE="+indexFile)
	}
	env = append(env,
		"GIT_AUTHOR_NAME=Astrorder",
		"GIT_AUTHOR_EMAIL=checkpoints@astrorder.local",
		"GIT_COMMITTER_NAME=Astrorder",
		"GIT_COMMITTER_EMAIL=checkpoints@astrorder.local",
		"GIT_TERMINAL_PROMPT=0",
	)
	return env
}

func runCheckpointGit(ctx context.Context, gitBinary, dir, indexFile string, limit int, args ...string) (GitResult, error) {
	if err := ctx.Err(); err != nil {
		return GitResult{}, err
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(commandCtx, gitBinary, args...)
	command.Dir = dir
	command.Env = checkpointGitEnv(indexFile)
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
		return GitResult{ExitCode: -2, Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}, errSnapshotOutputExceeded
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return GitResult{}, errorf(503, "git_unavailable", "Git executable is unavailable", err)
		}
	}
	return GitResult{ExitCode: exitCode(err), Stdout: stdout.buffer.Bytes(), Stderr: stderr.buffer.Bytes()}, nil
}

func tempCheckpointIndexPath() (string, func(), error) {
	dir, err := os.MkdirTemp("", "astrorder-ckpt-*")
	if err != nil {
		return "", nil, errorf(500, "checkpoint_temp_failed", "Failed to prepare temporary git index", err)
	}
	return filepath.Join(dir, "index"), func() { _ = os.RemoveAll(dir) }, nil
}

func resolveRepoMeta(ctx context.Context, gitBinary, workspace string) (repoRoot, branch, head string, err error) {
	rootRes, err := runCheckpointGit(ctx, gitBinary, workspace, "", maxSnapshotOutput, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", "", err
	}
	if rootRes.ExitCode != 0 {
		return "", "", "", snapshotGitFailure(rootRes, "当前目录不是 Git 仓库")
	}
	repoRoot = strings.TrimSpace(string(rootRes.Stdout))
	if repoRoot == "" {
		return "", "", "", errorf(422, "git_snapshot_failed", "无法读取 Git 仓库根目录", nil)
	}
	branchRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	if branchRes.ExitCode == 0 {
		branch = strings.TrimSpace(string(branchRes.Stdout))
	} else {
		fallback, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "rev-parse", "--short", "HEAD")
		if err != nil {
			return "", "", "", err
		}
		if fallback.ExitCode == 0 {
			branch = strings.TrimSpace(string(fallback.Stdout))
		}
	}
	headRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	if headRes.ExitCode == 0 {
		head = strings.TrimSpace(string(headRes.Stdout))
	}
	return repoRoot, branch, head, nil
}

// writeWorkingTree creates an immutable Git tree SHA representing the entire
// non-ignored working tree (tracked + untracked files) using an isolated temp
// index so the repository's real .git/index and git status are never modified.
func writeWorkingTree(ctx context.Context, gitBinary, repoRoot, head string) (string, error) {
	indexPath, cleanup, err := tempCheckpointIndexPath()
	if err != nil {
		return "", err
	}
	defer cleanup()

	if head != "" {
		rt, err := runCheckpointGit(ctx, gitBinary, repoRoot, indexPath, maxSnapshotOutput, "read-tree", head)
		if err != nil {
			return "", err
		}
		if rt.ExitCode != 0 {
			return "", snapshotGitFailure(rt, "无法初始化临时检查点索引")
		}
	}
	addRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, indexPath, maxSnapshotOutput, "add", "-A", "--", ".")
	if err != nil {
		return "", err
	}
	if addRes.ExitCode != 0 {
		return "", snapshotGitFailure(addRes, "无法扫描工作区变更")
	}
	treeRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, indexPath, maxSnapshotOutput, "write-tree")
	if err != nil {
		return "", err
	}
	if treeRes.ExitCode != 0 {
		return "", snapshotGitFailure(treeRes, "无法写入检查点树对象")
	}
	treeSHA := strings.TrimSpace(string(treeRes.Stdout))
	if !validGitSHA40.MatchString(treeSHA) {
		return "", errorf(502, "checkpoint_failed", "Git returned an invalid tree SHA", nil)
	}
	return treeSHA, nil
}

func computeTreeDelta(ctx context.Context, gitBinary, repoRoot, fromTree, toTree string) ([]CheckpointFileStat, int, int, error) {
	if fromTree == "" || toTree == "" || fromTree == toTree {
		return []CheckpointFileStat{}, 0, 0, nil
	}
	statusRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "diff-tree", "-r", "--name-status", "-z", fromTree, toTree)
	if err != nil {
		return nil, 0, 0, err
	}
	if statusRes.ExitCode != 0 {
		return nil, 0, 0, snapshotGitFailure(statusRes, "无法对比检查点文件变更")
	}
	numRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "diff-tree", "-r", "--numstat", "-z", fromTree, toTree)
	if err != nil {
		return nil, 0, 0, err
	}
	if numRes.ExitCode != 0 {
		return nil, 0, 0, snapshotGitFailure(numRes, "无法统计检查点增删行数")
	}

	byPath := map[string]*CheckpointFileStat{}
	statusParts := bytes.Split(statusRes.Stdout, []byte{0})
	for i := 0; i+1 < len(statusParts); i += 2 {
		code := strings.TrimSpace(string(statusParts[i]))
		filePath := string(statusParts[i+1])
		if code == "" || filePath == "" {
			continue
		}
		if (strings.HasPrefix(code, "R") || strings.HasPrefix(code, "C")) && i+2 < len(statusParts) {
			filePath = string(statusParts[i+2])
			i++
		}
		byPath[filePath] = &CheckpointFileStat{Path: filePath, Status: string(code[0])}
	}

	totalAdd, totalDel := 0, 0
	numParts := bytes.Split(numRes.Stdout, []byte{0})
	for i := 0; i < len(numParts); i++ {
		entry := string(numParts[i])
		if entry == "" {
			continue
		}
		fields := strings.SplitN(entry, "\t", 3)
		if len(fields) < 3 {
			continue
		}
		filePath := fields[2]
		if filePath == "" && i+2 < len(numParts) {
			filePath = string(numParts[i+2])
			i += 2
		}
		if filePath == "" {
			continue
		}
		add, _ := strconv.Atoi(fields[0])
		del, _ := strconv.Atoi(fields[1])
		totalAdd += add
		totalDel += del
		stat, ok := byPath[filePath]
		if !ok {
			stat = &CheckpointFileStat{Path: filePath, Status: "M"}
			byPath[filePath] = stat
		}
		stat.Insertions = add
		stat.Deletions = del
	}

	files := make([]CheckpointFileStat, 0, len(byPath))
	for _, stat := range byPath {
		files = append(files, *stat)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, totalAdd, totalDel, nil
}

func captureLocalCheckpoint(ctx context.Context, gitBinary, workspace, refName, prevTreeSHA string) (GitResult, error) {
	wire, err := doCaptureCheckpoint(ctx, gitBinary, workspace, refName, prevTreeSHA)
	if err != nil {
		return GitResult{}, err
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return GitResult{}, errorf(500, "checkpoint_failed", "Failed to encode checkpoint result", err)
	}
	return GitResult{ExitCode: 0, Stdout: encoded}, nil
}

func doCaptureCheckpoint(ctx context.Context, gitBinary, workspace, refName, prevTreeSHA string) (CheckpointCaptureWire, error) {
	validatedRef, err := validateCheckpointRef(refName)
	if err != nil {
		return CheckpointCaptureWire{}, err
	}
	if prevTreeSHA != "" && !validGitSHA40.MatchString(prevTreeSHA) {
		return CheckpointCaptureWire{}, errorf(400, "invalid_tree_sha", "Previous checkpoint tree SHA is invalid", nil)
	}
	repoRoot, branch, head, err := resolveRepoMeta(ctx, gitBinary, workspace)
	if err != nil {
		return CheckpointCaptureWire{}, err
	}
	treeSHA, err := writeWorkingTree(ctx, gitBinary, repoRoot, head)
	if err != nil {
		return CheckpointCaptureWire{}, err
	}

	commitArgs := []string{"commit-tree", treeSHA, "-m", "astrorder checkpoint " + validatedRef}
	if head != "" {
		commitArgs = []string{"commit-tree", treeSHA, "-p", head, "-m", "astrorder checkpoint " + validatedRef}
	}
	commitRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, commitArgs...)
	if err != nil {
		return CheckpointCaptureWire{}, err
	}
	if commitRes.ExitCode != 0 {
		return CheckpointCaptureWire{}, snapshotGitFailure(commitRes, "无法创建检查点提交对象")
	}
	commitSHA := strings.TrimSpace(string(commitRes.Stdout))
	if !validGitSHA40.MatchString(commitSHA) {
		return CheckpointCaptureWire{}, errorf(502, "checkpoint_failed", "Git returned an invalid commit SHA", nil)
	}

	refRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "update-ref", validatedRef, commitSHA)
	if err != nil {
		return CheckpointCaptureWire{}, err
	}
	if refRes.ExitCode != 0 {
		return CheckpointCaptureWire{}, snapshotGitFailure(refRes, "无法保存检查点引用")
	}

	compareFrom := prevTreeSHA
	if compareFrom == "" && head != "" {
		// Compare against HEAD^{tree} for the initial baseline checkpoint so the user
		// sees uncommitted files relative to HEAD.
		headTreeRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "rev-parse", "--verify", "-q", head+"^{tree}")
		if err == nil && headTreeRes.ExitCode == 0 {
			candidate := strings.TrimSpace(string(headTreeRes.Stdout))
			if validGitSHA40.MatchString(candidate) {
				compareFrom = candidate
			}
		}
	}
	files, insertions, deletions, err := computeTreeDelta(ctx, gitBinary, repoRoot, compareFrom, treeSHA)
	if err != nil {
		return CheckpointCaptureWire{}, err
	}
	return CheckpointCaptureWire{
		Workspace:    workspace,
		RepoRoot:     repoRoot,
		Branch:       branch,
		Head:         head,
		TreeSHA:      treeSHA,
		CommitSHA:    commitSHA,
		RefName:      validatedRef,
		PrevTreeSHA:  prevTreeSHA,
		FilesChanged: len(files),
		Insertions:   insertions,
		Deletions:    deletions,
		Files:        files,
	}, nil
}

func diffLocalCheckpoint(ctx context.Context, gitBinary, workspace, fromTree, toTree, filterPath string) (GitResult, error) {
	if !validGitSHA40.MatchString(fromTree) {
		return GitResult{}, errorf(400, "invalid_tree_sha", "Source checkpoint tree SHA is invalid", nil)
	}
	if toTree != "working" && !validGitSHA40.MatchString(toTree) {
		return GitResult{}, errorf(400, "invalid_tree_sha", "Target checkpoint tree SHA is invalid", nil)
	}
	repoRoot, _, head, err := resolveRepoMeta(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	resolvedTo := toTree
	if toTree == "working" {
		resolvedTo, err = writeWorkingTree(ctx, gitBinary, repoRoot, head)
		if err != nil {
			return GitResult{}, err
		}
	}

	files, insertions, deletions, err := computeTreeDelta(ctx, gitBinary, repoRoot, fromTree, resolvedTo)
	if err != nil {
		return GitResult{}, err
	}

	diffArgs := []string{"diff", "--no-color", fromTree, resolvedTo}
	if filterPath != "" {
		diffArgs = append(diffArgs, "--", filterPath)
	}
	diffRes, diffErr := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxCheckpointDiff, diffArgs...)
	truncated := false
	if errors.Is(diffErr, errSnapshotOutputExceeded) {
		truncated = true
	} else if diffErr != nil {
		return GitResult{}, diffErr
	} else if diffRes.ExitCode != 0 {
		return GitResult{}, snapshotGitFailure(diffRes, "无法生成检查点 Diff")
	}

	wire := CheckpointDiffWire{
		FromTree:     fromTree,
		ToTree:       resolvedTo,
		FilesChanged: len(files),
		Insertions:   insertions,
		Deletions:    deletions,
		Files:        files,
		Diff:         string(diffRes.Stdout),
		Truncated:    truncated,
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return GitResult{}, errorf(500, "checkpoint_diff_failed", "Failed to encode checkpoint diff", err)
	}
	return GitResult{ExitCode: 0, Stdout: encoded}, nil
}

func restoreLocalCheckpoint(ctx context.Context, gitBinary, workspace string, roots []string, targetTree, backupRefName, restorePath string) (GitResult, error) {
	if !validGitSHA40.MatchString(targetTree) {
		return GitResult{}, errorf(400, "invalid_tree_sha", "Target checkpoint tree SHA is invalid", nil)
	}
	restorePath = strings.TrimSpace(restorePath)
	if restorePath != "" && !validSnapshotRelativePath(restorePath) {
		return GitResult{}, errorf(400, "invalid_checkpoint_path", "Unsafe path specified for single-file restore", nil)
	}
	repoRoot, _, head, err := resolveRepoMeta(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	// Ensure the target object exists and is a Git tree before modifying anything.
	typeRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "cat-file", "-t", targetTree)
	if err != nil {
		return GitResult{}, err
	}
	if typeRes.ExitCode != 0 || strings.TrimSpace(string(typeRes.Stdout)) != "tree" {
		return GitResult{}, errorf(404, "checkpoint_tree_missing", "目标检查点的 Git 快照对象已不存在", nil)
	}

	var backupWire *CheckpointCaptureWire
	var currentTree string
	if strings.TrimSpace(backupRefName) != "" {
		captured, capErr := doCaptureCheckpoint(ctx, gitBinary, workspace, backupRefName, targetTree)
		if capErr != nil {
			return GitResult{}, capErr
		}
		backupWire = &captured
		currentTree = captured.TreeSHA
	} else {
		currentTree, err = writeWorkingTree(ctx, gitBinary, repoRoot, head)
		if err != nil {
			return GitResult{}, err
		}
	}

	removedCount := 0
	if restorePath != "" {
		candidate := filepath.Clean(filepath.Join(repoRoot, filepath.FromSlash(restorePath)))
		if !within(candidate, repoRoot) || !withinAny(candidate, roots) {
			return GitResult{}, errorf(403, "path_out_of_scope", "Checkpoint restore path escapes allowed workspace", nil)
		}
		// Check whether restorePath existed in targetTree.
		existsRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "cat-file", "-e", targetTree+":"+restorePath)
		if err != nil {
			return GitResult{}, err
		}
		if existsRes.ExitCode == 0 {
			indexPath, cleanup, err := tempCheckpointIndexPath()
			if err != nil {
				return GitResult{}, err
			}
			defer cleanup()

			rtRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, indexPath, maxSnapshotOutput, "read-tree", targetTree)
			if err != nil {
				return GitResult{}, err
			}
			if rtRes.ExitCode != 0 {
				return GitResult{}, snapshotGitFailure(rtRes, "无法读取目标检查点目录树")
			}
			coRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, indexPath, maxSnapshotOutput, "checkout-index", "-f", "--", restorePath)
			if err != nil {
				return GitResult{}, err
			}
			if coRes.ExitCode != 0 {
				return GitResult{}, snapshotGitFailure(coRes, "无法将指定文件恢复到目标检查点")
			}
		} else {
			// File did not exist at targetTree; remove only if it is tracked in currentTree (i.e. non-ignored).
			curExistsRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "cat-file", "-e", currentTree+":"+restorePath)
			if err != nil {
				return GitResult{}, err
			}
			if curExistsRes.ExitCode == 0 {
				if removeErr := os.Remove(candidate); removeErr == nil {
					removedCount = 1
					pruneEmptyParents(filepath.Dir(candidate), repoRoot)
				} else if !os.IsNotExist(removeErr) {
					return GitResult{}, errorf(500, "checkpoint_restore_failed", "移除检查点后新增的指定文件失败", removeErr)
				}
			}
		}
	} else if currentTree != targetTree {
		// Remove ONLY non-ignored files that exist in currentTree but did NOT exist in targetTree.
		// Gitignored files (.env, node_modules, build artifacts) are never part of currentTree and
		// are therefore guaranteed to stay untouched.
		addedRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "diff-tree", "-r", "--name-only", "--diff-filter=A", "-z", targetTree, currentTree)
		if err != nil {
			return GitResult{}, err
		}
		if addedRes.ExitCode != 0 {
			return GitResult{}, snapshotGitFailure(addedRes, "无法计算需清理的新增文件")
		}
		for _, raw := range bytes.Split(addedRes.Stdout, []byte{0}) {
			if len(raw) == 0 {
				continue
			}
			rel := string(raw)
			if !validSnapshotRelativePath(rel) {
				return GitResult{}, errorf(400, "invalid_checkpoint_path", "Unsafe path encountered during checkpoint restore", nil)
			}
			candidate := filepath.Clean(filepath.Join(repoRoot, filepath.FromSlash(rel)))
			if !within(candidate, repoRoot) || !withinAny(candidate, roots) {
				return GitResult{}, errorf(403, "path_out_of_scope", "Checkpoint restore path escapes allowed workspace", nil)
			}
			if removeErr := os.Remove(candidate); removeErr == nil {
				removedCount++
				pruneEmptyParents(filepath.Dir(candidate), repoRoot)
			} else if !os.IsNotExist(removeErr) {
				return GitResult{}, errorf(500, "checkpoint_restore_failed", "清理检查点后新增的文件失败", removeErr)
			}
		}

		// Check out all files from targetTree via an isolated temp index so the repo's
		// real .git/index and HEAD remain intact.
		indexPath, cleanup, err := tempCheckpointIndexPath()
		if err != nil {
			return GitResult{}, err
		}
		defer cleanup()

		rtRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, indexPath, maxSnapshotOutput, "read-tree", targetTree)
		if err != nil {
			return GitResult{}, err
		}
		if rtRes.ExitCode != 0 {
			return GitResult{}, snapshotGitFailure(rtRes, "无法读取目标检查点目录树")
		}
		coRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, indexPath, maxSnapshotOutput, "checkout-index", "-a", "-f")
		if err != nil {
			return GitResult{}, err
		}
		if coRes.ExitCode != 0 {
			return GitResult{}, snapshotGitFailure(coRes, "无法将工作区文件恢复到目标检查点")
		}
	}

	wire := CheckpointRestoreWire{
		RestoredTree: targetTree,
		RemovedFiles: removedCount,
		Backup:       backupWire,
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return GitResult{}, errorf(500, "checkpoint_restore_failed", "Failed to encode restore result", err)
	}
	return GitResult{ExitCode: 0, Stdout: encoded}, nil
}

func pruneEmptyParents(dir, stopRoot string) {
	current := filepath.Clean(dir)
	root := filepath.Clean(stopRoot)
	for current != root && within(current, root) {
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(current); err != nil {
			return
		}
		parent := filepath.Dir(current)
		if parent == current {
			return
		}
		current = parent
	}
}

func deleteLocalCheckpoint(ctx context.Context, gitBinary, workspace, refName string) (GitResult, error) {
	validatedRef, err := validateCheckpointRef(refName)
	if err != nil {
		return GitResult{}, err
	}
	repoRoot, _, _, err := resolveRepoMeta(ctx, gitBinary, workspace)
	if err != nil {
		return GitResult{}, err
	}
	delRes, err := runCheckpointGit(ctx, gitBinary, repoRoot, "", maxSnapshotOutput, "update-ref", "-d", validatedRef)
	if err != nil {
		return GitResult{}, err
	}
	if delRes.ExitCode != 0 {
		return GitResult{}, snapshotGitFailure(delRes, "无法删除检查点引用")
	}
	return GitResult{ExitCode: 0, Stdout: []byte(`{"deleted":true}`)}, nil
}

// ParseCheckpointCapture decodes GitCheckpointCapture stdout JSON.
func ParseCheckpointCapture(data []byte) (CheckpointCaptureWire, error) {
	var wire CheckpointCaptureWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return CheckpointCaptureWire{}, errorf(502, "invalid_checkpoint", "Checkpoint capture response is invalid", err)
	}
	if !validGitSHA40.MatchString(wire.TreeSHA) {
		return CheckpointCaptureWire{}, errorf(502, "invalid_checkpoint", "Checkpoint capture response missing tree SHA", nil)
	}
	return wire, nil
}

// ParseCheckpointDiff decodes GitCheckpointDiff stdout JSON.
func ParseCheckpointDiff(data []byte) (CheckpointDiffWire, error) {
	var wire CheckpointDiffWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return CheckpointDiffWire{}, errorf(502, "invalid_checkpoint_diff", "Checkpoint diff response is invalid", err)
	}
	return wire, nil
}

// ParseCheckpointRestore decodes GitCheckpointRestore stdout JSON.
func ParseCheckpointRestore(data []byte) (CheckpointRestoreWire, error) {
	var wire CheckpointRestoreWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return CheckpointRestoreWire{}, errorf(502, "invalid_checkpoint_restore", "Checkpoint restore response is invalid", err)
	}
	return wire, nil
}
