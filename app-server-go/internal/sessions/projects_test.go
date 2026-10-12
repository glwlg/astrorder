package sessions

import (
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateProject(t *testing.T) {
	srv := &mockDaemonServer{}
	h, _, _, cleanup := setupTestServer(t, srv)
	defer cleanup()

	body := `{"workspace":"C:/myrepo","name":"My Project"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.handleCreateProject(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("create project failed with status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "My Project") {
		t.Fatalf("expected project name in response: %s", rec.Body.String())
	}
}

func TestBatchDeleteSessions(t *testing.T) {
	srv := &mockDaemonServer{deleteCb: func(req *sessiondv1.DeleteSessionRequest) (*sessiondv1.CommandReceipt, error) {
		return &sessiondv1.CommandReceipt{Session: req.Session, OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
			Outcome: &sessiondv1.CommandReceipt_Completed{Completed: &sessiondv1.CommandResult{}}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()

	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "ag-1", "kind": "codex", "name": "Codex", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "s-1", "agent_id": "ag-1", "title": "S1", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "s-2", "agent_id": "ag-1", "title": "S2", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"})

	body := `{"sessions":[{"agent_id":"ag-1","id":"s-1"},{"agent_id":"ag-1","id":"s-2"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/batch-delete", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.handleBatchDeleteSessions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("batch delete failed with status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"ok":true`) || !strings.Contains(rec.Body.String(), `"deleted"`) {
		t.Fatalf("unexpected batch delete response: %s", rec.Body.String())
	}

	// Verify they are deleted in store
	s1, _ := st.GetSession(ctx, "ag-1", "s-1")
	if s1 != nil {
		t.Fatalf("session s-1 was not deleted from store")
	}
}
