package notifications

import (
	"context"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/store"
)

func TestGroupNoticesUseExactMemberBindingAndRemainCanonical(t *testing.T) {
	st := testStore(t)
	ctx := context.Background()
	group := map[string]any{"id": "group-1", "name": "授权复核讨论", "members": []any{
		map[string]any{"id": "design", "agent_id": "a", "alias": "方案设计"},
		map[string]any{"id": "dev", "agent_id": "a", "alias": "开发"},
	}}
	if err := st.SetPreference(ctx, "bot_group:group-1", group, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, "group_session:group-1:dev", map[string]any{"agent_id": "a", "session_id": "s"}, true); err != nil {
		t.Fatal(err)
	}
	event := store.Event{Type: "codex.notification", AgentID: "a", SessionID: "s", Data: map[string]any{
		"frame": map[string]any{"method": "turn/completed", "params": map[string]any{"turn": map[string]any{"id": "turn-1", "status": "completed"}}},
	}}
	first, err := Events(ctx, st.DB(), []store.Event{event})
	if err != nil || len(first) != 1 {
		t.Fatal(first, err)
	}
	notice := first[0].Data
	if notice["group_id"] != "group-1" || notice["title"] != "授权复核讨论" || notice["message"] != "开发 · 会话已完成" {
		t.Fatalf("incorrect group identity: %+v", notice)
	}
	if _, err := st.AppendEvents(ctx, first); err != nil {
		t.Fatal(err)
	}
	repeated, err := Events(ctx, st.DB(), []store.Event{event})
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := st.AppendEvents(ctx, repeated)
	if err != nil || len(inserted) != 0 {
		t.Fatalf("duplicated canonical reminder: %v %v", inserted, err)
	}
	// Questions still open the session containing their answer controls.
	questions, err := Events(ctx, st.DB(), []store.Event{{
		Type: "approval.upsert", AgentID: "a", SessionID: "s",
		Data: map[string]any{"id": "question", "state": "pending", "method": "item/tool/requestUserInput"},
	}})
	if err != nil || len(questions) != 1 || questions[0].Data["group_id"] != "" {
		t.Fatalf("question answer controls became unreachable: %v %v", questions, err)
	}
	// Other sessions on the same Agent, including a fork's source, keep their identity.
	for _, session := range []string{"source", "ordinary"} {
		if _, err := st.UpsertSession(ctx, map[string]any{"id": session, "agent_id": "a", "title": session, "status": "idle", "updated_at": time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
			t.Fatal(err)
		}
		event.SessionID = session
		notices, err := Events(ctx, st.DB(), []store.Event{event})
		if err != nil || len(notices) != 1 || notices[0].Data["group_id"] != "" || notices[0].Data["title"] != "会话已完成" {
			t.Fatalf("unrelated session changed: %+v %v", notices, err)
		}
	}
	// Removing a member must not let its stale binding claim future notifications.
	group["members"] = []any{map[string]any{"id": "design", "agent_id": "a", "alias": "方案设计"}}
	if err := st.SetPreference(ctx, "bot_group:group-1", group, true); err != nil {
		t.Fatal(err)
	}
	event.SessionID = "s"
	notices, err := Events(ctx, st.DB(), []store.Event{event})
	if err != nil || len(notices) != 1 || notices[0].Data["group_id"] != "" {
		t.Fatal(notices, err)
	}
}

func TestPushRoutesGroupNoticesToGroupOnEveryDevice(t *testing.T) {
	for _, mobile := range []bool{false, true} {
		path := noticePath(Notice{GroupID: "group-1", AgentID: "a", SessionID: "s"}, mobile)
		want := "/groups/group-1"
		if mobile {
			want = "/mobile" + want
		}
		if path != want || strings.Contains(path, "/chat/") {
			t.Fatal(path)
		}
	}
}
