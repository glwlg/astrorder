package botgroups

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

type controlledAgents struct {
	started     chan Turn
	interrupted chan Turn
	release     chan struct{}
}

func (c *controlledAgents) Run(ctx context.Context, turn Turn, bound func(string) error) (Reply, error) {
	if err := bound("group-native-session"); err != nil {
		return Reply{}, err
	}
	c.started <- turn
	select {
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	case <-c.release:
		return Reply{Text: "完成", NativeIDs: []string{"reply"}}, nil
	}
}
func (c *controlledAgents) Interrupt(_ context.Context, turn Turn) error {
	c.interrupted <- turn
	return nil
}

type publisher struct {
	mu     sync.Mutex
	events []store.Event
}

func (p *publisher) PublishCommitted(events []store.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, events...)
}

func TestGroupStopCancelsOnlyBoundMemberAndPreservesMessages(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	controller := &controlledAgents{started: make(chan Turn, 2), interrupted: make(chan Turn, 2), release: make(chan struct{})}
	service, err := New(context.Background(), Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	call := func(method, path, body string) map[string]any {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer fixture")
		response := httptest.NewRecorder()
		service.ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload
	}
	payload := call(http.MethodPost, "/api/v1/bot-groups", `{"name":"专家群","max_hops":3,"members":[{"machine_id":"local","agent_id":"a","name":"甲"},{"machine_id":"local","agent_id":"b","name":"乙"}]}`)
	id := payload["group"].(map[string]any)["id"].(string)
	message := call(http.MethodPost, "/api/v1/bot-groups/"+id+"/messages", `{"text":"请 @乙 处理"}`)
	if message["target_agent_id"] != "b" {
		t.Fatal("mention order lost")
	}
	select {
	case turn := <-controller.started:
		if turn.Member.AgentID != "b" || !strings.Contains(turn.Prompt, "请 @乙 处理") {
			t.Fatalf("invalid dispatch: %v", turn)
		}
	case <-time.After(time.Second):
		t.Fatal("no member dispatched")
	}
	stopped := call(http.MethodPost, "/api/v1/bot-groups/"+id+"/stop", "")
	if stopped["group"].(map[string]any)["active_hop"].(float64) != 0 {
		t.Fatal("group not stopped")
	}
	select {
	case turn := <-controller.interrupted:
		if turn.Member.AgentID != "b" || turn.SessionID != "group-native-session" {
			t.Fatal("wrong native session interrupted")
		}
	case <-time.After(time.Second):
		t.Fatal("stop did not interrupt bound member")
	}
	messages := call(http.MethodGet, "/api/v1/bot-groups/"+id+"/messages", "")
	if messages["count"].(float64) != 1 {
		t.Fatal("user history lost")
	}
	service.Close()
	prefs, err := st.ListPreferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if prefs["bot_group:"+id] == nil || prefs["bot_group_msgs:"+id] == nil {
		t.Fatal("legacy keys not persisted")
	}
	call(http.MethodDelete, "/api/v1/bot-groups/"+id, "")
	if result := call(http.MethodGet, "/api/v1/bot-groups", ""); result["count"].(float64) != 0 {
		t.Fatal("delete failed")
	}
}

func TestRestartPreservesUnconfirmedTurnWithoutRedispatch(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	member := Member{AgentID: "a", MachineID: "local"}
	g := Group{ID: "g", Name: "群", Members: []Member{member}, MaxHops: 3, ActiveHop: 1, ActiveSpeakers: []string{"a"}, MemberStates: map[string]MemberState{"a": {Status: "running", SessionID: "native"}}}
	if err = st.SetPreference(ctx, "bot_group:g", g, true); err != nil {
		t.Fatal(err)
	}
	controller := &controlledAgents{started: make(chan Turn, 1), interrupted: make(chan Turn, 1), release: make(chan struct{})}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	recovered, err := service.get(ctx, "g")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.MemberStates["a"].Status != "unknown" {
		t.Fatalf("running status was fabricated after restart: %+v", recovered)
	}
	_, err = service.send(ctx, "g", map[string]json.RawMessage{"text": json.RawMessage(`"任务"`)})
	if err == nil {
		t.Fatal("unconfirmed turn was eligible for redispatch")
	}
	if _, err = service.stop(ctx, "g"); err != nil {
		t.Fatal(err)
	}
	select {
	case turn := <-controller.interrupted:
		if turn.SessionID != "native" || turn.Member.AgentID != "a" {
			t.Fatal("wrong recovered turn")
		}
	default:
		t.Fatal("recovered session not explicitly interrupted")
	}
}

func TestConcurrentSendReservesMemberBeforeAcceptingSecondMessage(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	controller := &controlledAgents{started: make(chan Turn, 4), interrupted: make(chan Turn, 4), release: make(chan struct{})}
	service, err := New(ctx, Options{Store: st, Controller: controller, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: &publisher{}})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	g, err := service.create(ctx, map[string]json.RawMessage{"name": json.RawMessage(`"群"`), "members": json.RawMessage(`[{"machine_id":"local","agent_id":"a"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := service.send(ctx, g.ID, map[string]json.RawMessage{"text": json.RawMessage(`"任务"`)})
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d submissions for one running member", accepted)
	}
	messages, err := service.messages(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("accepted-but-undispatched messages: %d", len(messages))
	}
}
