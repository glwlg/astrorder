package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func seedProjectSession(t *testing.T, h *Handler, aid, sid, source, pid string) {
	t.Helper()
	ctx := context.Background()
	if _, err := h.store.UpsertSession(ctx, map[string]any{"id": sid, "agent_id": aid, "source_id": source, "project_id": pid, "title": "keep", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
}
func TestFormalProjectCreateRoutePersists(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"workspace":"C:/fixture","name":"formal project"}`))
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	rows, err := st.ListProjects(context.Background())
	if w.Code != 200 || err != nil || len(rows) != 1 {
		t.Fatalf("formal project route=%d persisted=%d error=%v", w.Code, len(rows), err)
	}
}
func TestBatchDeleteUnconfirmedReceiptPreservesRows(t *testing.T) {
	for _, state := range []pb.OperationState{pb.OperationState_OPERATION_STATE_COMPLETED, pb.OperationState_OPERATION_STATE_PENDING} {
		t.Run(state.String(), func(t *testing.T) {
			h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{deleteCb: func(*pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
				return &pb.CommandReceipt{OperationState: state}, nil
			}})
			defer cleanup()
			seedProjectSession(t, h, " a ", " s ", "source", "p")
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/batch-delete", strings.NewReader(`{"sessions":[{"agent_id":" a ","id":" s "}]}`))
			h.handleBatchDeleteSessions(w, r)
			var result struct {
				Deleted []map[string]string `json:"deleted"`
				Failed  []map[string]string `json:"failed"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			row, err := st.GetSession(context.Background(), " a ", " s ")
			if err != nil || row == nil || len(result.Deleted) != 0 || len(result.Failed) != 1 || result.Failed[0]["id"] != " s " {
				t.Fatalf("unconfirmed result=%s retained=%v err=%v", w.Body.String(), row != nil, err)
			}
		})
	}
}
func TestProjectDeleteRejectsAmbiguousSourceBeforeDeletingSessions(t *testing.T) {
	calls := 0
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{deleteCb: func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
		calls++
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Session: r.Session, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}})
	defer cleanup()
	ctx := context.Background()
	for _, source := range []string{"one", "two"} {
		if _, err := st.UpsertProject(ctx, map[string]any{"source_id": source, "project_id": "same", "workspace": "C:/fixture", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	seedProjectSession(t, h, "a", "s", "one", "same")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/delete", strings.NewReader(`{"project_id":"same","session_keys":[{"agent_id":"a","id":"s"}]}`))
	h.handleDeleteProject(w, r)
	projects, err := st.ListProjects(ctx)
	if err != nil || w.Code != 409 || len(projects) != 2 || calls != 0 {
		t.Fatalf("ambiguous delete code=%d projects=%d calls=%d error=%v", w.Code, len(projects), calls, err)
	}
}
func TestProjectDeleteRetainsProjectWhenNativeDeleteFails(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{deleteCb: func(*pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Rejected{Rejected: &pb.BusinessError{Message: "native busy"}}}, nil
	}})
	defer cleanup()
	ctx := context.Background()
	project, err := st.UpsertProject(ctx, map[string]any{"source_id": "one", "project_id": "p", "workspace": "C:/fixture", "updated_at": "2026-10-09T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	seedProjectSession(t, h, "a", "s", "one", "p")
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/delete", strings.NewReader(`{"source_id":"one","project_id":"p","session_keys":[{"agent_id":"a","id":"s"}]}`))
	h.handleDeleteProject(w, r)
	retained, err := st.GetProject(ctx, project["id"].(string))
	if err != nil || retained == nil || strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("project removed after failed native delete: code=%d body=%s retained=%v error=%v", w.Code, w.Body.String(), retained != nil, err)
	}
}
