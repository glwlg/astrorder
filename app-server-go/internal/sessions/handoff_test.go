package sessions

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

func handoffPost(h *Handler, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandoffValidationBeforeNativeMutation(t *testing.T) {
	for _, tc := range []struct {
		name, kind, connection, state string
		code                          int
	}{
		{"cross environment", "codex", "other", "idle", 422},
		{"busy source", "codex", "", "running", 409},
		{"no native fork", "grok", "", "idle", 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h, st, _, close := setupTestServer(t, &mockDaemonServer{createCb: func(*pb.CreateSessionRequest) (*pb.CommandReceipt, error) { calls++; return nil, nil }})
			defer close()
			ctx := context.Background()
			st.UpsertAgent(ctx, map[string]any{"id": "source", "kind": tc.kind, "name": "source", "status": "ready"})
			st.UpsertAgent(ctx, map[string]any{"id": "target", "kind": "hermes", "name": "target", "status": "ready", "connection_id": tc.connection})
			st.UpsertSession(ctx, map[string]any{"id": "parent", "agent_id": "source", "title": "source", "workspace": "C:/work", "status": tc.state, "updated_at": "2026-10-17T00:00:00Z"})
			w := handoffPost(h, "/api/v1/sessions/parent/handoff", `{"operation_id":"test","source_agent_id":"source","target_agent_id":"target","provider":"p","model":"m","effort":"high"}`)
			if w.Code != tc.code || calls != 0 {
				t.Fatalf("%d %s calls=%d", w.Code, w.Body.String(), calls)
			}
		})
	}
}

func TestHandoffLocalNativeSummaryAndFirstSend(t *testing.T) {
	for _, connection := range []string{"", "ssh-test"} {
		t.Run("environment="+connection, func(t *testing.T) { testHandoffNativeSummaryAndFirstSend(t, connection) })
	}
}

