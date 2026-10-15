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
)

// The native CLI runs against injected model output, never a user's API or home.
func TestRealCodexOrdinaryModeUserInput(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	var count atomic.Int32
	bodies := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		bodies <- body
		var item map[string]any
		if count.Add(1) == 1 {
			item = map[string]any{"id": "fc_movie", "type": "function_call", "call_id": "movie_call", "name": "request_user_input", "arguments": `{"questions":[{"id":"movie","header":"电影","question":"喜欢哪种电影？","options":[{"label":"科幻片","description":"未来科技"},{"label":"喜剧片","description":"轻松幽默"}]}]}`, "status": "completed"}
		} else {
			item = map[string]any{"id": "msg_done", "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fixture done.", "annotations": []any{}}}, "status": "completed", "phase": "final_answer"}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": "response_fixture", "object": "response", "status": "in_progress", "output": []any{}}},
			{"type": "response.output_item.added", "output_index": 0, "item": item},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": "response_fixture", "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}},
		} {
			data, _ := json.Marshal(frame)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame["type"], data)
		}
	}))
	defer server.Close()
	root, err := os.MkdirTemp(os.Getenv("TMPDIR"), "codex-question-native-")
	if err != nil {
		t.Fatal(err)
	}
	home, work := filepath.Join(root, "home"), filepath.Join(root, "work")
	for _, directory := range []string{home, work} {
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		var cleanupErr error
		for deadline := time.Now().Add(3 * time.Second); ; {
			cleanupErr = os.RemoveAll(root)
			if cleanupErr == nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if cleanupErr != nil {
			t.Errorf("isolated native process left open files: %v", cleanupErr)
		}
	})
	config := fmt.Sprintf("model=\"gpt-5-codex\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Local question fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\n", server.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	notifications := make(chan Notification, 64)
	runtime := New(Config{Executable: executable, Workspace: work, Allowed: []string{work}, Environment: []string{"CODEX_HOME=" + home}, OnNotification: func(n Notification) { notifications <- n }})
	defer runtime.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, err := runtime.Create(ctx, Request{Cwd: work, Params: map[string]any{"approvalPolicy": "never", "sandbox": "danger-full-access"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Command(ctx, Command{Action: "session.send", SessionID: created.SessionID, Text: "Ask a movie choice using the native tool."}); err != nil {
		t.Fatal(err)
	}
	answered := false
	for {
		select {
		case n := <-notifications:
			if n.Method == "item/tool/requestUserInput" {
				if answered {
					t.Fatal("duplicate question")
				}
				if runtime.Status() != "waiting_approval" {
					t.Fatalf("status=%s", runtime.Status())
				}
				_, err = runtime.Command(ctx, Command{Action: "session.approve", SessionID: created.SessionID, ApprovalID: n.ApprovalID, Decision: "accept", Options: map[string]any{"answers": map[string]any{"movie": map[string]any{"answers": []any{"科幻片"}}}}})
				if err != nil {
					t.Fatal(err)
				}
				answered = true
			}
			if n.Method == "turn/completed" {
				if !answered {
					t.Fatal("ordinary-mode question never reached Astrorder")
				}
				first, second := <-bodies, <-bodies
				tools, _ := first["tools"].([]any)
				available := false
				for _, raw := range tools {
					tool, _ := raw.(map[string]any)
					if tool["name"] == "request_user_input" {
						available = strings.Contains(fmt.Sprint(tool["description"]), "Default")
					}
				}
				if !available {
					t.Fatal("native tool unavailable in Default mode")
				}
				input, _ := second["input"].([]any)
				returned := false
				for _, raw := range input {
					row, _ := raw.(map[string]any)
					if row["type"] == "function_call_output" && row["call_id"] == "movie_call" {
						returned = strings.Contains(fmt.Sprint(row["output"]), "科幻片")
					}
				}
				if !returned {
					t.Fatal("answer did not return to original native tool call")
				}
				t.Log("real Codex + actual Go runtime + isolated Responses fixture: Default-mode question, explicit answer, same native call, completion passed")
				return
			}
		case <-ctx.Done():
			t.Fatal("native question/answer timed out", ctx.Err())
		}
	}
}
