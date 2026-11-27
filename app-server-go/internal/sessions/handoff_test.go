package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	creates, sends, closes, deletes := 0, 0, 0, 0
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
			if raw := r.GetCodex().GetNativeOptions(); raw != nil {
				var nativeOpts map[string]any
				if err := json.Unmarshal(raw.GetUtf8Json(), &nativeOpts); err != nil {
					t.Fatalf("decode codex NativeOptions: %v", err)
				}
				for _, reserved := range []string{"cwd", "parent_session_id", "model", "modelProvider", "effort"} {
					if _, exists := nativeOpts[reserved]; exists {
						t.Fatalf("codex NativeOptions leaked reserved key %q: %+v", reserved, nativeOpts)
					}
				}
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
	srv.deleteCb = func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
		deletes++
		if r.GetSession().GetSessionId() != "summary" {
			t.Fatalf("unexpected delete session target: %+v", r.GetSession())
		}
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
	if creates != 2 || sends != 1 || deletes != 1 || closes != 0 {
		t.Fatalf("duplicate handoff or missing fork delete: creates=%d sends=%d deletes=%d closes=%d", creates, sends, deletes, closes)
	}
	if forkRow, err := st.GetSession(ctx, "source", "summary"); err != nil || forkRow != nil {
		t.Fatalf("expected completed handoff summary fork deleted from app storage, got row=%+v err=%v", forkRow, err)
	}
	row, _ := st.GetSession(ctx, "target", "target-native")
	h.bgTasks = newBackgroundTaskManager()
	if w := handoffPost(h, "/api/v1/sessions/parent/handoff", body); w.Code != 200 || creates != 2 {
		t.Fatal("completed retry after restart failed", w.Code)
	}
	if !strings.Contains(stringValue(row, "handoff_context"), "complete native summary") || strings.HasPrefix(stringValue(row, "handoff_context"), handoffRefPrefix) {
		t.Fatalf("expected inline handoff summary stored on target session, got %q", stringValue(row, "handoff_context"))
	}
	previewW := handoffGet(h, "/api/v1/sessions/target-native/handoff-context?agent_id=target")
	if previewW.Code != 200 || !strings.Contains(previewW.Body.String(), `"mode":"native_summary"`) || !strings.Contains(previewW.Body.String(), "complete native summary") {
		t.Fatalf("expected inline summary preview readable after fork deletion, got %d %s", previewW.Code, previewW.Body.String())
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
	if creates != 3 || deletes != 1 || closes != 1 || interrupts != 1 {
		t.Fatalf("cancel cleanup: creates=%d deletes=%d closes=%d interrupts=%d", creates, deletes, closes, interrupts)
	}
}

type handoffProbeDaemon struct {
	*mockDaemonServer
	listModelsCb func(*pb.ListModelsRequest) (*pb.ListModelsResponse, error)
}

func (d *handoffProbeDaemon) ListModels(_ context.Context, req *pb.ListModelsRequest) (*pb.ListModelsResponse, error) {
	return d.listModelsCb(req)
}

func TestHandoffCodexToAntigravityAndReverse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		connection string
		agyKind    string
	}{
		{"local", "", "antigravity"},
		{"ssh", "ssh-test", "antigravity-ssh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection := tc.connection
			for _, requestedEffort := range []string{"high", ""} {
				opID := "codex-to-agy-" + tc.name + "-" + requestedEffort
				t.Run("codex_to_agy_effort="+requestedEffort, func(t *testing.T) {
					var (
						creates []*pb.CreateSessionRequest
						sends   []*pb.SendRequest
					)
					srv := &mockDaemonServer{}
					var subCalls atomic.Int32
					srv.statusCb = func() (*pb.GetStatusResponse, error) {
						owned, runtime := true, pb.RuntimeKind_RUNTIME_KIND_CODEX
						if connection != "" {
							runtime = pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
						}
						snaps := []*pb.SessionSnapshot{
							{Session: buildSessionRef("codex-agent", "native-codex", connection), Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &runtime},
						}
						for i := 0; i < 350; i++ {
							snaps = append(snaps, &pb.SessionSnapshot{
								Session:            &pb.SessionRef{SessionId: fmt.Sprintf("bg-session-%03d", i)},
								Status:             pb.SessionStatus_SESSION_STATUS_IDLE,
								MinSeqId:           1,
								ReplayThroughSeqId: 50000,
							})
						}
						return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: snaps}, nil
					}
					srv.readModelCb = func(req *pb.ReadModelRequest) (*pb.ModelState, error) {
						if req.GetSession().GetAgentId() == "agy-agent" {
							return &pb.ModelState{Provider: optString("antigravity"), Model: optString("gemini-4-carbon-medium")}, nil
						}
						return &pb.ModelState{Provider: optString("magpie"), Model: optString("group/pro"), Effort: optString("xhigh")}, nil
					}
					srv.createCb = func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
						creates = append(creates, r)
						if codexOpts := r.GetCodex(); codexOpts != nil {
							if raw := codexOpts.GetNativeOptions(); raw != nil {
								var nativeOpts map[string]any
								if err := json.Unmarshal(raw.GetUtf8Json(), &nativeOpts); err != nil {
									t.Fatalf("decode codex NativeOptions: %v", err)
								}
								if _, exists := nativeOpts["cwd"]; exists {
									t.Fatalf("codex NativeOptions unexpectedly contained cwd: %+v", nativeOpts)
								}
							}
							if codexOpts.GetParentSessionId() != "native-codex" || codexOpts.GetEffort() != "xhigh" {
								t.Fatalf("unexpected codex summary fork options: %+v", codexOpts)
							}
							return &pb.CommandReceipt{
								Session:        buildSessionRef("codex-agent", "codex-summary", connection),
								OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
								Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
								Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
							}, nil
						}
						agyOpts := r.GetAntigravity()
						if agyOpts == nil {
							t.Fatalf("expected AntigravityCreateOptions, got: %+v", r)
						}
						if agyOpts.GetModel() != "gemini-4-carbon-medium" || agyOpts.Effort != nil {
							t.Fatalf("unexpected Antigravity create options (effort must be omitted): %+v", agyOpts)
						}
						return &pb.CommandReceipt{
							Session:        buildSessionRef("agy-agent", "agy-target", connection),
							OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
							Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
							Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
						}, nil
					}
					srv.getHistoryPageCb = func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
						items := []*pb.NativeJson{{Schema: "codex.item.v1", Utf8Json: []byte(`{"id":"old","role":"assistant","text":"prior turn"}`)}}
						if len(sends) > 0 {
							items = append(items, &pb.NativeJson{Schema: "codex.item.v1", Utf8Json: []byte(`{"id":"sum-1","role":"assistant","text":"codex handoff summary"}`)})
						}
						return &pb.HistoryPageResponse{Session: r.Session, Items: items}, nil
					}
					srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
						sends = append(sends, r)
						return &pb.CommandReceipt{
							Session:        r.Session,
							Accepted:       true,
							TurnId:         optString("turn-1"),
							OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
							Status:         pb.SessionStatus_SESSION_STATUS_RUNNING,
							Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
						}, nil
					}
					srv.eventsCb = func(r *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
						call := subCalls.Add(1)
						if len(r.Cursors) < 351 || r.Cursors[0].GetSession().GetSessionId() != "codex-summary" {
							t.Fatalf("expected >= 351 cursors with summary first, got len=%d first=%+v", len(r.Cursors), r.Cursors)
						}
						if call == 1 {
							if r.Cursors[0].GetAfterSeqId() != 0 || r.Cursors[1].GetAfterSeqId() != 50000 {
								t.Fatalf("unexpected initial cursors: summary=%d other=%d", r.Cursors[0].GetAfterSeqId(), r.Cursors[1].GetAfterSeqId())
							}
							progressNative, _ := encodeNativeJSON("astrorder.event.payload.v1", map[string]any{
								"agent_id": "codex-agent",
								"frame":    map[string]any{"method": "turn/started"},
							})
							// Match real daemon EventStream envelopes: AgentId and ConnectionId are nil on SessionRef.
							if err := stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Replay{Replay: &pb.EventEnvelope{
								DaemonId:      "test-daemon",
								Session:       &pb.SessionRef{SessionId: "codex-summary"},
								SeqId:         1,
								EventType:     "codex.notification",
								NativePayload: progressNative,
							}}}); err != nil {
								return err
							}
							return stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Overflow{Overflow: &pb.StreamOverflow{
								QueueCapacity: 64,
								LastEmitted: []*pb.SessionCursor{{
									Session:    &pb.SessionRef{SessionId: "codex-summary"},
									AfterSeqId: 1,
								}},
							}}})
						}
						if r.Cursors[0].GetAfterSeqId() != 1 {
							t.Fatalf("expected reconnect cursor at seq 1, got %d", r.Cursors[0].GetAfterSeqId())
						}
						native, _ := encodeNativeJSON("astrorder.event.payload.v1", map[string]any{
							"agent_id": "codex-agent",
							"frame": map[string]any{
								"method": "turn/completed",
								"params": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "completed"}},
							},
						})
						return stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Live{Live: &pb.EventEnvelope{
							DaemonId:      "test-daemon",
							Session:       &pb.SessionRef{SessionId: "codex-summary"},
							SeqId:         2,
							EventType:     "codex.notification",
							NativePayload: native,
						}}})
					}
					srv.closeCb = func(r *pb.CloseSessionRequest) (*pb.CommandReceipt, error) {
						return &pb.CommandReceipt{
							OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
							Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
						}, nil
					}
					srv.deleteCb = func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
						return &pb.CommandReceipt{
							OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
							Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
						}, nil
					}

					h, st, _, cleanup := setupTestServer(t, srv)
					defer cleanup()
					ctx := context.Background()
					if connection != "" {
						if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": connection, "host": "test.invalid", "workspace": "/work", "state": "ready"}); err != nil {
							t.Fatal(err)
						}
					}
					st.UpsertAgent(ctx, map[string]any{"id": "codex-agent", "kind": "codex", "name": "Codex", "status": "ready", "connection_id": connection})
					st.UpsertAgent(ctx, map[string]any{"id": "agy-agent", "kind": tc.agyKind, "name": "Antigravity", "status": "ready", "connection_id": connection})
					st.UpsertSession(ctx, map[string]any{"id": "codex-parent", "source_session_id": "native-codex", "agent_id": "codex-agent", "title": "work", "workspace": "/work", "connection_id": connection, "status": "idle", "updated_at": "2026-10-17T00:00:00Z"})

					reqBody, _ := json.Marshal(map[string]any{
						"operation_id":    opID,
						"source_agent_id": "codex-agent",
						"target_agent_id": "agy-agent",
						"provider":        "antigravity",
						"model":           "gemini-4-carbon-medium",
						"effort":          requestedEffort,
					})
					w := handoffPost(h, "/api/v1/sessions/codex-parent/handoff", string(reqBody))
					if w.Code != 200 {
						t.Fatalf("Codex -> Antigravity handoff failed: %d %s", w.Code, w.Body.String())
					}
					if len(creates) != 2 {
						t.Fatalf("expected 2 native creates, got %d", len(creates))
					}
					if forkRow, err := st.GetSession(ctx, "codex-agent", "codex-summary"); err != nil || forkRow != nil {
						t.Fatalf("expected codex handoff summary fork removed after completion, got %+v %v", forkRow, err)
					}
					row, err := st.GetSession(ctx, "agy-agent", "agy-target")
					if err != nil || row == nil {
						t.Fatalf("missing target session: %v", err)
					}
					if row["selected_model_provider"] != "antigravity" || row["selected_model"] != "gemini-4-carbon-medium" {
						t.Fatalf("unexpected model binding: %+v", row)
					}
					if eff, _ := row["selected_reasoning_effort"].(string); eff != "" {
						t.Fatalf("expected empty selected_reasoning_effort for Antigravity target, got %q", eff)
					}

					// Even if a legacy Antigravity binding carries an effort, send skips SetReasoning.
					if err := st.WithTx(ctx, func(tx *sql.Tx) error {
						_, err := tx.ExecContext(ctx, `UPDATE sessions SET selected_reasoning_effort='high' WHERE agent_id=? AND id=?`, "agy-agent", "agy-target")
						return err
					}); err != nil {
						t.Fatal(err)
					}
					sendW := handoffPost(h, "/api/v1/commands", `{"id":"send-agy-1","agent_id":"agy-agent","session_id":"agy-target","action":"send","text":"continue"}`)
					if sendW.Code != 200 {
						t.Fatalf("first send to handed-off Antigravity failed: %d %s", sendW.Code, sendW.Body.String())
					}
					if len(sends) != 2 || !strings.Contains(sends[1].Input[0].GetText().Text, "codex handoff summary") {
						t.Fatalf("expected handoff summary in Antigravity first send, got %+v", sends)
					}
				})
			}

			t.Run("agy_to_codex", func(t *testing.T) {
				var (
					creates []*pb.CreateSessionRequest
					sends   []*pb.SendRequest
				)
				srv := &mockDaemonServer{}
				srv.statusCb = func() (*pb.GetStatusResponse, error) {
					owned, runtime := true, pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY
					if connection != "" {
						runtime = pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH
					}
					return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{
						{Session: buildSessionRef("agy-agent", "native-agy", connection), Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &runtime},
					}}, nil
				}
				srv.readModelCb = func(req *pb.ReadModelRequest) (*pb.ModelState, error) {
					if req.GetSession().GetAgentId() == "codex-agent" {
						return &pb.ModelState{Provider: optString("openai"), Model: optString("gpt-5.4"), Effort: optString("high")}, nil
					}
					return &pb.ModelState{Provider: optString("antigravity"), Model: optString("gemini-4-carbon-medium")}, nil
				}
				srv.createCb = func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
					creates = append(creates, r)
					if agyOpts := r.GetAntigravity(); agyOpts != nil {
						if agyOpts.GetParentSessionId() != "native-agy" || agyOpts.Effort != nil {
							t.Fatalf("unexpected antigravity fork options: %+v", agyOpts)
						}
						return &pb.CommandReceipt{
							Session:        buildSessionRef("agy-agent", "agy-summary", connection),
							OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
							Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
							Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
						}, nil
					}
					codexOpts := r.GetCodex()
					if codexOpts == nil || codexOpts.GetModel() != "gpt-5.4" || codexOpts.GetEffort() != "high" {
						t.Fatalf("unexpected codex target options: %+v", r)
					}
					return &pb.CommandReceipt{
						Session:        buildSessionRef("codex-agent", "codex-target", connection),
						OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
						Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
						Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
					}, nil
				}
				srv.getHistoryPageCb = func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
					items := []*pb.NativeJson{{Schema: "antigravity.message.v1", Utf8Json: []byte(`{"id":"old","role":"assistant","kind":"message","text":"prior"}`)}}
					if len(sends) > 0 {
						items = append(items, &pb.NativeJson{Schema: "antigravity.message.v1", Utf8Json: []byte(`{"id":"sum-agy","role":"assistant","kind":"message","text":"agy handoff summary"}`)})
					}
					return &pb.HistoryPageResponse{Session: r.Session, Items: items}, nil
				}
				srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
					sends = append(sends, r)
					return &pb.CommandReceipt{
						Session:        r.Session,
						Accepted:       true,
						OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
						Status:         pb.SessionStatus_SESSION_STATUS_RUNNING,
						Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
					}, nil
				}
				srv.eventsCb = func(r *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
					native, _ := encodeNativeJSON("fixture", map[string]any{
						"type": "command.upsert",
						"data": map[string]any{"id": "agy-to-codex-" + tc.name + ":summary-send", "state": "completed"},
					})
					return stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Replay{Replay: &pb.EventEnvelope{
						DaemonId:      "test-daemon",
						Session:       r.Cursors[0].Session,
						EventType:     "connector.event",
						NativePayload: native,
					}}})
				}
				srv.closeCb = func(r *pb.CloseSessionRequest) (*pb.CommandReceipt, error) {
					return &pb.CommandReceipt{
						OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
						Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
					}, nil
				}

				h, st, _, cleanup := setupTestServer(t, srv)
				defer cleanup()
				ctx := context.Background()
				if connection != "" {
					if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": connection, "host": "test.invalid", "workspace": "/work", "state": "ready"}); err != nil {
						t.Fatal(err)
					}
				}
				st.UpsertAgent(ctx, map[string]any{"id": "agy-agent", "kind": tc.agyKind, "name": "Antigravity", "status": "ready", "connection_id": connection})
				st.UpsertAgent(ctx, map[string]any{"id": "codex-agent", "kind": "codex", "name": "Codex", "status": "ready", "connection_id": connection})
				st.UpsertSession(ctx, map[string]any{"id": "agy-parent", "source_session_id": "native-agy", "agent_id": "agy-agent", "title": "agy-work", "workspace": "/work", "connection_id": connection, "status": "idle", "updated_at": "2026-10-17T00:00:00Z"})

				w := handoffPost(h, "/api/v1/sessions/agy-parent/handoff", `{"operation_id":"agy-to-codex-`+tc.name+`","source_agent_id":"agy-agent","target_agent_id":"codex-agent","provider":"openai","model":"gpt-5.4","effort":"high"}`)
				if w.Code != 200 {
					t.Fatalf("Antigravity -> Codex handoff failed: %d %s", w.Code, w.Body.String())
				}
				// Compliance: Antigravity source must NOT be forked or sent an automated summary prompt!
				if len(creates) != 1 || creates[0].GetCodex() == nil {
					t.Fatalf("expected exactly 1 target Codex create without Antigravity fork, got %+v", creates)
				}
				if len(sends) != 0 {
					t.Fatalf("expected 0 automated summary sends to Antigravity, got %d", len(sends))
				}
				sendW := handoffPost(h, "/api/v1/commands", `{"id":"send-codex-1","agent_id":"codex-agent","session_id":"codex-target","action":"send","text":"continue"}`)
				if sendW.Code != 200 {
					t.Fatalf("first send to handed-off Codex failed: %d %s", sendW.Code, sendW.Body.String())
				}
				if len(sends) != 1 || !strings.Contains(sends[0].Input[0].GetText().Text, "prior") {
					t.Fatalf("expected deterministic Antigravity transcript digest in Codex first send, got %+v", sends)
				}
			})
		})
	}
}

