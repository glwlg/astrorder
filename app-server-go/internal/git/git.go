// Package git exposes scope-checked Git HTTP contracts. User arguments are
// typed targetfs requests and are never interpolated into remote shell text.
package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/targetfs"
)

const maxJSONBody = 1024 * 1024

type Registration struct {
	Kind   string `json:"kind"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Auth   string `json:"auth"`
	Owner  string `json:"owner"`
}

func Registrations() []Registration {
	return []Registration{
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/git/status", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodPost, Path: "/api/v1/git/branch", Auth: "browser", Owner: "go-app"},
		{Kind: "http", Method: http.MethodGet, Path: "/api/v1/git/diff-raw", Auth: "browser", Owner: "go-app"},
	}
}

type BrowserAuthorizer interface {
	AuthorizeBrowser(*http.Request) *auth.Error
}

type Publisher interface {
	Publish(context.Context, string, map[string]any) error
}

type Options struct {
	Authorizer BrowserAuthorizer
	Resolver   *targetfs.Resolver
	Publisher  Publisher
}

type Handler struct{ options Options }

func NewHandler(options Options) http.Handler { return &Handler{options: options} }

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	path := request.URL.Path
	valid := false
	for _, registration := range Registrations() {
		if registration.Method == request.Method && registration.Path == path {
			valid = true
			break
		}
	}
	if !valid {
		writeError(response, http.StatusNotFound, "Not Found")
		return
	}
	if h.options.Authorizer == nil {
		writeError(response, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := h.options.Authorizer.AuthorizeBrowser(request); failure != nil {
		writeError(response, failure.Status, failure.Detail)
		return
	}
	if h.options.Resolver == nil {
		writeError(response, http.StatusServiceUnavailable, "Target filesystem is not configured")
		return
	}
	var err error
	switch path {
	case "/api/v1/git/status":
		err = h.status(response, request)
	case "/api/v1/git/branch":
		err = h.branch(response, request)
	case "/api/v1/git/diff-raw":
		err = h.diff(response, request)
	}
	if err != nil {
		writeTargetError(response, err)
	}
}

type changedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Staged bool   `json:"staged"`
}

type statusResponse struct {
	Status         string        `json:"status"`
	Workspace      string        `json:"workspace"`
	CurrentBranch  string        `json:"current_branch"`
	Branch         string        `json:"branch"`
	Branches       []string      `json:"branches"`
	Ahead          int           `json:"ahead"`
	Behind         int           `json:"behind"`
	ChangedFiles   int           `json:"changed_files"`
	Insertions     int           `json:"insertions"`
	Deletions      int           `json:"deletions"`
	StagedCount    int           `json:"staged_count"`
	UnstagedCount  int           `json:"unstaged_count"`
	UntrackedCount int           `json:"untracked_count"`
	Clean          bool          `json:"clean"`
	Files          []changedFile `json:"files"`
}

func (h *Handler) resolve(request *http.Request, workspace, sessionID, connectionID string) (targetfs.Resolved, error) {
	return h.options.Resolver.Resolve(request.Context(), targetfs.ResolveRequest{
		Workspace: targetfs.CleanPath(workspace), SessionID: sessionID, ConnectionID: connectionID,
	})
}

func (h *Handler) status(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	resolved, err := h.resolve(request, query.Get("workspace"), query.Get("session_id"), query.Get("connection_id"))
	if err != nil {
		return err
	}
	branchesResult, err := runGit(request.Context(), resolved, targetfs.GitRequest{Operation: targetfs.GitBranches, Workspace: resolved.Workspace})
	if err != nil {
		return err
	}
	if branchesResult.ExitCode != 0 {
		return gitStatusError(branchesResult)
	}
	current, branches := parseBranches(branchesResult.Stdout)
	statusResult, err := runGit(request.Context(), resolved, targetfs.GitRequest{Operation: targetfs.GitStatus, Workspace: resolved.Workspace})
	if err != nil {
		return err
	}
	if statusResult.ExitCode != 0 {
		return gitStatusError(statusResult)
	}
	files, staged, unstaged, untracked, err := parseStatus(statusResult.Stdout)
	if err != nil {
		return err
	}
	insertions, deletions := 0, 0
	for _, cached := range []bool{false, true} {
		result, runErr := runGit(request.Context(), resolved, targetfs.GitRequest{Operation: targetfs.GitNumstat, Workspace: resolved.Workspace, Cached: cached})
		if runErr != nil {
			return runErr
		}
		if result.ExitCode != 0 {
			return gitStatusError(result)
		}
		added, removed, parseErr := parseNumstat(result.Stdout)
		if parseErr != nil {
			return parseErr
		}
		insertions += added
		deletions += removed
	}
	ahead, behind := 0, 0
	base := query.Get("base_branch")
	if base == "" && current != "" && current != "HEAD" {
		base = "origin/" + current
	}
	if base != "" && current != "" {
		result, runErr := runGit(request.Context(), resolved, targetfs.GitRequest{Operation: targetfs.GitAheadBehind, Workspace: resolved.Workspace, BaseBranch: base, Branch: current})
		if runErr != nil {
			return runErr
		}
		if result.ExitCode == 0 {
			behind, ahead = parseAheadBehind(result.Stdout)
		}
	}
	writeJSON(response, http.StatusOK, statusResponse{
		Status: "ok", Workspace: resolved.Workspace, CurrentBranch: current, Branch: current, Branches: branches,
		Ahead: ahead, Behind: behind, ChangedFiles: len(files), Insertions: insertions, Deletions: deletions,
		StagedCount: staged, UnstagedCount: unstaged, UntrackedCount: untracked, Clean: len(files) == 0, Files: files,
	})
	return nil
}

func (h *Handler) branch(response http.ResponseWriter, request *http.Request) error {
	var payload struct {
		Branch       string `json:"branch"`
		Create       bool   `json:"create"`
		Workspace    string `json:"workspace"`
		SessionID    string `json:"session_id"`
		ConnectionID string `json:"connection_id"`
	}
	if err := decodeJSON(response, request, &payload); err != nil {
		return err
	}
	payload.Branch = strings.TrimSpace(payload.Branch)
	if payload.Branch == "" {
		return &targetfs.Error{Status: 400, Code: "branch_required", Detail: "branch name is required"}
	}
	resolved, err := h.resolve(request, payload.Workspace, payload.SessionID, payload.ConnectionID)
	if err != nil {
		return err
	}
	result, err := runGit(request.Context(), resolved, targetfs.GitRequest{Operation: targetfs.GitCheckout, Workspace: resolved.Workspace, Branch: payload.Branch, Create: payload.Create})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(string(result.Stderr))
		if detail == "" {
			detail = strings.TrimSpace(string(result.Stdout))
		}
		if detail == "" {
			detail = fmt.Sprintf("Git checkout exited with code %d", result.ExitCode)
		}
		return &targetfs.Error{Status: 400, Code: "git_checkout_failed", Detail: detail}
	}
	if h.options.Publisher != nil {
		if err := h.options.Publisher.Publish(request.Context(), "git.branch.changed", map[string]any{
			"workspace": resolved.Workspace, "branch": payload.Branch, "connection_id": resolved.ConnectionID,
		}); err != nil {
			return &targetfs.Error{Status: 500, Code: "publish_failed", Detail: "Git branch change could not be published", Err: err}
		}
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok", "current_branch": payload.Branch})
	return nil
}

func (h *Handler) diff(response http.ResponseWriter, request *http.Request) error {
	query := request.URL.Query()
	resolved, err := h.resolve(request, query.Get("workspace"), query.Get("session_id"), query.Get("connection_id"))
	if err != nil {
		return err
	}
	path := query.Get("path")
	if path != "" {
		clean, cleanErr := cleanGitPath(path)
		if cleanErr != nil {
			return cleanErr
		}
		path = clean
	}
	base := query.Get("base_branch")
	result, err := runGit(request.Context(), resolved, targetfs.GitRequest{Operation: targetfs.GitDiff, Workspace: resolved.Workspace, Path: path, BaseBranch: base})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return gitCommandError(result)
	}
	output := result.Stdout
	if len(bytes.TrimSpace(output)) == 0 && base == "" {
		result, err = runGit(request.Context(), resolved, targetfs.GitRequest{Operation: targetfs.GitDiff, Workspace: resolved.Workspace, Path: path, Cached: true})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return gitCommandError(result)
		}
		output = result.Stdout
	}
	if len(bytes.TrimSpace(output)) == 0 && path != "" {
		statusResult, statusErr := runGit(request.Context(), resolved, targetfs.GitRequest{Operation: targetfs.GitPathStatus, Workspace: resolved.Workspace, Path: path})
		if statusErr != nil {
			return statusErr
		}
		if statusResult.ExitCode != 0 {
			return gitCommandError(statusResult)
		}
		if !isUntracked(statusResult.Stdout) {
			return &targetfs.Error{Status: 404, Code: "git_path_not_found", Detail: "Git path was not found"}
		}
		readResult, readErr := resolved.FileSystem.Read(request.Context(), filepath.Join(resolved.Workspace, filepath.FromSlash(path)))
		if readErr != nil {
			return readErr
		}
		defer readResult.Reader.Close()
		content, readErr := io.ReadAll(readResult.Reader)
		if readErr != nil {
			return &targetfs.Error{Status: 500, Code: "read_failed", Detail: "Failed to read untracked file", Err: readErr}
		}
		output = untrackedDiff(path, content)
	}
	if len(bytes.TrimSpace(output)) == 0 {
		output = []byte("No changes detected.")
	}
	response.Header().Set("Content-Type", "text/plain; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, err = response.Write(output)
	return err
}

func cleanGitPath(value string) (string, error) {
	if strings.ContainsRune(value, 0) || filepath.IsAbs(value) {
		return "", &targetfs.Error{Status: 400, Code: "invalid_git_path", Detail: "Git path must be relative to the workspace"}
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(clean, `\\`) {
		return "", &targetfs.Error{Status: 400, Code: "invalid_git_path", Detail: "Git path must be relative to the workspace"}
	}
	return filepath.ToSlash(clean), nil
}

func runGit(ctx context.Context, resolved targetfs.Resolved, request targetfs.GitRequest) (targetfs.GitResult, error) {
	return resolved.FileSystem.ExecGit(ctx, request)
}

func parseBranches(output []byte) (string, []string) {
	current := "master"
	branches := make([]string, 0)
	seen := make(map[string]bool)
	for _, raw := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.Contains(line, " -> ") {
			line = strings.TrimSpace(strings.SplitN(line, " -> ", 2)[1])
		}
		if strings.HasPrefix(line, "*") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
			if strings.HasPrefix(line, "(HEAD detached at ") && strings.HasSuffix(line, ")") {
				line = strings.TrimSuffix(strings.TrimPrefix(line, "(HEAD detached at "), ")")
			}
			current = line
		}
		if !seen[line] {
			seen[line] = true
			branches = append(branches, line)
		}
	}
	return current, branches
}

func parseStatus(output []byte) ([]changedFile, int, int, int, error) {
	if len(output) == 0 {
		return []changedFile{}, 0, 0, 0, nil
	}
	fields := bytes.Split(output, []byte{0})
	files := make([]changedFile, 0)
	staged, unstaged, untracked := 0, 0, 0
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if len(field) == 0 {
			continue
		}
		if len(field) < 3 || field[2] != ' ' {
			return nil, 0, 0, 0, &targetfs.Error{Status: 502, Code: "invalid_git_status", Detail: "Git returned malformed status output"}
		}
		x, y := field[0], field[1]
		path := string(field[3:])
		if x == 'R' || x == 'C' {
			if index+1 >= len(fields) || len(fields[index+1]) == 0 {
				return nil, 0, 0, 0, &targetfs.Error{Status: 502, Code: "invalid_git_status", Detail: "Git returned an incomplete rename status"}
			}
			path = string(fields[index+1])
			index++
		}
		status := "modified"
		isStaged := x != ' ' && x != '?'
		if x == '?' && y == '?' {
			untracked++
			status = "untracked"
		} else {
			if isStaged {
				staged++
				status = "staged"
			}
			if y != ' ' {
				unstaged++
				if !isStaged {
					status = "modified"
				}
			}
		}
		files = append(files, changedFile{Path: path, Status: status, Staged: isStaged})
	}
	return files, staged, unstaged, untracked, nil
}

func parseNumstat(output []byte) (int, int, error) {
	insertions, deletions := 0, 0
	for _, line := range strings.Split(strings.TrimRight(string(output), "\x00\r\n"), "\x00") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		if parts[0] != "-" {
			value, err := strconv.Atoi(parts[0])
			if err != nil {
				return 0, 0, &targetfs.Error{Status: 502, Code: "invalid_git_numstat", Detail: "Git returned malformed insertion count", Err: err}
			}
			insertions += value
		}
		if parts[1] != "-" {
			value, err := strconv.Atoi(parts[1])
			if err != nil {
				return 0, 0, &targetfs.Error{Status: 502, Code: "invalid_git_numstat", Detail: "Git returned malformed deletion count", Err: err}
			}
			deletions += value
		}
	}
	return insertions, deletions, nil
}

func parseAheadBehind(output []byte) (int, int) {
	parts := strings.Fields(string(output))
	if len(parts) < 2 {
		return 0, 0
	}
	behind, _ := strconv.Atoi(parts[0])
	ahead, _ := strconv.Atoi(parts[1])
	return behind, ahead
}

func isUntracked(output []byte) bool {
	for _, field := range bytes.Split(output, []byte{0}) {
		if bytes.HasPrefix(field, []byte("?? ")) {
			return true
		}
	}
	return false
}

func untrackedDiff(path string, content []byte) []byte {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(content) == 0 {
		lines = []string{}
	}
	var output strings.Builder
	fmt.Fprintf(&output, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", path, path, path, len(lines))
	for _, line := range lines {
		output.WriteByte('+')
		output.WriteString(line)
		output.WriteByte('\n')
	}
	return []byte(output.String())
}

func gitStatusError(result targetfs.GitResult) error {
	return &targetfs.Error{Status: 422, Code: "git_status_failed", Detail: "无法读取该工作区的 Git 状态；请确认会话工作区目录。"}
}

func gitCommandError(result targetfs.GitResult) error {
	detail := strings.TrimSpace(string(result.Stderr))
	if detail == "" {
		detail = strings.TrimSpace(string(result.Stdout))
	}
	if detail == "" {
		detail = fmt.Sprintf("Git exited with code %d", result.ExitCode)
	}
	return &targetfs.Error{Status: 400, Code: "git_failed", Detail: detail}
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, maxJSONBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return &targetfs.Error{Status: 400, Code: "invalid_json", Detail: "Invalid JSON", Err: err}
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return &targetfs.Error{Status: 400, Code: "invalid_json", Detail: "Invalid JSON"}
	}
	return nil
}

func writeTargetError(response http.ResponseWriter, err error) {
	var target *targetfs.Error
	if errors.As(err, &target) {
		writeError(response, target.Status, target.Detail)
		return
	}
	writeError(response, http.StatusInternalServerError, "Internal server error")
}

func writeError(response http.ResponseWriter, status int, detail string) {
	writeJSON(response, status, map[string]string{"detail": detail})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
