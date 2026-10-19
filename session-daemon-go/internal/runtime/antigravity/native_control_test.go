package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestForkUsesNativeBoundaryAndKeepsParent(t *testing.T) {
	exe, _ := os.Executable()
	dir := t.TempDir()
	a := New(Config{Executable: exe, Arguments: []string{"-test.run=TestStreamHelper", "--"}, Environment: []string{"ASTRORDER_AGY_HELPER=1"}, Workspace: dir, Allowed: []string{dir}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	parent, err := a.Create(ctx, core.Request{Fields: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "GetConversationMetadata") {
			json.NewEncoder(w).Encode(map[string]any{"numSteps": 4})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "ForkConversation") {
			t.Errorf("unexpected method %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		calls++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["sourceCascadeId"] != parent.SessionID || body["forkAtStepIndex"] != float64(1) {
			t.Errorf("native boundary: %+v", body)
		}
		json.NewEncoder(w).Encode(map[string]any{"newCascadeId": "22222222-2222-4222-8222-222222222222", "forkedAtStepIndex": 1})
	}))
	defer srv.Close()
	a.mu.Lock()
	s := a.sessions[parent.SessionID]
	a.mu.Unlock()
	s.mu.Lock()
	s.nativeURL = srv.URL
	s.mu.Unlock()
	child, err := a.Create(ctx, core.Request{Fields: map[string]any{"parent_session_id": parent.SessionID, "target_message_id": itemID(parent.SessionID, 1, ""), "cwd": dir}})
	if err != nil {
		t.Fatal(err)
	}
	if child.SessionID == parent.SessionID || child.SessionID != "22222222-2222-4222-8222-222222222222" || calls != 1 {
		t.Fatalf("native fork: %+v calls=%d", child, calls)
	}
	if a.Snapshot()[parent.SessionID].Status != "idle" {
		t.Fatal("parent transport changed")
	}
	s.mu.Lock()
	s.status = "running"
	s.mu.Unlock()
	if _, err = a.Create(ctx, core.Request{Fields: map[string]any{"parent_session_id": parent.SessionID}}); err == nil {
		t.Fatal("active fork accepted")
	}
	if calls != 1 {
		t.Fatal("active fork dispatched")
	}
}

func TestInterruptWaitsForNativeResult(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; json.NewEncoder(w).Encode(map[string]any{}) }))
	defer srv.Close()
	a := New(Config{})
	s := &session{id: "11111111-1111-4111-8111-111111111111", status: "running", command: "send-command", nativeURL: srv.URL}
	a.sessions[s.id] = s
	result, err := a.Command(context.Background(), core.Request{SessionID: s.id, Action: "session.interrupt"})
	if err != nil || result.Payload["accepted"] != true || calls != 1 {
		t.Fatalf("interrupt: %+v %v calls=%d", result, err, calls)
	}
	if s.status != "running" || s.command != "send-command" {
		t.Fatal("cancel RPC was mistaken for completed turn")
	}
}