func testHandoffNativeSummaryAndFirstSend(t *testing.T, connection string) {
	creates, sends, closes := 0, 0, 0
	srv := &mockDaemonServer{}
	srv.statusCb = func() (*pb.GetStatusResponse, error) {
		owned, runtime := true, pb.RuntimeKind_RUNTIME_KIND_HERMES
		if connection != "" {
			runtime = pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES
		}
		return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{{Session: buildSessionRef("source", "native-parent", connection), Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &runtime}, {Session: buildSessionRef("source", "summary", connection), Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &runtime}}}, nil
	}
	srv.readModelCb = func(r *pb.ReadModelRequest) (*pb.ModelState, error) {
		return &pb.ModelState{Provider: optString("p"), Model: optString("m"), Effort: optString("high")}, nil
	}
	srv.createCb = func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		creates++
		if r.Target.GetConnectionId() != connection || connection != "" && (r.Target.Ssh == nil || r.Target.Ssh.Host != "test.invalid") {
			t.Fatalf("lost SSH routing: %v", r.Target)
		}
		sid := "summary"
		if creates == 1 {
			if r.GetHermes().GetParentSessionId() != "native-parent" {
				t.Fatalf("fork lost parent: %v", r)
			}
		} else {
			sid = "target-native"
			if r.GetCodex().GetModel() != "m" || r.GetCodex().GetParentSessionId() != "" {
				t.Fatalf("target options: %v", r)
			}
		}
		return &pb.CommandReceipt{Session: buildSessionRef(r.Target.GetAgentId(), sid, r.Target.GetConnectionId()), OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}
	srv.getHistoryPageCb = func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
		items := []*pb.NativeJson{{Schema: "hermes.message.v1", Utf8Json: []byte(`{"row_id":1,"role":"assistant","content":"old answer"}`)}}
		if sends > 0 {
			items = append(items, &pb.NativeJson{Schema: "hermes.message.v1", Utf8Json: []byte(`{"row_id":2,"role":"assistant","content":"complete native summary"}`)})
		}
		return &pb.HistoryPageResponse{Session: r.Session, Items: items}, nil
	}
	srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
		sends++
		if sends == 1 {
			if r.Session.SessionId != "summary" || !strings.Contains(r.Input[0].GetText().Text, "完整会话上下文") {
				t.Fatalf("summary send: %v", r)
			}
		} else if sends == 2 {
			if !strings.Contains(r.Input[0].GetText().Text, "complete native summary") || !strings.HasSuffix(r.Input[0].GetText().Text, "continue") {
				t.Fatalf("first send lost context: %v", r)
			}
		} else if strings.Contains(r.Input[0].GetText().Text, "complete native summary") {
			t.Fatal("context resent")
		}
		return &pb.CommandReceipt{Session: r.Session, Accepted: true, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Status: pb.SessionStatus_SESSION_STATUS_RUNNING, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}
	srv.eventsCb = func(r *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		native, _ := encodeNativeJSON("fixture", map[string]any{"command_id": "handoff-once:summary-send", "status": "complete"})
		return stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Replay{Replay: &pb.EventEnvelope{DaemonId: "test-daemon", Session: r.Cursors[0].Session, EventType: "hermes.command_complete", NativePayload: native}}})
	}
	srv.closeCb = func(r *pb.CloseSessionRequest) (*pb.CommandReceipt, error) {
		closes++
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if connection != "" {
		if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": connection, "host": "test.invalid", "workspace": "/work", "state": "ready"}); err != nil {
			t.Fatal(err)
		}
	}
	for id, kind := range map[string]string{"source": "hermes", "target": "codex"} {
		st.UpsertAgent(ctx, map[string]any{"id": id, "kind": kind, "name": id, "status": "ready", "connection_id": connection})
	}
	st.UpsertSession(ctx, map[string]any{"id": "parent", "source_session_id": "native-parent", "agent_id": "source", "title": "work", "workspace": "/work", "connection_id": connection, "status": "idle", "updated_at": "2026-10-17T00:00:00Z"})
	body := `{"operation_id":"handoff-once","source_agent_id":"source","target_agent_id":"target","provider":"p","model":"m","effort":"high"}`
	for i := 0; i < 2; i++ {
		w := handoffPost(h, "/api/v1/sessions/parent/handoff", body)
		if w.Code != 200 {
			t.Fatalf("handoff: %d %s", w.Code, w.Body.String())
		}
	}
	if creates != 2 || sends != 1 || closes != 1 {
		t.Fatalf("duplicate handoff: creates=%d sends=%d", creates, sends)
	}
	row, _ := st.GetSession(ctx, "target", "target-native")
	h.bgTasks = newBackgroundTaskManager()
	if w := handoffPost(h, "/api/v1/sessions/parent/handoff", body); w.Code != 200 || creates != 2 {
		t.Fatal("completed retry after restart failed", w.Code)
	}
	if strings.Contains(stringValue(row, "handoff_context"), "complete native summary") {
		t.Fatal("summary duplicated in App storage")
	}
	for i := 0; i < 2; i++ {
		raw, _ := json.Marshal(map[string]any{"id": string(rune('a' + i)), "agent_id": "target", "session_id": "target-native", "action": "send", "text": "continue"})
		w := handoffPost(h, "/api/v1/commands", string(raw))
		if w.Code != 200 {
			t.Fatalf("send: %d %s", w.Code, w.Body.String())
		}
	}
	entered := make(chan struct{})
	srv.eventsCb = func(r *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		close(entered)
		<-stream.Context().Done()
		return stream.Context().Err()
	}
	srv.createCb = func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		creates++
		return &pb.CommandReceipt{Session: buildSessionRef("source", "cancel-summary", connection), OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Status: pb.SessionStatus_SESSION_STATUS_IDLE, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}
	srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
		sends++
		return &pb.CommandReceipt{Session: r.Session, Accepted: true, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
	}
	interrupts := 0
	srv.interruptCb = func(r *pb.InterruptRequest) (*pb.CommandReceipt, error) {
		interrupts++
		return &pb.CommandReceipt{}, nil
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- handoffPost(h, "/api/v1/sessions/parent/handoff", strings.ReplaceAll(body, "handoff-once", "cancel-operation"))
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("summary did not reach completion wait")
	}
	if w := handoffPost(h, "/api/v1/handoffs/cancel-operation/cancel", `{}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"cancelled":true`) {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	select {
	case w := <-done:
		if w.Code != 409 {
			t.Fatalf("cancel outcome: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not finish")
	}
	if creates != 3 || closes != 3 || interrupts != 1 {
		t.Fatalf("cancel cleanup: creates=%d closes=%d interrupts=%d", creates, closes, interrupts)
	}
}
