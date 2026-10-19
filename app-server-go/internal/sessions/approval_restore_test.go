package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type approvalRestorePeer struct {
	*mockDaemonServer
	mode   string
	writes int
	reject bool
	stale  bool
}

func (s *approvalRestorePeer) ReadApprovalMode(_ context.Context, req *pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error) {
	if req.Session.SessionId != "native" || req.Session.GetAgentId() != "a" {
		return nil, status.Error(codes.PermissionDenied, "wrong identity")
	}
	return &pb.ApprovalModeState{Mode: s.mode}, nil
}

func (s *approvalRestorePeer) SetApprovalMode(_ context.Context, req *pb.SetApprovalModeRequest) (*pb.CommandReceipt, error) {
	if req.Session.SessionId != "native" || req.Session.GetAgentId() != "a" || req.Meta.GetOperationId() == "" {
		return nil, status.Error(codes.PermissionDenied, "wrong identity or missing operation")
	}
	s.writes++
	if s.reject {
		return nil, status.Error(codes.Unavailable, "restore rejected")
	}
	if !s.stale {
		s.mode = req.Mode
	}
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
}

func startApprovalRestorePeer(t *testing.T, peer *approvalRestorePeer) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterDaemonControlServer(server, peer)
	pb.RegisterRuntimeQueryServer(server, peer)
	pb.RegisterSessionControlServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	return listener.Addr().String(), func() { server.Stop(); _ = listener.Close() }
}

func dialApprovalRestorePeer(t *testing.T, addr string) *daemonclient.Client {
	t.Helper()
	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{Target: addr, Credential: "test-secret", RequestTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestCodexApprovalRestoreBeforeSend(t *testing.T) {
	for _, tc := range []struct {
		name, saved            string
		reject, stale, missing bool
		wantCode, wantSend     int
	}{
		{name: "restore", saved: "full_access", wantCode: 200, wantSend: 1},
		{name: "reattach", saved: "full_access", missing: true, wantCode: 200, wantSend: 1},
		{name: "no selection", wantCode: 200, wantSend: 1},
		{name: "rejected", saved: "full_access", reject: true, wantCode: 502},
		{name: "unconfirmed", saved: "full_access", stale: true, wantCode: 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			peer := &approvalRestorePeer{mockDaemonServer: &mockDaemonServer{}, mode: "manual", reject: tc.reject, stale: tc.stale}
			attached := !tc.missing
			peer.readModelCb = func(*pb.ReadModelRequest) (*pb.ModelState, error) {
				if !attached {
					return nil, status.Error(codes.NotFound, "owner lost")
				}
				return &pb.ModelState{}, nil
			}
			peer.attachCb = func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
				attached = true
				return &pb.SessionReceipt{Session: r.Session, Attached: true, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
			}
			sends := 0
			peer.sendCb = func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
				sends++
				if tc.saved != "" && peer.mode != tc.saved {
					t.Errorf("send used %s, wanted %s", peer.mode, tc.saved)
				}
				return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Accepted: true, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
			}
			h, st, _, cleanup := setupTestServer(t, peer.mockDaemonServer)
			defer cleanup()
			addr, stop := startApprovalRestorePeer(t, peer)
			defer stop()
			client := dialApprovalRestorePeer(t, addr)
			defer client.Close()
			h.client = client
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "a", "status": "idle", "workspace": "C:/work", "updated_at": "2026-10-16T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			if tc.saved != "" {
				if _, err := st.SetSessionApprovalModeBinding(ctx, "a", "native", tc.saved); err != nil {
					t.Fatal(err)
				}
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/commands", bytes.NewBufferString(`{"id":"restore-send","agent_id":"a","session_id":"native","action":"send","text":"fixture"}`))
			r.Header.Set("Authorization", "Bearer browser-sec")
			h.ServeHTTP(w, r)
			if w.Code != tc.wantCode || sends != tc.wantSend {
				t.Fatalf("status=%d sends=%d body=%s", w.Code, sends, w.Body.String())
			}
			if tc.saved == "" && peer.writes != 0 {
				t.Fatal("changed untouched session policy")
			}
			// Failed restoration must retain the durable choice for a later retry.
			row, err := st.GetSession(ctx, "a", "native")
			if err != nil {
				t.Fatal(err)
			}
			if stringValue(row, "selected_approval_mode") != tc.saved {
				t.Fatal("durable selection changed")
			}
		})
	}
}

func TestCodexExplicitApprovalReplacesPersistedSelection(t *testing.T) {
	ctx := context.Background()
	peer := &approvalRestorePeer{mockDaemonServer: &mockDaemonServer{}, mode: "manual"}
	h, st, _, cleanup := setupTestServer(t, peer.mockDaemonServer)
	defer cleanup()
	addr, stop := startApprovalRestorePeer(t, peer)
	defer stop()
	client := dialApprovalRestorePeer(t, addr)
	defer client.Close()
	h.client = client
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "a", "status": "idle", "updated_at": "2026-10-16T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetSessionApprovalModeBinding(ctx, "a", "native", "full_access"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/native/approval-mode", strings.NewReader(`{"agent_id":"a","mode":"manual"}`))
	h.handleSetSessionApprovalMode(w, r, "native")
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if peer.writes != 1 {
		t.Fatalf("explicit change unnecessarily restored old access: writes=%d", peer.writes)
	}
	// Even a stale session snapshot must restore the newer durable choice.
	if err := h.ensureNativeBinding(ctx, map[string]any{"id": "native", "agent_id": "a", "selected_approval_mode": "full_access"}); err != nil {
		t.Fatal(err)
	}
	if peer.mode != "manual" {
		t.Fatal("old selection overrode explicit change")
	}
}

func TestCodexApprovalRestoredFromDurableSelection(t *testing.T) {
	for _, mode := range []string{"manual", "auto", "full_access"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			h, st, client, cleanup := setupTestServer(t, &mockDaemonServer{})
			defer cleanup()
			_, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Codex", "status": "ready"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "a", "status": "idle", "workspace": "C:/work", "updated_at": "2026-10-16T00:00:00Z"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetSessionApprovalModeBinding(ctx, "a", "native", mode); err != nil {
				t.Fatal(err)
			}
			// Reopen the durable store and construct a fresh App handler. The
			// daemon peer has an existing owner but has lost its approval cache.
			rows, err := st.QueryRows(ctx, "PRAGMA database_list")
			if err != nil {
				t.Fatal(err)
			}
			path := rows[0]["file"].(string)
			reopened, err := store.Open(filepath.Clean(path))
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			peer := &approvalRestorePeer{mockDaemonServer: &mockDaemonServer{}, mode: "manual"}
			addr, stop := startApprovalRestorePeer(t, peer)
			defer stop()
			reconnected := dialApprovalRestorePeer(t, addr)
			defer reconnected.Close()
			fresh := &Handler{store: reopened, client: reconnected, publisher: h.publisher, authorizer: h.authorizer, defaultWorkspace: h.defaultWorkspace, bgTasks: newBackgroundTaskManager()}
			_ = client
			for i := 0; i < 2; i++ {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/native/approval-mode?agent_id=a", nil)
				fresh.handleGetSessionApprovalMode(w, r, "native")
				var body struct {
					Mode string `json:"mode"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				if w.Code != http.StatusOK || body.Mode != mode {
					t.Fatalf("saved %s lost after App reconnect: %d %s", mode, w.Code, w.Body.String())
				}
			}
			wantWrites := 1
			if mode == "manual" {
				wantWrites = 0
			}
			if peer.writes != wantWrites {
				t.Fatalf("restore writes=%d want=%d", peer.writes, wantWrites)
			}
		})
	}
}
