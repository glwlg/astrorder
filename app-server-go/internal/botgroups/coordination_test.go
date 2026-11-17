package botgroups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
)

type stagedController struct {
	started chan Turn
	replies map[string]chan Reply
	mu      sync.Mutex
	texts   map[string]string
}

func (c *stagedController) Run(ctx context.Context, turn Turn, bound func(string) error) (Reply, error) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return Reply{}, errors.New("group task has an unexpected deadline")
	}
	if err := bound("session-" + memberID(turn.Member)); err != nil {
		return Reply{}, err
	}
	if turn.RecordBaseline != nil {
		if err := turn.RecordBaseline([]string{"previous"}); err != nil {
			return Reply{}, err
		}
	}
	c.started <- turn
	select {
	case reply := <-c.replies[memberID(turn.Member)]:
		c.mu.Lock()
		for _, id := range reply.NativeIDs {
			c.texts[id] = reply.Text
		}
		c.mu.Unlock()
		return reply, nil
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	}
}

func (c *stagedController) Interrupt(context.Context, Turn) error { return nil }

func (c *stagedController) ReadReply(_ context.Context, _, _ string, ids []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		text, ok := c.texts[id]
		if !ok {
			return "", errors.New("reply not found")
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n"), nil
}

func waitGroup(t *testing.T, s *Service, id string, predicate func(Group) bool) Group {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		group, err := s.get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(group) {
			return group
		}
		select {
		case <-deadline:
			t.Fatalf("group did not reach expected state: %+v", group)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestSystemOneRoutesEachMemberAndDeliversAllUnreadMessages(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			State     DecisionInput  `json:"state"`
			Questions map[string]any `json:"questions"`
		}
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer fixture-key" || json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("decision did not use the configured System One gateway")
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if request.State.Group.MaxHops != 10 || request.State.Group.CurrentHop < 0 ||
			request.State.Group.Remaining != max(0, 10-request.State.Group.CurrentHop) {
			t.Errorf("System One did not receive relay progress: %+v", request.State.Group)
		}
		designReady, developerDone := false, false
		for _, message := range request.State.Messages {
			designReady = designReady || strings.Contains(message.Text, "设计概要")
			developerDone = developerDone || strings.Contains(message.Text, "开发已理解")
		}
		answers := map[string]any{}
		for i, candidate := range request.State.Candidates {
			if candidate.Hop < 1 {
				t.Errorf("System One did not receive candidate hop: %+v", candidate)
			}
			key := fmt.Sprintf("member_%d", i)
			if request.Questions[key] == nil {
				t.Errorf("missing decision for %s", candidate.MemberID)
			}
			choice := "send"
			if candidate.MemberID == "developer" && !designReady {
				choice = "wait"
			}
			answers[key] = map[string]any{"choice": choice}
		}
		round := "continue"
		if developerDone {
			round = "complete"
		}
		answers["round"] = map[string]any{"choice": round}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers})
	}))
	defer upstream.Close()
	if _, err := modelgateway.SaveGatewayConfig(ctx, st, map[string]any{"gateway_type": "magpie", "inference_url": upstream.URL + "/v1", "api_key": "fixture-key"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(ctx, browser.JevConfigPreference, browser.JevConfig{Mode: "gateway", Model: "typesafe/jev-preview"}, true); err != nil {
		t.Fatal(err)
	}
	controller := &stagedController{
		started: make(chan Turn, 8),
		replies: map[string]chan Reply{"design": make(chan Reply, 2), "developer": make(chan Reply, 2)},
		texts:   make(map[string]string),
	}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{
		"name":    json.RawMessage(`"群"`),
		"members": json.RawMessage(`[{"id":"design","machine_id":"local","agent_id":"a","alias":"设计"},{"id":"developer","machine_id":"local","agent_id":"b","alias":"开发"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	send := func(text string) {
		t.Helper()
		raw, _ := json.Marshal(text)
		if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": raw}); err != nil {
			t.Fatal(err)
		}
	}
	send("@设计 @开发 设计先说明，开发等设计说明后再理解")
	var first Turn
	select {
	case first = <-controller.started:
	case <-time.After(3 * time.Second):
		t.Fatal("design did not start")
	}
	if memberID(first.Member) != "design" {
		t.Fatalf("wrong first member: %s", memberID(first.Member))
	}
	send("@开发 补充背景")
	select {
	case turn := <-controller.started:
		t.Fatalf("developer started before design: %s", memberID(turn.Member))
	case <-time.After(150 * time.Millisecond):
	}
	controller.replies["design"] <- Reply{Text: "@开发 设计概要", NativeIDs: []string{"design-reply"}}
	var developer Turn
	select {
	case developer = <-controller.started:
	case <-time.After(3 * time.Second):
		t.Fatal("developer was not started after design")
	}
	if memberID(developer.Member) != "developer" || !strings.Contains(developer.Prompt, "设计先说明") ||
		!strings.Contains(developer.Prompt, "补充背景") || !strings.Contains(developer.Prompt, "设计概要") ||
		strings.Index(developer.Prompt, "补充背景") > strings.Index(developer.Prompt, "设计概要") {
		t.Fatalf("developer did not receive unread messages in order: %s", developer.Prompt)
	}
	send("@开发 执行期间新增的信息")
	select {
	case <-controller.started:
		t.Fatal("busy developer received a concurrent message")
	case <-time.After(150 * time.Millisecond):
	}
	controller.replies["developer"] <- Reply{Text: "开发已理解", NativeIDs: []string{"developer-reply-1"}}
	select {
	case next := <-controller.started:
		if memberID(next.Member) != "developer" || !strings.Contains(next.Prompt, "执行期间新增的信息") || strings.Contains(next.Prompt, "设计先说明") {
			t.Fatalf("busy member did not receive only newly unread messages: %s", next.Prompt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("busy member's pending message was lost")
	}
	controller.replies["developer"] <- Reply{Text: "开发已理解新增信息", NativeIDs: []string{"developer-reply-2"}}
	waitGroup(t, service, group.ID, func(g Group) bool { return g.Coordination != nil && g.Coordination.Status == "complete" })
	messages, err := service.messages(ctx, group.ID)
	if err != nil || len(messages) != 6 {
		t.Fatalf("group did not retain all messages: count=%d err=%v", len(messages), err)
	}
	if calls.Load() < 3 {
		t.Fatal("System One was not asked again after group replies")
	}
}

func TestStopDuringDecisionDoesNotDispatchLateResult(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	decider := decisionFunc(func(ctx context.Context, input DecisionInput) (Decision, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return Decision{}, ctx.Err()
		}
		return sendAllDecider.Decide(ctx, input)
	})
	controller := &controlledAgents{started: make(chan Turn, 1), interrupted: make(chan Turn, 1), release: make(chan struct{})}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: decider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"任务"`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("decision did not start")
	}
	if _, err := service.stop(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-controller.started:
		t.Fatal("stopped group dispatched a stale model result")
	case <-time.After(150 * time.Millisecond):
	}
	stopped, err := service.get(ctx, group.ID)
	if err != nil || stopped.Coordination.Status != "stopped" || stopped.MemberStates["a"].Status != "cancelled" {
		t.Fatalf("stop did not clear the waiting member: %+v %v", stopped, err)
	}
}

func TestNewMessageInvalidatesAnInFlightDecision(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var calls atomic.Int32
	decider := decisionFunc(func(ctx context.Context, input DecisionInput) (Decision, error) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return Decision{}, ctx.Err()
			}
		}
		return sendAllDecider.Decide(ctx, input)
	})
	controller := &controlledAgents{started: make(chan Turn, 2), interrupted: make(chan Turn, 2), release: make(chan struct{})}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: decider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"第一条", "第二条"} {
		raw, _ := json.Marshal(text)
		if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": raw}); err != nil {
			t.Fatal(err)
		}
		if text == "第一条" {
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("decision did not start")
			}
		}
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case turn := <-controller.started:
		if !strings.Contains(turn.Prompt, "第一条") || !strings.Contains(turn.Prompt, "第二条") || calls.Load() < 2 {
			t.Fatal("stale decision lost the later message")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("updated decision did not dispatch")
	}
}

