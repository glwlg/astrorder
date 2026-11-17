package botgroups

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
)

type legacyHistoryServer struct {
	pb.UnimplementedRuntimeQueryServer
	prompt string
}

func (s *legacyHistoryServer) GetHistoryPage(_ context.Context, r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	return &pb.HistoryPageResponse{Session: r.Session, Items: []*pb.NativeJson{
		native(map[string]any{"id": "user", "type": "userMessage", "text": s.prompt}),
		native(map[string]any{"id": "progress", "type": "agentMessage", "phase": "commentary", "text": "处理中"}),
		native(map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "已完成", "timestamp": "2026-11-11T08:50:00Z"}),
		native(map[string]any{"id": "next-user", "type": "userMessage", "text": "另一项任务"}),
		native(map[string]any{"id": "next-answer", "type": "agentMessage", "phase": "final_answer", "text": "其他回复"}),
	}}, nil
}

func TestRecoverTimeoutRequiresExactCompletedCommandAndFinalReply(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "甲", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	member := Member{AgentID: "a", MachineID: "local"}
	group := Group{ID: "g", Members: []Member{member}}
	if err := st.SetPreference(ctx, "group_session:g:a", map[string]any{"agent_id": "a", "session_id": "native"}, true); err != nil {
		t.Fatal(err)
	}
	prompt := "共享黑板 session_key=group::g\n当前任务：请处理"
	payload := map[string]any{"id": "cmd-g-one", "agent_id": "a", "session_id": "native", "action": "send", "text": prompt}
	if _, _, err := st.CreateCommand(ctx, payload, "completed"); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	end := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	user := Message{Text: "请处理", CreatedAt: start}
	failure := Message{SessionID: "native", CreatedAt: end}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterRuntimeQueryServer(server, &legacyHistoryServer{prompt: prompt})
	go server.Serve(listener)
	defer server.Stop()
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	controller := &NativeController{Store: st, Client: &daemonclient.Client{Runtime: pb.NewRuntimeQueryClient(connection)}}
	reply, err := controller.RecoverTimeout(ctx, group, member, user, failure)
	if err != nil || reply.Text != "已完成" || len(reply.NativeIDs) != 1 || reply.NativeIDs[0] != "answer" {
		t.Fatalf("wrong recovered reply: %+v %v", reply, err)
	}
	payload["id"] = "cmd-g-two"
	if _, _, err := st.CreateCommand(ctx, payload, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RecoverTimeout(ctx, group, member, user, failure); err == nil {
		t.Fatal("ambiguous commands were accepted")
	}
}

type recoveredController struct {
	*stagedController
	repliesByMember map[string]Reply
}

func (c *recoveredController) RecoverTimeout(_ context.Context, _ Group, member Member, _, _ Message) (Reply, error) {
	reply, ok := c.repliesByMember[memberID(member)]
	if !ok {
		return Reply{}, errors.New("reply unavailable")
	}
	return reply, nil
}

func TestRecoverTimeoutRestoresReferencesWithoutReplayingOldCommands(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	base := &stagedController{started: make(chan Turn, 2), replies: map[string]chan Reply{}, texts: map[string]string{
		"design-reply": "@开发 设计结果", "developer-reply": "@设计 等待设计",
	}}
	controller := &recoveredController{stagedController: base, repliesByMember: map[string]Reply{
		"design":    {Text: "@开发 设计结果", NativeIDs: []string{"design-reply"}, CreatedAt: "2026-11-11T08:54:00Z"},
		"developer": {Text: "@设计 等待设计", NativeIDs: []string{"developer-reply"}, CreatedAt: "2026-11-11T08:53:00Z"},
	}}
	waitDecider := decisionFunc(func(_ context.Context, input DecisionInput) (Decision, error) {
		result := Decision{Send: map[string]bool{}}
		for _, candidate := range input.Candidates {
			result.Send[candidate.MemberID] = false
		}
		return result, nil
	})
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: waitDecider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{
		"name":    json.RawMessage(`"群"`),
		"members": json.RawMessage(`[{"id":"design","machine_id":"local","agent_id":"a","alias":"设计"},{"id":"developer","machine_id":"local","agent_id":"b","alias":"开发"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	user := Message{ID: "user", GroupID: group.ID, SenderType: "user", SenderName: "用户", Text: "@设计 @开发 先设计", CreatedAt: "2026-11-11T08:45:00Z"}
	if err := service.appendMessage(ctx, user); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"design", "developer"} {
		failure := Message{ID: "error-" + id, GroupID: group.ID, SenderType: "system", SenderMemberID: id, SenderName: id, SessionID: "session-" + id, Text: "执行失败：context deadline exceeded", HopCount: 1, CreatedAt: "2026-11-11T08:47:00Z"}
		if err := service.appendMessage(ctx, failure); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.mutate(ctx, group.ID, func(g *Group) error {
		for _, id := range []string{"design", "developer"} {
			g.MemberStates[id] = MemberState{Status: "failed", SessionID: "session-" + id, LastError: "context deadline exceeded"}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.recoverTimedOut(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	waitGroup(t, service, group.ID, func(g Group) bool { return g.Coordination != nil && g.Coordination.Status == "waiting" })
	messages, err := service.messages(ctx, group.ID)
	if err != nil || len(messages) != 5 || messages[3].Text != "@设计 等待设计" || messages[4].Text != "@开发 设计结果" {
		t.Fatalf("historical replies were not restored in order: %+v %v", messages, err)
	}
	if _, err := service.recoverTimedOut(ctx, group.ID); err == nil {
		t.Fatal("same timeout was recovered twice")
	}
	select {
	case <-controller.started:
		t.Fatal("recovery resent an old command")
	default:
	}
}
