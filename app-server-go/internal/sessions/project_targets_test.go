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

func TestProjectDeleteResolvesScopedSessionsAndReportsPartialFailure(t *testing.T) {
	ctx := context.Background()
	for _, failSecond := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial"}[failSecond], func(t *testing.T) {
			calls := map[string]int{}
			h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{deleteCb: func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
				calls[r.Session.SessionId]++
				if failSecond && r.Session.SessionId == "second" {
					return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_PENDING}, nil
				}
				return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Session: r.Session, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
			}})
			defer cleanup()
			project, err := st.UpsertProject(ctx, map[string]any{"source_id": "one", "project_id": "p", "workspace": "C:/fixture", "updated_at": "2026-10-09T00:00:00Z"})
			if err != nil {
				t.Fatal(err)
			}
			seedProjectSession(t, h, "a", "first", "one", "p")
			seedProjectSession(t, h, "a", "second", "one", "p")
			seedProjectSession(t, h, "b", "foreign", "two", "p")
			r := httptest.NewRequest(http.MethodPost, "/api/v1/projects/delete", strings.NewReader(`{"source_id":"one","project_id":"p","session_keys":[{"agent_id":"a","id":"first"},{"agent_id":"a","id":"first"}]}`))
			r.Header.Set("Authorization", "Bearer browser-sec")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var result struct {
				OK      bool                `json:"ok"`
				Deleted []map[string]string `json:"deleted_sessions"`
				Failed  []map[string]string `json:"failed"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			retained, err := st.GetProject(ctx, project["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := st.GetSession(ctx, "b", "foreign")
			if err != nil || foreign == nil || calls["foreign"] != 0 || calls["first"] != 1 || calls["second"] != 1 {
				t.Fatalf("scope or dedupe failed calls=%v err=%v", calls, err)
			}
			if failSecond {
				if w.Code != 502 || result.OK || len(result.Deleted) != 1 || len(result.Failed) != 1 || retained == nil {
					t.Fatalf("partial result=%s project retained=%v", w.Body.String(), retained != nil)
				}
			} else if w.Code != 200 || !result.OK || len(result.Deleted) != 2 || retained != nil {
				t.Fatalf("success result=%s retained=%v", w.Body.String(), retained != nil)
			}
		})
	}
}
