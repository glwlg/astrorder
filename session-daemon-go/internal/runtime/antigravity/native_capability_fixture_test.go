package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeCapabilityFixture(t *testing.T) {
	exe := os.Getenv("ASTRORDER_AGY_CAPABILITY_FIXTURE_EXE")
	if exe == "" {
		t.Skip("explicit isolated native protocol fixture")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, entry := range os.Environ() {
		for i, c := range entry {
			if c == '=' {
				key := entry[:i]
				if len(key) > 0 && (startsOverride(key)) {
					t.Setenv(key, "")
				}
				break
			}
		}
	}
	calls := make(chan struct{}, 20)
	cancelStarted := make(chan struct{}, 1)
	releaseCancel := make(chan struct{})
	defer close(releaseCancel)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		messages, _ := body["messages"].([]any)
		latest := ""
		for _, entry := range messages {
			message, _ := entry.(map[string]any)
			if message["role"] == "user" {
				raw, _ := json.Marshal(message["content"])
				latest = string(raw)
			}
		}
		if strings.Contains(latest, "Cancel fixture") {
			select {
			case cancelStarted <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
				return
			case <-releaseCancel:
				return
			}
		}
		select {
		case calls <- struct{}{}:
		default:
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []map[string]any{{"role": "assistant", "content": "Native capability fixture."}, {}} {
			reason := any(nil)
			if len(chunk) == 0 {
				reason = "stop"
			}
			raw, _ := json.Marshal(map[string]any{"id": "fixture", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": chunk, "finish_reason": reason}}, "usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 3, "total_tokens": 14}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	os.MkdirAll(filepath.Join(home, ".antigravity"), 0700)
	settings, _ := json.Marshal(map[string]any{"gateway": map[string]any{"enabled": true, "url": srv.URL + "/v1", "apiKey": "fixture-only", "wireProtocol": "openai"}, "models": map[string]any{"list": []any{map[string]any{"modelId": "probe", "displayName": "Probe"}}}})
	if err := os.WriteFile(filepath.Join(home, ".antigravity", "settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}
	a := New(Config{Executable: exe, Arguments: []string{"--gemini_dir=" + filepath.Join(home, ".gemini")}, Home: home, Workspace: home, Allowed: []string{home}})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	parent, err := a.Create(ctx, core.Request{Fields: map[string]any{"model": "probe"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: parent.SessionID, Action: "session.send", Fields: map[string]any{"text": "Native fixture", "command_id": "fixture"}}); err != nil {
		t.Fatal(err)
	}
	wait := func(id string) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && a.Snapshot()[id].Status == "running" {
			time.Sleep(20 * time.Millisecond)
		}
		if a.Snapshot()[id].Status != "idle" {
			t.Fatalf("native turn did not settle: %+v", a.Snapshot()[id])
		}
	}
	wait(parent.SessionID)
	child, err := a.Create(ctx, core.Request{Fields: map[string]any{"parent_session_id": parent.SessionID, "target_message_id": itemID(parent.SessionID, 1, "")}})
	if err != nil {
		t.Fatal(err)
	}
	if child.SessionID == parent.SessionID {
		t.Fatal("fork replaced parent")
	}
	history, _, err := readHistory(ctx, a.historyDir(), child.SessionID, "", 100)
	if err != nil || len(history) < 2 {
		t.Fatalf("fork history: %d %v", len(history), err)
	}
	if _, err = a.Command(ctx, core.Request{SessionID: child.SessionID, Action: "session.send", Fields: map[string]any{"text": "Continue fixture", "command_id": "child"}}); err != nil {
		t.Fatal(err)
	}
	wait(child.SessionID)
	t.Logf("native fork confirmed, parent preserved, child history=%d, child turn completed, local gateway calls=%d", len(history), len(calls))
	if _, err = a.Command(ctx, core.Request{SessionID: parent.SessionID, Action: "session.send", Fields: map[string]any{"text": "Cancel fixture", "command_id": "cancel"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelStarted:
	case <-ctx.Done():
		t.Fatal("cancel fixture request missing")
	}
	if _, err = a.Command(ctx, core.Request{SessionID: parent.SessionID, Action: "session.interrupt"}); err != nil {
		t.Fatal(err)
	}
	wait(parent.SessionID)
	t.Log("native cancellation settled by stream result")
	if _, err = a.Command(ctx, core.Request{SessionID: parent.SessionID, Action: "session.send", Fields: map[string]any{"text": "Continue after cancellation", "command_id": "after-cancel"}}); err != nil {
		t.Fatal(err)
	}
	wait(parent.SessionID)
	t.Log("same native conversation completed a turn after cancellation")
	if _, err = a.Command(ctx, core.Request{SessionID: child.SessionID, Action: "session.delete"}); err != nil {
		t.Fatal(err)
	}
	catalog, err := a.Query(ctx, core.Request{Fields: map[string]any{"method": "session.list"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog["items"].([]map[string]any) {
		if item["id"] == child.SessionID {
			t.Fatal("deleted child reappeared in native catalog")
		}
	}
	if err = a.validateResume(ctx, child.SessionID); err == nil {
		t.Fatal("deleted child remained resumable")
	}
	parentHistory, _, err := readHistory(ctx, a.historyDir(), parent.SessionID, "", 100)
	if err != nil || len(parentHistory) < 2 {
		t.Fatalf("child deletion damaged parent: %d %v", len(parentHistory), err)
	}
	t.Log("native deletion removed child from catalog, prevented resume and retained parent history")
}
func startsOverride(key string) bool {
	for _, p := range []string{"ASTRORDER_", "AGY_", "GEMINI_", "GOOGLE_", "ANTIGRAVITY_"} {
		if len(key) >= len(p) && key[:len(p)] == p {
			return true
		}
	}
	return false
}
