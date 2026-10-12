package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

// TestFormalReviewRelayRunsRoute is the first formal-router regression for the
// review relay control plane: the SPA list call must be served by the Go App
// Server instead of falling through to the reserved 404 path.
func TestFormalReviewRelayRunsRoute(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	handler, err := NewHandlerChecked(Options{Config: config.Config{BrowserSecret: "fixture"}, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/review-relay/runs?source_agent_id=agent-a&source_session_id=session-1", nil)
	request.Header.Set("Authorization", "Bearer fixture")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("review relay runs route = %d %s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 0 {
		t.Fatalf("review relay runs = %#v", payload.Items)
	}
}

// relayHandler opens a fresh store-backed handler for one test.
func relayHandler(t *testing.T, databasePath string) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	handler, err := NewHandlerChecked(Options{Config: config.Config{BrowserSecret: "fixture"}, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	return handler, st
}

func relayCall(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer fixture")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeMap(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode %q: %v", response.Body.String(), err)
	}
	return payload
}

func TestReviewRelayBindingCRUDAndPersistence(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "app.db")
	handler, st := relayHandler(t, databasePath)
	base := "/api/v1/review-relay/bindings/agent-a/session-1"

	// A missing binding is a null success, not a 404.
	missing := relayCall(t, handler, http.MethodGet, base, "")
	if missing.Code != http.StatusOK || strings.TrimSpace(missing.Body.String()) != "null" {
		t.Fatalf("missing binding = %d %q", missing.Code, missing.Body.String())
	}

	created := relayCall(t, handler, http.MethodPut, base, `{"review_agent_id":"agent-b","review_session_id":"session-2","workspace":"P:/work"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("put binding = %d %s", created.Code, created.Body.String())
	}
	row := decodeMap(t, created)
	if row["source_agent_id"] != "agent-a" || row["source_session_id"] != "session-1" ||
		row["review_agent_id"] != "agent-b" || row["review_session_id"] != "session-2" ||
		row["workspace"] != "P:/work" || row["enabled"] != true {
		t.Fatalf("binding = %#v", row)
	}

	fetched := relayCall(t, handler, http.MethodGet, base, "")
	if fetched.Code != http.StatusOK || decodeMap(t, fetched)["enabled"] != true {
		t.Fatalf("get binding = %d %s", fetched.Code, fetched.Body.String())
	}

	// The composite identity is exact: another session is unaffected.
	other := relayCall(t, handler, http.MethodGet, "/api/v1/review-relay/bindings/agent-a/session-9", "")
	if other.Code != http.StatusOK || strings.TrimSpace(other.Body.String()) != "null" {
		t.Fatalf("sibling binding = %d %q", other.Code, other.Body.String())
	}

	// Explicit enabled=false survives.
	disabled := relayCall(t, handler, http.MethodPut, base, `{"review_agent_id":"agent-b","review_session_id":"session-2","workspace":"P:/work","enabled":false}`)
	if disabled.Code != http.StatusOK || decodeMap(t, disabled)["enabled"] != false {
		t.Fatalf("disabled binding = %d %s", disabled.Code, disabled.Body.String())
	}

	// Validation: unknown field, empty workspace, oversize id.
	for _, body := range []string{
		`{"review_agent_id":"agent-b","review_session_id":"session-2","workspace":"P:/work","extra":1}`,
		`{"review_agent_id":"agent-b","review_session_id":"session-2","workspace":""}`,
		`{"review_agent_id":"agent-b","review_session_id":"session-2"}`,
		`{"review_agent_id":"` + strings.Repeat("x", 257) + `","review_session_id":"session-2","workspace":"P:/work"}`,
		`null`,
		`{"review_agent_id":"agent-b","review_session_id":"session-2","workspace":"P:/work"} {"trailing":1}`,
		`{"review_agent_id":"agent-b","review_session_id":"session-2","workspace":42}`,
	} {
		invalid := relayCall(t, handler, http.MethodPut, base, body)
		if invalid.Code != http.StatusUnprocessableEntity || strings.TrimSpace(invalid.Body.String()) != `{"detail":"Invalid request"}` {
			t.Fatalf("put %s = %d %s", body, invalid.Code, invalid.Body.String())
		}
	}

	// Delete is idempotent and survives reopen.
	deleted := relayCall(t, handler, http.MethodDelete, base, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete binding = %d %s", deleted.Code, deleted.Body.String())
	}
	again := relayCall(t, handler, http.MethodDelete, base, "")
	if again.Code != http.StatusNoContent {
		t.Fatalf("second delete binding = %d %s", again.Code, again.Body.String())
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, _ := relayHandler(t, databasePath)
	afterDelete := relayCall(t, reopened, http.MethodGet, base, "")
	if afterDelete.Code != http.StatusOK || strings.TrimSpace(afterDelete.Body.String()) != "null" {
		t.Fatalf("binding after delete reopen = %d %q", afterDelete.Code, afterDelete.Body.String())
	}
}

func TestReviewRelayBindingSurvivesStoreReopen(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "app.db")
	handler, st := relayHandler(t, databasePath)
	base := "/api/v1/review-relay/bindings/agent-a/session-1"
	if response := relayCall(t, handler, http.MethodPut, base, `{"review_agent_id":"agent-b","review_session_id":"session-2","workspace":"P:/持久"}`); response.Code != http.StatusOK {
		t.Fatalf("put binding = %d %s", response.Code, response.Body.String())
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _ := relayHandler(t, databasePath)
	response := relayCall(t, reopened, http.MethodGet, base, "")
	if response.Code != http.StatusOK {
		t.Fatalf("get binding after reopen = %d %s", response.Code, response.Body.String())
	}
	row := decodeMap(t, response)
	if row["review_agent_id"] != "agent-b" || row["workspace"] != "P:/持久" || row["enabled"] != true {
		t.Fatalf("binding after reopen = %#v", row)
	}
}

func TestReviewRelayRunLifecycleAndRawComment(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "app.db")
	handler, st := relayHandler(t, databasePath)
	createBody := `{"source_agent_id":"agent-a","source_session_id":"session-1","review_agent_id":"agent-b","review_session_id":"session-2","command_id":"cmd-1","status":"validating"}`

	created := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/runs", createBody)
	if created.Code != http.StatusOK {
		t.Fatalf("create run = %d %s", created.Code, created.Body.String())
	}
	row := decodeMap(t, created)
	runID, _ := row["id"].(string)
	if !strings.HasPrefix(runID, "review-") || row["status"] != "validating" || row["command_id"] != "cmd-1" {
		t.Fatalf("run = %#v", row)
	}
	if baseline, ok := row["baseline_ids"].([]any); !ok || len(baseline) != 0 {
		t.Fatalf("baseline = %#v", row["baseline_ids"])
	}

	fetched := relayCall(t, handler, http.MethodGet, "/api/v1/review-relay/runs/"+runID, "")
	if fetched.Code != http.StatusOK || decodeMap(t, fetched)["id"] != runID {
		t.Fatalf("get run = %d %s", fetched.Code, fetched.Body.String())
	}

	// Raw markers must round-trip byte-for-byte, including escapes and unicode.
	rawComment := "::code-comment{file=\"src/a.ts\",line=3,body=\"he said \\\"hi\\\" — 中文\"}\nsecond line"
	patched := relayCall(t, handler, http.MethodPatch, "/api/v1/review-relay/runs/"+runID,
		`{"status":"draft_ready","comment_text":`+jsonString(rawComment)+`}`)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch run = %d %s", patched.Code, patched.Body.String())
	}
	patchedRow := decodeMap(t, patched)
	if patchedRow["status"] != "draft_ready" || patchedRow["comment_text"] != rawComment {
		t.Fatalf("patched run = %#v", patchedRow)
	}

	// null fields are omitted, not cleared.
	nullPatch := relayCall(t, handler, http.MethodPatch, "/api/v1/review-relay/runs/"+runID, `{"status":null,"comment_text":null}`)
	if nullPatch.Code != http.StatusOK {
		t.Fatalf("null patch = %d %s", nullPatch.Code, nullPatch.Body.String())
	}
	nullRow := decodeMap(t, nullPatch)
	if nullRow["status"] != "draft_ready" || nullRow["comment_text"] != rawComment {
		t.Fatalf("null patch mutated = %#v", nullRow)
	}

	// Missing run semantics.
	missingGet := relayCall(t, handler, http.MethodGet, "/api/v1/review-relay/runs/review-missing", "")
	if missingGet.Code != http.StatusNotFound || strings.TrimSpace(missingGet.Body.String()) != `{"detail":"review run not found"}` {
		t.Fatalf("missing get = %d %s", missingGet.Code, missingGet.Body.String())
	}
	missingPatch := relayCall(t, handler, http.MethodPatch, "/api/v1/review-relay/runs/review-missing", `{"status":"failed"}`)
	if missingPatch.Code != http.StatusNotFound || strings.TrimSpace(missingPatch.Body.String()) != `{"detail":"review run not found"}` {
		t.Fatalf("missing patch = %d %s", missingPatch.Code, missingPatch.Body.String())
	}

	// Body validation: unknown field, bad status, oversize comment/error, type, trailing.
	for _, body := range []string{
		`{"status":"bogus"}`,
		`{"status":"reviewing","unknown":1}`,
		`{"status":42}`,
		`{"status":"empty"} {"status":"failed"}`,
		`{"comment_text":` + jsonString(strings.Repeat("c", 200001)) + `}`,
		`{"error":` + jsonString(strings.Repeat("e", 2001)) + `}`,
	} {
		invalid := relayCall(t, handler, http.MethodPatch, "/api/v1/review-relay/runs/"+runID, body)
		if invalid.Code != http.StatusUnprocessableEntity || strings.TrimSpace(invalid.Body.String()) != `{"detail":"Invalid request"}` {
			t.Fatalf("patch %d bytes = %d %s", len(body), invalid.Code, invalid.Body.String())
		}
	}

	// Create validation: missing fields, invalid status, baseline bound.
	for _, body := range []string{
		`{"source_agent_id":"agent-a","source_session_id":"session-1","review_agent_id":"agent-b","review_session_id":"session-2","status":"validating"}`,
		`{"source_agent_id":"agent-a","source_session_id":"session-1","review_agent_id":"agent-b","review_session_id":"session-2","command_id":"cmd-1","status":"nope"}`,
		`{"source_agent_id":"agent-a","source_session_id":"session-1","review_agent_id":"agent-b","review_session_id":"session-2","command_id":"cmd-1","status":"validating","extra":1}`,
		`{"source_agent_id":"agent-a","source_session_id":"session-1","review_agent_id":"agent-b","review_session_id":"session-2","command_id":"cmd-1","status":"validating","baseline_ids":` + baselineIDs(1001) + `}`,
	} {
		invalid := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/runs", body)
		if invalid.Code != http.StatusUnprocessableEntity || strings.TrimSpace(invalid.Body.String()) != `{"detail":"Invalid request"}` {
			t.Fatalf("create %d bytes = %d %s", len(body), invalid.Code, invalid.Body.String())
		}
	}

	// List validation: both query identities are required.
	for _, target := range []string{
		"/api/v1/review-relay/runs",
		"/api/v1/review-relay/runs?source_agent_id=agent-a",
		"/api/v1/review-relay/runs?source_agent_id=agent-a&source_session_id=",
		"/api/v1/review-relay/runs?source_agent_id=agent-a&source_session_id=session-1&active_only=maybe",
		"/api/v1/review-relay/runs?source_agent_id=" + strings.Repeat("x", 257) + "&source_session_id=session-1",
	} {
		invalid := relayCall(t, handler, http.MethodGet, target, "")
		if invalid.Code != http.StatusUnprocessableEntity {
			t.Fatalf("list %s = %d %s", target, invalid.Code, invalid.Body.String())
		}
	}

	// Durable raw comment and status survive a full close/reopen cycle.
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _ := relayHandler(t, databasePath)
	restored := relayCall(t, reopened, http.MethodGet, "/api/v1/review-relay/runs/"+runID, "")
	if restored.Code != http.StatusOK {
		t.Fatalf("get after reopen = %d %s", restored.Code, restored.Body.String())
	}
	restoredRow := decodeMap(t, restored)
	if restoredRow["comment_text"] != rawComment || restoredRow["status"] != "draft_ready" {
		t.Fatalf("run after reopen = %#v", restoredRow)
	}
}

func TestReviewRelayCompositeIdentityActiveFilterAndOrder(t *testing.T) {
	handler, _ := relayHandler(t, filepath.Join(t.TempDir(), "app.db"))
	create := func(agentID, sessionID, status string) string {
		body := `{"source_agent_id":"` + agentID + `","source_session_id":"` + sessionID +
			`","review_agent_id":"agent-b","review_session_id":"session-2","command_id":"cmd","status":"` + status + `"}`
		response := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/runs", body)
		if response.Code != http.StatusOK {
			t.Fatalf("create run = %d %s", response.Code, response.Body.String())
		}
		id, _ := decodeMap(t, response)["id"].(string)
		return id
	}

	first := create("agent-a", "shared", "validating")
	time.Sleep(2 * time.Millisecond)
	second := create("agent-a", "shared", "draft_ready")
	time.Sleep(2 * time.Millisecond)
	third := create("agent-a", "shared", "failed")
	foreign := create("agent-b", "shared", "validating")

	list := func(target string) []map[string]any {
		response := relayCall(t, handler, http.MethodGet, target, "")
		if response.Code != http.StatusOK {
			t.Fatalf("list %s = %d %s", target, response.Code, response.Body.String())
		}
		var payload struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Items
	}

	items := list("/api/v1/review-relay/runs?source_agent_id=agent-a&source_session_id=shared")
	if len(items) != 3 {
		t.Fatalf("items = %#v", items)
	}
	if items[0]["id"] != third || items[1]["id"] != second || items[2]["id"] != first {
		t.Fatalf("order = %#v", items)
	}

	active := list("/api/v1/review-relay/runs?source_agent_id=agent-a&source_session_id=shared&active_only=true")
	if len(active) != 1 || active[0]["id"] != first {
		t.Fatalf("active = %#v", active)
	}

	foreignItems := list("/api/v1/review-relay/runs?source_agent_id=agent-b&source_session_id=shared")
	if len(foreignItems) != 1 || foreignItems[0]["id"] != foreign {
		t.Fatalf("foreign = %#v", foreignItems)
	}
}

func TestReviewRelayAuthNoStoreAndMethodBoundaries(t *testing.T) {
	handler, _ := relayHandler(t, filepath.Join(t.TempDir(), "app.db"))

	// Unauthenticated access is rejected before any store work.
	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/review-relay/runs?source_agent_id=a&source_session_id=s", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unauthenticated)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated = %d %s", response.Code, response.Body.String())
	}

	// Successful control-plane reads carry no-store.
	allowed := relayCall(t, handler, http.MethodGet, "/api/v1/review-relay/bindings/agent-a/session-1", "")
	if allowed.Code != http.StatusOK || allowed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("no-store = %d %#v", allowed.Code, allowed.Header())
	}

	// Unmatched review-relay paths fall through to the reserved 404.
	notFound := relayCall(t, handler, http.MethodGet, "/api/v1/review-relay/unknown", "")
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unknown path = %d %s", notFound.Code, notFound.Body.String())
	}
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func baselineIDs(count int) string {
	items := make([]string, count)
	for i := range items {
		items[i] = `"msg-1"`
	}
	return "[" + strings.Join(items, ",") + "]"
}

