package botgroups

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func codexQueryPage(page *pb.HistoryPageResponse) *pb.NativeQueryResponse {
	data := make([]json.RawMessage, 0, len(page.Items))
	for i := len(page.Items) - 1; i >= 0; i-- {
		data = append(data, json.RawMessage(page.Items[i].Utf8Json))
	}
	var next any
	if page.GetHasMore() {
		next = page.NextCursor.GetValue()
	}
	return &pb.NativeQueryResponse{Result: native(map[string]any{"data": data, "nextCursor": next})}
}

type readonlyGroupHistoryClient struct {
	pb.RuntimeQueryClient
	query      func(*pb.NativeQueryRequest) (*pb.NativeQueryResponse, error)
	ownedReads int
}

func (c *readonlyGroupHistoryClient) QueryNative(_ context.Context, request *pb.NativeQueryRequest, _ ...grpc.CallOption) (*pb.NativeQueryResponse, error) {
	return c.query(request)
}
func (c *readonlyGroupHistoryClient) GetHistoryPage(context.Context, *pb.HistoryPageRequest, ...grpc.CallOption) (*pb.HistoryPageResponse, error) {
	c.ownedReads++
	return nil, status.Error(codes.NotFound, "session is not daemon-owned")
}

func TestGroupRepliesReadDormantCodexHistoryWithoutAttaching(t *testing.T) {
	for _, connection := range []string{"local", "ssh-host"} {
		t.Run(connection, func(t *testing.T) {
			ctx := context.Background()
			st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if connection != "local" {
				if _, err := st.SaveSSHConnection(ctx, map[string]any{"id": connection, "host": "example.test", "profile_name": "work", "workspace": "/connection-default"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "Codex", "status": "ready", "connection_id": connection}); err != nil {
				t.Fatal(err)
			}
			before, err := st.UpsertSession(ctx, map[string]any{"id": "child", "agent_id": "a", "connection_id": connection, "workspace": "/original", "title": "群会话", "status": "idle", "control_state": "owned", "updated_at": now()})
			if err != nil {
				t.Fatal(err)
			}
			var cursors []string
			peer := &readonlyGroupHistoryClient{}
			peer.query = func(request *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
				query := request.GetCodexThreadItemsList()
				if query == nil || query.ThreadId != "child" || query.GetSortDirection() != "desc" ||
					query.Limit != 200 || request.Target.GetAgentId() != "a" {
					t.Fatalf("history scope changed: %v", request)
				}
				if connection == "local" {
					if request.Target.Runtime != pb.RuntimeKind_RUNTIME_KIND_CODEX || request.Target.ConnectionId != nil {
						t.Fatal("local history used a remote identity")
					}
				} else if request.Target.Runtime != pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH ||
					request.Target.GetConnectionId() != connection || request.Target.GetSsh().GetWorkspace() != "/original" ||
					request.Target.GetSsh().GetProfileName() != "work" {
					t.Fatalf("SSH history target changed: %v", request.Target)
				}
				cursors = append(cursors, query.Cursor.GetValue())
				if query.Cursor.GetValue() == "" {
					return &pb.NativeQueryResponse{Result: native(map[string]any{"data": []any{
						map[string]any{"id": "unrelated", "type": "agentMessage", "text": "其他回复"},
						map[string]any{"item": map[string]any{"id": "second", "type": "agentMessage", "phase": "final_answer", "text": "第二段"}},
					}, "nextCursor": "older:opaque"})}, nil
				}
				return &pb.NativeQueryResponse{Result: native(map[string]any{"data": []any{
					map[string]any{"id": "first", "type": "agentMessage", "phase": "final_answer", "text": "第一段\n保留换行"},
				}, "nextCursor": nil})}, nil
			}
			// No SessionControl client is supplied: a history read cannot attach,
			// resume, interrupt or send to either the child or its source.
			controller := &NativeController{Store: st, Client: &daemonclient.Client{Runtime: peer}, DefaultWorkspace: "/changed-default"}
			text, err := controller.ReadReply(ctx, "a", "child", []string{"first", "second"})
			if err != nil || text != "第一段\n保留换行\n\n第二段" {
				t.Fatalf("dormant native replies unavailable: %q %v", text, err)
			}
			if peer.ownedReads != 0 || !reflect.DeepEqual(cursors, []string{"", "older:opaque"}) {
				t.Fatalf("history required ownership or lost pagination: owned=%d cursors=%v", peer.ownedReads, cursors)
			}
			after, err := st.GetSession(ctx, "a", "child")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("reading history changed session state: %v", err)
			}
			if _, err := controller.ReadReply(ctx, "a", "child", []string{"missing"}); err == nil {
				t.Fatal("missing native reply was silently accepted")
			}
			peer.query = func(*pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
				return nil, status.Error(codes.Unavailable, "native offline")
			}
			if _, err := controller.ReadReply(ctx, "a", "child", []string{"first"}); status.Code(err) != codes.Unavailable || peer.ownedReads != 0 {
				t.Fatalf("native failure was hidden or used an owned read: %v", err)
			}
		})
	}
}
