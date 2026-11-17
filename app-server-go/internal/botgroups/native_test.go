package botgroups

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
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
	replied   atomic.Bool
	paged     atomic.Bool
	sentText  atomic.Value
	sendCount atomic.Int32
	sentInput atomic.Value
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
	s.sendCount.Add(1)
	s.sentInput.Store(r.Input)
	s.sentText.Store(r.Input[0].GetText().GetText())
	s.replied.Store(true)
	if _, err := s.store.SetCommandState(ctx, "a", "native-session", r.Meta.OperationId, "completed", nil, false); err != nil {
		return nil, err
	}
	return &pb.CommandReceipt{OperationState: pb.OperationState_OPERATION_STATE_COMPLETED, Outcome: &pb.CommandReceipt_Completed{Completed: &pb.CommandResult{Accepted: true}}}, nil
}
func (s *nativeGroupServer) ReadModel(_ context.Context, r *pb.ReadModelRequest) (*pb.ModelState, error) {
	return &pb.ModelState{Session: r.Session}, nil
}
func (s *nativeGroupServer) GetHistoryPage(_ context.Context, r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	if s.paged.Load() && r.Before.GetValue() == "" {
		more := true
		return &pb.HistoryPageResponse{Session: r.Session, Items: []*pb.NativeJson{native(map[string]any{"id": "newer", "type": "agentMessage", "text": "newer turn"})}, HasMore: &more, NextCursor: &pb.NullableString{State: &pb.NullableString_Value{Value: "older"}}}, nil
	}
	items := []*pb.NativeJson{native(map[string]any{"id": "previous", "type": "agentMessage", "text": "earlier result"})}
	if s.replied.Load() {
		items = append(items,
			native(map[string]any{"id": "question", "type": "userMessage", "text": s.sentText.Load().(string)}),
			native(map[string]any{"id": "interim", "type": "agentMessage", "phase": "commentary", "text": "@其他成员 临时进度"}),
			native(map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "原生结果"}),
			native(map[string]any{"id": "later-question", "type": "userMessage", "text": "其他任务"}),
			native(map[string]any{"id": "later-answer", "type": "agentMessage", "phase": "final_answer", "text": "其他任务的回复"}))
	}
	return &pb.HistoryPageResponse{Session: r.Session, Items: items}, nil
}
func (s *nativeGroupServer) QueryNative(ctx context.Context, r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
	query := r.GetCodexThreadItemsList()
	if query == nil || query.ThreadId != "native-session" || r.Target.GetAgentId() != "a" {
		return nil, errors.New("incorrect native history scope")
	}
	page, err := s.GetHistoryPage(ctx, &pb.HistoryPageRequest{Session: &pb.SessionRef{SessionId: query.ThreadId}, Before: query.Cursor})
	if err != nil {
		return nil, err
	}
	return codexQueryPage(page), nil
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
	controller := &NativeController{Store: st, Client: &daemonclient.Client{Session: pb.NewSessionControlClient(connection), Runtime: pb.NewRuntimeQueryClient(connection)}, Publisher: &publisher{}, PollInterval: 5 * time.Millisecond}
	turn := Turn{GroupID: "g", Member: Member{AgentID: "a", MachineID: "local"}, CommandID: "command", Prompt: "任务"}
	controller.AttachmentsDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(controller.AttachmentsDir, "image.blob"), []byte("picture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddAttachment(ctx, map[string]any{"id": "image", "name": "shot.png", "media_type": "image/png", "storage_name": "image.blob", "size": 7}); err != nil {
		t.Fatal(err)
	}
	turn.Attachments, err = st.AttachmentRows(ctx, []string{"image"})
	if err != nil {
		t.Fatal(err)
	}
	var recordedBaseline []string
	accepted := false
	turn.RecordBaseline = func(ids []string) error { recordedBaseline = ids; return nil }
	turn.RecordAccepted = func() error { accepted = true; return nil }
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
	input := peer.sentInput.Load().([]*pb.InputPart)
	if len(input) != 2 || input[1].GetImageUrl().GetUrl() != "data:image/png;base64,cGljdHVyZQ==" {
		t.Fatalf("image did not reach native send: %+v", input)
	}
	imageCommand, err := st.GetCommand(ctx, "a", "native-session", turn.CommandID)
	if err != nil || len(imageCommand["attachments"].([]any)) != 1 {
		t.Fatalf("command did not retain image metadata: %+v %v", imageCommand, err)
	}
	if len(recordedBaseline) != 1 || recordedBaseline[0] != "previous" || !accepted {
		t.Fatal("native delivery was not durably marked before waiting")
	}
	// A restarted app follows the recorded command without submitting it again.
	turn.SessionID = "native-session"
	turn.Prompt = ""
	accepted = false
	resumed, err := controller.Resume(ctx, turn, recordedBaseline)
	if err != nil || resumed.Text != "原生结果" || !accepted || peer.sendCount.Load() != 1 {
		t.Fatalf("completed command was not safely resumed: %+v %v", resumed, err)
	}
	turn.CommandID = "accepted-command"
	payload := map[string]any{"id": turn.CommandID, "agent_id": "a", "session_id": "native-session", "action": "send", "text": "任务"}
	if _, _, err := st.CreateCommand(ctx, payload, "accepted"); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	time.AfterFunc(15*time.Millisecond, func() {
		_, err := st.SetCommandState(ctx, "a", "native-session", turn.CommandID, "completed", nil, false)
		completed <- err
	})
	accepted = false
	resumed, err = controller.Resume(ctx, turn, recordedBaseline)
	if finishErr := <-completed; finishErr != nil {
		t.Fatal(finishErr)
	}
	if err != nil || resumed.Text != "原生结果" || !accepted || peer.sendCount.Load() != 1 {
		t.Fatalf("accepted command was not safely resumed: %+v %v", resumed, err)
	}
	peer.paged.Store(true)
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
