package grok

import (
	"context"
	"io"
	"testing"

	core "astrorder.dev/session-daemon/internal/runtime"
)

func TestGrokApprovalFailedDeliveryRetainsPending(t *testing.T) {
	r, w := io.Pipe()
	r.Close()
	c := newClient(nil, w, nil)
	defer c.close()
	a := New(Config{})
	owned := &ownedSession{client: c, status: "waiting_approval", pendingApprovals: map[string]*pendingApproval{"grok:1": {method: "session/request_permission", nativeID: float64(1), params: map[string]any{"options": []any{map[string]any{"kind": "allow_once", "optionId": "once"}}}}}, approvalKinds: map[string]string{"grok:1": "session/request_permission"}}
	a.sessions["s"] = owned
	_, err := a.Command(context.Background(), core.Request{SessionID: "s", Action: "session.approve", Fields: map[string]any{"approval_id": "grok:1", "decision": "accept"}})
	if err == nil {
		t.Fatal("failed delivery accepted")
	}
	if owned.pendingApprovals["grok:1"] == nil || owned.status != "waiting_approval" {
		t.Fatal("failed delivery discarded pending approval")
	}
}

func TestGrokApprovalOnlySelectsNativeOnceKind(t *testing.T) {
	params := map[string]any{"options": []any{map[string]any{"kind": "unknown", "optionId": "allow_custom"}, map[string]any{"kind": "allow_always", "optionId": "permanent"}}}
	if id := findOptionID(params, "allow_once", "allow"); id != "" {
		t.Fatalf("selected non-once permission: %q", id)
	}
}
