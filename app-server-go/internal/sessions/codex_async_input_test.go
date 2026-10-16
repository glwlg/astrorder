package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodexAsyncAnswerUsesOrdinaryHTTPCommand(t *testing.T) {
	for _, state := range []string{"idle", "running"} {
		t.Run(state, func(t *testing.T) {
			calls := 0
			srv := &mockDaemonServer{sendCb: func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
				calls++
				if r.Meta.OperationId != "async-answer:question" || len(r.Input) != 1 || r.Input[0].GetText().GetText() != "时间旅行" {
					t.Fatalf("lost ordinary answer: %v", r)
				}
				return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
			}}
			h, st, _, cleanup := setupTestServer(t, srv)
			defer cleanup()
			ctx := context.Background()
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": state, "control_state": "owned", "updated_at": "2026-10-16T05:53:24Z"}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(`{"id":"async-answer:question","agent_id":"a","session_id":"s","action":"send","text":"时间旅行","attachment_ids":[],"target_id":"question"}`))
				req.Header.Set("Authorization", "Bearer browser-sec")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != 200 {
					t.Fatalf("answer route %d %s", w.Code, w.Body.String())
				}
			}
			receipt, err := st.GetCommand(ctx, "a", "s", "async-answer:question")
			if err != nil || receipt["target_id"] != "question" || calls != 1 {
				t.Fatalf("lost receipt or duplicate dispatch: %v %v calls=%d", receipt, err, calls)
			}
		})
	}
}

func TestCodexAsyncQuestionsSurviveNativeHistoryProjection(t *testing.T) {
	for _, kind := range []string{"codex", "codex-ssh"} {
		questions := []any{map[string]any{"title": "喜欢什么？", "options": []any{"时间旅行", "人工智能"}}}
		rows := projectHistoryItem(map[string]any{"turnId": "turn", "item": map[string]any{"id": "question", "type": "agentMessage", "text": "喜欢什么？", "delivery": "async", "questions": questions}}, "a", "s", "s", "a", kind)
		if len(rows) != 1 || rows[0]["delivery"] != "async" || rows[0]["questions"] == nil {
			t.Fatalf("%s lost async question: %#v", kind, rows)
		}
	}
}
