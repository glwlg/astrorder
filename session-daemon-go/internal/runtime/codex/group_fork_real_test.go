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

// Runs a real Codex process with isolated files and a local deterministic model.
func TestRealCodexGroupForkPreservesParentHistory(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/responses" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		index := requests.Add(1)
		answer := "parent-answer"
		if index > 1 {
			answer = "child-answer"
		}
		item := map[string]any{"id": fmt.Sprintf("msg_%d", index), "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": answer, "annotations": []any{}}}, "status": "completed", "phase": "final_answer"}
		frames := []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("response_%d", index), "object": "response", "status": "in_progress", "output": []any{}}},
			{"type": "response.output_item.added", "output_index": 0, "item": item},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("response_%d", index), "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}},
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range frames {
			data, _ := json.Marshal(frame)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame["type"], data)
		}
	}))
	defer server.Close()
	workspace, home := t.TempDir(), t.TempDir()
	config := fmt.Sprintf("model=\"gpt-5-codex\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Local fork fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\n", server.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	completed := make(chan string, 4)
	a := NewAdapter(Config{Executable: executable, Workspace: workspace, Allowed: []string{workspace}, AgentID: "fork-fixture", Environment: []string{"CODEX_HOME=" + home}}, func(session, event string, payload map[string]any, _ string) error {
		frame, _ := payload["frame"].(map[string]any)
		if event == "codex.notification" && frame["method"] == "turn/completed" {
			completed <- session
		}
		return nil
	})
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	parent, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace}})
	if err != nil {
		t.Fatal(err)
	}
	send := func(id, prompt string) {
		t.Helper()
		if _, err := a.Command(ctx, core.Request{Action: "session.send", SessionID: id, Fields: map[string]any{"prompt": prompt, "command_id": prompt}}); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-completed:
			if got != id {
				t.Fatalf("wrong completed session %q", got)
			}
		case <-ctx.Done():
			t.Fatal("native turn did not complete")
		}
	}
	history := func(id string) string {
		t.Helper()
		page, err := a.Command(ctx, core.Request{Action: "session.history_page", SessionID: id, Fields: map[string]any{"limit": 100}})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(page.Payload["items"])
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	send(parent.SessionID, "parent-question")
	before := history(parent.SessionID)
	if !strings.Contains(before, "parent-answer") {
		t.Fatalf("parent history missing: %s", before)
	}
	child, err := a.Create(ctx, core.Request{Fields: map[string]any{"cwd": workspace, "parent_session_id": parent.SessionID}})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if child.SessionID == "" || child.SessionID == parent.SessionID {
		t.Fatal("fork did not create a distinct session")
	}
	if inherited := history(child.SessionID); !strings.Contains(inherited, "parent-answer") {
		t.Fatalf("child did not inherit source history: %s", inherited)
	}
	send(child.SessionID, "child-question")
	if after := history(parent.SessionID); after != before {
		t.Fatalf("parent history changed: before=%s after=%s", before, after)
	}
	if after := history(child.SessionID); !strings.Contains(after, "parent-answer") || !strings.Contains(after, "child-answer") {
		t.Fatalf("child history did not diverge: %s", after)
	}
	if requests.Load() != 2 {
		t.Fatalf("unexpected model requests: %d", requests.Load())
	}
}
