package agentapi

import (
	"context"
	"testing"
)

func TestDelegationWorkspaceExplicitAndCrossAgentDefaults(t *testing.T) {
	for _, tc := range []struct{ name, agent, explicit, want string }{
		{"same_agent_explicit", "parent-hermes-default", "C:/explicit", "C:/explicit"},
		{"cross_agent_default", "child-codex-1", "", "C:/installed-app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, st, ctrl := setupDelegationTestEnv(t)
			defer st.Close()
			h.options.DefaultWorkspace = "C:/installed-app"
			input := map[string]any{"operation_id": "workspace-" + tc.name, "agent_id": tc.agent, "workspace": tc.explicit, "prompt": "review text", "caller": map[string]any{"agent_id": "parent-hermes-default", "connection_id": "local", "profile_name": "default", "runtime": "hermes", "session_id": "parent-session-1"}}
			if _, err := h.Invoke(context.Background(), "sessions.delegate", input); err != nil {
				t.Fatal(err)
			}
			op, found, err := st.GetDelegationOperation(context.Background(), "workspace-"+tc.name)
			if err != nil || !found || op.RequestPayload["workspace"] != tc.want {
				t.Fatalf("resolved workspace = %+v, %v", op, err)
			}
			if ctrl.createCalls != 1 || ctrl.sendCalls != 1 {
				t.Fatal("unexpected dispatch count")
			}
		})
	}
}
