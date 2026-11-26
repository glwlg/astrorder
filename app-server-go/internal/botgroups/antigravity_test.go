package botgroups

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type antigravityGroupServer struct {
	pb.UnimplementedSessionControlServer
	pb.UnimplementedRuntimeQueryServer

	mu            sync.Mutex
	store         *store.Store
	createdTarget *pb.RuntimeTarget
	createdOpts   *pb.AntigravityCreateOptions
	setModelReq   *pb.SetModelRequest
	setReasonReq  *pb.SetReasoningRequest
	setApproval   *pb.SetApprovalModeRequest
	sendReq       *pb.SendRequest
	historyItems  []map[string]any
}

func (s *antigravityGroupServer) CreateSession(_ context.Context, req *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createdTarget = req.GetTarget()
	s.createdOpts = req.GetAntigravity()
	agentID := req.GetTarget().GetAgentId()
	raw, _ := json.Marshal(map[string]any{"agent_id": agentID})
	return &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Session: &pb.SessionRef{
			SessionId:    "agy-group-sess-1",
			AgentId:      &agentID,
			ConnectionId: req.GetTarget().ConnectionId,
		},
		Outcome: &pb.CommandReceipt_Completed{
			Completed: &pb.CommandResult{
				Native: &pb.NativeJson{Utf8Json: raw},
			},
		},
	}, nil
}

func (s *antigravityGroupServer) ReadModel(_ context.Context, req *pb.ReadModelRequest) (*pb.ModelState, error) {
	provider := "antigravity"
	model := "gemini-4-neon-high"
	return &pb.ModelState{
		Session:  req.GetSession(),
		Provider: &provider,
		Model:    &model,
	}, nil
}

func (s *antigravityGroupServer) SetModel(_ context.Context, req *pb.SetModelRequest) (*pb.CommandReceipt, error) {
	s.mu.Lock()
	s.setModelReq = req
	s.mu.Unlock()
	return &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
	}, nil
}

func (s *antigravityGroupServer) SetReasoning(_ context.Context, req *pb.SetReasoningRequest) (*pb.CommandReceipt, error) {
	s.mu.Lock()
	s.setReasonReq = req
	s.mu.Unlock()
	return &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
	}, nil
}

func (s *antigravityGroupServer) ReadApprovalMode(_ context.Context, req *pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error) {
	s.mu.Lock()
	mode := "manual"
	if s.setApproval != nil {
		mode = s.setApproval.GetMode()
	}
	s.mu.Unlock()
	return &pb.ApprovalModeState{
		Session: req.GetSession(),
		Mode:    mode,
	}, nil
}

func (s *antigravityGroupServer) SetApprovalMode(_ context.Context, req *pb.SetApprovalModeRequest) (*pb.CommandReceipt, error) {
	s.mu.Lock()
	s.setApproval = req
	s.mu.Unlock()
	return &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
	}, nil
}

func (s *antigravityGroupServer) Send(ctx context.Context, req *pb.SendRequest) (*pb.CommandReceipt, error) {
	s.mu.Lock()
	s.sendReq = req
	prompt := ""
	for _, block := range req.GetInput() {
		if t := block.GetText(); t != nil {
			prompt = t.GetText()
		}
	}
	s.historyItems = []map[string]any{
		{"id": "agy:1", "role": "user", "kind": "message", "text": prompt + "\n\n[Image attached at: /tmp/img.png]"},
		{"id": "agy:2", "role": "assistant", "kind": "message", "text": "AGY 群聊回复正常"},
	}
	s.mu.Unlock()
	go func() {
		time.Sleep(15 * time.Millisecond)
		_, _ = s.store.SetCommandState(context.Background(), req.GetSession().GetAgentId(), req.GetSession().GetSessionId(), req.GetMeta().GetOperationId(), "completed", nil, false)
	}()
	return &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}},
	}, nil
}

func (s *antigravityGroupServer) GetHistoryPage(_ context.Context, req *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	s.mu.Lock()
	items := make([]*pb.NativeJson, 0, len(s.historyItems))
	for _, item := range s.historyItems {
		raw, _ := json.Marshal(item)
		items = append(items, &pb.NativeJson{Utf8Json: raw})
	}
	s.mu.Unlock()
	return &pb.HistoryPageResponse{
		Session: req.GetSession(),
		Items:   items,
	}, nil
}

type fakeAgyExecutableResolver struct {
	path string
}

func (f *fakeAgyExecutableResolver) Resolve(_ context.Context, kind string, _ map[string]any) (string, error) {
	if kind == "antigravity" {
		return f.path, nil
	}
	return "", nil
}

