package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestViewingDormantCodexNeverAttachesOrClearsSleep(t *testing.T) {
	for _, code := range []codes.Code{codes.NotFound, codes.Unavailable, codes.FailedPrecondition} {
		t.Run(code.String(), func(t *testing.T) {
			attaches, sends := 0, 0
			srv := &mockDaemonServer{
				readModelCb: func(r *pb.ReadModelRequest) (*pb.ModelState, error) {
					if r.Session.SessionId != "native" {
						t.Error("read used display identity")
					}
					message := "session is asleep"
					if code == codes.FailedPrecondition {
						message = "thread already has an active writer"
					}
					return nil, status.Error(code, message)
				},
				attachCb: func(*pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
					attaches++
					return nil, status.Error(codes.Internal, "must not attach")
				},
				sendCb: func(*pb.SendRequest) (*pb.CommandReceipt, error) {
					sends++
					return nil, status.Error(codes.Internal, "must not send")
				},
				queryCb: func(r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
					var body any
					switch q := r.Query.(type) {
					case *pb.NativeQueryRequest_CodexThreadItemsList:
						if q.CodexThreadItemsList.ThreadId != "native" {
							t.Error("wrong history identity")
						}
						body = map[string]any{"data": []any{codexItem("agentMessage", "answer", "已完成")}, "nextCursor": nil}
					case *pb.NativeQueryRequest_CodexThreadRead:
						if q.CodexThreadRead.ThreadId != "native" {
							t.Error("wrong settings identity")
						}
						body = map[string]any{"thread": map[string]any{"id": "native", "model": "test-model", "modelProvider": "provider"}}
					default:
						t.Fatalf("unexpected native query: %T", q)
					}
					raw, _ := json.Marshal(body)
					return &pb.NativeQueryResponse{Result: &pb.NativeJson{Utf8Json: raw}}, nil
				},
			}
			h, st, _, cleanup := setupTestServer(t, srv)
			defer cleanup()
			ctx := context.Background()
			seedRefreshAgent(t, st, "a", "codex", "")
			seedRefreshSession(t, st, "a", "display", map[string]any{"source_session_id": "native", "status": "idle", "selected_approval_mode": "full_access"})
			row, _ := st.GetSession(ctx, "a", "display")
			if _, err := st.SetSessionApprovalModeBinding(ctx, "a", "display", "full_access"); err != nil {
				t.Fatal(err)
			}
			if err := st.SetPreference(ctx, sleepPreferenceKey("a", "display"), true, true); err != nil {
				t.Fatal(err)
			}
			for _, suffix := range []string{"view-lease", "messages", "model", "approval-mode"} {
				method := http.MethodGet
				if suffix == "view-lease" {
					method = http.MethodPost
				}
				req := httptest.NewRequest(method, "/api/v1/sessions/display/"+suffix+"?agent_id=a", nil)
				req.Header.Set("Authorization", "Bearer browser-sec")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != 200 {
					t.Fatalf("%s: %d %s", suffix, w.Code, w.Body.String())
				}
			}
			if attaches != 0 || sends != 0 {
				t.Fatalf("viewing caused activation: attach=%d send=%d", attaches, sends)
			}
			pref, err := st.GetPreference(ctx, sleepPreferenceKey("a", "display"))
			if err != nil || pref.Value != true {
				t.Fatal("viewing cleared sleep state", pref, err)
			}
			after, _ := st.GetSession(ctx, "a", "display")
			if after["updated_at"] != row["updated_at"] || after["status"] != row["status"] {
				t.Fatal("viewing changed session activity")
			}
		})
	}
}
