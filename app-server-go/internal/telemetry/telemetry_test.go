package telemetry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
)

func newTestHandler(t *testing.T, maxBytes int64) (*Handler, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "telemetry")
	authenticator := auth.New(config.Config{BrowserSecret: "test-secret"}, nil)
	return NewHandler(Options{
		Authorizer:   authenticator,
		TelemetryDir: dir,
		MaxFileBytes: maxBytes,
	}), dir
}

func authedReq(method, path, body string) *http.Request {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-secret")
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

func TestTelemetryIngestSummaryAndReset(t *testing.T) {
	h, dir := newTestHandler(t, 1024*1024)

	// Unauthenticated request rejected.
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest(http.MethodPost, ClientPerfPath, strings.NewReader(`{"client_id":"c1","surface":"desktop-wails"}`)))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d", unauth.Code)
	}

	// Missing required fields rejected.
	bad := httptest.NewRecorder()
	h.ServeHTTP(bad, authedReq(http.MethodPost, ClientPerfPath, `{"surface":"desktop-wails"}`))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 without client_id, got %d", bad.Code)
	}

	// Valid ingest persists JSONL + latest-summary.json.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, authedReq(http.MethodPost, ClientPerfPath, `{"client_id":"desk-1","surface":"desktop-wails","route":"/chat/:id","memory":{"js_heap_used_mb":142.5}}`))
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 on ingest, got %d: %s", w.Code, w.Body.String())
	}

	logBytes, err := os.ReadFile(filepath.Join(dir, "client-perf.jsonl"))
	if err != nil {
		t.Fatalf("expected client-perf.jsonl to be written: %v", err)
	}
	if !strings.Contains(string(logBytes), `"client_id":"desk-1"`) || !strings.Contains(string(logBytes), `"server_runtime"`) {
		t.Fatalf("unexpected jsonl line: %s", string(logBytes))
	}
	if _, err := os.Stat(filepath.Join(dir, "latest-summary.json")); err != nil {
		t.Fatalf("expected latest-summary.json to be written: %v", err)
	}

	// GET returns aggregated summary with recent tail.
	getW := httptest.NewRecorder()
	h.ServeHTTP(getW, authedReq(http.MethodGet, ClientPerfPath+"?limit=5", ""))
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 on summary GET, got %d: %s", getW.Code, getW.Body.String())
	}
	var resp struct {
		ClientCount int              `json:"client_count"`
		Clients     []clientRecord   `json:"clients"`
		Recent      []map[string]any `json:"recent"`
	}
	if err := json.Unmarshal(getW.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ClientCount != 1 || len(resp.Clients) != 1 || resp.Clients[0].ClientID != "desk-1" || resp.Clients[0].ReportsReceived != 1 {
		t.Fatalf("unexpected clients summary: %+v", resp)
	}
	if len(resp.Recent) != 1 {
		t.Fatalf("expected 1 recent item, got %d", len(resp.Recent))
	}

	// DELETE resets state and removes files.
	delW := httptest.NewRecorder()
	h.ServeHTTP(delW, authedReq(http.MethodDelete, ClientPerfPath, ""))
	if delW.Code != http.StatusOK {
		t.Fatalf("expected 200 on DELETE, got %d", delW.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "client-perf.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected client-perf.jsonl to be removed after reset")
	}
}

func TestTelemetryLogRotation(t *testing.T) {
	h, dir := newTestHandler(t, 420)
	for i := 0; i < 6; i++ {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, authedReq(http.MethodPost, ClientPerfPath, `{"client_id":"desk-rot","surface":"desktop-wails","route":"/chat/:id","padding":"xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"}`))
		if w.Code != http.StatusAccepted {
			t.Fatalf("ingest %d failed: %d", i, w.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "client-perf.1.jsonl")); err != nil {
		t.Fatalf("expected rotated client-perf.1.jsonl to exist: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "client-perf.jsonl"))
	if err != nil {
		t.Fatalf("expected active client-perf.jsonl to exist: %v", err)
	}
	if info.Size() > 420 {
		t.Fatalf("expected active log size <= 420 bytes after rotation, got %d", info.Size())
	}
}