func TestAntigravitySupportedInBotGroupsLocalAndSSH(t *testing.T) {
	for _, tc := range []struct {
		name        string
		machine     string
		agentID     string
		kind        string
		wantRuntime pb.RuntimeKind
	}{
		{name: "local", machine: "local", agentID: "local-antigravity", kind: "antigravity", wantRuntime: pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY},
		{name: "ssh", machine: "wsl-ssh", agentID: "ssh-antigravity-wsl-ssh", kind: "antigravity-ssh", wantRuntime: pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			connID := ""
			if tc.machine != "local" {
				connID = tc.machine
				if _, err := st.SaveSSHConnection(ctx, map[string]any{
					"id":        tc.machine,
					"host":      "127.0.0.1",
					"port":      22,
					"user":      "remoteuser",
					"workspace": "/srv/group-ws",
				}); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := st.UpsertAgent(ctx, map[string]any{
				"id":            tc.agentID,
				"kind":          tc.kind,
				"name":          "Antigravity",
				"connection_id": connID,
				"status":        "ready",
			}); err != nil {
				t.Fatal(err)
			}

			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			srv := grpc.NewServer()
			peer := &antigravityGroupServer{store: st}
			pb.RegisterSessionControlServer(srv, peer)
			pb.RegisterRuntimeQueryServer(srv, peer)
			go srv.Serve(lis)
			defer srv.Stop()

			conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithInsecure())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()

			controller := &NativeController{
				Store:            st,
				Client:           &daemonclient.Client{Session: pb.NewSessionControlClient(conn), Runtime: pb.NewRuntimeQueryClient(conn)},
				Publisher:        &publisher{},
				PollInterval:     5 * time.Millisecond,
				DefaultWorkspace: "P:/WinDefault",
				RemoteExecutable: &fakeAgyExecutableResolver{path: "/usr/local/bin/agy"},
			}

			svc, err := New(ctx, Options{
				Store:      st,
				Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil),
				Publisher:  &publisher{},
				Controller: controller,
				Decider:    sendAllDecider,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer svc.Close()

			// 1. Creating a Bot Group with an Antigravity member succeeds.
			createBody := `{"name":"AGY 群聊","members":[{"id":"m1","agent_id":"` + tc.agentID + `","machine_id":"` + tc.machine + `","alias":"AGY-1","model_provider":"antigravity","model_name":"gemini-4-neon-high","approval_policy":"full_access"}]}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/bot-groups", strings.NewReader(createBody))
			req.Header.Set("Authorization", "Bearer fixture")
			rec := httptest.NewRecorder()
			svc.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
				t.Fatalf("expected group create to succeed for Antigravity, got %d: %s", rec.Code, rec.Body.String())
			}
			var created struct {
				Group Group `json:"group"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Group.ID == "" {
				t.Fatalf("invalid create response: %s", rec.Body.String())
			}

			// 2. NativeController.Run creates session, configures approval/model without failing on effort, sends, and extracts reply.
			effort := "high"
			modelName := "gemini-4-neon-high"
			provider := "antigravity"
			approval := "full_access"
			member := Member{
				ID:             "m1",
				AgentID:        tc.agentID,
				MachineID:      tc.machine,
				ModelProvider:  &provider,
				ModelName:      &modelName,
				ThinkingEffort: &effort,
				ApprovalPolicy: &approval,
			}
			reply, err := controller.Run(ctx, Turn{
				GroupID:   created.Group.ID,
				Member:    member,
				CommandID: "cmd-agy-1",
				Prompt:    "请分析一下架构",
			}, func(string) error { return nil })
			if err != nil {
				t.Fatalf("NativeController.Run for Antigravity failed: %v", err)
			}
			if reply.Text != "AGY 群聊回复正常" {
				t.Fatalf("unexpected AGY group reply: %+v", reply)
			}

			peer.mu.Lock()
			defer peer.mu.Unlock()
			if peer.createdTarget.GetRuntime() != tc.wantRuntime {
				t.Fatalf("runtime = %v, want %v", peer.createdTarget.GetRuntime(), tc.wantRuntime)
			}
			if tc.machine != "local" && peer.createdTarget.GetSsh().GetAntigravityExecutable() != "/usr/local/bin/agy" {
				t.Fatalf("expected resolved SSH antigravity_executable, got %q", peer.createdTarget.GetSsh().GetAntigravityExecutable())
			}
			if peer.setReasonReq != nil {
				t.Fatalf("expected SetReasoning to be skipped when Antigravity model is set, got %+v", peer.setReasonReq)
			}
			if peer.setApproval == nil || peer.setApproval.GetMode() != "full_access" {
				t.Fatalf("expected full_access approval mode to be applied, got %+v", peer.setApproval)
			}
			if peer.sendReq == nil || peer.sendReq.GetPolicy() != nil {
				t.Fatalf("expected Antigravity Send to omit SendPolicy.Model, got %+v", peer.sendReq.GetPolicy())
			}
		})
	}
}
