package botgroups

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type boundNativeServer struct {
	pb.UnimplementedSessionControlServer
	pb.UnimplementedRuntimeQueryServer
	attached     bool
	mismatch     bool
	attaches     int
	cwd          string
	sshWorkspace string
	provider     string
	model        string
	effort       string
	mode         string
	writes       []string
	stale        bool
}

func (s *boundNativeServer) ReadModel(_ context.Context, req *pb.ReadModelRequest) (*pb.ModelState, error) {
	if !s.attached {
		return nil, status.Error(codes.NotFound, "not attached")
	}
	ref := req.Session
	if s.mismatch {
		ref = &pb.SessionRef{SessionId: "parent", AgentId: req.Session.AgentId}
	}
	return &pb.ModelState{Session: ref, Provider: &s.provider, Model: &s.model, Effort: &s.effort}, nil
}
func (s *boundNativeServer) AttachSession(_ context.Context, req *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
	s.attaches++
	s.cwd = req.GetCwd()
	s.sshWorkspace = req.GetTarget().GetSsh().GetWorkspace()
	return &pb.SessionReceipt{Attached: true, Session: req.Session, Status: pb.SessionStatus_SESSION_STATUS_IDLE}, nil
}
func (s *boundNativeServer) SetModel(_ context.Context, req *pb.SetModelRequest) (*pb.CommandReceipt, error) {
	s.writes = append(s.writes, "model:"+req.Session.SessionId)
	if !s.stale {
		s.provider, s.model = req.Provider, req.Model
	}
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
}
func (s *boundNativeServer) SetReasoning(_ context.Context, req *pb.SetReasoningRequest) (*pb.CommandReceipt, error) {
	s.writes = append(s.writes, "effort:"+req.Session.SessionId)
	s.effort = req.Effort
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
}
func (s *boundNativeServer) ReadApprovalMode(_ context.Context, req *pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error) {
	return &pb.ApprovalModeState{Session: req.Session, Mode: s.mode}, nil
}
func (s *boundNativeServer) SetApprovalMode(_ context.Context, req *pb.SetApprovalModeRequest) (*pb.CommandReceipt, error) {
	s.writes = append(s.writes, "approval:"+req.Session.SessionId)
	s.mode = req.Mode
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{}}}, nil
}

func TestBoundForkUsesExistingRuntimeOrAttachesChild(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Agent", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UpsertSession(ctx, map[string]any{"id": "child", "agent_id": "a", "title": "child", "workspace": "/project", "status": "idle", "control_state": "owned", "updated_at": now()}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetPreference(ctx, "group_session:g:one", map[string]any{"agent_id": "a", "session_id": "child"}, true); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	peer := &boundNativeServer{attached: true}
	pb.RegisterSessionControlServer(server, peer)
	pb.RegisterRuntimeQueryServer(server, peer)
	go server.Serve(listener)
	defer server.Stop()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	controller := &NativeController{Store: st, Client: &daemonclient.Client{Session: pb.NewSessionControlClient(connection), Runtime: pb.NewRuntimeQueryClient(connection)}, Publisher: &publisher{}}
	child, workspace := "child", "/project"
	turn := Turn{GroupID: "g", Member: Member{ID: "one", AgentID: "a", MachineID: "local", ForkSessionID: &child, Workspace: &workspace}}
	ref, err := controller.session(ctx, turn)
	if err != nil || ref.SessionId != child || peer.attaches != 0 {
		t.Fatalf("already attached child was replaced: %v %v attaches=%d", ref, err, peer.attaches)
	}
	peer.attached = false
	ref, err = controller.session(ctx, turn)
	if err != nil || ref.SessionId != child || peer.attaches != 1 || peer.cwd != workspace {
		t.Fatalf("wrong attach: %v %v attaches=%d cwd=%q", ref, err, peer.attaches, peer.cwd)
	}
	turn.Member.Workspace = nil
	controller.DefaultWorkspace = "/new-default"
	ref, err = controller.session(ctx, turn)
	if err != nil || peer.cwd != workspace {
		t.Fatalf("bound session changed with default workspace: %v %v cwd=%q", ref, err, peer.cwd)
	}
	turn.Member.Workspace = &workspace
	peer.attached, peer.mismatch = true, true
	if _, err = controller.session(ctx, turn); err == nil {
		t.Fatal("mismatched native identity accepted")
	}
	peer.mismatch, peer.mode = false, "manual"
	provider, model, effort, mode := "openai", "model-new", "high", "auto"
	turn.CommandID = "turn"
	turn.Member.ModelProvider, turn.Member.ModelName = &provider, &model
	turn.Member.ThinkingEffort, turn.Member.ApprovalPolicy = &effort, &mode
	if err := controller.configureMemberSession(ctx, turn, ref); err != nil {
		t.Fatal(err)
	}
	if peer.model != model || peer.provider != provider || peer.effort != effort || peer.mode != mode {
		t.Fatalf("child settings not applied: %+v", peer)
	}
	if got := strings.Join(peer.writes, ","); got != "model:child,effort:child,approval:child" {
		t.Fatalf("settings applied to wrong session: %s", got)
	}
	peer.stale, peer.model = true, "old"
	if err := controller.configureMemberSession(ctx, turn, ref); err == nil {
		t.Fatal("unconfirmed model accepted")
	}
}

