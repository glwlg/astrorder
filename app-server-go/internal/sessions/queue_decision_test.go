package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type queueFixture struct {
	h          *Handler
	st         *store.Store
	historyMu  sync.Mutex
	history    []*pb.NativeJson
	jevCalls   atomic.Int32
	sendCalls  atomic.Int32
	jevHandler func(http.ResponseWriter, *http.Request)
}

func (f *queueFixture) setHistory(raw ...string) {
	f.historyMu.Lock()
	defer f.historyMu.Unlock()
	f.history = nil
	for _, item := range raw {
		f.history = append(f.history, &pb.NativeJson{Utf8Json: []byte(item)})
	}
}

func newQueueFixture(t *testing.T, configureJev bool) *queueFixture {
	t.Helper()
	f := &queueFixture{}
	owned, aid, kind := true, "a", pb.RuntimeKind_RUNTIME_KIND_HERMES
	srv := &guidanceModelServer{snapshot: &pb.SessionSnapshot{Session: &pb.SessionRef{SessionId: "s", AgentId: &aid}, RuntimeOwned: &owned, Runtime: &kind, Status: pb.SessionStatus_SESSION_STATUS_IDLE, ReplayThroughSeqId: 10}}
	srv.getHistoryPageCb = func(request *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
		f.historyMu.Lock()
		defer f.historyMu.Unlock()
		return &pb.HistoryPageResponse{Session: request.Session, Items: append([]*pb.NativeJson(nil), f.history...)}, nil
	}
	srv.sendCb = func(request *pb.SendRequest) (*pb.CommandReceipt, error) {
		f.sendCalls.Add(1)
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
	}
	h, st, _, cleanup := setupTestServer(t, &srv.mockDaemonServer)
	t.Cleanup(cleanup)
	addr, stop := startGuidanceStatusTarget(t, srv)
	t.Cleanup(stop)
	client := dialModelRestoreTarget(t, addr)
	t.Cleanup(func() { _ = client.Close() })
	h.client.Daemon, h.client.Session = client.Daemon, client.Session
	f.h, f.st = h, st
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "hermes", "name": "fixture", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "control_state": "owned", "updated_at": "2026-10-30T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	f.setHistory(`{"row_id":1,"role":"user","content":"以前的问题"}`, `{"row_id":2,"role":"assistant","content":"处理完成，可选：需要我继续吗？"}`)
	if _, err := h.Submit(ctx, map[string]any{"id": "next", "agent_id": "a", "session_id": "s", "action": "enqueue", "text": "下一条私有消息"}); err != nil {
		t.Fatal(err)
	}
	if configureJev {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.jevCalls.Add(1)
			if f.jevHandler != nil {
				f.jevHandler(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"answers":{"queue_confirmation":{"choice":"not_required"}}}`))
		}))
		t.Cleanup(upstream.Close)
		if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{"gateway_type": "magpie", "inference_url": upstream.URL + "/v1", "api_key": "fixture-key"}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPreference(ctx, browser.JevConfigPreference, browser.JevConfig{Mode: "gateway", Model: "typesafe/jev-preview"}, true); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *queueFixture) dispatch(t *testing.T) {
	t.Helper()
	if err := f.h.DispatchQueue(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *queueFixture) command(t *testing.T, id string) map[string]any {
	t.Helper()
	row, err := f.st.GetCommand(context.Background(), "a", "s", id)
	if err != nil || row == nil {
		t.Fatalf("command %s: %v %v", id, row, err)
	}
	return row
}

func TestQueueJevDecisionSendsCompletedReplyAndWaitsForRequiredConfirmation(t *testing.T) {
	t.Run("optional question", func(t *testing.T) {
		f := newQueueFixture(t, true)
		f.jevHandler = func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				State     map[string]any `json:"state"`
				Questions map[string]any `json:"questions"`
			}
			if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer fixture-key" || json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("Jev did not use the configured gateway")
			}
			if body.State["final_reply"] != "处理完成，可选：需要我继续吗？" || len(body.State) != 1 || body.Questions["queue_confirmation"] == nil {
				t.Errorf("wrong decision context: %+v", body)
			}
			_, _ = w.Write([]byte(`{"answers":{"queue_confirmation":{"choice":"not_required"}}}`))
		}
		f.dispatch(t)
		if f.sendCalls.Load() != 1 || f.jevCalls.Load() != 1 || f.command(t, "next")["state"] != "accepted" {
			t.Fatalf("ordinary completion was not sent: calls=%d jev=%d row=%v", f.sendCalls.Load(), f.jevCalls.Load(), f.command(t, "next"))
		}
	})
	t.Run("required then answered", func(t *testing.T) {
		f := newQueueFixture(t, true)
		f.jevHandler = func(w http.ResponseWriter, r *http.Request) {
			choice := "required"
			if f.jevCalls.Load() > 1 {
				choice = "not_required"
			}
			_, _ = fmt.Fprintf(w, `{"answers":{"queue_confirmation":{"choice":%q}}}`, choice)
		}
		f.dispatch(t)
		f.dispatch(t)
		row := f.command(t, "next")
		if f.sendCalls.Load() != 0 || f.jevCalls.Load() != 1 || row["state"] != "queued" || !strings.Contains(stringValue(row, "error"), "需要你的确认") {
			t.Fatalf("required decision not retained: %v calls=%d jev=%d", row, f.sendCalls.Load(), f.jevCalls.Load())
		}
		f.setHistory(`{"row_id":1,"role":"user","content":"开始"}`, `{"row_id":2,"role":"assistant","content":"请确认才能继续"}`, `{"row_id":3,"role":"user","content":"同意"}`, `{"row_id":4,"role":"assistant","content":"已经处理"}`)
		f.dispatch(t)
		if f.sendCalls.Load() != 1 || f.jevCalls.Load() != 2 {
			t.Fatalf("new reply did not resume queue: sends=%d jev=%d", f.sendCalls.Load(), f.jevCalls.Load())
		}
	})
}

func TestQueueJevErrorsAndPendingApprovalDoNotSend(t *testing.T) {
	t.Setenv("JEV_API_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	for _, tc := range []struct {
		name      string
		configure bool
		response  string
		notice    string
	}{
		{"missing configuration", false, "", "请配置 System One"},
		{"invalid decision", true, `{"answers":{"queue_confirmation":{"choice":"maybe"}}}`, "无效"},
		{"invalid json", true, `not json`, "System One 判断失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newQueueFixture(t, tc.configure)
			f.jevHandler = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tc.response)) }
			f.dispatch(t)
			f.dispatch(t)
			row := f.command(t, "next")
			if f.sendCalls.Load() != 0 || row["state"] != "queued" || !strings.Contains(stringValue(row, "error"), tc.notice) || f.jevCalls.Load() > 1 {
				t.Fatalf("unavailable decision sent or failed to explain: %+v sends=%d jev=%d", row, f.sendCalls.Load(), f.jevCalls.Load())
			}
		})
	}
	t.Run("native approval", func(t *testing.T) {
		f := newQueueFixture(t, true)
		_, err := f.st.AppendEvent(context.Background(), store.Event{ID: "approval", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "approval", "state": "pending"}})
		if err != nil {
			t.Fatal(err)
		}
		f.dispatch(t)
		if f.sendCalls.Load() != 0 || f.jevCalls.Load() != 0 || !strings.Contains(stringValue(f.command(t, "next"), "error"), "等待处理") {
			t.Fatal("pending native approval was bypassed")
		}
	})
}

func TestQueueDoesNotSendWhenHistoryOrQueueChangesDuringDecision(t *testing.T) {
	t.Run("new final reply", func(t *testing.T) {
		f := newQueueFixture(t, true)
		f.jevHandler = func(w http.ResponseWriter, r *http.Request) {
			if f.jevCalls.Load() == 1 {
				f.setHistory(`{"row_id":3,"role":"user","content":"新的任务"}`, `{"row_id":4,"role":"assistant","content":"请先确认"}`)
				_, _ = w.Write([]byte(`{"answers":{"queue_confirmation":{"choice":"not_required"}}}`))
			} else {
				_, _ = w.Write([]byte(`{"answers":{"queue_confirmation":{"choice":"required"}}}`))
			}
		}
		f.dispatch(t)
		if f.sendCalls.Load() != 0 {
			t.Fatal("sent using an outdated reply")
		}
		f.dispatch(t)
		if f.sendCalls.Load() != 0 || f.jevCalls.Load() != 2 || !strings.Contains(stringValue(f.command(t, "next"), "error"), "需要你的确认") {
			t.Fatal("new final reply was not judged")
		}
	})
	t.Run("queue removed", func(t *testing.T) {
		f := newQueueFixture(t, true)
		f.jevHandler = func(w http.ResponseWriter, r *http.Request) {
			_, ok, err := f.st.CancelQueuedCommand(context.Background(), "a", "s", "next")
			if err != nil || !ok {
				t.Errorf("cancel: %v %v", ok, err)
			}
			_, _ = w.Write([]byte(`{"answers":{"queue_confirmation":{"choice":"not_required"}}}`))
		}
		f.dispatch(t)
		if f.sendCalls.Load() != 0 || f.command(t, "next")["state"] != "cancelled" {
			t.Fatal("cancelled queue entry was dispatched")
		}
	})
	t.Run("approval arrives", func(t *testing.T) {
		f := newQueueFixture(t, true)
		f.jevHandler = func(w http.ResponseWriter, r *http.Request) {
			_, err := f.st.AppendEvent(context.Background(), store.Event{ID: "new-approval", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "approval", "state": "pending"}})
			if err != nil {
				t.Error(err)
			}
			_, _ = w.Write([]byte(`{"answers":{"queue_confirmation":{"choice":"not_required"}}}`))
		}
		f.dispatch(t)
		if f.sendCalls.Load() != 0 || f.command(t, "next")["state"] != "queued" {
			t.Fatal("approval arriving during decision was bypassed")
		}
	})
}

type queueStatusClient struct {
	pb.DaemonControlClient
	snapshots []*pb.SessionSnapshot
	sequence  *atomic.Int64
}

func (q queueStatusClient) GetStatus(context.Context, *pb.GetStatusRequest, ...grpc.CallOption) (*pb.GetStatusResponse, error) {
	snapshots := make([]*pb.SessionSnapshot, 0, len(q.snapshots))
	for _, snapshot := range q.snapshots {
		copy := proto.Clone(snapshot).(*pb.SessionSnapshot)
		if q.sequence != nil {
			copy.ReplayThroughSeqId = q.sequence.Load()
		}
		snapshots = append(snapshots, copy)
	}
	return &pb.GetStatusResponse{DaemonId: "test-daemon", Sessions: snapshots}, nil
}

type queueHistoryClient struct {
	pb.RuntimeQueryClient
	sequence *atomic.Int64
}

func (q queueHistoryClient) GetHistoryPage(ctx context.Context, request *pb.HistoryPageRequest, opts ...grpc.CallOption) (*pb.HistoryPageResponse, error) {
	page, err := q.RuntimeQueryClient.GetHistoryPage(ctx, request, opts...)
	q.sequence.Add(1)
	return page, err
}

func TestQueueAllowsHistoryReadsThatEmitNativeEvents(t *testing.T) {
	f := newQueueFixture(t, true)
	owned, aid, kind := true, "a", pb.RuntimeKind_RUNTIME_KIND_HERMES
	sequence := &atomic.Int64{}
	f.h.client.Daemon = queueStatusClient{DaemonControlClient: f.h.client.Daemon, sequence: sequence, snapshots: []*pb.SessionSnapshot{{Session: &pb.SessionRef{AgentId: &aid, SessionId: "s"}, RuntimeOwned: &owned, Runtime: &kind, Status: pb.SessionStatus_SESSION_STATUS_IDLE}}}
	f.h.client.Runtime = queueHistoryClient{RuntimeQueryClient: f.h.client.Runtime, sequence: sequence}
	f.dispatch(t)
	if f.sendCalls.Load() != 1 || f.jevCalls.Load() != 1 {
		t.Fatalf("history side effect prevented dispatch: sends=%d jev=%d", f.sendCalls.Load(), f.jevCalls.Load())
	}
}

func TestQueueDecisionDoesNotBlockOtherSessionsOrDuplicateConcurrentDispatch(t *testing.T) {
	f := newQueueFixture(t, true)
	ctx := context.Background()
	if _, err := f.st.UpsertSession(ctx, map[string]any{"id": "s2", "agent_id": "a", "status": "idle", "control_state": "owned", "updated_at": "2026-10-30T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.Submit(ctx, map[string]any{"id": "next2", "agent_id": "a", "session_id": "s2", "action": "enqueue", "text": "next"}); err != nil {
		t.Fatal(err)
	}
	owned, aid, kind := true, "a", pb.RuntimeKind_RUNTIME_KIND_HERMES
	var snapshots []*pb.SessionSnapshot
	for _, sid := range []string{"s", "s2"} {
		snapshots = append(snapshots, &pb.SessionSnapshot{Session: &pb.SessionRef{AgentId: &aid, SessionId: sid}, RuntimeOwned: &owned, Runtime: &kind, Status: pb.SessionStatus_SESSION_STATUS_IDLE, ReplayThroughSeqId: 10})
	}
	f.h.client.Daemon = queueStatusClient{DaemonControlClient: f.h.client.Daemon, snapshots: snapshots}
	firstStarted, secondStarted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var handled atomic.Int32
	var secondOnce sync.Once
	f.jevHandler = func(w http.ResponseWriter, r *http.Request) {
		if handled.Add(1) == 1 {
			close(firstStarted)
			<-release
		} else {
			secondOnce.Do(func() { close(secondStarted) })
		}
		_, _ = w.Write([]byte(`{"answers":{"queue_confirmation":{"choice":"not_required"}}}`))
	}
	done := make(chan error, 1)
	go func() { done <- f.h.DispatchQueue(ctx) }()
	select {
	case <-firstStarted:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("first Jev request did not start")
	}
	// Another dispatcher must not judge or claim either in-flight session again.
	secondDispatch := make(chan error, 1)
	go func() { secondDispatch <- f.h.DispatchQueue(ctx) }()
	secondWasConcurrent := false
	select {
	case <-secondStarted:
		secondWasConcurrent = true
	case <-time.After(3 * time.Second):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDispatch; err != nil {
		t.Fatal(err)
	}
	if !secondWasConcurrent || f.jevCalls.Load() != 2 || f.sendCalls.Load() != 2 {
		t.Fatalf("sessions did not dispatch independently exactly once: concurrent=%v jev=%d sends=%d", secondWasConcurrent, f.jevCalls.Load(), f.sendCalls.Load())
	}
}

func TestQueueDoesNotUseAnOlderReplyAfterNewUserInput(t *testing.T) {
	f := newQueueFixture(t, true)
	f.setHistory(`{"row_id":1,"role":"assistant","content":"旧回复"}`, `{"row_id":2,"role":"user","content":"新问题"}`)
	f.dispatch(t)
	if f.sendCalls.Load() != 0 || f.jevCalls.Load() != 0 || !strings.Contains(stringValue(f.command(t, "next"), "error"), "最终回复尚未就绪") {
		t.Fatal("older reply was used as the final response to the latest input")
	}
}

func TestQueueUsesCodexFinalAnswerInsteadOfCommentaryOrAsyncQuestion(t *testing.T) {
	f := newQueueFixture(t, false)
	ctx := context.Background()
	agent := map[string]any{"id": "a", "kind": "codex"}
	session, err := f.st.GetSession(ctx, "a", "s")
	if err != nil {
		t.Fatal(err)
	}
	f.setHistory(
		`{"turnId":"turn","item":{"id":"user","type":"userMessage","content":[{"type":"text","text":"问题"}]}}`,
		`{"turnId":"turn","item":{"id":"partial","type":"agentMessage","phase":"partial_answer","text":"处理中"}}`,
		`{"turnId":"turn","item":{"id":"final","type":"agentMessage","phase":"final_answer","text":"处理完成"}}`,
		`{"turnId":"turn","item":{"id":"question","type":"agentMessage","delivery":"async","text":"选择一个"}}`,
		`{"turnId":"turn","item":{"id":"comment","type":"agentMessage","phase":"commentary","text":"临时更新"}}`,
	)
	reply, err := f.h.latestQueueReply(ctx, agent, session)
	if err != nil || reply.id != "final" || reply.text != "处理完成" {
		t.Fatalf("wrong Codex final answer: %+v %v", reply, err)
	}
	f.setHistory(
		`{"item":{"id":"user","type":"userMessage","content":[{"type":"text","text":"问题"}]}}`,
		`{"item":{"id":"comment","type":"agentMessage","phase":"commentary","text":"处理中"}}`,
	)
	if _, err := f.h.latestQueueReply(ctx, agent, session); err == nil {
		t.Fatal("commentary was used as the final answer")
	}
}

type queueOperationClient struct {
	result func(*pb.GetOperationRequest) *pb.OperationReceipt
}

func (q queueOperationClient) GetOperation(_ context.Context, request *pb.GetOperationRequest, _ ...grpc.CallOption) (*pb.OperationReceipt, error) {
	return q.result(request), nil
}

func TestQueueReconcilesOnlyExactAuthoritativeOperationReceipt(t *testing.T) {
	for _, tc := range []struct {
		name, state, native string
		mismatch            bool
		wantState           string
		wantSend            int32
	}{
		{"guidance completed", "completed", `{"completed":true}`, false, "completed", 1},
		{"accepted but turn not completed", "completed", `{"accepted":true}`, false, "accepted", 0},
		{"unconfirmed", "uncertain", "", false, "unknown", 0},
		{"foreign operation", "completed", `{"completed":true}`, true, "unknown", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newQueueFixture(t, true)
			_, _, err := f.st.CreateCommand(context.Background(), map[string]any{"id": "previous", "agent_id": "a", "session_id": "s", "action": "send", "text": "earlier"}, "unknown")
			if err != nil {
				t.Fatal(err)
			}
			f.h.client.Operation = queueOperationClient{result: func(req *pb.GetOperationRequest) *pb.OperationReceipt {
				if req.Key.OperationId != "previous" || req.Key.LegacyAction != "session.send" || req.Key.SessionId.GetValue() != "s" {
					t.Errorf("wrong lookup: %+v", req.Key)
				}
				state := pb.OperationState_OPERATION_STATE_COMPLETED
				if tc.state == "uncertain" {
					state = pb.OperationState_OPERATION_STATE_UNCERTAIN
				}
				key := req.Key
				if tc.mismatch {
					key = &pb.OperationKey{OperationId: "different"}
				}
				return &pb.OperationReceipt{DaemonId: "test-daemon", Key: key, State: state, TypedResponse: &pb.CommandReceipt{
					DaemonId: "test-daemon", OperationId: "previous", Session: &pb.SessionRef{SessionId: "s"}, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true,
					Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true, Native: &pb.NativeJson{Utf8Json: []byte(tc.native)}}},
				}}
			}}
			f.dispatch(t)
			if row := f.command(t, "previous"); row["state"] != tc.wantState || f.sendCalls.Load() != tc.wantSend {
				t.Fatalf("receipt reconciled incorrectly: %v sends=%d", row, f.sendCalls.Load())
			}
		})
	}
}
