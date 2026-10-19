package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"testing"
)

func TestRefreshAntigravityDiscoversMetadataAndPreservesLiveState(t *testing.T) {
	calls := 0
	srv := &mockDaemonServer{queryCb: func(r *pb.NativeQueryRequest) (*pb.NativeQueryResponse, error) {
		calls++
		if r.GetAntigravitySessionList() == nil || r.Target.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY {
			t.Errorf("wrong native selector: %T", r.Query)
		}
		return &pb.NativeQueryResponse{Result: &pb.NativeJson{Utf8Json: []byte(`{"items":[{"id":"11111111-1111-4111-8111-111111111111","title":"native title","workspace":"P:/work","updated_at":"2026-10-18T02:00:00Z","status":"idle"},{"id":"22222222-2222-4222-8222-222222222222","title":"new native","workspace":"P:/work","updated_at":"2026-10-18T03:00:00Z","status":"idle"}]}`)}}, nil
	}}
	h, st, _, cleanup := setupTestServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "agy", "kind": "antigravity", "name": "Antigravity", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "11111111-1111-4111-8111-111111111111", "agent_id": "agy", "title": "my title", "status": "running", "updated_at": "2026-10-17T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	report, err := h.RefreshActivity(ctx, "agy")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || report["updated"] != 2 {
		t.Fatalf("refresh skipped: %+v calls=%d", report, calls)
	}
	row, _ := st.GetSession(ctx, "agy", "11111111-1111-4111-8111-111111111111")
	if row["status"] != "running" || row["title"] != "my title" {
		t.Fatalf("live state overwritten: %+v", row)
	}
	fresh, _ := st.GetSession(ctx, "agy", "22222222-2222-4222-8222-222222222222")
	if fresh == nil || fresh["source_session_id"] != fresh["id"] || fresh["control_state"] != "unbound" {
		t.Fatalf("new native metadata: %+v", fresh)
	}
	if _, err := st.DeleteSession(ctx, "agy", "22222222-2222-4222-8222-222222222222"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.RefreshActivity(ctx, "agy"); err != nil {
		t.Fatal(err)
	}
	fresh, _ = st.GetSession(ctx, "agy", "22222222-2222-4222-8222-222222222222")
	if fresh != nil {
		t.Fatal("deleted session resurrected")
	}
}
