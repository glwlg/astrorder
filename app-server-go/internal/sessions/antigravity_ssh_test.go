package sessions

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func setupTestServerWithCustomRuntime(t *testing.T, base *mockDaemonServer, rt pb.RuntimeQueryServer) (*Handler, *store.Store, *daemonclient.Client, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	pb.RegisterDaemonControlServer(grpcSrv, base)
	pb.RegisterSessionControlServer(grpcSrv, base)
	pb.RegisterRuntimeQueryServer(grpcSrv, rt)
	pb.RegisterEventStreamServer(grpcSrv, base)
	go func() { _ = grpcSrv.Serve(lis) }()

	ctx := context.Background()
	client, err := daemonclient.Dial(ctx, daemonclient.Config{
		Target:         lis.Addr().String(),
		Credential:     "test-secret",
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		grpcSrv.Stop()
		t.Fatalf("dial: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		client.Close()
		grpcSrv.Stop()
		t.Fatalf("store.Open: %v", err)
	}

	authorizer := auth.New(config.Config{BrowserSecret: "browser-sec"}, auth.OriginLookupFunc(func(ctx context.Context) (auth.OriginPreferences, error) {
		return auth.OriginPreferences{AllowedOrigins: []string{"http://localhost"}}, nil
	}))
	pub := &mockPublisher{}
	rawHandler, err := NewHandler(Options{
		Store:            st,
		Client:           client,
		Authorizer:       authorizer,
		Publisher:        pub,
		DefaultWorkspace: "C:/work",
	})
	if err != nil {
		st.Close()
		client.Close()
		grpcSrv.Stop()
		t.Fatalf("NewHandler: %v", err)
	}
	return rawHandler.(*Handler), st, client, func() {
		client.Close()
		st.Close()
		grpcSrv.Stop()
	}
}

type antigravitySSHMockServer struct {
	*mockDaemonServer
	approvalMode string
	model        string
	setModels    int
	setApprovals int
}

func (s *antigravitySSHMockServer) SetModel(_ context.Context, r *pb.SetModelRequest) (*pb.CommandReceipt, error) {
	s.setModels++
	s.model = r.Model
	return &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Session:        r.Session,
		Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
		Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true, Model: &r.Model}},
	}, nil
}

func (s *antigravitySSHMockServer) ReadApprovalMode(_ context.Context, r *pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error) {
	return &pb.ApprovalModeState{Session: r.Session, Mode: s.approvalMode}, nil
}

func (s *antigravitySSHMockServer) SetApprovalMode(_ context.Context, r *pb.SetApprovalModeRequest) (*pb.CommandReceipt, error) {
	s.setApprovals++
	s.approvalMode = r.Mode
	return &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Session:        r.Session,
		Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
		Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
	}, nil
}

