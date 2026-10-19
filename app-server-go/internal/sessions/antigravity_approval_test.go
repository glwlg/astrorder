package sessions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAntigravityApprovalSelectionRestoresBeforeSend(t *testing.T) {
	ctx := context.Background()
	peer := &approvalRestorePeer{mockDaemonServer: &mockDaemonServer{}, mode: "manual"}
	h, st, _, cleanup := setupTestServer(t, peer.mockDaemonServer)
	defer cleanup()
	addr, stop := startApprovalRestorePeer(t, peer)
	defer stop()
	client := dialApprovalRestorePeer(t, addr)
	defer client.Close()
	h.client = client
	if _, err := st.UpsertAgent(ctx, map[string]any{"id": "a", "kind": "antigravity", "name": "Antigravity", "status": "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertSession(ctx, map[string]any{"id": "native", "agent_id": "a", "status": "idle", "updated_at": "2026-10-18T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	// No explicit choice must leave native defaults intact.
	row, _ := st.GetSession(ctx, "a", "native")
	if err := h.ensureNativeBinding(ctx, row); err != nil {
		t.Fatal(err)
	}
	if peer.writes != 0 {
		t.Fatal("changed default permission without explicit selection")
	}
	w := httptest.NewRecorder()
	h.handleSetSessionApprovalMode(w, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/native/approval-mode", strings.NewReader(`{"agent_id":"a","mode":"full_access"}`)), "native")
	if w.Code != 200 {
		t.Fatalf("explicit selection: %d %s", w.Code, w.Body.String())
	}
	peer.mode = "manual" // Native transport was resumed with default permissions.
	row, _ = st.GetSession(ctx, "a", "native")
	if err := h.ensureNativeBinding(ctx, row); err != nil {
		t.Fatal(err)
	}
	if peer.mode != "full_access" || peer.writes != 2 {
		t.Fatalf("saved choice not restored: mode=%s writes=%d", peer.mode, peer.writes)
	}
	peer.mode = "manual"
	peer.stale = true
	if err := h.ensureNativeBinding(ctx, row); err == nil {
		t.Fatal("unconfirmed restore allowed a subsequent send")
	}
}