type failedInterruptController struct{ distinctController }

func (c *failedInterruptController) Interrupt(context.Context, Turn) error {
	return errors.New("interrupt unavailable")
}

func TestUnconfirmedStopBlocksAnotherDelivery(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	controller := &failedInterruptController{distinctController{started: make(chan Turn, 1), interrupted: make(chan Turn, 1)}}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: sendAllDecider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"任务"`)}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-controller.started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	if _, err := service.stop(ctx, group.ID); err == nil {
		t.Fatal("unconfirmed interruption was reported as successful")
	}
	stopped, err := service.get(ctx, group.ID)
	if err != nil || stopped.MemberStates["a"].Status != "unknown" || len(stopped.Coordination.InFlight) != 1 {
		t.Fatalf("uncertain task was not retained: %+v %v", stopped, err)
	}
	if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"再次执行"`)}); err == nil {
		t.Fatal("another task was sent before the interruption was confirmed")
	}
}

type resumingController struct {
	started chan Turn
	resumed chan Turn
}

func (c *resumingController) Run(ctx context.Context, turn Turn, bound func(string) error) (Reply, error) {
	if err := bound("native"); err != nil {
		return Reply{}, err
	}
	if err := turn.RecordBaseline([]string{"old"}); err != nil {
		return Reply{}, err
	}
	c.started <- turn
	<-ctx.Done()
	return Reply{}, ctx.Err()
}

