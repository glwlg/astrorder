package botgroups

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestHopLimitPausesDeliveryAndCanBeRaisedWithoutLosingUnreadMessages(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	controller := &stagedController{
		started: make(chan Turn, 8),
		replies: map[string]chan Reply{"design": make(chan Reply, 2), "developer": make(chan Reply, 2)},
		texts:   make(map[string]string),
	}
	decisions := make(chan DecisionInput, 16)
	decider := decisionFunc(func(ctx context.Context, input DecisionInput) (Decision, error) {
		decisions <- input
		return sendAllDecider.Decide(ctx, input)
	})
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: decider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{
		"name":     json.RawMessage(`"群"`),
		"max_hops": json.RawMessage(`3`),
		"members":  json.RawMessage(`[{"id":"design","machine_id":"local","agent_id":"a","alias":"设计"},{"id":"developer","machine_id":"local","agent_id":"b","alias":"开发"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"@设计 原始任务"`)}); err != nil {
		t.Fatal(err)
	}
	next := func(member string, hop int) Turn {
		t.Helper()
		select {
		case turn := <-controller.started:
			if memberID(turn.Member) != member || turn.Hop != hop {
				t.Fatalf("wrong delivery: %+v", turn)
			}
			return turn
		case <-time.After(3 * time.Second):
			t.Fatal("delivery did not start")
			return Turn{}
		}
	}
	next("design", 1)
	controller.replies["design"] <- Reply{Text: "@开发 设计概要", NativeIDs: []string{"design-first"}}
	next("developer", 2)
	controller.replies["developer"] <- Reply{Text: "@设计 请复核", NativeIDs: []string{"developer-first"}}
	next("design", 3)
	controller.replies["design"] <- Reply{Text: "@开发 请给最小模块设计草案", NativeIDs: []string{"design-second"}}
	paused := waitGroup(t, service, group.ID, func(g Group) bool { return g.Coordination.Status == "error" })
	if paused.Coordination.Pending["developer"].Hop != 4 || len(paused.Coordination.InFlight) != 0 ||
		len(paused.ActiveSpeakers) != 0 || paused.MemberStates["developer"].Status != "waiting" ||
		!strings.Contains(paused.Coordination.Error, "第 4 次") || !strings.Contains(paused.Coordination.Error, "上限 3 次") {
		t.Fatalf("hop limit did not preserve paused delivery: %+v", paused)
	}
	select {
	case turn := <-controller.started:
		t.Fatalf("over-limit command was created: %+v", turn)
	default:
	}
	updated, err := service.update(ctx, group.ID, map[string]json.RawMessage{"max_hops": json.RawMessage(`20`)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MaxHops != 20 || updated.Coordination.Status != "error" ||
		!reflect.DeepEqual(updated.Coordination.Pending, paused.Coordination.Pending) ||
		!reflect.DeepEqual(updated.Coordination.Delivered, paused.Coordination.Delivered) {
		t.Fatalf("limit update lost the paused round: %+v", updated)
	}
	select {
	case <-controller.started:
		t.Fatal("saving the limit implicitly retried delivery")
	default:
	}
	if _, err := service.retryDecision(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	turn := next("developer", 4)
	if !strings.Contains(turn.Prompt, "请给最小模块设计草案") || strings.Contains(turn.Prompt, "原始任务") ||
		!strings.Contains(turn.Prompt, "第 4 次接力，上限 20 次；本次回复后剩余 16 次") {
		t.Fatalf("retry replayed delivered messages or lost progress: %s", turn.Prompt)
	}
	controller.replies["developer"] <- Reply{Text: "草案已提供", NativeIDs: []string{"developer-second"}}
	waitGroup(t, service, group.ID, func(g Group) bool { return g.Coordination.Status == "complete" })
	select {
	case <-controller.started:
		t.Fatal("retried delivery was duplicated")
	default:
	}
	if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"@设计 新一轮任务"`)}); err != nil {
		t.Fatal(err)
	}
	next("design", 1)
	controller.replies["design"] <- Reply{Text: "新一轮已回复", NativeIDs: []string{"design-third"}}
	waitGroup(t, service, group.ID, func(g Group) bool { return g.Coordination.Status == "complete" })
	sawLimit, sawRetry, sawNewRound := false, false, false
	for len(decisions) > 0 {
		input := <-decisions
		for _, candidate := range input.Candidates {
			if candidate.MemberID == "developer" && candidate.Hop == 4 {
				if input.Group.CurrentHop != 3 || input.Messages[len(input.Messages)-1].HopCount != 3 {
					t.Fatalf("System One lost current relay progress: %+v", input)
				}
				switch input.Group.MaxHops {
				case 3:
					sawLimit = input.Group.Remaining == 0
				case 20:
					sawRetry = input.Group.Remaining == 17
				}
			}
			if candidate.MemberID == "design" && candidate.Hop == 1 && input.Group.MaxHops == 20 {
				sawNewRound = input.Group.CurrentHop == 0 && input.Group.Remaining == 20
			}
		}
	}
	if !sawLimit || !sawRetry || !sawNewRound {
		t.Fatalf("System One progress missing: limit=%v retry=%v new_round=%v", sawLimit, sawRetry, sawNewRound)
	}
}

func TestGroupHopLimitDefaultsAndBounds(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service, err := New(ctx, Options{Store: st, Controller: &controlledAgents{}, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: sendAllDecider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{
		"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`),
	})
	if err != nil || group.MaxHops != 10 {
		t.Fatalf("new group did not default to 10: %+v %v", group, err)
	}
	for _, limit := range []string{"1", "20"} {
		if _, err := service.update(ctx, group.ID, map[string]json.RawMessage{"max_hops": json.RawMessage(limit)}); err != nil {
			t.Fatalf("valid limit %s rejected: %v", limit, err)
		}
	}
	for _, limit := range []string{"0", "21", "1.5"} {
		if _, err := service.update(ctx, group.ID, map[string]json.RawMessage{"max_hops": json.RawMessage(limit)}); err == nil {
			t.Fatalf("invalid limit %s accepted", limit)
		}
	}
}