func TestSessionModelsProbeWithoutExistingSession(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var attachSeen *pb.AttachSessionRequest
	base := &mockDaemonServer{
		attachCb: func(req *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			attachSeen = req
			return &pb.SessionReceipt{Attached: true, Session: req.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
	}
	srv := &handoffProbeDaemon{
		mockDaemonServer: base,
		listModelsCb: func(req *pb.ListModelsRequest) (*pb.ListModelsResponse, error) {
			lbl := "Gemini 4 Carbon (Medium)"
			return &pb.ListModelsResponse{
				Items: []*pb.ModelOption{{Provider: "antigravity", Model: "gemini-4-carbon-medium", Label: &lbl}},
			}, nil
		},
	}
	grpcSrv := grpc.NewServer()
	pb.RegisterDaemonControlServer(grpcSrv, srv)
	pb.RegisterSessionControlServer(grpcSrv, srv)
	pb.RegisterRuntimeQueryServer(grpcSrv, srv)
	pb.RegisterEventStreamServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	defer func() {
		grpcSrv.Stop()
		_ = lis.Close()
	}()

	ctx := context.Background()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{Target: lis.Addr().String(), Credential: "test-secret", RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": "ssh-wsl", "host": "127.0.0.1", "workspace": "/work", "state": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "agy-empty", "kind": "antigravity-ssh", "name": "WSL AGY", "status": "ready", "connection_id": "ssh-wsl"}); err != nil {
		t.Fatal(err)
	}
	authorizer := auth.New(config.Config{BrowserSecret: "browser-sec"}, nil)
	rawHandler, err := NewHandler(Options{Store: st, Client: client, Authorizer: authorizer, Publisher: &mockPublisher{}})
	if err != nil {
		t.Fatal(err)
	}
	h := rawHandler.(*Handler)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/__probe__/models?agent_id=agy-empty", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("expected 200 for __probe__ models, got %d: %s", w.Code, w.Body.String())
	}
	if attachSeen == nil || !attachSeen.GetRuntimeControl() || attachSeen.GetSession().GetSessionId() != "probe-antigravity-ssh-wsl" {
		t.Fatalf("unexpected probe attach request: %+v", attachSeen)
	}
	if !strings.Contains(w.Body.String(), "gemini-4-carbon-medium") {
		t.Fatalf("expected Antigravity model in response, got %s", w.Body.String())
	}

	req404 := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/nonexistent/models?agent_id=agy-empty", nil)
	req404.Header.Set("Authorization", "Bearer browser-sec")
	w404 := httptest.NewRecorder()
	h.ServeHTTP(w404, req404)
	if w404.Code != 404 {
		t.Fatalf("expected 404 for unknown non-probe session, got %d: %s", w404.Code, w404.Body.String())
	}
}

type antigravityReattachDaemon struct {
	*mockDaemonServer
	readApprovalCb func(*pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error)
}

func (d *antigravityReattachDaemon) ReadApprovalMode(_ context.Context, req *pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error) {
	return d.readApprovalCb(req)
}

func TestAntigravityReattachPermissionAndWorkerModelRestore(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		attached          bool
		attachSeen        *pb.AttachSessionRequest
		sendSeen          *pb.SendRequest
		readApprovalCount int
	)
	base := &mockDaemonServer{
		statusCb: func() (*pb.GetStatusResponse, error) {
			return &pb.GetStatusResponse{DaemonId: "test-daemon"}, nil
		},
		readModelCb: func(req *pb.ReadModelRequest) (*pb.ModelState, error) {
			if !attached {
				return nil, status.Error(codes.NotFound, "not owned")
			}
			// Simulate Orchestrator worker sub-model reported after turn execution.
			return &pb.ModelState{Provider: optString("antigravity"), Model: optString("gemini-flash-worker-agy")}, nil
		},
		attachCb: func(req *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			attached = true
			attachSeen = req
			return &pb.SessionReceipt{Attached: true, Session: req.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
		sendCb: func(req *pb.SendRequest) (*pb.CommandReceipt, error) {
			sendSeen = req
			return &pb.CommandReceipt{
				Session:        req.Session,
				Accepted:       true,
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Status:         pb.SessionStatus_SESSION_STATUS_RUNNING,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
			}, nil
		},
	}
	srv := &antigravityReattachDaemon{
		mockDaemonServer: base,
		readApprovalCb: func(req *pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error) {
			readApprovalCount++
			return &pb.ApprovalModeState{Session: req.Session, Mode: "full_access"}, nil
		},
	}
	grpcSrv := grpc.NewServer()
	pb.RegisterDaemonControlServer(grpcSrv, srv)
	pb.RegisterSessionControlServer(grpcSrv, srv)
	pb.RegisterRuntimeQueryServer(grpcSrv, srv)
	pb.RegisterEventStreamServer(grpcSrv, srv)
	go func() { _ = grpcSrv.Serve(lis) }()
	defer func() {
		grpcSrv.Stop()
		_ = lis.Close()
	}()

	ctx := context.Background()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{Target: lis.Addr().String(), Credential: "test-secret", RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": "ssh-wsl", "host": "127.0.0.1", "workspace": "/work", "state": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "agy-agent", "kind": "antigravity-ssh", "name": "WSL AGY", "status": "ready", "connection_id": "ssh-wsl"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{
		"id":                      "agy-sess-1",
		"source_session_id":       "agy-sess-1",
		"agent_id":                "agy-agent",
		"title":                   "Orchestrator Session",
		"workspace":               "/work",
		"connection_id":           "ssh-wsl",
		"status":                  "idle",
		"selected_model_provider": "antigravity",
		"selected_model":          "gemini-pro-orchestrator",
		"selected_approval_mode":  "full_access",
		"updated_at":              "2026-11-18T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	authorizer := auth.New(config.Config{BrowserSecret: "browser-sec"}, nil)
	rawHandler, err := NewHandler(Options{Store: st, Client: client, Authorizer: authorizer, Publisher: &mockPublisher{}})
	if err != nil {
		t.Fatal(err)
	}
	h := rawHandler.(*Handler)

	w := handoffPost(h, "/api/v1/commands", `{"id":"cmd-worker-send","agent_id":"agy-agent","session_id":"agy-sess-1","action":"send","text":"hello again"}`)
	if w.Code != 200 {
		t.Fatalf("expected 200 sending to Antigravity Orchestrator session, got %d: %s", w.Code, w.Body.String())
	}
	if attachSeen == nil || attachSeen.GetNativeOptions() == nil {
		t.Fatalf("expected AttachSession with NativeOptions permission_mode, got %+v", attachSeen)
	}
	var attachOpts map[string]any
	if err := json.Unmarshal(attachSeen.GetNativeOptions().GetUtf8Json(), &attachOpts); err != nil {
		t.Fatal(err)
	}
	if attachOpts["permission_mode"] != "always-proceed" {
		t.Fatalf("expected permission_mode=always-proceed on attach, got %+v", attachOpts)
	}
	if readApprovalCount != 1 {
		t.Fatalf("expected single ReadApprovalMode check without SetApprovalMode restart, got %d", readApprovalCount)
	}
	if sendSeen == nil {
		t.Fatal("expected Send to be dispatched without SetModel failure")
	}
}

func TestHandoffCodexReadsOnlyNewestPageAndFallsBackToUnboundOnTimeout(t *testing.T) {
	var (
		sends            []*pb.SendRequest
		historyPageCalls int
		unboundQueries   int
	)
	srv := &mockDaemonServer{}
	srv.statusCb = func() (*pb.GetStatusResponse, error) {
		owned, runtime := true, pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
		return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{
			{Session: buildSessionRef("codex-agent", "native-codex", "ssh-wsl"), Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &runtime},
		}}, nil
	}
	srv.readModelCb = func(req *pb.ReadModelRequest) (*pb.ModelState, error) {
		if req.GetSession().GetAgentId() == "agy-agent" {
			return &pb.ModelState{Provider: optString("antigravity"), Model: optString("gemini-4-carbon-medium")}, nil
		}
		return &pb.ModelState{Provider: optString("magpie"), Model: optString("group/pro"), Effort: optString("xhigh")}, nil
	}
	srv.createCb = func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		if r.GetCodex() != nil {
			return &pb.CommandReceipt{
				Session:        buildSessionRef("codex-agent", "codex-summary", "ssh-wsl"),
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
			}, nil
		}
		return &pb.CommandReceipt{
			Session:        buildSessionRef("agy-agent", "agy-target", "ssh-wsl"),
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}
	srv.getHistoryPageCb = func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
		historyPageCalls++
		if r.Before != nil && r.Before.GetValue() != "" {
			t.Fatalf("handoff should not walk older pages for codex baseline/summary; got cursor=%q", r.Before.GetValue())
		}
		if len(sends) == 0 {
			// Baseline page has a NextCursor indicating 29 older pages exist; handoff must NOT chase them.
			return &pb.HistoryPageResponse{
				Session:    r.Session,
				Items:      []*pb.NativeJson{{Schema: "codex.item.v1", Utf8Json: []byte(`{"turnId":"turn-old","item":{"id":"msg-old","type":"agentMessage","text":"prior turn"}}`)}},
				NextCursor: &pb.NullableString{State: &pb.NullableString_Value{Value: "codex:page-2"}},
			}, nil
		}
		// Simulate the exact failure where the bound Codex runtime's readLoop is backlogged
		// behind delta commits and ReadSession times out after 30s.
		return nil, status.Error(codes.DeadlineExceeded, "context deadline exceeded")
	}
	srv.queryCb = func(r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
		unboundQueries++
		q := r.GetCodexThreadItemsList()
		if q == nil || q.ThreadId != "codex-summary" {
			t.Fatalf("unexpected unbound query: %+v", r)
		}
		// Codex thread/items/list sortDirection=desc returns newest first.
		native, err := encodeNativeJSON("codex.thread_items_list.v1", map[string]any{
			"data": []any{
				map[string]any{"turnId": "turn-1", "item": map[string]any{"id": "msg-summary", "type": "agentMessage", "text": "# Handoff Summary\nRecovered via unbound read."}},
				map[string]any{"turnId": "turn-1", "item": map[string]any{"id": "usr-prompt", "type": "userMessage", "content": []any{map[string]any{"type": "text", "text": "summarize"}}}},
				map[string]any{"turnId": "turn-old", "item": map[string]any{"id": "msg-old", "type": "agentMessage", "text": "prior turn"}},
			},
			"nextCursor": "older-page-token",
		})
		if err != nil {
			t.Fatal(err)
		}
		return &pb.NativeQueryResponse{Result: native}, nil
	}
	srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
		sends = append(sends, r)
		return &pb.CommandReceipt{
			Session:        r.Session,
			Accepted:       true,
			TurnId:         optString("turn-1"),
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Status:         pb.SessionStatus_SESSION_STATUS_RUNNING,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}
	srv.eventsCb = func(r *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		native, _ := encodeNativeJSON("astrorder.event.payload.v1", map[string]any{
			"agent_id": "codex-agent",
			"frame": map[string]any{
				"method": "turn/completed",
				"params": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "completed"}},
			},
		})
		return stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Live{Live: &pb.EventEnvelope{
			DaemonId:      "test-daemon",
			Session:       &pb.SessionRef{SessionId: "codex-summary"},
			SeqId:         1,
			EventType:     "codex.notification",
			NativePayload: native,
		}}})
	}
	srv.closeCb = func(r *pb.CloseSessionRequest) (*pb.CommandReceipt, error) {
		return &pb.CommandReceipt{
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}
	srv.deleteCb = func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
		return &pb.CommandReceipt{
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}
	// Also test resolveHandoffContext when rebinding fails (e.g. active writer lock still held).
	srv.attachCb = func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
		if r.GetSession().GetSessionId() == "codex-summary" {
			return nil, status.Error(codes.Unavailable, "thread codex-summary already has an active writer")
		}
		return &pb.SessionReceipt{Attached: true, Session: r.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
	}

	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": "ssh-wsl", "host": "127.0.0.1", "workspace": "/work", "state": "ready"}); err != nil {
		t.Fatal(err)
	}
	st.UpsertAgent(ctx, map[string]any{"id": "codex-agent", "kind": "codex", "name": "Codex", "status": "ready", "connection_id": "ssh-wsl"})
	st.UpsertAgent(ctx, map[string]any{"id": "agy-agent", "kind": "antigravity-ssh", "name": "Antigravity", "status": "ready", "connection_id": "ssh-wsl"})
	st.UpsertSession(ctx, map[string]any{"id": "codex-parent", "source_session_id": "native-codex", "agent_id": "codex-agent", "title": "work", "workspace": "/work", "connection_id": "ssh-wsl", "status": "idle", "updated_at": "2026-11-22T00:00:00Z"})

	w := handoffPost(h, "/api/v1/sessions/codex-parent/handoff", `{"operation_id":"codex-timeout-fallback","source_agent_id":"codex-agent","target_agent_id":"agy-agent","provider":"antigravity","model":"gemini-4-carbon-medium","effort":""}`)
	if w.Code != 200 {
		t.Fatalf("expected Codex -> Antigravity handoff to recover via unbound read, got %d: %s", w.Code, w.Body.String())
	}
	if historyPageCalls != 2 {
		t.Fatalf("expected exactly 2 bound GetHistoryPage calls (1 baseline + 1 timed-out summary read), got %d", historyPageCalls)
	}
	if unboundQueries != 1 {
		t.Fatalf("expected 1 unbound fallback query during handoff, got %d", unboundQueries)
	}

	// First send to target Antigravity uses the inlined summary directly (no extra daemon read after fork deletion).
	sendW := handoffPost(h, "/api/v1/commands", `{"id":"send-agy-after-fallback","agent_id":"agy-agent","session_id":"agy-target","action":"send","text":"continue work"}`)
	if sendW.Code != 200 {
		t.Fatalf("first send failed: %d %s", sendW.Code, sendW.Body.String())
	}
	if len(sends) != 2 || !strings.Contains(sends[1].Input[0].GetText().Text, "Recovered via unbound read.") {
		t.Fatalf("expected recovered handoff summary in first send, got %+v", sends)
	}
	if unboundQueries != 1 {
		t.Fatalf("expected inline summary to avoid extra unbound read on first send, got %d", unboundQueries)
	}
	if forkRow, err := st.GetSession(ctx, "codex-agent", "codex-summary"); err != nil || forkRow != nil {
		t.Fatalf("expected codex-summary fork deleted after handoff, got %+v %v", forkRow, err)
	}

	// Legacy native-handoff-v1 reference still falls back to unbound read when rebinding fails.
	legacyRef, _ := json.Marshal(handoffReference{AgentID: "codex-agent", SessionID: "codex-summary", ConnectionID: "ssh-wsl", MessageID: "msg-summary"})
	legacySession := map[string]any{"id": "agy-legacy", "agent_id": "agy-agent", "connection_id": "ssh-wsl", "workspace": "/work", "handoff_from_session_id": "codex-parent"}
	resolved, err := h.resolveHandoffContext(ctx, handoffRefPrefix+string(legacyRef), legacySession)
	if err != nil || !strings.Contains(resolved, "Recovered via unbound read.") {
		t.Fatalf("expected legacy native-handoff-v1 unbound fallback to remain readable, got resolved=%q err=%v", resolved, err)
	}
	if unboundQueries != 2 {
		t.Fatalf("expected unbound read for legacy native-handoff-v1 fallback, got %d", unboundQueries)
	}
}

