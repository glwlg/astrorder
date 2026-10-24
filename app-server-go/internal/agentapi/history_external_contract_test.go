package agentapi

import (
	"context"
	"fmt"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestExternalHermesHistoryRejectsWrongIdentityAndProfile(t *testing.T) {
	for _, tc := range []struct{ name, sid, profile string }{
		{"wrong-session", "other-session", "default"},
		{"wrong-profile", "external-history", "other-profile"},
		{"missing-profile", "external-history", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &unownedHistoryDaemonServer{attached: map[string]bool{}, hermesPayload: map[string]any{"result": map[string]any{"session_id": tc.sid, "messages": []any{}, "info": map[string]any{"profile_name": tc.profile}}}}
			h, st, cleanup := setupUnownedServer(t, srv)
			defer cleanup()
			ctx := context.Background()
			if _, err := st.UpsertAgent(ctx, map[string]any{"id": "hermes-test", "name": "Test Hermes", "kind": "hermes", "status": "ready"}); err != nil {
				t.Fatal(err)
			}
			if _, err := st.UpsertSession(ctx, map[string]any{"id": "external-history", "agent_id": "hermes-test", "status": "idle", "control_state": "external", "updated_at": "2026-10-19T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
			if _, err := h.invokeSessionsRead(ctx, map[string]any{"key": "hermes-test::external-history"}); err == nil {
				t.Fatal("foreign native identity/profile accepted")
			}
			if srv.attachCount != 0 {
				t.Fatalf("external history attached %d times", srv.attachCount)
			}
		})
	}
}

func TestExternalHermesHistoryPagesRemainStableAndDoNotPersistMessages(t *testing.T) {
	srv := &unownedHistoryDaemonServer{attached: map[string]bool{}}
	for i := 1; i <= 7; i++ {
		srv.hermesItems = append(srv.hermesItems, &pb.NativeJson{Utf8Json: []byte(fmt.Sprintf(`{"row_id":%d,"role":"assistant","content":"row-%d"}`, i, i))})
	}
	h, st, cleanup := setupUnownedServer(t, srv)
	defer cleanup()
	ctx := context.Background()
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "hermes-test", "name": "Test Hermes", "kind": "hermes", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "external-history", "agent_id": "hermes-test", "status": "idle", "control_state": "external", "updated_at": "2026-10-19T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	page, err := h.invokeSessionsRead(ctx, map[string]any{"key": "hermes-test::external-history", "limit": 3})
	if err != nil {
		t.Fatal(err)
	}
	first := page.(map[string]any)
	if first["has_more"] != true {
		t.Fatal("missing continuation")
	}
	srv.mu.Lock()
	srv.hermesItems = append(srv.hermesItems, &pb.NativeJson{Utf8Json: []byte(`{"row_id":8,"role":"assistant","content":"new-row"}`)})
	srv.mu.Unlock()
	page, err = h.invokeSessionsRead(ctx, map[string]any{"key": "hermes-test::external-history", "limit": 3, "before": first["next_cursor"]})
	if err != nil {
		t.Fatal(err)
	}
	second := page.(map[string]any)
	items := second["items"].([]map[string]any)
	if len(items) != 3 || items[0]["text"] != "row-2" || items[2]["text"] != "row-4" {
		t.Fatalf("unstable older page: %+v", items)
	}
	page, err = h.invokeSessionsRead(ctx, map[string]any{"key": "hermes-test::external-history", "limit": 3, "before": second["next_cursor"]})
	if err != nil {
		t.Fatal(err)
	}
	last := page.(map[string]any)
	if last["has_more"] != false || last["count"] != 1 {
		t.Fatalf("incorrect terminal page: %+v", last)
	}
	var count int
	if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM messages WHERE agent_id = ? AND session_id = ?", "hermes-test", "external-history").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || srv.attachCount != 0 {
		t.Fatalf("read wrote messages=%d or attached=%d", count, srv.attachCount)
	}
	if srv.lastTarget.GetAgentId() != "hermes-test" || srv.lastTarget.GetRuntime() != pb.RuntimeKind_RUNTIME_KIND_HERMES {
		t.Fatal("native target identity not preserved")
	}
}