func TestPendingRoundOnlyAllowsSafeLimitChanges(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	service, err := New(ctx, Options{Store: st, Controller: &controlledAgents{}, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: sendAllDecider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{
		"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		status   string
		member   string
		inFlight bool
		payload  map[string]json.RawMessage
	}{
		{"deciding", "deciding", "waiting", false, map[string]json.RawMessage{"max_hops": json.RawMessage(`20`)}},
		{"unknown", "error", "unknown", false, map[string]json.RawMessage{"max_hops": json.RawMessage(`20`)}},
		{"running", "error", "running", false, map[string]json.RawMessage{"max_hops": json.RawMessage(`20`)}},
		{"in-flight", "error", "waiting", true, map[string]json.RawMessage{"max_hops": json.RawMessage(`20`)}},
		{"other-settings", "error", "waiting", false, map[string]json.RawMessage{"name": json.RawMessage(`"改名"`), "max_hops": json.RawMessage(`20`)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, err := service.mutate(ctx, group.ID, func(g *Group) error {
				g.MemberStates["a"] = MemberState{Status: test.member}
				g.Coordination = &Coordination{Status: test.status, Pending: map[string]PendingDelivery{"a": {Hop: 11}}}
				if test.inFlight {
					g.Coordination.InFlight = map[string]Delivery{"a": {CommandID: "uncertain"}}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.update(ctx, group.ID, test.payload)
			var conflict *serviceError
			if !errors.As(err, &conflict) || conflict.code != 409 {
				t.Fatalf("unsafe update not rejected: %v", err)
			}
			after, err := service.get(ctx, group.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("rejected update changed the group: %v", err)
			}
		})
	}
}
