package projection

import (
	"astrorder.dev/app-server/internal/store"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestApprovalResolutionPreservesNativeIdentifierType(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.AppendEvent(ctx, store.Event{ID: "first", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "a-text", "native_id": "91", "state": "pending"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent(ctx, store.Event{ID: "second", Type: "approval.upsert", AgentID: "a", SessionID: "s", Data: map[string]any{"id": "b-number", "native_id": json.Number("91"), "state": "pending"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		got, err := findMatchingApprovalID(ctx, tx, "a", "s", json.Number("91"))
		if err != nil {
			return err
		}
		if got != "b-number" {
			return fmt.Errorf("numeric ID resolved to %q", got)
		}
		got, err = findMatchingApprovalID(ctx, tx, "a", "s", "91")
		if err != nil {
			return err
		}
		if got != "a-text" {
			return fmt.Errorf("text ID resolved to %q", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestPermissionsApprovalProjectsRequestedGrants(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	grants := map[string]any{"fileSystem": map[string]any{"write": []any{"C:/fixture"}}, "network": map[string]any{"enabled": true}}
	var events []store.Event
	if err := st.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		events, err = projectCodexNotificationTx(ctx, tx, "stream", "a", map[string]any{"agent_id": "a", "session_id": "s", "approval_id": "exact-permission", "frame": map[string]any{"id": json.Number("91"), "method": "item/permissions/requestApproval", "params": map[string]any{"threadId": "s", "permissions": grants}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "approval.upsert" {
			if event.Data["method"] != "item/permissions/requestApproval" || event.Data["permissions"] == nil {
				t.Fatalf("permission request lost: %v", event.Data)
			}
			return
		}
	}
	t.Fatal("permissions approval missing")
}
