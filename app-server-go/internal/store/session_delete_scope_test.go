package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestDeleteSessionClearsExactApprovalsAndPreservesWildcardNeighbours(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, sid := range []string{"s_%", "sXother"} {
		if _, err := st.UpsertSession(ctx, map[string]any{"id": sid, "agent_id": "a", "title": "fixture", "status": "idle", "updated_at": "2026-10-09T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPreference(ctx, "blackboard:session:a::"+sid+":key", "value", true); err != nil {
			t.Fatal(err)
		}
		if err := st.SetActiveTurn(ctx, "a", sid, "turn"); err != nil {
			t.Fatal(err)
		}
		if err := st.SetPreference(ctx, NativeTurnCommandKey("a", sid), []string{"turn", "command"}, true); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AppendEvent(ctx, Event{ID: "approval-" + sid, Type: "approval.upsert", AgentID: "a", SessionID: sid, Data: map[string]any{"id": "exact", "agent_id": "a", "session_id": sid, "state": "pending"}}); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := st.DeleteSession(ctx, "a", "s_%"); err != nil || !ok {
		t.Fatal(err)
	}
	neighbour, err := st.GetPreference(ctx, "blackboard:session:a::sXother:key")
	if err != nil || !neighbour.Exists {
		t.Fatal("wildcard neighbour removed")
	}
	approvals, err := st.PendingApprovals(ctx)
	if err != nil || len(approvals) != 1 || approvals[0]["session_id"] != "sXother" {
		t.Fatalf("stale or neighbouring approval=%+v err=%v", approvals, err)
	}
	key, _ := json.Marshal([]string{"a", "s_%"})
	turn, err := st.GetPreference(ctx, "native_turn:"+string(key))
	if err != nil || turn.Exists {
		t.Fatal("deleted turn remained")
	}
	binding, err := st.GetPreference(ctx, NativeTurnCommandKey("a", "s_%"))
	if err != nil || binding.Exists {
		t.Fatal("deleted turn command binding remained")
	}
	binding, err = st.GetPreference(ctx, NativeTurnCommandKey("a", "sXother"))
	if err != nil || !binding.Exists {
		t.Fatal("neighbouring turn command binding was removed")
	}
}
