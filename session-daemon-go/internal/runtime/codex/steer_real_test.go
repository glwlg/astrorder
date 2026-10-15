package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// This runs the installed native CLI against a deterministic local Responses
// fixture. No user home, credentials, chats, remote API or model is used.
func TestRealCodexGuidanceWithIsolatedResponsesFixture(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	modelStarted := make(chan struct{}, 1)
	releaseModel := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Method != http.MethodPost {
			http.Error(w, "fixture only accepts Responses", http.StatusNotFound)
			return
		}
		requests.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad fixture JSON", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"fixture-response\",\"object\":\"response\",\"status\":\"in_progress\",\"output\":[]}}\n\n")
		w.(http.Flusher).Flush()
		select {
		case modelStarted <- struct{}{}:
		default:
		}
		select {
		case <-releaseModel:
		case <-r.Context().Done():
		}
	}))
	defer func() { close(releaseModel); server.Close() }()
	workspace, home := t.TempDir(), t.TempDir()
	config := fmt.Sprintf("model = \"gpt-5-codex\"\nmodel_provider = \"guidance_fixture\"\n[model_providers.guidance_fixture]\nname = \"Local guidance fixture\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\n", server.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	a := NewAdapter(Config{Executable: executable, Workspace: workspace, Allowed: []string{workspace}, AgentID: "steer-real", Environment: []string{"CODEX_HOME=" + home}}, nil)
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace}})
	if err != nil {
		t.Fatalf("native create: %v", err)
	}
	started, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: created.SessionID, Fields: map[string]any{"prompt": "Wait for the local fixture reply.", "command_id": "initial-real"}})
	if err != nil {
		t.Fatalf("native start: %v", err)
	}
	select {
	case <-modelStarted:
	case <-ctx.Done():
		t.Fatal("native CLI did not reach isolated Responses fixture")
	}
	guided, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: created.SessionID, Fields: map[string]any{"prompt": "Use the updated guidance.", "command_id": "guidance-real"}})
	if err != nil || guided.Payload["accepted"] != true || guided.Payload["completed"] != true || guided.Payload["turn_id"] != started.Payload["turn_id"] {
		t.Fatalf("native guidance: %+v %v", guided, err)
	}
	a.mu.Lock()
	runtime := a.sessions[created.SessionID].runtime
	a.mu.Unlock()
	if runtime.Status() != "running" {
		t.Fatalf("guidance ended original turn: %s", runtime.Status())
	}
	if requests.Load() != 1 {
		t.Fatalf("guidance started another model request: %d", requests.Load())
	}
	turn, _ := started.Payload["turn_id"].(string)
	if _, err := a.Command(ctx, core.Request{Action: "session.interrupt", SessionID: created.SessionID, Fields: map[string]any{"turn_id": turn}}); err != nil && !strings.Contains(err.Error(), "not the active") {
		t.Fatalf("isolated cleanup interrupt: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.Status() == "running" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{home, workspace} {
		var err error
		for until := time.Now().Add(3 * time.Second); ; {
			err = os.RemoveAll(dir)
			if err == nil || time.Now().After(until) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("isolated native process left open files: %v", err)
		}
	}
	t.Log("real Codex process + isolated home + loopback Responses fixture: exact original turn acknowledged; isolated process and files cleaned")
}
