package codex

import (
	"encoding/json"
	"testing"
)

func TestNativeApprovalKeysDoNotMergeNumericAndTextIDs(t *testing.T) {
	r := New(Config{})
	defer r.Close()
	r.setSession("thread", "idle")
	for _, id := range []any{json.Number("91"), "91"} {
		r.handleNotification("item/commandExecution/requestApproval", map[string]any{"id": id, "params": map[string]any{"threadId": "thread"}})
	}
	r.mu.Lock()
	count := len(r.pendingApprovals)
	r.mu.Unlock()
	if count != 2 {
		t.Fatalf("native approval identities merged: %d", count)
	}
	r.handleNotification("serverRequest/resolved", map[string]any{"params": map[string]any{"threadId": "thread", "requestId": json.Number("91")}})
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pendingApprovals) != 1 {
		t.Fatalf("resolution removed both ID types: %v", r.pendingApprovals)
	}
	for _, nativeID := range r.pendingApprovals {
		if nativeID != "91" {
			t.Fatalf("wrong remaining request: %v", nativeID)
		}
	}
}
