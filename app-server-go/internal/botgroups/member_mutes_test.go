package botgroups

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

type memberMuteFixture struct {
	service    *Service
	controller *stagedController
	group      Group
	replies    int
}

func newMemberMuteFixture(t *testing.T, decider Decider) *memberMuteFixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	controller := &stagedController{
		started: make(chan Turn, 32),
		replies: map[string]chan Reply{"design": make(chan Reply, 4), "developer": make(chan Reply, 4)},
		texts:   make(map[string]string),
	}
	if decider == nil {
		decider = sendAllDecider
	}
	service, err := New(context.Background(), Options{
		Store: st, Controller: controller, Decider: decider,
		Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	group, err := service.create(context.Background(), map[string]json.RawMessage{
		"name":    json.RawMessage(`"开发群"`),
		"members": json.RawMessage(`[{"id":"design","machine_id":"local","agent_id":"a","alias":"设计"},{"id":"developer","machine_id":"local","agent_id":"a","alias":"开发"}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &memberMuteFixture{service: service, controller: controller, group: group}
}

func (f *memberMuteFixture) mute(t *testing.T, changes map[string]bool) Group {
	t.Helper()
	raw, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	group, err := f.service.update(context.Background(), f.group.ID, map[string]json.RawMessage{"member_mutes": raw})
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func (f *memberMuteFixture) send(t *testing.T, text string, extra map[string]json.RawMessage) map[string]any {
	t.Helper()
	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]json.RawMessage{"text": raw}
	for key, value := range extra {
		payload[key] = value
	}
	result, err := f.service.send(context.Background(), f.group.ID, payload)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)
}

func (f *memberMuteFixture) next(t *testing.T, expected string) Turn {
	t.Helper()
	select {
	case turn := <-f.controller.started:
		if memberID(turn.Member) != expected {
			t.Fatalf("dispatched %s, want %s", memberID(turn.Member), expected)
		}
		return turn
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not receive its task", expected)
		return Turn{}
	}
}

func (f *memberMuteFixture) finish(t *testing.T, mid, text string) {
	t.Helper()
	f.replies++
	f.controller.replies[mid] <- Reply{Text: text, NativeIDs: []string{fmt.Sprintf("reply-%d", f.replies)}}
	waitGroup(t, f.service, f.group.ID, func(g Group) bool {
		return g.Coordination != nil && g.Coordination.Status == "complete"
	})
}

func (f *memberMuteFixture) noDispatch(t *testing.T) {
	t.Helper()
	waitGroup(t, f.service, f.group.ID, func(g Group) bool {
		return g.Coordination != nil && g.Coordination.Status == "complete"
	})
	select {
	case turn := <-f.controller.started:
		t.Fatalf("unexpected dispatch: %+v", turn)
	default:
	}
}

func TestMemberMuteBlocksBroadcastExplicitTargetsAndAgentMentions(t *testing.T) {
	f := newMemberMuteFixture(t, nil)
	f.mute(t, map[string]bool{"design": true})
	for _, text := range []string{"开始开发", "@all 继续开发", "@设计 @开发 继续", "@设计 @all 复核"} {
		result := f.send(t, text, nil)
		if result["target_member_id"] != "developer" {
			t.Fatalf("muted member remained a target: %+v", result)
		}
		f.next(t, "developer")
		f.finish(t, "developer", "@设计 请看一下开发结果")
		f.noDispatch(t)
	}
	for _, extra := range []map[string]json.RawMessage{
		nil,
		{"target_member_id": json.RawMessage(`"design"`)},
		{"mentions": json.RawMessage(`["design"]`)},
	} {
		result := f.send(t, "@设计 这条不应发送", extra)
		if result["target_member_id"] != nil || result["target_agent_id"] != nil {
			t.Fatalf("muted explicit target was retained: %+v", result)
		}
		f.noDispatch(t)
	}
	group, err := f.service.get(context.Background(), f.group.ID)
	if err != nil || !group.MemberStates["design"].Muted || group.MemberStates["developer"].Muted ||
		len(group.Coordination.Pending) != 0 {
		t.Fatalf("mute affected another member sharing the same agent: %+v error=%v", group, err)
	}
}

func TestMemberRestorationSkipsAllMutedHistoryAndSurvivesRestart(t *testing.T) {
	f := newMemberMuteFixture(t, nil)
	f.send(t, "@设计 先讨论架构", nil)
	f.next(t, "design")
	f.finish(t, "design", "初始设计已确认")
	f.mute(t, map[string]bool{"design": true})
	for i := range 24 {
		f.send(t, fmt.Sprintf("@开发 禁言期间用户消息第%d轮", i), nil)
		f.next(t, "developer")
		f.finish(t, "developer", fmt.Sprintf("@设计 禁言期间开发回复第%d轮", i))
	}
	f.noDispatch(t)
	f.service.Close()
	restarted, err := New(context.Background(), f.service.options)
	if err != nil {
		t.Fatal(err)
	}
	f.service = restarted
	t.Cleanup(restarted.Close)
	snapshot, err := restarted.get(context.Background(), f.group.ID)
	if err != nil || !snapshot.MemberStates["design"].Muted {
		t.Fatalf("restart lost mute state: %+v error=%v", snapshot, err)
	}
	f.mute(t, map[string]bool{"design": false})
	f.noDispatch(t)
	f.send(t, "@设计 开发总结：已经按确认的方案实现，请复核", nil)
	turn := f.next(t, "design")
	if !strings.Contains(turn.Prompt, "开发总结：已经按确认的方案实现") ||
		strings.Contains(turn.Prompt, "禁言期间") || strings.Contains(turn.Prompt, "先讨论架构") {
		t.Fatalf("restoration replayed skipped history: %s", turn.Prompt)
	}
	f.finish(t, "design", "复核通过")
}

func TestLateCompletionCannotRewindRestoredHistoryBoundary(t *testing.T) {
	f := newMemberMuteFixture(t, nil)
	f.send(t, "@设计 禁言前已开始的任务", nil)
	f.next(t, "design")
	f.mute(t, map[string]bool{"design": true, "developer": true})
	for i := range 30 {
		f.send(t, fmt.Sprintf("禁言期间第%d条记录", i), nil)
	}
	f.mute(t, map[string]bool{"design": false})
	// The old command completes after restoration and rewrites Delivered.
	f.finish(t, "design", "先前任务的最终结果")
	f.noDispatch(t)
	f.send(t, "@设计 恢复后的新任务", nil)
	turn := f.next(t, "design")
	if !strings.Contains(turn.Prompt, "恢复后的新任务") ||
		strings.Contains(turn.Prompt, "禁言期间") || strings.Contains(turn.Prompt, "禁言前已开始") {
		t.Fatalf("late completion rewound the history boundary: %s", turn.Prompt)
	}
	f.finish(t, "design", "新任务完成")
}

func TestMuteInvalidatesDecisionAlreadyInProgress(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	f := newMemberMuteFixture(t, decisionFunc(func(ctx context.Context, input DecisionInput) (Decision, error) {
		close(entered)
		select {
		case <-release:
			return sendAllDecider.Decide(ctx, input)
		case <-ctx.Done():
			return Decision{}, ctx.Err()
		}
	}))
	f.send(t, "@设计 暂未发送的任务", nil)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("System One did not start deciding")
	}
	f.mute(t, map[string]bool{"design": true})
	close(release)
	f.noDispatch(t)
	group := f.mute(t, map[string]bool{"design": false})
	if group.MemberStates["design"].Status == "waiting" {
		t.Fatal("restored member retained a suppressed pending task")
	}
	f.noDispatch(t)
}

func TestMutedGroupsStillUseSystemOneToJudgeCompletedWork(t *testing.T) {
	judgments := make(chan DecisionInput, 8)
	f := newMemberMuteFixture(t, decisionFunc(func(ctx context.Context, input DecisionInput) (Decision, error) {
		judgments <- input
		decision, err := sendAllDecider.Decide(ctx, input)
		if len(input.Candidates) == 0 {
			decision.Complete = false
		}
		return decision, err
	}))
	f.mute(t, map[string]bool{"design": true})
	f.send(t, "@开发 开始开发", nil)
	f.next(t, "developer")
	f.controller.replies["developer"] <- Reply{Text: "还需要用户确认一个问题", NativeIDs: []string{"pending-confirmation"}}
	waitGroup(t, f.service, f.group.ID, func(g Group) bool {
		return g.Coordination.Status == "waiting"
	})
	sawCompletionJudgment := false
	for len(judgments) > 0 {
		input := <-judgments
		if len(input.Candidates) == 0 {
			sawCompletionJudgment = len(input.Group.Muted) == 1 && input.Group.Muted[0] == "design"
		}
	}
	if !sawCompletionJudgment {
		t.Fatal("muting replaced the System One completion judgment")
	}
}

func TestAllMemberMutesAreAtomicAndDoNotReplayOnRestore(t *testing.T) {
	f := newMemberMuteFixture(t, nil)
	for _, raw := range []string{`{"design":true,"missing":true}`, `{"design":null}`, `{}`, `null`, `[]`} {
		_, err := f.service.update(context.Background(), f.group.ID, map[string]json.RawMessage{"member_mutes": json.RawMessage(raw)})
		if err == nil {
			t.Fatalf("invalid mute change accepted: %s", raw)
		}
		group, getErr := f.service.get(context.Background(), f.group.ID)
		if getErr != nil || group.MemberStates["design"].Muted {
			t.Fatalf("rejected mute change was partially applied: %+v error=%v", group, getErr)
		}
	}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/bot-groups/"+f.group.ID,
		strings.NewReader(`{"member_mutes":{"design":true,"developer":true}}`))
	request.Header.Set("Authorization", "Bearer fixture")
	response := httptest.NewRecorder()
	f.service.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"muted":true`) {
		t.Fatalf("mute endpoint failed: %d %s", response.Code, response.Body.String())
	}
	f.send(t, "@all 全员禁言期间的消息", nil)
	f.noDispatch(t)
	f.mute(t, map[string]bool{"design": false, "developer": false})
	f.noDispatch(t)
	f.send(t, "@all 恢复后的协作任务", nil)
	members := make(map[string]bool)
	for range 2 {
		select {
		case turn := <-f.controller.started:
			members[memberID(turn.Member)] = true
			if strings.Contains(turn.Prompt, "全员禁言期间") {
				t.Fatalf("all-member restoration replayed history: %s", turn.Prompt)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("restored member did not receive the new task")
		}
	}
	if !members["design"] || !members["developer"] {
		t.Fatalf("restoration missed a member: %v", members)
	}
}
