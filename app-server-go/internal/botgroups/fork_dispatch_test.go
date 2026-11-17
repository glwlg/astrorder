package botgroups

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type forkDispatchServer struct {
	pb.UnimplementedSessionControlServer
	pb.UnimplementedRuntimeQueryServer
	store *store.Store
	sent  atomic.Bool
	text  atomic.Value
}

func (s *forkDispatchServer) ReadModel(_ context.Context, r *pb.ReadModelRequest) (*pb.ModelState, error) {
	if r.Session.GetSessionId() != "child" {
		return nil, fmt.Errorf("unexpected source model query")
	}
	return &pb.ModelState{Session: r.Session}, nil
}
func (s *forkDispatchServer) ReadApprovalMode(_ context.Context, r *pb.ReadApprovalModeRequest) (*pb.ApprovalModeState, error) {
	if r.Session.GetSessionId() != "child" {
		return nil, fmt.Errorf("unexpected source approval query")
	}
	return &pb.ApprovalModeState{Session: r.Session, Mode: "manual"}, nil
}
func (s *forkDispatchServer) GetHistoryPage(_ context.Context, r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	if r.Session.GetSessionId() != "child" {
		return nil, fmt.Errorf("unexpected source history query")
	}
	var items []*pb.NativeJson
	if s.sent.Load() {
		items = []*pb.NativeJson{
			native(map[string]any{"id": "question", "type": "userMessage", "text": s.text.Load().(string)}),
			native(map[string]any{"id": "reply", "type": "agentMessage", "text": "子会话结果"}),
		}
	}
	return &pb.HistoryPageResponse{Session: r.Session, Items: items}, nil
}
func (s *forkDispatchServer) QueryNative(ctx context.Context, r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
	query := r.GetCodexThreadItemsList()
	if query == nil || query.ThreadId != "child" || r.Target.GetAgentId() != "a" {
		return nil, fmt.Errorf("unexpected source history query")
	}
	page, err := s.GetHistoryPage(ctx, &pb.HistoryPageRequest{Session: &pb.SessionRef{SessionId: query.ThreadId}})
	if err != nil {
		return nil, err
	}
	return codexQueryPage(page), nil
}
func (s *forkDispatchServer) Send(ctx context.Context, r *pb.SendRequest) (*pb.CommandReceipt, error) {
	if r.Session.GetSessionId() != "child" || r.Session.GetAgentId() != "a" || r.Policy.GetApprovalPolicy() != "" {
		return nil, fmt.Errorf("wrong native send scope or approval policy")
	}
	s.text.Store(r.Input[0].GetText().GetText())
	s.sent.Store(true)
	if _, err := s.store.SetCommandState(ctx, "a", "child", r.Meta.GetOperationId(), "completed", nil, false); err != nil {
		return nil, err
	}
	return &pb.CommandReceipt{Session: r.Session, Accepted: true, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
}

func TestForkMemberDispatchUsesOnlyChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, unused := forkFixture(t)
	unused.Close()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Codex", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	before, err := st.GetSession(ctx, "a", "parent")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	peer := &forkDispatchServer{store: st}
	pb.RegisterSessionControlServer(server, peer)
	pb.RegisterRuntimeQueryServer(server, peer)
	go server.Serve(listener)
	defer server.Stop()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	pub := &publisher{}
	controller := &NativeController{Store: st, Client: &daemonclient.Client{Session: pb.NewSessionControlClient(connection), Runtime: pb.NewRuntimeQueryClient(connection)}, Publisher: pub, PollInterval: time.Millisecond}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: pub, Decider: sendAllDecider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	member := forkMember()
	mode := "manual"
	member.ApprovalPolicy = &mode
	group, err := service.create(ctx, forkPayload(t, member))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"@Codex-1 处理"`)}); err != nil {
		t.Fatal(err)
	}
	for {
		group, err = service.get(ctx, group.ID)
		if err != nil {
			t.Fatal(err)
		}
		state := group.MemberStates["one"]
		if state.Status == "success" {
			break
		}
		if state.Status == "failed" {
			t.Fatalf("child dispatch failed: %s", state.LastError)
		}
		select {
		case <-ctx.Done():
			t.Fatal("child dispatch timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
	messages, err := service.messages(ctx, group.ID)
	if err != nil || len(messages) != 2 || messages[1].Text != "子会话结果" || messages[1].SessionID != "child" || messages[1].SenderMemberID != "one" {
		t.Fatalf("wrong group replies: %+v %v", messages, err)
	}
	after, err := st.GetSession(ctx, "a", "parent")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("parent changed: %v %v", after, err)
	}
	scope, err := st.GetPreference(ctx, "blackboard_scope:a::parent")
	if err != nil || scope.Value != "session::parent" {
		t.Fatalf("parent scope changed: %+v %v", scope, err)
	}
}
