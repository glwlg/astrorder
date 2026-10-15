package sessions

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDiscoverSubagentPreservesParentAndSource(t *testing.T) {
	child := fakeThread("child", "Verifier", "", "cli", 1791890000)
	child["source"] = map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{"parent_thread_id": "parent", "depth": 1}}}
	child["parentThreadId"] = "parent"
	h, st, srv, _ := newNativeFixture(t, child)
	ctx := context.Background()
	agent, _ := st.GetAgent(ctx, "codex-1")
	if _, err := h.refreshAgentActivity(ctx, agent, "codex"); err != nil {
		t.Fatal(err)
	}
	row, err := st.GetSession(ctx, "codex-1", "child")
	if err != nil || row == nil || row["native_kind"] != "subagent" || row["parent_session_id"] != "parent" {
		t.Fatal("subagent hidden or relationship lost", row, err)
	}
	found := false
	for _, raw := range srv.lists {
		var opts map[string]any
		_ = json.Unmarshal([]byte(raw), &opts)
		if kinds, ok := opts["sourceKinds"].([]any); ok {
			for _, kind := range kinds {
				if kind == "subAgent" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("native discovery never requested subagent sources")
	}
}
