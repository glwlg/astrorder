package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var commandSendCalls int

func (m *mockDaemonServer) Send(ctx context.Context, r *pb.SendRequest) (*pb.CommandReceipt, error) {
	commandSendCalls++
	if r.Meta == nil || r.Meta.GetCommandId() == "" || r.Meta.OperationId != r.Meta.GetCommandId() || r.Meta.Canonicalization != pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
		panic("invalid durable metadata")
	}
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Session: r.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
}
func TestCommandSubmissionIsIdempotentWithoutFabricatedCompletion(t *testing.T) {
	commandSendCalls = 0
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "title": "test", "status": "idle", "control_state": "owned", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(`{"id":"exact command","agent_id":"a","session_id":"s","action":"send","text":"fixture"}`))
		r.Header.Set("Authorization", "Bearer browser-sec")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("command route: %d %s", w.Code, w.Body.String())
		}
	}
	command, err := st.GetCommand(ctx, "a", "s", "exact command")
	if err != nil || commandSendCalls != 1 || command["state"] != "accepted" {
		t.Fatalf("duplicate or fake completion: calls=%d cmd=%+v err=%v", commandSendCalls, command, err)
	}
}

func postSend(t *testing.T, h http.Handler, id, agentID, text string) {
	t.Helper()
	body := `{"id":"` + id + `","agent_id":"` + agentID + `","session_id":"s-` + agentID + `","action":"send","text":"` + text + `"}`
	r := httptest.NewRequest(http.MethodPost, "/api/v1/commands", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("command route: %d %s", w.Code, w.Body.String())
	}
}

// Grok ACP never echoes the user prompt, so the accepted prompt must be stored.
// Codex and Hermes echo natively and must not get a duplicate local row.
func TestAcceptedSendPersistsUserMessageOnlyForGrok(t *testing.T) {
	h, st, _, cleanup := setupTestServer(t, &mockDaemonServer{})
	defer cleanup()
	ctx := context.Background()
	for _, kind := range []string{"grok", "codex"} {
		if _, err := st.UpsertAgent(ctx, map[string]any{"id": kind, "kind": kind, "name": kind, "status": "ready"}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertSession(ctx, map[string]any{"id": "s-" + kind, "agent_id": kind, "title": "t", "status": "idle", "control_state": "owned", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		postSend(t, h, "cmd-"+kind, kind, "hello "+kind)
	}
	row, err := st.GetMessage(ctx, "grok", "s-grok", "user-cmd-grok")
	if err != nil || row == nil || row["role"] != "user" || row["text"] != "hello grok" || row["command_id"] != "cmd-grok" {
		t.Fatalf("grok user message = %+v err=%v", row, err)
	}
	row, err = st.GetMessage(ctx, "codex", "s-codex", "user-cmd-codex")
	if err != nil || row != nil {
		t.Fatalf("codex must not get a local user row: %+v err=%v", row, err)
	}
}