type createPerWorkspaceServer struct {
	pb.UnimplementedSessionControlServer
	workspaces []string
}

func (s *createPerWorkspaceServer) CreateSession(_ context.Context, req *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
	workspace := req.GetCodex().GetCwd()
	s.workspaces = append(s.workspaces, workspace)
	return &pb.CommandReceipt{
		Session:        &pb.SessionRef{SessionId: "native-" + workspace},
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Outcome:        &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Native: native(map[string]any{"agent_id": "a"})}},
	}, nil
}

func TestSameAgentCreatesIndependentNativeSessionsInChosenWorkspaces(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Agent", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "bot_group:g", Group{ID: "g"}, true); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	peer := &createPerWorkspaceServer{}
	pb.RegisterSessionControlServer(server, peer)
	go server.Serve(listener)
	defer server.Stop()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	controller := &NativeController{Store: st, Client: &daemonclient.Client{Session: pb.NewSessionControlClient(connection)}, Publisher: &publisher{}}
	one, two := "/one", "/two"
	first, err := controller.session(ctx, Turn{GroupID: "g", CommandID: "first", Member: Member{ID: "one", AgentID: "a", MachineID: "local", Workspace: &one}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := controller.session(ctx, Turn{GroupID: "g", CommandID: "second", Member: Member{ID: "two", AgentID: "a", MachineID: "local", Workspace: &two}})
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionId == second.SessionId || strings.Join(peer.workspaces, ",") != "/one,/two" {
		t.Fatalf("native sessions did not retain workspace: %v %v %v", first, second, peer.workspaces)
	}
	for id, want := range map[string]string{"one": first.SessionId, "two": second.SessionId} {
		preference, err := st.GetPreference(ctx, "group_session:g:"+id)
		if err != nil || preference.Value.(map[string]any)["session_id"] != want {
			t.Fatalf("wrong binding for %s: %v %v", id, preference, err)
		}
	}
}

func TestRemoteBoundSessionReattachesInItsRecordedWorkspace(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.SaveSSHConnection(ctx, map[string]any{"id": "ssh-host", "host": "example.test", "workspace": "/new-default"}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Agent", "status": "ready", "connection_id": "ssh-host"}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UpsertSession(ctx, map[string]any{"id": "child", "agent_id": "a", "title": "child", "workspace": "/original", "connection_id": "ssh-host", "status": "idle", "control_state": "owned", "updated_at": now()}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetPreference(ctx, "group_session:g:one", map[string]any{"agent_id": "a", "session_id": "child", "connection_id": "ssh-host"}, true); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	peer := &boundNativeServer{}
	pb.RegisterSessionControlServer(server, peer)
	pb.RegisterRuntimeQueryServer(server, peer)
	go server.Serve(listener)
	defer server.Stop()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	controller := &NativeController{Store: st, Client: &daemonclient.Client{Session: pb.NewSessionControlClient(connection), Runtime: pb.NewRuntimeQueryClient(connection)}, Publisher: &publisher{}}
	turn := Turn{GroupID: "g", Member: Member{ID: "one", AgentID: "a", MachineID: "ssh-host"}}
	if _, err := controller.session(ctx, turn); err != nil {
		t.Fatal(err)
	}
	if peer.cwd != "/original" || peer.sshWorkspace != "/original" {
		t.Fatalf("wrong remote workspace: cwd=%q ssh=%q", peer.cwd, peer.sshWorkspace)
	}
}