func validateGitRepo(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	runValidateGit(t, repository, "init", "-q")
	if err := os.WriteFile(filepath.Join(repository, "base.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runValidateGit(t, repository, "add", "base.txt")
	runValidateGit(t, repository, "-c", "user.name=Astrorder Test", "-c", "user.email=test@invalid", "commit", "-qm", "base")
	return repository
}

func runValidateGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

// seedValidateSessions writes one agent and one session projection per side.
func seedValidateSessions(t *testing.T, st *store.Store, agentID, sessionID, workspace, connectionID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id": agentID, "kind": "codex", "name": agentID, "status": "ready", "connection_id": connectionID,
	}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"id": sessionID, "agent_id": agentID, "status": "idle", "workspace": workspace,
		"updated_at": time.Now().UTC(),
	}
	if connectionID != "" {
		payload["connection_id"] = connectionID
	}
	if _, err := st.UpsertSession(ctx, payload); err != nil {
		t.Fatal(err)
	}
}

func TestReviewRelayValidateMatchesSnapshotsAndFailsClosed(t *testing.T) {
	handler, st := relayHandler(t, filepath.Join(t.TempDir(), "app.db"))
	repository := validateGitRepo(t)
	seedValidateSessions(t, st, "agent-a", "session-1", repository, "")
	seedValidateSessions(t, st, "agent-b", "session-2", repository, "")

	valid := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/validate", `{
		"source_workspace":`+jsonString(repository)+`,"review_workspace":`+jsonString(repository)+`,
		"source_agent_id":"agent-a","source_session_id":"session-1",
		"review_agent_id":"agent-b","review_session_id":"session-2"}`)
	if valid.Code != http.StatusOK {
		t.Fatalf("validate = %d %s", valid.Code, valid.Body.String())
	}
	validRow := decodeMap(t, valid)
	if validRow["valid"] != true {
		t.Fatalf("validate = %#v", validRow)
	}
	source, ok := validRow["source"].(map[string]any)
	if !ok || source["repo_root"] == "" || source["head"] == "" || source["change_fingerprint"] == "" {
		t.Fatalf("source snapshot = %#v", validRow["source"])
	}

	// A content-only untracked change between the two workspaces fails closed.
	if err := os.WriteFile(filepath.Join(repository, "drift.txt"), []byte("drift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := validateGitRepo(t)
	seedValidateSessions(t, st, "agent-c", "session-3", other, "")
	seedValidateSessions(t, st, "agent-d", "session-4", other, "")
	mismatch := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/validate", `{
		"source_workspace":`+jsonString(repository)+`,"review_workspace":`+jsonString(other)+`,
		"source_agent_id":"agent-a","source_session_id":"session-1",
		"review_agent_id":"agent-c","review_session_id":"session-3"}`)
	if mismatch.Code != http.StatusConflict {
		t.Fatalf("mismatch validate = %d %s", mismatch.Code, mismatch.Body.String())
	}
	mismatchRow := decodeMap(t, mismatch)
	detail, ok := mismatchRow["detail"].(map[string]any)
	if !ok || detail["source"] == nil || detail["review"] == nil {
		t.Fatalf("mismatch detail = %#v", mismatchRow["detail"])
	}

	// Partial session identifiers are rejected before any snapshot work.
	partial := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/validate", `{
		"source_workspace":`+jsonString(repository)+`,"review_workspace":`+jsonString(repository)+`,
		"source_agent_id":"agent-a"}`)
	if partial.Code != http.StatusUnprocessableEntity || !strings.Contains(partial.Body.String(), "必须同时指定开发与审查会话") {
		t.Fatalf("partial validate = %d %s", partial.Code, partial.Body.String())
	}

	// Missing sessions are 404.
	missing := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/validate", `{
		"source_workspace":`+jsonString(repository)+`,"review_workspace":`+jsonString(repository)+`,
		"source_agent_id":"agent-a","source_session_id":"session-1",
		"review_agent_id":"agent-b","review_session_id":"session-missing"}`)
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "开发或审查会话不存在") {
		t.Fatalf("missing session validate = %d %s", missing.Code, missing.Body.String())
	}

	// A changed stored workspace is 409.
	changed := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/validate", `{
		"source_workspace":`+jsonString(repository)+`,"review_workspace":"P:/stale",
		"source_agent_id":"agent-a","source_session_id":"session-1",
		"review_agent_id":"agent-b","review_session_id":"session-2"}`)
	if changed.Code != http.StatusConflict || !strings.Contains(changed.Body.String(), "会话工作区已变化") {
		t.Fatalf("changed workspace validate = %d %s", changed.Code, changed.Body.String())
	}

	// Unknown body fields and unknown SSH connections fail with 422.
	extra := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/validate", `{
		"source_workspace":`+jsonString(repository)+`,"review_workspace":`+jsonString(repository)+`,"extra":1}`)
	if extra.Code != http.StatusUnprocessableEntity {
		t.Fatalf("extra field validate = %d %s", extra.Code, extra.Body.String())
	}
}

func TestReviewRelayValidateConnectionBoundary(t *testing.T) {
	handler, st := relayHandler(t, filepath.Join(t.TempDir(), "app.db"))
	repository := validateGitRepo(t)

	// Different connections fail before any snapshot is captured.
	seedValidateSessions(t, st, "agent-a", "session-1", repository, "")
	if _, err := st.UpsertAgent(context.Background(), map[string]any{
		"id": "agent-b", "kind": "codex", "name": "agent-b", "status": "ready", "connection_id": "ssh-far",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(context.Background(), map[string]any{
		"id": "session-2", "agent_id": "agent-b", "status": "idle", "workspace": repository, "connection_id": "ssh-far",
		"updated_at": time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	crossEnvironment := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/validate", `{
		"source_workspace":`+jsonString(repository)+`,"review_workspace":`+jsonString(repository)+`,
		"source_agent_id":"agent-a","source_session_id":"session-1",
		"review_agent_id":"agent-b","review_session_id":"session-2"}`)
	if crossEnvironment.Code != http.StatusConflict || !strings.Contains(crossEnvironment.Body.String(), "不在同一个运行环境") {
		t.Fatalf("cross environment validate = %d %s", crossEnvironment.Code, crossEnvironment.Body.String())
	}

	// A shared remote connection that no longer exists is 422, never a local probe.
	if _, err := st.UpsertAgent(context.Background(), map[string]any{
		"id": "agent-c", "kind": "codex", "name": "agent-c", "status": "ready", "connection_id": "ssh-missing",
	}); err != nil {
		t.Fatal(err)
	}
	for _, seed := range []struct{ agentID, sessionID string }{{"agent-c", "session-3"}, {"agent-d", "session-4"}} {
		if _, err := st.UpsertAgent(context.Background(), map[string]any{
			"id": seed.agentID, "kind": "codex", "name": seed.agentID, "status": "ready", "connection_id": "ssh-missing",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertSession(context.Background(), map[string]any{
			"id": seed.sessionID, "agent_id": seed.agentID, "status": "idle", "workspace": repository, "connection_id": "ssh-missing",
			"updated_at": time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	missingConnection := relayCall(t, handler, http.MethodPost, "/api/v1/review-relay/validate", `{
		"source_workspace":`+jsonString(repository)+`,"review_workspace":`+jsonString(repository)+`,
		"source_agent_id":"agent-c","source_session_id":"session-3",
		"review_agent_id":"agent-d","review_session_id":"session-4"}`)
	if missingConnection.Code != http.StatusUnprocessableEntity || !strings.Contains(missingConnection.Body.String(), "SSH 连接不存在") {
		t.Fatalf("missing connection validate = %d %s", missingConnection.Code, missingConnection.Body.String())
	}
}