func handoffGet(h *Handler, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer browser-sec")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestGetSessionHandoffContext(t *testing.T) {
	var (
		unboundQueries int
		attachCalls    int
		closeCalls     int
	)
	srv := &mockDaemonServer{}
	srv.queryCb = func(r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
		unboundQueries++
		q := r.GetCodexThreadItemsList()
		if q == nil || q.ThreadId != "codex-summary" {
			t.Fatalf("unexpected unbound query: %+v", r)
		}
		native, err := encodeNativeJSON("codex.thread_items_list.v1", map[string]any{
			"data": []any{
				map[string]any{"turnId": "turn-1", "item": map[string]any{"id": "msg-summary", "type": "agentMessage", "text": "## 当前进展\n已完成核心链路验证。\n\n- 下一步：发布新版"}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &pb.NativeQueryResponse{Result: native}, nil
	}
	srv.attachCb = func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
		attachCalls++
		return &pb.SessionReceipt{Attached: true, Session: r.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
	}
	srv.closeCb = func(r *pb.CloseSessionRequest) (*pb.CommandReceipt, error) {
		closeCalls++
		return &pb.CommandReceipt{
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}
	srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
		return &pb.CommandReceipt{
			Session:        r.Session,
			Accepted:       true,
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Status:         pb.SessionStatus_SESSION_STATUS_RUNNING,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}
	srv.getHistoryPageCb = func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
		return &pb.HistoryPageResponse{
			Session: r.Session,
			Items: []*pb.NativeJson{
				{Schema: "codex.item.v1", Utf8Json: []byte(`{"turnId":"turn-1","item":{"id":"msg-summary","type":"agentMessage","text":"## 当前进展\n已完成核心链路验证。\n\n- 下一步：发布新版"}}`)},
			},
		}, nil
	}

	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	st.UpsertAgent(ctx, map[string]any{"id": "codex-agent", "kind": "codex", "name": "Codex", "status": "ready"})
	st.UpsertAgent(ctx, map[string]any{"id": "agy-agent", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	st.UpsertSession(ctx, map[string]any{"id": "src-codex", "agent_id": "codex-agent", "title": "监控Agent", "workspace": "/work", "status": "idle", "updated_at": "2026-11-23T00:00:00Z"})

	refBytes, _ := json.Marshal(handoffReference{AgentID: "codex-agent", SessionID: "codex-summary", MessageID: "msg-summary"})
	st.UpsertSession(ctx, map[string]any{
		"id":                      "agy-target",
		"agent_id":                "agy-agent",
		"title":                   "监控Agent",
		"workspace":               "/work",
		"status":                  "idle",
		"updated_at":              "2026-11-23T00:01:00Z",
		"handoff_from_agent_id":   "codex-agent",
		"handoff_from_session_id": "src-codex",
		"handoff_context":         handoffRefPrefix + string(refBytes),
	})

	// 1. Native Codex summary preview prefers read-only unbound query without attaching/closing.
	w := handoffGet(h, "/api/v1/sessions/agy-target/handoff-context?agent_id=agy-agent")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for native handoff preview, got %d: %s", w.Code, w.Body.String())
	}
	var nativeResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &nativeResp); err != nil {
		t.Fatal(err)
	}
	if nativeResp["available"] != true || nativeResp["mode"] != "native_summary" || nativeResp["consumed"] != false {
		t.Fatalf("unexpected native preview metadata: %+v", nativeResp)
	}
	if nativeResp["source_agent_kind"] != "codex" || nativeResp["source_session_title"] != "监控Agent" {
		t.Fatalf("unexpected source metadata: %+v", nativeResp)
	}
	if got := stringValue(nativeResp, "summary"); got != "## 当前进展\n已完成核心链路验证。\n\n- 下一步：发布新版" {
		t.Fatalf("unexpected summary body: %q", got)
	}
	if unboundQueries != 1 || attachCalls != 0 || closeCalls != 0 {
		t.Fatalf("expected pure unbound read (queries=1 attach=0 close=0), got queries=%d attach=%d close=%d", unboundQueries, attachCalls, closeCalls)
	}

	// After claiming context on first send, preview remains readable and reports consumed=true.
	if _, err := st.ClaimSessionHandoffContext(ctx, "agy-agent", "agy-target"); err != nil {
		t.Fatal(err)
	}
	wAfter := handoffGet(h, "/api/v1/sessions/agy-target/handoff-context?agent_id=agy-agent")
	if wAfter.Code != http.StatusOK {
		t.Fatalf("expected 200 after consume, got %d: %s", wAfter.Code, wAfter.Body.String())
	}
	var afterResp map[string]any
	if err := json.Unmarshal(wAfter.Body.Bytes(), &afterResp); err != nil {
		t.Fatal(err)
	}
	if afterResp["available"] != true || afterResp["consumed"] != true || !strings.Contains(stringValue(afterResp, "summary"), "已完成核心链路验证") {
		t.Fatalf("unexpected post-consume response: %+v", afterResp)
	}

	// 2. Deterministic Antigravity excerpt strips wrapper boilerplate cleanly.
	excerpt := buildDeterministicHandoffContext("antigravity", "src-agy", "/work", []map[string]any{
		{"role": "user", "kind": "message", "text": "修复采集端下拉框"},
		{"role": "assistant", "kind": "message", "text": "已修复并验证通过。"},
	})
	st.UpsertSession(ctx, map[string]any{
		"id":                      "codex-target",
		"agent_id":                "codex-agent",
		"title":                   "反向转交",
		"workspace":               "/work",
		"status":                  "idle",
		"updated_at":              "2026-11-23T00:02:00Z",
		"handoff_from_agent_id":   "agy-agent",
		"handoff_from_session_id": "src-agy",
		"handoff_context":         excerpt,
	})
	wExcerpt := handoffGet(h, "/api/v1/sessions/codex-target/handoff-context?agent_id=codex-agent")
	if wExcerpt.Code != http.StatusOK {
		t.Fatalf("expected 200 for excerpt handoff, got %d: %s", wExcerpt.Code, wExcerpt.Body.String())
	}
	var excerptResp map[string]any
	if err := json.Unmarshal(wExcerpt.Body.Bytes(), &excerptResp); err != nil {
		t.Fatal(err)
	}
	if excerptResp["available"] != true || excerptResp["mode"] != "transcript_excerpt" || excerptResp["source_agent_kind"] != "antigravity" {
		t.Fatalf("unexpected excerpt metadata: %+v", excerptResp)
	}
	if summary := stringValue(excerptResp, "summary"); strings.Contains(summary, "<源会话历史记录摘录>") || !strings.Contains(summary, "修复采集端下拉框") {
		t.Fatalf("unexpected extracted excerpt summary: %q", summary)
	}

	// 3. Session without handoff_context returns available=false.
	wNone := handoffGet(h, "/api/v1/sessions/src-codex/handoff-context?agent_id=codex-agent")
	if wNone.Code != http.StatusOK {
		t.Fatalf("expected 200 for non-handoff session, got %d: %s", wNone.Code, wNone.Body.String())
	}
	var noneResp map[string]any
	if err := json.Unmarshal(wNone.Body.Bytes(), &noneResp); err != nil {
		t.Fatal(err)
	}
	if noneResp["available"] != false {
		t.Fatalf("expected available=false, got %+v", noneResp)
	}

	// 4. Validation errors.
	if wBad := handoffGet(h, "/api/v1/sessions/agy-target/handoff-context"); wBad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 without agent_id, got %d", wBad.Code)
	}
	if wMissing := handoffGet(h, "/api/v1/sessions/no-such-session/handoff-context?agent_id=agy-agent"); wMissing.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown session, got %d", wMissing.Code)
	}
}

func TestHandoffForkDeleteFailureFallsBackToCloseAndKeepsEphemeralRow(t *testing.T) {
	var (
		deletes int
		closes  int
	)
	srv := &mockDaemonServer{}
	srv.statusCb = func() (*pb.GetStatusResponse, error) {
		owned, runtime := true, pb.RuntimeKind_RUNTIME_KIND_CODEX
		return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: []*pb.SessionSnapshot{
			{Session: buildSessionRef("codex-agent", "native-codex", ""), Status: pb.SessionStatus_SESSION_STATUS_IDLE, RuntimeOwned: &owned, Runtime: &runtime},
		}}, nil
	}
	srv.readModelCb = func(req *pb.ReadModelRequest) (*pb.ModelState, error) {
		if req.GetSession().GetAgentId() == "agy-agent" {
			return &pb.ModelState{Provider: optString("antigravity"), Model: optString("gemini-4-carbon-medium")}, nil
		}
		return &pb.ModelState{Provider: optString("openai"), Model: optString("gpt-5.4"), Effort: optString("high")}, nil
	}
	srv.createCb = func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
		if r.GetCodex() != nil {
			return &pb.CommandReceipt{
				Session:        buildSessionRef("codex-agent", "codex-summary-keep", ""),
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
			}, nil
		}
		return &pb.CommandReceipt{
			Session:        buildSessionRef("agy-agent", "agy-target-keep", ""),
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}
	sends := 0
	srv.getHistoryPageCb = func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
		items := []*pb.NativeJson{{Schema: "codex.item.v1", Utf8Json: []byte(`{"id":"old","role":"assistant","text":"prior"}`)}}
		if sends > 0 {
			items = append(items, &pb.NativeJson{Schema: "codex.item.v1", Utf8Json: []byte(`{"id":"sum-1","role":"assistant","text":"fallback delete summary"}`)})
		}
		return &pb.HistoryPageResponse{Session: r.Session, Items: items}, nil
	}
	srv.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
		sends++
		return &pb.CommandReceipt{
			Session:        r.Session,
			Accepted:       true,
			TurnId:         optString("turn-1"),
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Status:         pb.SessionStatus_SESSION_STATUS_RUNNING,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}
	srv.eventsCb = func(r *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.EventStreamFrame]) error {
		native, _ := encodeNativeJSON("astrorder.event.payload.v1", map[string]any{
			"agent_id": "codex-agent",
			"frame": map[string]any{
				"method": "turn/completed",
				"params": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "completed"}},
			},
		})
		return stream.Send(&pb.EventStreamFrame{Frame: &pb.EventStreamFrame_Live{Live: &pb.EventEnvelope{
			DaemonId:      "test-daemon",
			Session:       &pb.SessionRef{SessionId: "codex-summary-keep"},
			SeqId:         1,
			EventType:     "codex.notification",
			NativePayload: native,
		}}})
	}
	srv.deleteCb = func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
		deletes++
		return nil, status.Error(codes.Unavailable, "native delete temporarily failed")
	}
	srv.closeCb = func(r *pb.CloseSessionRequest) (*pb.CommandReceipt, error) {
		closes++
		return &pb.CommandReceipt{
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}},
		}, nil
	}

	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	st.UpsertAgent(ctx, map[string]any{"id": "codex-agent", "kind": "codex", "name": "Codex", "status": "ready"})
	st.UpsertAgent(ctx, map[string]any{"id": "agy-agent", "kind": "antigravity", "name": "Antigravity", "status": "ready"})
	st.UpsertSession(ctx, map[string]any{"id": "codex-parent", "source_session_id": "native-codex", "agent_id": "codex-agent", "title": "work", "workspace": "/work", "status": "idle", "updated_at": "2026-11-27T00:00:00Z"})

	w := handoffPost(h, "/api/v1/sessions/codex-parent/handoff", `{"operation_id":"delete-fallback-op","source_agent_id":"codex-agent","target_agent_id":"agy-agent","provider":"antigravity","model":"gemini-4-carbon-medium","effort":""}`)
	if w.Code != 200 {
		t.Fatalf("handoff should still succeed when fork delete fails: %d %s", w.Code, w.Body.String())
	}
	if deletes != 1 || closes != 1 {
		t.Fatalf("expected 1 delete attempt and 1 fallback close, got deletes=%d closes=%d", deletes, closes)
	}
	targetRow, err := st.GetSession(ctx, "agy-agent", "agy-target-keep")
	if err != nil || !strings.Contains(stringValue(targetRow, "handoff_context"), "fallback delete summary") {
		t.Fatalf("expected target session to preserve inline summary, got %+v %v", targetRow, err)
	}
	forkRow, err := st.GetSession(ctx, "codex-agent", "codex-summary-keep")
	if err != nil || forkRow == nil || forkRow["ephemeral"] != true {
		t.Fatalf("expected fork row kept as ephemeral=true when native delete fails, got %+v %v", forkRow, err)
	}
}
