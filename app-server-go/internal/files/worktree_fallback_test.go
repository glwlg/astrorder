package files

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitForTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Astrorder Test",
		"GIT_AUTHOR_EMAIL=test@astrorder.local",
		"GIT_COMMITTER_NAME=Astrorder Test",
		"GIT_COMMITTER_EMAIL=test@astrorder.local",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func TestWorktreeFallbackResolvesRawExistsStageAndOpen(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	base := t.TempDir()
	mainRepo := filepath.Join(base, "OpsCore")
	wtDir := filepath.Join(base, "opscore-wave4")
	unrelatedDir := filepath.Join(base, "unrelated-other")

	if err := os.MkdirAll(mainRepo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(unrelatedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainRepo, "README.md"), []byte("# OpsCore\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runGitForTest(t, mainRepo, "init", "-b", "main")
	runGitForTest(t, mainRepo, "add", "README.md")
	runGitForTest(t, mainRepo, "commit", "-m", "init")
	runGitForTest(t, mainRepo, "worktree", "add", "-b", "feat/wave4", wtDir)

	// Create SQL migration ONLY inside the linked worktree (not in mainRepo)
	relSQL := filepath.Join("backend", "migrations", "biz-system", "003_audit.sql")
	wtSQL := filepath.Join(wtDir, relSQL)
	if err := os.MkdirAll(filepath.Dir(wtSQL), 0o755); err != nil {
		t.Fatal(err)
	}
	sqlBody := "ALTER TABLE biz_system_audit_logs ADD COLUMN trace_id TEXT;\n"
	if err := os.WriteFile(wtSQL, []byte(sqlBody), 0o600); err != nil {
		t.Fatal(err)
	}

	// Create an unrelated file outside any linked worktree (must remain 404)
	if err := os.WriteFile(filepath.Join(unrelatedDir, "secret.sql"), []byte("SELECT 1;"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := fakeStore{
		session: map[string]any{
			"id":            "sess-wave4",
			"workspace":     mainRepo,
			"connection_id": "local",
		},
	}
	opener := &openRecorder{}
	handler := testHandler(mainRepo, store, &eventRecorder{}, opener)

	mainMissingSQL := filepath.Join(mainRepo, relSQL)

	// 1. GET /api/v1/files/raw falls back to linked worktree + emits headers
	res := call(t, handler, http.MethodGet, "/api/v1/files/raw?path="+queryEscape(mainMissingSQL)+"&session_id=sess-wave4", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("raw GET expected 200, got %d: %s", res.Code, res.Body.String())
	}
	if res.Body.String() != sqlBody {
		t.Fatalf("raw body = %q, want %q", res.Body.String(), sqlBody)
	}
	resolvedHeader, _ := url.QueryUnescape(res.Header().Get(headerResolvedPath))
	wtHeader, _ := url.QueryUnescape(res.Header().Get(headerWorktreePath))
	branchHeader, _ := url.QueryUnescape(res.Header().Get(headerWorktreeBranch))
	if !sameNormalizedPath(resolvedHeader, wtSQL) {
		t.Fatalf("X-Astrorder-Resolved-Path = %q, want %q", resolvedHeader, wtSQL)
	}
	if !sameNormalizedPath(wtHeader, wtDir) {
		t.Fatalf("X-Astrorder-Worktree-Path = %q, want %q", wtHeader, wtDir)
	}
	if branchHeader != "feat/wave4" {
		t.Fatalf("X-Astrorder-Worktree-Branch = %q, want feat/wave4", branchHeader)
	}

	// 2. HEAD /api/v1/files/raw also resolves via linked worktree
	headRes := call(t, handler, http.MethodHead, "/api/v1/files/raw?path="+queryEscape(mainMissingSQL)+"&session_id=sess-wave4", nil)
	if headRes.Code != http.StatusOK {
		t.Fatalf("raw HEAD expected 200, got %d", headRes.Code)
	}

	// 3. POST /api/v1/files/exists detects file in linked worktree
	existsBody := `{"workspace":` + quote(mainRepo) + `,"session_id":"sess-wave4","paths":["backend/migrations/biz-system/003_audit.sql","secret.sql"]}`
	existsRes := call(t, handler, http.MethodPost, "/api/v1/files/exists", strings.NewReader(existsBody))
	if existsRes.Code != http.StatusOK {
		t.Fatalf("exists status = %d body = %s", existsRes.Code, existsRes.Body.String())
	}
	var existsPayload struct {
		Existing      []string          `json:"existing"`
		ResolvedPaths map[string]string `json:"resolved_paths"`
	}
	if err := json.Unmarshal(existsRes.Body.Bytes(), &existsPayload); err != nil {
		t.Fatalf("decode exists: %v", err)
	}
	if len(existsPayload.Existing) != 1 || existsPayload.Existing[0] != "backend/migrations/biz-system/003_audit.sql" {
		t.Fatalf("unexpected existing = %#v", existsPayload.Existing)
	}
	if !sameNormalizedPath(existsPayload.ResolvedPaths["backend/migrations/biz-system/003_audit.sql"], wtSQL) {
		t.Fatalf("unexpected resolved_paths = %#v", existsPayload.ResolvedPaths)
	}

	// 4. POST /api/v1/files/stage resolves and stages real file path from linked worktree
	stageBody := `{"paths":[` + quote(mainMissingSQL) + `],"session_id":"sess-wave4","copy_to_clipboard":false}`
	stageRes := call(t, handler, http.MethodPost, "/api/v1/files/stage", strings.NewReader(stageBody))
	if stageRes.Code != http.StatusOK {
		t.Fatalf("stage status = %d body = %s", stageRes.Code, stageRes.Body.String())
	}
	var stagePayload struct {
		OK         bool     `json:"ok"`
		LocalPaths []string `json:"local_paths"`
	}
	if err := json.Unmarshal(stageRes.Body.Bytes(), &stagePayload); err != nil {
		t.Fatalf("decode stage: %v", err)
	}
	if len(stagePayload.LocalPaths) != 1 || !sameNormalizedPath(stagePayload.LocalPaths[0], wtSQL) {
		t.Fatalf("stage local_paths = %#v, want [%q]", stagePayload.LocalPaths, wtSQL)
	}

	// 5. POST /api/v1/system/open-file opens real file in linked worktree
	openBody := `{"path":` + quote(mainMissingSQL) + `,"session_id":"sess-wave4"}`
	openRes := call(t, handler, http.MethodPost, "/api/v1/system/open-file", strings.NewReader(openBody))
	if openRes.Code != http.StatusOK {
		t.Fatalf("open-file status = %d body = %s", openRes.Code, openRes.Body.String())
	}
	if !sameNormalizedPath(opener.path, wtSQL) {
		t.Fatalf("opener.path = %q, want %q", opener.path, wtSQL)
	}

	// 6. Security boundary: file in unrelated non-worktree directory must remain 404
	unrelatedMissing := filepath.Join(mainRepo, "secret.sql")
	notFoundRes := call(t, handler, http.MethodGet, "/api/v1/files/raw?path="+queryEscape(unrelatedMissing)+"&session_id=sess-wave4", nil)
	if notFoundRes.Code != http.StatusNotFound {
		t.Fatalf("unrelated file expected 404, got %d: %s", notFoundRes.Code, notFoundRes.Body.String())
	}

	// 7. GET /api/v1/files/raw without session_id still resolves via ancestor walk-up
	noSessionRes := call(t, handler, http.MethodGet, "/api/v1/files/raw?path="+queryEscape(mainMissingSQL), nil)
	if noSessionRes.Code != http.StatusOK || noSessionRes.Body.String() != sqlBody {
		t.Fatalf("raw without session_id expected 200, got %d: %s", noSessionRes.Code, noSessionRes.Body.String())
	}

	// 8. Layer 2: tree & raw directly inside a registered linked worktree succeed even outside AllowedRoots
	treeRes := call(t, handler, http.MethodGet, "/api/v1/files/tree?path="+queryEscape(wtDir)+"&session_id=sess-wave4&depth=4", nil)
	if treeRes.Code != http.StatusOK || !strings.Contains(treeRes.Body.String(), "003_audit.sql") {
		t.Fatalf("tree on linked worktree expected 200 with 003_audit.sql, got %d: %s", treeRes.Code, treeRes.Body.String())
	}
	directRawRes := call(t, handler, http.MethodGet, "/api/v1/files/raw?path="+queryEscape(wtSQL)+"&session_id=sess-wave4", nil)
	if directRawRes.Code != http.StatusOK || directRawRes.Body.String() != sqlBody {
		t.Fatalf("direct raw on linked worktree expected 200, got %d: %s", directRawRes.Code, directRawRes.Body.String())
	}

	// 9. Layer 2 security boundary: tree on unrelated sibling directory must stay 403
	forbiddenTreeRes := call(t, handler, http.MethodGet, "/api/v1/files/tree?path="+queryEscape(unrelatedDir)+"&session_id=sess-wave4&depth=2", nil)
	if forbiddenTreeRes.Code != http.StatusForbidden {
		t.Fatalf("tree on unrelated directory expected 403, got %d: %s", forbiddenTreeRes.Code, forbiddenTreeRes.Body.String())
	}
}
