package agentapi

import (
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"path/filepath"
	"testing"
)

type nativeHistoryFixture struct {
	pb.UnimplementedRuntimeQueryServer
}

func (nativeHistoryFixture) GetHistoryPage(_ context.Context, r *pb.HistoryPageRequest) (*pb.HistoryPageResponse, error) {
	return &pb.HistoryPageResponse{Session: r.Session, Items: []*pb.NativeJson{{Utf8Json: []byte(`{"item":{"id":"msg","type":"agentMessage","text":"AUDIT_OK"}}`)}}}, nil
}
func TestMCPReadProjectsNativeEnvelope(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_, _ = st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "codex", "name": "a", "status": "ready"})
	_, _ = st.UpsertSession(ctx, map[string]any{"id": "s", "agent_id": "a", "status": "idle", "updated_at": "2026-10-13T00:00:00Z"})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterRuntimeQueryServer(server, nativeHistoryFixture{})
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	h := &Handler{options: Options{Store: st, Client: &daemonclient.Client{Runtime: pb.NewRuntimeQueryClient(conn)}}}
	result, err := h.invokeSessionsRead(ctx, map[string]any{"key": "a::s"})
	if err != nil {
		t.Fatal(err)
	}
	items := result.(map[string]any)["items"].([]map[string]any)
	if len(items) != 1 || items[0]["role"] != "assistant" || items[0]["text"] != "AUDIT_OK" {
		t.Fatalf("native messages lost: %+v", items)
	}
}
