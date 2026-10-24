package agentapi

import (
	"context"
	"encoding/json"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestDelegationSameAgentInheritsVerifiedCallerWorkspace(t *testing.T) {
	h, st, ctrl := setupDelegationTestEnv(t)
	defer st.Close()
	h.options.DefaultWorkspace = "C:/installed-app"
	_, err := st.UpsertAgent(context.Background(), map[string]any{"id": "parent-hermes-default", "name": "Parent Hermes", "kind": "hermes", "status": "ready", "connection_id": "local", "profile_name": "default"})
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"operation_id": "inherit-caller-workspace", "agent_id": "parent-hermes-default", "prompt": "review text", "caller": map[string]any{"agent_id": "parent-hermes-default", "connection_id": "local", "profile_name": "default", "runtime": "hermes", "session_id": "parent-session-1"}}
	if _, err = h.Invoke(context.Background(), "sessions.delegate", input); err != nil {
		t.Fatal(err)
	}
	var native map[string]any
	if err = json.Unmarshal(ctrl.createReqSaved.GetHermes().NativeOptions.Utf8Json, &native); err != nil {
		t.Fatal(err)
	}
	if native["cwd"] != "C:/parent/work" {
		t.Fatalf("child native cwd = %v, want verified caller workspace", native["cwd"])
	}
	op, found, err := st.GetDelegationOperation(context.Background(), "inherit-caller-workspace")
	if err != nil || !found || op.Stage != store.OpStageCompleted || op.RequestPayload["workspace"] != "C:/parent/work" {
		t.Fatalf("resolved workspace missing from journal: %+v, %v", op, err)
	}
}
