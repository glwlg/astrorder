package botgroups

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func forkFixture(t *testing.T) (*store.Store, *Service) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for _, payload := range []map[string]any{
		{"id": "parent", "agent_id": "a", "title": "parent", "workspace": "/project", "status": "idle", "control_state": "owned", "updated_at": now()},
		{"id": "child", "agent_id": "a", "title": "child", "workspace": "/project", "status": "idle", "control_state": "owned", "handoff_from_agent_id": "a", "handoff_from_session_id": "parent", "updated_at": now()},
	} {
		if _, err := st.UpsertSession(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	if err = st.SetPreference(ctx, "blackboard_scope:a::parent", "session::parent", true); err != nil {
		t.Fatal(err)
	}
	controller := &distinctController{started: make(chan Turn, 2), interrupted: make(chan Turn, 2)}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	return st, service
}

func forkPayload(t *testing.T, member Member) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal([]Member{member})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]json.RawMessage{"name": json.RawMessage(`"群"`), "members": raw}
}

func forkMember() Member {
	parent, child, workspace, alias := "parent", "child", "/project", "Codex-1"
	return Member{ID: "one", AgentID: "a", MachineID: "local", Alias: &alias, Workspace: &workspace, SourceSessionID: &parent, ForkSessionID: &child}
}

func TestForkBindingPreservesParentAndUnbindsOnlyChild(t *testing.T) {
	ctx := context.Background()
	st, service := forkFixture(t)
	before, err := st.GetSession(ctx, "a", "parent")
	if err != nil {
		t.Fatal(err)
	}
	member := forkMember()
	group, err := service.create(ctx, forkPayload(t, member))
	if err != nil {
		t.Fatal(err)
	}
	if group.MemberStates["one"].SessionID != "child" {
		t.Fatalf("child not recorded: %+v", group.MemberStates)
	}
	binding, err := st.GetPreference(ctx, memberBindingKey(group.ID, member))
	if err != nil || binding.Value.(map[string]any)["session_id"] != "child" {
		t.Fatalf("wrong binding: %+v %v", binding, err)
	}
	scope, err := st.GetPreference(ctx, "blackboard_scope:a::child")
	if err != nil || scope.Value != "group::"+group.ID {
		t.Fatalf("wrong child scope: %+v %v", scope, err)
	}
	if _, err = service.create(ctx, forkPayload(t, member)); err == nil {
		t.Fatal("child reused in another group")
	}
	changed := member
	workspace := "/other"
	changed.Workspace = &workspace
	payload := forkPayload(t, changed)
	if _, err = service.update(ctx, group.ID, map[string]json.RawMessage{"members": payload["members"]}); err == nil {
		t.Fatal("bound member workspace was changed")
	}
	if err = service.remove(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	after, err := st.GetSession(ctx, "a", "parent")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("source changed: before=%v after=%v err=%v", before, after, err)
	}
	parentScope, err := st.GetPreference(ctx, "blackboard_scope:a::parent")
	if err != nil || parentScope.Value != "session::parent" {
		t.Fatalf("source scope changed: %+v %v", parentScope, err)
	}
	childScope, err := st.GetPreference(ctx, "blackboard_scope:a::child")
	if err != nil || childScope.Exists {
		t.Fatalf("child scope not removed: %+v %v", childScope, err)
	}
	child, err := st.GetSession(ctx, "a", "child")
	if err != nil || child == nil {
		t.Fatalf("child was deleted: %v %v", child, err)
	}
}

func TestForkBindingRejectsUnconfirmedChildren(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *store.Store, *Member)
	}{
		{"parent itself", func(_ *testing.T, _ *store.Store, m *Member) { m.ForkSessionID = m.SourceSessionID }},
		{"wrong parent", func(_ *testing.T, _ *store.Store, m *Member) { p := "other"; m.SourceSessionID = &p }},
		{"wrong agent", func(_ *testing.T, _ *store.Store, m *Member) { m.AgentID = "other" }},
		{"wrong connection", func(_ *testing.T, _ *store.Store, m *Member) { m.MachineID = "ssh-other" }},
		{"wrong workspace", func(_ *testing.T, _ *store.Store, m *Member) { w := "/different"; m.Workspace = &w }},
		{"running child", func(t *testing.T, st *store.Store, _ *Member) {
			if _, err := st.UpdateSession(context.Background(), "a", "child", map[string]any{"status": "running"}); err != nil {
				t.Fatal(err)
			}
		}},
		{"child has another scope", func(t *testing.T, st *store.Store, _ *Member) {
			if err := st.SetPreference(context.Background(), "blackboard_scope:a::child", "group::other", true); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty source workspace", func(t *testing.T, st *store.Store, m *Member) {
			for _, id := range []string{"parent", "child"} {
				if _, err := st.UpdateSession(context.Background(), "a", id, map[string]any{"workspace": ""}); err != nil {
					t.Fatal(err)
				}
			}
			w := ""
			m.Workspace = &w
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			st, service := forkFixture(t)
			member := forkMember()
			test.mutate(t, st, &member)
			if _, err := service.create(context.Background(), forkPayload(t, member)); err == nil {
				t.Fatal("invalid child accepted")
			}
			scope, err := st.GetPreference(context.Background(), "blackboard_scope:a::parent")
			if err != nil || scope.Value != "session::parent" {
				t.Fatalf("source scope changed: %+v %v", scope, err)
			}
		})
	}
}
