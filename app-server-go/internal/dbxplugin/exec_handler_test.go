package dbxplugin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func newAuthedRequest(method, path string, body any) *http.Request {
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Host = "127.0.0.1:8787"
	req.Header.Set("Origin", "http://127.0.0.1:8787")
	req.Header.Set("Authorization", "Bearer top-secret-token")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

func TestExecQueryGuardrailsAutoConnectAndAudit(t *testing.T) {
	var connectCalls atomic.Int32
	var execCalls atomic.Int32
	connected := map[string]bool{}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case BasePath + "/api/connection/list":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":            "conn-local",
					"name":          "pg-local",
					"db_type":       "postgresql",
					"host":          "127.0.0.1",
					"port":          5432,
					"database":      "opscore_dev",
					"is_production": false,
				},
				{
					"id":            "conn-prod",
					"name":          "ops@10.20.64.38",
					"db_type":       "postgresql",
					"host":          "10.20.64.38",
					"port":          5432,
					"database":      "ops_metadata",
					"is_production": true,
				},
			})
		case BasePath + "/api/connection/connect":
			connectCalls.Add(1)
			var payload struct {
				Config map[string]any `json:"config"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			id, _ := payload.Config["id"].(string)
			connected[id] = true
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
		case BasePath + "/api/schema/databases":
			id := r.URL.Query().Get("connection_id")
			if !connected[id] {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "connection not connected"})
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]string{{"name": "opscore_dev"}, {"name": "ops_metadata"}})
		case BasePath + "/api/query/execute-multi":
			execCalls.Add(1)
			var payload struct {
				ConnectionID string `json:"connectionId"`
				SQL          string `json:"sql"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if !connected[payload.ConnectionID] {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "connection not found / not connected"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"columns":    []map[string]string{{"name": "version", "dataType": "text"}},
						"rows":       [][]any{{"007"}},
						"elapsed_ms": 14,
					},
				},
				"total_elapsed_ms": 14,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()

	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	auditDir := t.TempDir()
	h := NewHandler(Options{
		Store:           st,
		Authorizer:      newTestAuthenticator(),
		DefaultUpstream: upstream.URL,
		BrowserSecret:   "top-secret-token",
		AuditDir:        auditDir,
	})

	// 1. Non-PROD read-only executes immediately without confirmation and auto-connects.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(http.MethodPost, ExecBasePath+"/query", map[string]any{
		"connection_id": "conn-local",
		"database":      "opscore_dev",
		"sql":           "SELECT version FROM schema_migrations;",
		"confirmed":     false,
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-prod read-only, got %d: %s", rec.Code, rec.Body.String())
	}
	var res ExecuteResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if !res.OK || len(res.Results) != 1 || res.Results[0].RowCount != 1 {
		t.Fatalf("unexpected execute result: %+v", res)
	}
	if connectCalls.Load() != 1 {
		t.Fatalf("expected 1 auto-connect call, got %d", connectCalls.Load())
	}

	// 2. PROD read-only WITHOUT confirmation MUST be rejected with HTTP 428.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(http.MethodPost, ExecBasePath+"/query", map[string]any{
		"connection_id": "conn-prod",
		"database":      "ops_metadata",
		"sql":           "SELECT version FROM agent_maintenance_schema_migrations;",
		"confirmed":     false,
	}))
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 for unconfirmed PROD read-only, got %d: %s", rec.Code, rec.Body.String())
	}

	// 3. Non-PROD write WITHOUT confirmation MUST be rejected with HTTP 428.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(http.MethodPost, ExecBasePath+"/query", map[string]any{
		"connection_id": "conn-local",
		"database":      "opscore_dev",
		"sql":           "UPDATE t SET a = 1 WHERE id = 2;",
		"confirmed":     false,
	}))
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 for unconfirmed write, got %d: %s", rec.Code, rec.Body.String())
	}

	// 4. PROD read-only WITH confirmation succeeds.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(http.MethodPost, ExecBasePath+"/query", map[string]any{
		"connection_id": "conn-prod",
		"database":      "ops_metadata",
		"sql":           "SELECT version FROM agent_maintenance_schema_migrations;",
		"confirmed":     true,
		"source_kind":   "file",
		"source_name":   "007_check.sql",
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for confirmed PROD query, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Audit log file recorded both executed runs.
	auditBytes, err := os.ReadFile(filepath.Join(auditDir, "dbx-sql-audit.jsonl"))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(auditBytes)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d: %s", len(lines), string(auditBytes))
	}

	// 6. Project bindings CRUD locks PROD flag and supports deletion.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(http.MethodPut, ExecBasePath+"/bindings", map[string]any{
		"project_key":   "project:local\x00opscore",
		"workspace_key": "connection:local\x00/home/luwei/workspace/OpsCore",
		"project_label": "OpsCore",
		"targets": []map[string]any{
			{
				"connection_id":   "conn-prod",
				"connection_name": "ops@10.20.64.38",
				"database":        "ops_metadata",
				"is_prod":         false, // Attempt downgrade: server must lock back to true
				"is_default":      true,
			},
			{
				"connection_id":   "conn-local",
				"connection_name": "pg-local",
				"database":        "opscore_dev",
				"is_prod":         true, // Manual PROD mark on non-prod connection
			},
		},
	}))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 saving binding, got %d: %s", rec.Code, rec.Body.String())
	}
	var saveResp struct {
		Binding ProjectDatabaseBinding `json:"binding"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saveResp); err != nil {
		t.Fatalf("decode binding response: %v", err)
	}
	if !saveResp.Binding.Targets[0].IsProd {
		t.Fatalf("expected DBX-marked PROD target to stay locked is_prod=true")
	}

	// 7. Manually marked PROD binding blocks unconfirmed SELECT on conn-local too!
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(http.MethodPost, ExecBasePath+"/query", map[string]any{
		"connection_id": "conn-local",
		"database":      "opscore_dev",
		"project_key":   "project:local\x00opscore",
		"sql":           "SELECT 1;",
		"confirmed":     false,
	}))
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("expected 428 when project binding manually marks target PROD, got %d", rec.Code)
	}

	// 8. List databases auto-connects when needed.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newAuthedRequest(http.MethodGet, ExecBasePath+"/databases?connection_id=conn-local", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ops_metadata") {
		t.Fatalf("unexpected databases response %d: %s", rec.Code, rec.Body.String())
	}
}
