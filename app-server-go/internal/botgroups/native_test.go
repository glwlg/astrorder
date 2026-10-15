package botgroups

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type nativeGroupServer struct {
	pb.UnimplementedSessionControlServer
	pb.UnimplementedRuntimeQueryServer
	store     *store.Store
	uncertain bool
	replied   bool
	paged     bool
}

func (s *nativeGroupServer) CreateSession(_ context.Context, r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
	if r.Meta.Canonicalization != pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
		return nil, errors.New("missing canonicalization")
	}
	if s.uncertain {
		return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_UNCERTAIN, Outcome: &pb.CommandReceipt_Uncertain{Uncertain: &pb.BusinessError{}}}, nil
	}
	return &pb.CommandReceipt{Session: &pb.SessionRef{SessionId: "native-session"}, OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Native: native(map[string]any{"agent_id": "a"})}}}, nil
}
func (s *nativeGroupServer) Send(ctx context.Context, r *pb.SendRequest) (*pb.CommandReceipt, error) {
	if r.Session.GetAgentId() != "a" || r.Session.SessionId != "native-session" {
		return nil, errors.New("incorrect native scope")
	}
	if r.Meta.CommandId == nil || r.Meta.OperationId != *r.Meta.CommandId {
		return nil, errors.New("incorrect native command identity")
	}
	s.replied = true
	if _, err := s.store.SetCommandState(ctx, "a", "native-session", r.Meta.OperationId, "completed", nil, false); err != nil {
		return nil, err
	}
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
}
func (s *nativeGroupServer) GetHistoryPage(_ context.Context, r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	if s.paged && r.Before.GetValue() == "" {
		more := true
		return &pb.HistoryPageResponse{Session: r.Session, Items: []*pb.NativeJson{native(map[string]any{"id": "newer", "type": "agentMessage", "text": "newer turn"})}, HasMore: &more, NextCursor: &pb.NullableString{State: &pb.NullableString_Value{Value: "older"}}}, nil
	}
	items := []*pb.NativeJson{native(map[string]any{"id": "previous", "type": "agentMessage", "text": "earlier result"})}
	if s.replied {
		items = append(items, native(map[string]any{"id": "answer", "type": "agentMessage", "text": "原生结果"}))
	}
	return &pb.HistoryPageResponse{Session: r.Session, Items: items}, nil
}
func TestNativeLateSendReceiptPreservesAlreadyCompletedCommand(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err = st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "甲", "status": "connected"}); err != nil {
		t.Fatal(err)
	}
	if err = st.SetPreference(ctx, "bot_group:g", Group{ID: "g", Name: "群", Members: []Member{{AgentID: "a", MachineID: "local"}}}, true); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	peer := &nativeGroupServer{store: st}
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
	turn := Turn{GroupID: "g", Member: Member{AgentID: "a", MachineID: "local"}, CommandID: "command", Prompt: "任务"}
	reply, err := controller.Run(ctx, turn, func(session string) error {
		if session != "native-session" {
			return errors.New("wrong native binding")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply.Text != "原生结果" || len(reply.NativeIDs) != 1 || reply.NativeIDs[0] != "answer" {
		t.Fatal(reply)
	}
	peer.paged = true
	resolved, err := controller.ReadReply(ctx, "a", "native-session", reply.NativeIDs)
	if err != nil || resolved != reply.Text {
		t.Fatal("older native reply not restored", resolved, err)
	}
	if _, err := controller.ReadReply(ctx, "a", "native-session", []string{"missing"}); err == nil {
		t.Fatal("missing native reply accepted")
	}
	command, err := st.GetCommand(ctx, "a", "native-session", "command")
	if err != nil || command["state"] != "completed" {
		t.Fatalf("late receipt regressed state: %+v %v", command, err)
	}
	binding, err := st.GetPreference(ctx, "blackboard_scope:a::native-session")
	if err != nil || binding.Value != "group::g" {
		t.Fatal("shared blackboard binding missing")
	}
}