func (c *resumingController) Resume(_ context.Context, turn Turn, baseline []string) (Reply, error) {
	if len(baseline) != 1 || baseline[0] != "old" {
		return Reply{}, errors.New("baseline was lost")
	}
	c.resumed <- turn
	return Reply{Text: "恢复后的回复", NativeIDs: []string{"new"}}, nil
}

func (c *resumingController) Interrupt(context.Context, Turn) error { return nil }
func (c *resumingController) ReadReply(context.Context, string, string, []string) (string, error) {
	return "恢复后的回复", nil
}

func TestRestartReattachesToRecordedDeliveryWithoutResending(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	controller := &resumingController{started: make(chan Turn, 2), resumed: make(chan Turn, 2)}
	opts := Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: sendAllDecider}
	first, err := New(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	group, err := first.create(ctx, map[string]json.RawMessage{"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"长任务"`)}); err != nil {
		t.Fatal(err)
	}
	var original Turn
	select {
	case original = <-controller.started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	first.Close()
	second, err := New(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	select {
	case resumed := <-controller.resumed:
		if resumed.CommandID != original.CommandID || resumed.SessionID != "native" {
			t.Fatal("restart followed the wrong native command")
		}
	case <-time.After(time.Second):
		t.Fatal("persisted task was not resumed")
	}
	waitGroup(t, second, group.ID, func(g Group) bool { return g.Coordination != nil && g.Coordination.Status == "complete" })
	select {
	case <-controller.started:
		t.Fatal("restart resent the task")
	default:
	}
	messages, err := second.messages(ctx, group.ID)
	if err != nil || len(messages) != 2 || messages[1].Text != "恢复后的回复" {
		t.Fatalf("resumed reply not recorded: %+v %v", messages, err)
	}
}

func TestDecisionFailureKeepsPendingMessagesUntilExplicitRetry(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var attempts atomic.Int32
	decider := decisionFunc(func(_ context.Context, input DecisionInput) (Decision, error) {
		if attempts.Add(1) == 1 {
			return Decision{}, errors.New("System One 不可用")
		}
		return sendAllDecider.Decide(ctx, input)
	})
	controller := &controlledAgents{started: make(chan Turn, 2), interrupted: make(chan Turn, 2), release: make(chan struct{})}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: decider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"重要消息"`)}); err != nil {
		t.Fatal(err)
	}
	failed := waitGroup(t, service, group.ID, func(g Group) bool { return g.Coordination != nil && g.Coordination.Status == "error" })
	if len(failed.Coordination.Pending) != 1 {
		t.Fatal("decision error discarded pending work")
	}
	select {
	case <-controller.started:
		t.Fatal("decision failure dispatched work")
	default:
	}
	if _, err := service.retryDecision(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case turn := <-controller.started:
		if !strings.Contains(turn.Prompt, "重要消息") {
			t.Fatal("retry lost the pending message")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not resume delivery")
	}
}

type failOnceController struct {
	started chan Turn
	calls   atomic.Int32
}

func (c *failOnceController) Run(ctx context.Context, turn Turn, bound func(string) error) (Reply, error) {
	if err := bound("native"); err != nil {
		return Reply{}, err
	}
	if err := turn.RecordAccepted(); err != nil {
		return Reply{}, err
	}
	c.started <- turn
	if c.calls.Add(1) == 1 {
		return Reply{}, errors.New("会话不可用")
	}
	<-ctx.Done()
	return Reply{}, ctx.Err()
}

func (c *failOnceController) Interrupt(context.Context, Turn) error { return nil }

func TestFailedDeliveryRetainsUnreadMessagesUntilExplicitRetry(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	controller := &failOnceController{started: make(chan Turn, 2)}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: sendAllDecider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"原始任务", "补充信息"} {
		raw, _ := json.Marshal(text)
		if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": raw}); err != nil {
			t.Fatal(err)
		}
		if text == "原始任务" {
			select {
			case <-controller.started:
			case <-time.After(time.Second):
				t.Fatal("initial delivery did not start")
			}
			waitGroup(t, service, group.ID, func(g Group) bool {
				_, pending := g.Coordination.Pending["a"]
				return pending && g.Coordination.Status == "error" && g.MemberStates["a"].Status == "failed"
			})
		}
	}
	select {
	case <-controller.started:
		t.Fatal("failed delivery was retried without an explicit request")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := service.retryDecision(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case turn := <-controller.started:
		if !strings.Contains(turn.Prompt, "原始任务") || !strings.Contains(turn.Prompt, "补充信息") {
			t.Fatalf("retry lost unread messages: %s", turn.Prompt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("failed delivery was not retried")
	}
}

func TestMentionOfBusyMemberIsQueuedUntilItsCurrentReplyFinishes(t *testing.T) {
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
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}, Decider: sendAllDecider})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	group, err := service.create(ctx, map[string]json.RawMessage{
		"name":    json.RawMessage(`"群"`),
		"members": json.RawMessage(`[{"id":"design","machine_id":"local","agent_id":"a","alias":"设计"},{"id":"developer","machine_id":"local","agent_id":"b","alias":"开发"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.send(ctx, group.ID, map[string]json.RawMessage{"text": json.RawMessage(`"@设计 @开发 请并行处理"`)}); err != nil {
		t.Fatal(err)
	}
	started := map[string]bool{}
	for range 2 {
		select {
		case turn := <-controller.started:
			started[memberID(turn.Member)] = true
		case <-time.After(2 * time.Second):
			t.Fatal("both members did not start")
		}
	}
	if len(started) != 2 {
		t.Fatalf("wrong running members: %v", started)
	}
	controller.replies["design"] <- Reply{Text: "@开发 请检查设计结果", NativeIDs: []string{"design-reply"}}
	waitGroup(t, service, group.ID, func(g Group) bool {
		_, pending := g.Coordination.Pending["developer"]
		return pending && g.MemberStates["developer"].Status == "running"
	})
	select {
	case <-controller.started:
		t.Fatal("busy member received overlapping work")
	case <-time.After(100 * time.Millisecond):
	}
	controller.replies["developer"] <- Reply{Text: "初步处理完成", NativeIDs: []string{"developer-first"}}
	select {
	case next := <-controller.started:
		if memberID(next.Member) != "developer" || !strings.Contains(next.Prompt, "请检查设计结果") {
			t.Fatalf("queued mention was not delivered: %+v", next)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("queued mention was lost")
	}
	controller.replies["developer"] <- Reply{Text: "已检查", NativeIDs: []string{"developer-second"}}
	waitGroup(t, service, group.ID, func(g Group) bool { return g.Coordination.Status == "complete" })
}
