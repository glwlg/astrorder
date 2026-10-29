package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestScopedCDPOnlyExposesOwnedTargets(t *testing.T) {
	s := &scopedCDP{
		primary: "own", targets: map[string]bool{"own": true}, sessions: map[string]bool{},
		pending: map[string]string{},
	}
	if !s.allowRequest(map[string]any{"id": 1, "method": "Target.getTargets"}) {
		t.Fatal("target enumeration was denied")
	}
	result := s.filterResponse(map[string]any{"id": 1, "result": map[string]any{"targetInfos": []any{
		map[string]any{"targetId": "own"}, map[string]any{"targetId": "other", "title": "private"},
	}}})
	infos := result[0]["result"].(map[string]any)["targetInfos"].([]any)
	if len(infos) != 1 || infos[0].(map[string]any)["targetId"] != "own" {
		t.Fatal("another session was exposed", infos)
	}
	if s.allowRequest(map[string]any{"id": 2, "method": "Target.attachToTarget", "params": map[string]any{"targetId": "other"}}) {
		t.Fatal("another session was attachable")
	}
	if s.allowRequest(map[string]any{"id": 3, "method": "Browser.close"}) {
		t.Fatal("shared browser was closable")
	}
	if s.allowRequest(map[string]any{"id": 3, "method": "Target.setAutoAttach", "params": map[string]any{
		"autoAttach": true, "flatten": true, "waitForDebuggerOnStart": true,
	}}) {
		t.Fatal("auto-attach could pause other sessions")
	}
	if !s.allowRequest(map[string]any{"id": 3, "method": "Target.setAutoAttach", "params": map[string]any{
		"autoAttach": true, "flatten": true, "waitForDebuggerOnStart": false,
	}}) {
		t.Fatal("safe auto-attach was denied")
	}
	if s.allowRequest(map[string]any{"id": 3, "method": "Target.closeTarget", "params": map[string]any{"targetId": "own"}}) {
		t.Fatal("the session's primary tab was closable by the engine")
	}
	attached := map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{
		"sessionId": "own-session", "targetInfo": map[string]any{"targetId": "own", "type": "page"},
	}}
	if len(s.filterResponse(attached)) != 1 || !s.allowRequest(map[string]any{"id": 4, "method": "Runtime.evaluate", "sessionId": "own-session"}) {
		t.Fatal("owned session was not usable")
	}
	if s.allowRequest(map[string]any{"id": 5, "method": "Runtime.evaluate", "sessionId": "other-session"}) {
		t.Fatal("another CDP session was usable")
	}
	if len(s.filterResponse(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{
		"targetInfo": map[string]any{"targetId": "other", "title": "private"},
	}})) != 0 {
		t.Fatal("another session's event was exposed")
	}
}

func TestScopedCDPRegistersCreatedTargets(t *testing.T) {
	created := make(chan string, 1)
	s := &scopedCDP{
		targets: map[string]bool{"own": true}, sessions: map[string]bool{},
		pending: map[string]string{}, onTarget: func(id string) { created <- id },
	}
	if !s.allowRequest(map[string]any{"id": 9, "method": "Target.createTarget", "params": map[string]any{"url": "about:blank"}}) {
		t.Fatal("own target creation was denied")
	}
	event := map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{
		"sessionId": "new-session", "targetInfo": map[string]any{"targetId": "new", "type": "page"},
	}}
	if len(s.filterResponse(event)) != 0 {
		t.Fatal("unconfirmed target appeared early")
	}
	result := s.filterResponse(map[string]any{"id": 9, "result": map[string]any{"targetId": "new"}})
	if len(result) != 2 || !s.allowRequest(map[string]any{"id": 10, "method": "Runtime.evaluate", "sessionId": "new-session"}) {
		t.Fatal("created target events were not restored")
	}
	if <-created != "new" {
		t.Fatal("created target was not registered")
	}
}

func TestScopedCDPKeepsEventsForConcurrentTargetCreations(t *testing.T) {
	s := &scopedCDP{
		targets: map[string]bool{"own": true}, sessions: map[string]bool{},
		pending: map[string]string{},
	}
	for _, id := range []int{1, 2} {
		if !s.allowRequest(map[string]any{"id": id, "method": "Target.createTarget"}) {
			t.Fatal("target creation was denied")
		}
	}
	for _, target := range []string{"first", "second"} {
		s.filterResponse(map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{
			"sessionId": target + "-session", "targetInfo": map[string]any{"targetId": target, "type": "page"},
		}})
	}
	for index, target := range []string{"first", "second"} {
		response := s.filterResponse(map[string]any{"id": index + 1, "result": map[string]any{"targetId": target}})
		if len(response) != 2 || !s.allowRequest(map[string]any{
			"id": 3, "method": "Runtime.evaluate", "sessionId": target + "-session",
		}) {
			t.Fatalf("events for %s were lost", target)
		}
	}
}

func TestBrowserTaskWaitingForSessionCanBeCancelled(t *testing.T) {
	manager := NewSessionManager("", true)
	store := newEngineStore(t)
	lock := manager.getTaskLock(fixtureSessionKey)
	lock <- struct{}{}
	defer func() { <-lock }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := manager.RunExternalBrowserTask(ctx, BrowserTaskRequest{
		URL: "https://example.com", Goal: "read", SessionKey: fixtureSessionKey,
	}, store)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("waiting task did not stop on cancellation: %v", err)
	}
}

func TestScopedCDPReconnectResetsConnectionState(t *testing.T) {
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	upstream := "ws" + fake.server.URL[len("http"):] + "/devtools/browser"
	url, state, stop, err := startScopedCDP(ctx, upstream, "own", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	first, err := DialCDP(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DialCDP(ctx, url)
	if err == nil {
		second.Close()
		first.Close()
		t.Fatal("concurrent connections were accepted")
	}
	state.mu.Lock()
	state.sessions["stale"] = true
	state.pending["1"] = "Target.getTargets"
	state.mu.Unlock()
	first.Close()
	for {
		state.mu.Lock()
		active := state.active
		state.mu.Unlock()
		if !active {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("first connection did not close")
		case <-time.After(10 * time.Millisecond):
		}
	}
	second, err = DialCDP(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.sessions) != 0 || len(state.pending) != 0 || !state.ownsTarget("own") {
		t.Fatal("reconnection did not clear connection state while preserving owned targets")
	}
}