func TestAntigravitySSHActionsAndHistoryProjection(t *testing.T) {
	const (
		connID   = "wsl-ssh"
		agentID  = "ssh-antigravity-wsl-ssh"
		parentID = "11111111-1111-4111-8111-111111111111"
		childID  = "22222222-2222-4222-8222-222222222222"
	)

	attached := false
	sent := 0
	interrupted := 0
	deleted := 0

	ext := &antigravitySSHMockServer{
		approvalMode: "manual",
		model:        "gemini-2.5-pro",
	}
	base := &mockDaemonServer{
		createCb: func(r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
			if r.Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH || r.Target.GetConnectionId() != connID {
				t.Fatalf("unexpected create target: %+v", r.Target)
			}
			if r.Target.GetSsh().AntigravityExecutable != nil {
				return nil, status.Error(codes.InvalidArgument, "remote executable should be unset without a resolver")
			}
			opts := r.GetAntigravity()
			if opts == nil {
				t.Fatalf("missing AntigravityCreateOptions: %+v", r)
			}
			sid := parentID
			if opts.ParentSessionId != nil {
				if opts.GetParentSessionId() != parentID || opts.GetTargetMessageId() != "antigravity:"+parentID+":1" {
					t.Fatalf("unexpected fork options: %+v", opts)
				}
				sid = childID
			} else if opts.Cwd != "/srv/remote-ws" {
				t.Fatalf("expected remote POSIX workspace fallback, got %q", opts.Cwd)
			}
			aid := agentID
			cid := connID
			return &pb.CommandReceipt{
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Session:        &pb.SessionRef{SessionId: sid, AgentId: &aid, ConnectionId: &cid},
				Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
			}, nil
		},
		readModelCb: func(r *pb.ReadModelRequest) (*pb.ModelState, error) {
			if r.Session.GetConnectionId() != connID {
				t.Fatalf("missing connection_id on ReadModel: %+v", r.Session)
			}
			if !attached && r.Session.SessionId == parentID {
				return nil, status.Error(codes.NotFound, "not bound")
			}
			provider := "antigravity"
			return &pb.ModelState{
				Session:  r.Session,
				Provider: &provider,
				Model:    &ext.model,
				Native:   &pb.NativeJson{Utf8Json: []byte(`{"model_switch_supported":true}`)},
			}, nil
		},
		attachCb: func(r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
			if r.Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH || r.Target.GetConnectionId() != connID || r.Session.GetConnectionId() != connID {
				t.Fatalf("unexpected attach request: %+v", r)
			}
			attached = true
			return &pb.SessionReceipt{Attached: true, Session: r.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
		},
		getHistoryPageCb: func(r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
			if r.Session.GetConnectionId() != connID {
				t.Fatalf("missing connection_id on GetHistoryPage: %+v", r.Session)
			}
			userRaw, _ := json.Marshal(map[string]any{"id": "antigravity:" + parentID + ":0", "role": "user", "kind": "message", "text": "hello remote agy"})
			asstRaw, _ := json.Marshal(map[string]any{"id": "antigravity:" + parentID + ":1", "role": "assistant", "kind": "message", "thinking": "remote plan", "text": "remote reply"})
			return &pb.HistoryPageResponse{
				Session: r.Session,
				Items: []*pb.NativeJson{
					{Utf8Json: userRaw},
					{Utf8Json: asstRaw},
				},
			}, nil
		},
		sendCb: func(r *pb.SendRequest) (*pb.CommandReceipt, error) {
			if r.Session.GetConnectionId() != connID {
				t.Fatalf("missing connection_id on Send: %+v", r.Session)
			}
			sent++
			return &pb.CommandReceipt{
				Accepted:       true,
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Session:        r.Session,
				Status:         pb.SessionStatus_SESSION_STATUS_RUNNING,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
			}, nil
		},
		interruptCb: func(r *pb.InterruptRequest) (*pb.CommandReceipt, error) {
			if r.Session.GetConnectionId() != connID {
				t.Fatalf("missing connection_id on Interrupt: %+v", r.Session)
			}
			interrupted++
			return &pb.CommandReceipt{
				Accepted:       true,
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Session:        r.Session,
				Status:         pb.SessionStatus_SESSION_STATUS_RUNNING,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
			}, nil
		},
		deleteCb: func(r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
			if r.Session.GetConnectionId() != connID {
				t.Fatalf("missing connection_id on Delete: %+v", r.Session)
			}
			deleted++
			return &pb.CommandReceipt{
				Accepted:       true,
				OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
				Session:        r.Session,
				Status:         pb.SessionStatus_SESSION_STATUS_IDLE,
				Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
			}, nil
		},
	}
	ext.mockDaemonServer = base

	// Wire RuntimeQuery methods on ext via custom server setup
	h, st, _, cleanup := setupTestServerWithCustomRuntime(t, base, ext)
	defer cleanup()

	ctx := context.Background()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{
		"id":        connID,
		"host":      "127.0.0.1",
		"port":      22,
		"user":      "dev",
		"workspace": "/srv/remote-ws",
		"state":     "connected",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertAgent(ctx, map[string]any{
		"id":            agentID,
		"kind":          "antigravity",
		"name":          "Antigravity · WSL",
		"connection_id": connID,
		"status":        "ready",
	}); err != nil {
		t.Fatal(err)
	}

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer browser-sec")
		req.Header.Set("Origin", "http://localhost")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 1. Create session (Windows DefaultWorkspace "C:/work" falls back to SSH "/srv/remote-ws")
	rec := do(http.MethodPost, "/api/v1/sessions", `{"agent_id":"`+agentID+`","title":"Remote Agy"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", rec.Code, rec.Body.String())
	}

	// 2. Reattach + History read
	rec = do(http.MethodGet, "/api/v1/sessions/"+parentID+"/messages?agent_id="+agentID, "")
	if rec.Code != http.StatusOK || !attached {
		t.Fatalf("history/reattach failed: code=%d attached=%v body=%s", rec.Code, attached, rec.Body.String())
	}
	var histResp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &histResp); err != nil {
		t.Fatal(err)
	}
	if len(histResp.Items) != 3 || histResp.Items[1]["kind"] != "thinking" || histResp.Items[2]["text"] != "remote reply" {
		t.Fatalf("unexpected projected history items: %+v", histResp.Items)
	}

	// Direct check that "antigravity-ssh" also projects thinking & tool kinds identically
	sshProjected := projectHistoryItem(map[string]any{
		"id":       "antigravity:" + parentID + ":2",
		"role":     "assistant",
		"kind":     "message",
		"thinking": "ssh thought",
		"text":     "ssh answer",
	}, agentID, parentID, parentID, agentID, "antigravity-ssh")
	if len(sshProjected) != 2 || sshProjected[0]["kind"] != "thinking" || sshProjected[1]["text"] != "ssh answer" {
		t.Fatalf("antigravity-ssh history projection mismatch: %+v", sshProjected)
	}

	// 3. Model switch & Approval mode switch
	rec = do(http.MethodPost, "/api/v1/sessions/"+parentID+"/model", `{"agent_id":"`+agentID+`","provider":"antigravity","model":"gemini-3-pro"}`)
	if rec.Code != http.StatusOK || ext.setModels != 1 {
		t.Fatalf("set model failed: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(http.MethodPost, "/api/v1/sessions/"+parentID+"/approval-mode", `{"agent_id":"`+agentID+`","mode":"full_access"}`)
	if rec.Code != http.StatusOK || ext.setApprovals != 1 {
		t.Fatalf("set approval failed: %d %s", rec.Code, rec.Body.String())
	}

	// 4. Fork rejected under ACP
	rec = do(http.MethodPost, "/api/v1/sessions/"+parentID+"/fork", `{"agent_id":"`+agentID+`","target_message_id":"antigravity:`+parentID+`:1"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 fork rejection under ACP, got %d %s", rec.Code, rec.Body.String())
	}

	// 5. Send & Interrupt
	rec = do(http.MethodPost, "/api/v1/commands", `{"id":"cmd-send-1","agent_id":"`+agentID+`","session_id":"`+parentID+`","action":"send","text":"ping"}`)
	if rec.Code != http.StatusOK || sent != 1 {
		t.Fatalf("send failed: code=%d sent=%d body=%s", rec.Code, sent, rec.Body.String())
	}
	rec = do(http.MethodPost, "/api/v1/commands", `{"id":"cmd-stop-1","agent_id":"`+agentID+`","session_id":"`+parentID+`","action":"stop","target_id":"`+parentID+`"}`)
	if rec.Code != http.StatusOK || interrupted != 1 {
		t.Fatalf("interrupt failed: code=%d interrupted=%d body=%s", rec.Code, interrupted, rec.Body.String())
	}

	// 6. Delete
	rec = do(http.MethodDelete, "/api/v1/sessions/"+parentID+"?agent_id="+agentID, "")
	if rec.Code != http.StatusOK || deleted != 1 {
		t.Fatalf("delete failed: code=%d deleted=%d body=%s", rec.Code, deleted, rec.Body.String())
	}
}

func TestAntigravityPresenceAndPreUpgradeDaemonCompatibility(t *testing.T) {
	const (
		connID     = "remote-ssh"
		remoteAgID = "ssh-antigravity-remote-ssh"
		localCodex = "local-codex"
	)

	preUpgrade := true
	base := &mockDaemonServer{
		statusCb: func() (*pb.GetStatusResponse, error) {
			owned := true
			codexKind := pb.RuntimeKind_RUNTIME_KIND_CODEX
			codexAgent := localCodex
			snapshots := []*pb.SessionSnapshot{
				{
					Session:      &pb.SessionRef{SessionId: "codex-live-1", AgentId: &codexAgent},
					Status:       pb.SessionStatus_SESSION_STATUS_RUNNING,
					RuntimeOwned: &owned,
					Runtime:      &codexKind,
				},
			}
			if !preUpgrade {
				agySSHKind := pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH
				agyAgent := remoteAgID
				cid := connID
				snapshots = append(snapshots, &pb.SessionSnapshot{
					Session:      &pb.SessionRef{SessionId: "11111111-1111-4111-8111-111111111111", AgentId: &agyAgent, ConnectionId: &cid},
					Status:       pb.SessionStatus_SESSION_STATUS_RUNNING,
					RuntimeOwned: &owned,
					Runtime:      &agySSHKind,
				})
			}
			return &pb.GetStatusResponse{
				DaemonId: "test-daemon",
				Sessions: snapshots,
			}, nil
		},
		readModelCb: func(r *pb.ReadModelRequest) (*pb.ModelState, error) {
			if r.Session.GetSessionId() == "33333333-3333-4333-8333-333333333333" {
				return nil, status.Error(codes.NotFound, "session not attached")
			}
			return &pb.ModelState{}, nil
		},
		queryCb: func(r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
			if r.Target.GetRuntime() == pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH {
				if preUpgrade {
					return nil, status.Error(codes.InvalidArgument, "native query request is invalid")
				}
				return &pb.NativeQueryResponse{
					Target: r.Target,
					Result: &pb.NativeJson{Utf8Json: []byte(`{"items":[{"id":"33333333-3333-4333-8333-333333333333","title":"Remote Discovered","workspace":"/srv/ws","updated_at":"2026-11-18T00:00:00Z","status":"idle"}]}`)},
				}, nil
			}
			if r.GetCodexThreadList() != nil {
				return &pb.NativeQueryResponse{
					Target: r.Target,
					Result: &pb.NativeJson{Utf8Json: []byte(`{"data":[],"nextCursor":null}`)},
				}, nil
			}
			return nil, status.Error(codes.Unimplemented, "unexpected query")
		},
	}

	h, st, client, cleanup := setupTestServer(t, base)
	defer cleanup()
	// Align expected daemon ID with mock Capabilities/Status DaemonId
	_ = client

	ctx := context.Background()
	if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": connID, "host": "10.0.0.2", "port": 22, "user": "ubuntu"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": localCodex, "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": remoteAgID, "kind": "antigravity", "name": "Remote Agy", "connection_id": connID, "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "codex-live-1", "agent_id": localCodex, "status": "running", "control_state": "owned", "updated_at": "2026-11-17T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "11111111-1111-4111-8111-111111111111", "agent_id": remoteAgID, "connection_id": connID, "status": "idle", "control_state": "owned", "updated_at": "2026-11-17T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	// Pre-upgrade small kernel: presence succeeds & keeps local Codex live, RefreshActivity degrades gracefully with no error
	req := httptest.NewRequest(http.MethodGet, "/api/v1/presence", nil)
	req.Header.Set("Authorization", "Bearer browser-sec")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-upgrade presence failed: %d %s", rec.Code, rec.Body.String())
	}
	var pres map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &pres); err != nil {
		t.Fatal(err)
	}
	if len(pres["live"].([]any)) != 1 {
		t.Fatalf("expected 1 live codex session before daemon upgrade, got: %+v", pres["live"])
	}

	report, err := h.RefreshActivity(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range report["agents"].([]map[string]any) {
		if entry["error"] != nil {
			t.Fatalf("expected graceful pre-upgrade activity degradation without error, got: %+v", entry)
		}
	}

	// Post-upgrade small kernel: RUNTIME_KIND_ANTIGRAVITY_SSH matches live presence & discovers remote sessions
	preUpgrade = false
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &pres) != nil {
		t.Fatalf("post-upgrade presence failed: %d %s", rec.Code, rec.Body.String())
	}
	if len(pres["live"].([]any)) != 2 {
		t.Fatalf("expected both codex and remote antigravity live sessions after daemon upgrade, got: %+v", pres["live"])
	}

	report, err = h.RefreshActivity(ctx, remoteAgID)
	if err != nil || report["updated"] != 1 {
		t.Fatalf("expected 1 remote antigravity session discovered after upgrade, got: %+v %v", report, err)
	}
	discovered, err := st.GetSession(ctx, remoteAgID, "33333333-3333-4333-8333-333333333333")
	if err != nil || discovered == nil || discovered["connection_id"] != connID {
		t.Fatalf("discovered remote antigravity session missing connection_id: %+v %v", discovered, err)
	}

	// Unbound discovered remote Antigravity session settings must return 200 instead of 502
	for _, sub := range []string{"model", "approval-mode", "agent-commands"} {
		reqSub := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/33333333-3333-4333-8333-333333333333/"+sub+"?agent_id="+remoteAgID, nil)
		reqSub.Header.Set("Authorization", "Bearer browser-sec")
		recSub := httptest.NewRecorder()
		h.ServeHTTP(recSub, reqSub)
		if recSub.Code != http.StatusOK {
			t.Fatalf("unbound remote antigravity GET /%s returned %d: %s", sub, recSub.Code, recSub.Body.String())
		}
	}
}
