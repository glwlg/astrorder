package botgroups

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

type distinctController struct {
	started     chan Turn
	interrupted chan Turn
}

func (c *distinctController) Run(ctx context.Context, turn Turn, bound func(string) error) (Reply, error) {
	if err := bound("session-" + memberID(turn.Member)); err != nil {
		return Reply{}, err
	}
	c.started <- turn
	<-ctx.Done()
	return Reply{}, ctx.Err()
}
func (c *distinctController) Interrupt(_ context.Context, turn Turn) error {
	c.interrupted <- turn
	return nil
}

func TestDuplicateAgentMembersDispatchAndStopIndependently(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	controller := &distinctController{started: make(chan Turn, 4), interrupted: make(chan Turn, 4)}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: sendAllDecider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	g, err := service.create(ctx, map[string]json.RawMessage{
		"name":    json.RawMessage(`"群"`),
		"members": json.RawMessage(`[{"id":"one","machine_id":"local","agent_id":"a","name":"Codex","alias":"Codex-1","workspace":"/one"},{"id":"two","machine_id":"local","agent_id":"a","name":"Codex","alias":"Codex-2","workspace":"/two"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.send(ctx, g.ID, map[string]json.RawMessage{"text": json.RawMessage(`"@Codex-1 first"`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["target_member_id"] != "one" {
		t.Fatal(result)
	}
	select {
	case turn := <-controller.started:
		if memberID(turn.Member) != "one" || memberValue(turn.Member.Workspace) != "/one" {
			t.Fatalf("wrong targeted member: %+v", turn)
		}
	case <-time.After(time.Second):
		t.Fatal("target did not start")
	}
	select {
	case turn := <-controller.started:
		t.Fatalf("unexpected extra turn: %+v", turn)
	default:
	}
	if _, err = service.send(ctx, g.ID, map[string]json.RawMessage{"text": json.RawMessage(`"@Codex-2 second"`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case turn := <-controller.started:
		if memberID(turn.Member) != "two" || memberValue(turn.Member.Workspace) != "/two" {
			t.Fatalf("wrong second member: %+v", turn)
		}
	case <-time.After(time.Second):
		t.Fatal("second member did not start")
	}
	g, err = service.get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.MemberStates["one"].SessionID != "session-one" || g.MemberStates["two"].SessionID != "session-two" {
		t.Fatalf("sessions crossed: %+v", g.MemberStates)
	}
	if _, err = service.stop(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	interrupted := map[string]string{}
	for range 2 {
		turn := <-controller.interrupted
		interrupted[memberID(turn.Member)] = turn.SessionID
	}
	if !reflect.DeepEqual(interrupted, map[string]string{"one": "session-one", "two": "session-two"}) {
		t.Fatalf("wrong interrupted sessions: %v", interrupted)
	}
	g, err = service.get(ctx, g.ID)
	if err != nil || g.MemberStates["one"].Status != "cancelled" || g.MemberStates["two"].Status != "cancelled" {
		t.Fatalf("incorrect states: %+v %v", g.MemberStates, err)
	}
	if _, err = service.send(ctx, g.ID, map[string]json.RawMessage{"text": json.RawMessage(`"@all again"`)}); err != nil {
		t.Fatal(err)
	}
	started := map[string]bool{}
	for range 2 {
		select {
		case turn := <-controller.started:
			started[memberID(turn.Member)] = true
		case <-time.After(time.Second):
			t.Fatal("broadcast did not start both")
		}
	}
	if len(started) != 2 {
		t.Fatalf("broadcast members: %v", started)
	}
	if _, err = service.stop(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
}

func TestMemberMentionAliasPriority(t *testing.T) {
	name, first, second := "Codex", "Codex", "Codex-2"
	members := []Member{{ID: "one", AgentID: "a", Name: &name, Alias: &first}, {ID: "two", AgentID: "a", Name: &name, Alias: &second}}
	for text, want := range map[string][]string{"@Codex": {"one"}, "@Codex-2": {"two"}, "@all": {"one", "two"}} {
		if got := Mentions(text, members); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", text, got, want)
		}
	}
}

func TestLegacyAliasesSurviveUnrelatedGroupEdits(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	alias := "旧 别名"
	group := Group{ID: "legacy", Name: "旧群", MaxHops: 3, Members: []Member{{AgentID: "a", MachineID: "local", Alias: &alias}, {AgentID: "b", MachineID: "local", Alias: &alias}}, MemberStates: map[string]MemberState{"a": {Status: "idle"}, "b": {Status: "idle"}}}
	if err = st.SetPreference(ctx, "bot_group:legacy", group, true); err != nil {
		t.Fatal(err)
	}
	controller := &distinctController{started: make(chan Turn, 2), interrupted: make(chan Turn, 2)}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	updated, err := service.update(ctx, "legacy", map[string]json.RawMessage{"name": json.RawMessage(`"新名称"`)})
	if err != nil || len(updated.Members) != 2 || updated.MemberStates["a"].Status != "idle" {
		t.Fatalf("legacy edit failed: %+v %v", updated, err)
	}
}
