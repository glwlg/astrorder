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

// Actual installed CLI and Go adapter, with injected model output in a clean home.
func TestRealCodexAsyncQuestionUsesOrdinaryInput(t *testing.T) {
	executable := os.Getenv("ASTRORDER_REAL_CODEX")
	if executable == "" {
		t.Skip("ASTRORDER_REAL_CODEX is not configured")
	}
	var count atomic.Int32
	bodies := make(chan map[string]any, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		bodies <- body
		n := count.Add(1)
		item := map[string]any{"id": fmt.Sprintf("msg_%d", n), "type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fixture continues.", "annotations": []any{}}}, "status": "completed", "phase": "final_answer"}
		if n == 1 {
			item = map[string]any{"id": "fc_async", "type": "function_call", "call_id": "async_call", "name": "request_user_input_async", "arguments": `{"questions":[{"title":"喜欢哪种题材？","options":["时间旅行","人工智能"]}]}`, "status": "completed"}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []map[string]any{
			{"type": "response.created", "response": map[string]any{"id": fmt.Sprintf("r%d", n), "object": "response", "status": "in_progress", "output": []any{}}},
			{"type": "response.output_item.added", "output_index": 0, "item": item},
			{"type": "response.output_item.done", "output_index": 0, "item": item},
			{"type": "response.completed", "response": map[string]any{"id": fmt.Sprintf("r%d", n), "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}},
		} {
			raw, _ := json.Marshal(frame)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", frame["type"], raw)
		}
	}))
	defer server.Close()
	root, err := os.MkdirTemp(os.Getenv("TMPDIR"), "codex-async-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for deadline := time.Now().Add(3 * time.Second); ; {
			err := os.RemoveAll(root)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Error(err)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	home, work := filepath.Join(root, "home"), filepath.Join(root, "work")
	for _, p := range []string{home, work} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("model=\"codex/gpt-6.1-sol\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\n", server.URL)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	notifications := make(chan Notification, 256)
	runtime := New(Config{Executable: executable, Arguments: []string{"app-server", "--listen", "stdio://", "--enable", "default_mode_request_user_input"}, Workspace: work, Allowed: []string{work}, Environment: []string{"CODEX_HOME=" + home}, OnNotification: func(n Notification) { notifications <- n }})
	defer runtime.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	created, err := runtime.Create(ctx, Request{Cwd: work, Params: map[string]any{"approvalPolicy": "never", "sandbox": "danger-full-access"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Command(ctx, Command{Action: "session.send", SessionID: created.SessionID, Text: "Ask async fixture question."}); err != nil {
		t.Fatal(err)
	}
	questionSeen, answerSent, answerEcho := false, false, false
	for {
		select {
		case n := <-notifications:
			if n.Method == "item/tool/requestUserInput" {
				t.Fatal("async generated a synchronous request")
			}
			params, _ := n.Frame["params"].(map[string]any)
			item, _ := params["item"].(map[string]any)
			if item["delivery"] == "async" && item["questions"] != nil {
				questionSeen = true
			}
			if answerSent && item["type"] == "userMessage" {
				raw, _ := json.Marshal(item)
				if strings.Contains(string(raw), "时间旅行") {
					answerEcho = true
				}
			}
			if n.Method == "turn/completed" {
				if !answerSent {
					if !questionSeen {
						for len(bodies) > 0 {
							b := <-bodies
							for _, x := range b["input"].([]any) {
								v, _ := x.(map[string]any)
								if v["type"] == "function_call_output" {
									t.Logf("fixture tool output: %v", v)
								}
							}
							for _, x := range b["tools"].([]any) {
								v, _ := x.(map[string]any)
								if strings.Contains(fmt.Sprint(v["name"]), "input") {
									t.Logf("fixture tool: %v", v)
								}
							}
						}
						t.Fatal("native async structured question missing")
					}
					if _, err := runtime.Command(ctx, Command{Action: "session.send", SessionID: created.SessionID, Text: "时间旅行"}); err != nil {
						t.Fatal(err)
					}
					answerSent = true
				} else {
					if !answerEcho {
						t.Fatal("native user answer missing")
					}
					found := false
					for len(bodies) > 0 {
						raw, _ := json.Marshal(<-bodies)
						if strings.Contains(string(raw), "时间旅行") {
							found = true
						}
					}
					if !found {
						t.Fatal("answer never reached model input")
					}
					t.Log("installed CLI async question + ordinary user answer + native echo + continuation verified; injected model output")
					return
				}
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
