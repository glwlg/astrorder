package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
)

// This exercises the actual upstream agents and configured gateway on an isolated browser.
func TestRealUpstreamBrowserEngines(t *testing.T) {
	if os.Getenv("ASTRORDER_TEST_BROWSER_REAL") != "1" {
		t.Skip("requires isolated browser and model credentials")
	}
	var llm map[string]any
	var gateway map[string]any
	if selected := os.Getenv("ASTRORDER_TEST_ENGINE"); selected == "browser_budget" || selected == "cancel" {
		llm = map[string]any{"base_url": "http://127.0.0.1:1/v1", "api_key": "test-key", "model": "test", "reasoning": "low"}
		gateway = map[string]any{"gateway_type": "magpie", "inference_url": "http://127.0.0.1:1/v1", "api_key": "test-key"}
	} else {
		if json.Unmarshal([]byte(os.Getenv("ASTRORDER_TEST_LLM_JSON")), &llm) != nil ||
			json.Unmarshal([]byte(os.Getenv("ASTRORDER_TEST_GATEWAY_JSON")), &gateway) != nil {
			t.Fatal("model configuration is missing")
		}
	}
	edge, err := FindEdgeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "edge-profile")
	cmd := exec.Command(edge, "--headless=new", "--edge-skip-compat-layer-relaunch", "--disable-gpu",
		"--no-first-run", "--remote-debugging-port=0", "--user-data-dir="+profile, "about:blank")
	hideWindow(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var port, browserPath string
	portFile := filepath.Join(profile, "DevToolsActivePort")
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		if data, err := os.ReadFile(portFile); err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) >= 2 {
				port, browserPath = strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1])
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if port == "" {
		t.Fatal("isolated browser did not start")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if client, err := DialCDP(ctx, "ws://127.0.0.1:"+port+browserPath); err == nil {
			_, _ = client.Call(ctx, "Browser.close", nil)
			_ = client.Close()
		}
		_ = cmd.Wait()
		time.Sleep(time.Second)
	}()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>Isolated Test</title></head><body>
<label for="code">Code</label><input id="code">
<label for="notes">Notes</label><input id="notes" value="remove me">
<label for="color">Color</label><select id="color"><option>Red</option><option>Blue</option></select>
<button onclick="document.getElementById('result').textContent=document.getElementById('code').value+' '+document.getElementById('color').value+'|'+document.getElementById('notes').value">Confirm</button>
<p id="result">Not submitted</p></body></html>`))
	}))
	defer page.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetPreference(context.Background(), "services:llm", llm, true); err != nil {
		t.Fatal(err)
	}
	if _, err := modelgateway.SaveGatewayConfig(context.Background(), st, gateway); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(context.Background(), JevConfigPreference, JevConfig{Mode: "gateway", Model: "typesafe/jev-preview"}, true); err != nil {
		t.Fatal(err)
	}
	m := NewSessionManager("http://127.0.0.1:"+port, true)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	own, err := m.Navigate(ctx, "agent::own", page.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.Navigate(ctx, "agent::other", page.URL+"?guard=1", "")
	if err != nil {
		t.Fatal(err)
	}
	version, err := m.endpoints.GetVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	proxy, _, stop, err := startScopedCDP(ctx, version.WebSocketDebuggerURL, own.TargetID, func(target string) {
		m.rememberEngineTarget(ctx, "agent::own", target)
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := DialCDP(ctx, proxy)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := client.Call(ctx, "Target.getTargets", nil)
	if err != nil || strings.Contains(string(targets), other.TargetID) || !strings.Contains(string(targets), own.TargetID) {
		t.Fatal("scoped target enumeration failed", err)
	}
	if _, err := client.Call(ctx, "Target.attachToTarget", map[string]any{"targetId": other.TargetID, "flatten": true}); err == nil {
		t.Fatal("other session was accessible")
	}
	if _, err := client.Call(ctx, "Browser.close", nil); err == nil {
		t.Fatal("shared browser was closable")
	}
	newTargetRaw, err := client.Call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"})
	if err != nil {
		t.Fatal("new task tab could not be created", err)
	}
	var newTarget struct {
		ID string `json:"targetId"`
	}
	if json.Unmarshal(newTargetRaw, &newTarget) != nil || newTarget.ID == "" {
		t.Fatal("new task tab has no target ID")
	}
	registered := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		m.targetMu.Lock()
		registered = ownsTarget(m.sessionTargets["agent::own"], newTarget.ID) && !ownsTarget(m.sessionTargets["agent::other"], newTarget.ID)
		m.targetMu.Unlock()
		if registered {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !registered {
		t.Fatal("new task tab was not bound to the owning session")
	}
	if _, err := client.Call(ctx, "Target.attachToTarget", map[string]any{"targetId": newTarget.ID, "flatten": true}); err != nil {
		t.Fatal("new task tab could not be attached", err)
	}
	if _, err := client.Call(ctx, "Target.closeTarget", map[string]any{"targetId": newTarget.ID}); err != nil {
		t.Fatal("new task tab could not be closed", err)
	}
	_ = client.Close()
	stop()
	for _, engine := range []string{"browser_use", "jev_ultrafast"} {
		if selected := os.Getenv("ASTRORDER_TEST_ENGINE"); selected != "" && selected != engine {
			continue
		}
		t.Run(engine, func(t *testing.T) {
			if err := st.SetPreference(ctx, BrowserEnginePreference, engine, true); err != nil {
				t.Fatal(err)
			}
			goal := "Fill Code with the supplied value, select Blue, click Confirm, and report the result."
			inputs := map[string]string{"Code": "2417"}
			if engine == "jev_ultrafast" {
				goal = "Fill Code with the supplied value, clear Notes, select Blue, click Confirm, and report the result."
				inputs["Notes"] = ""
			}
			result, err := m.RunExternalBrowserTask(ctx, BrowserTaskRequest{
				URL: page.URL, SessionKey: "agent::own", MaxSteps: 20,
				Goal: goal, Inputs: inputs,
			}, st)
			if err != nil {
				t.Fatal(err)
			}
			if !result.OK || !strings.Contains(result.VisibleText, "2417 Blue|") ||
				(engine == "jev_ultrafast" && strings.Contains(result.VisibleText, "2417 Blue|remove me")) {
				t.Fatal(fmt.Sprintf("task not completed: status=%s page=%q", result.Status, result.VisibleText))
			}
			if engine == "browser_use" {
				actionCount := 0
				for _, step := range result.Steps {
					if actions, ok := step["actions"].([]any); ok {
						actionCount += len(actions)
					}
				}
				if actionCount > 20 {
					t.Fatal("browser task exceeded the action limit")
				}
			}
			t.Logf("completed in %d ms using %d steps", result.ElapsedMS, len(result.Steps))
			guard, err := m.ExtractPageContent(ctx, "agent::other", other.TargetID)
			if err != nil || !strings.Contains(guard.Text, "Not submitted") {
				t.Fatal("another session was changed", err)
			}
			if engine == "jev_ultrafast" {
				result, err = m.RunExternalBrowserTask(ctx, BrowserTaskRequest{
					URL: page.URL, SessionKey: "agent::own", MaxSteps: 20,
					Goal: "Enter 2417 in Code, select Blue, click Confirm, and report the result.",
				}, st)
				if err != nil || !result.OK || !strings.Contains(result.VisibleText, "2417 Blue") {
					t.Fatal("Jev text generation failed", err)
				}
			}
		})
	}
	if selected := os.Getenv("ASTRORDER_TEST_ENGINE"); selected == "" || selected == "browser_clear" {
		t.Run("browser_clear", func(t *testing.T) {
			if err := st.SetPreference(ctx, BrowserEnginePreference, "browser_use", true); err != nil {
				t.Fatal(err)
			}
			result, err := m.RunExternalBrowserTask(ctx, BrowserTaskRequest{
				URL: page.URL, SessionKey: "agent::own", Goal: "Clear the Notes field.",
				Inputs: map[string]string{"Notes": ""}, MaxSteps: 8,
			}, st)
			if err != nil {
				t.Fatal("empty input task failed", err)
			}
			if !result.OK {
				t.Fatalf("empty input task did not complete: status=%s answer=%q", result.Status, result.Answer)
			}
			active, _, err := m.SessionPage(ctx, "agent::own", false, false, result.BrowserSession)
			if err != nil {
				t.Fatal(err)
			}
			client, err := DialCDP(ctx, active.WebSocketDebuggerURL)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			raw, err := client.Call(ctx, "Runtime.evaluate", map[string]any{
				"expression": "document.getElementById('notes').value", "returnByValue": true,
			})
			var value struct {
				Result struct {
					Value *string `json:"value"`
				} `json:"result"`
			}
			if err != nil || json.Unmarshal(raw, &value) != nil || value.Result.Value == nil || *value.Result.Value != "" {
				t.Fatal("the Notes field was not cleared", err)
			}
		})
	}
	if selected := os.Getenv("ASTRORDER_TEST_ENGINE"); selected == "" || selected == "browser_budget" {
		t.Run("browser_budget", func(t *testing.T) {
			var requests atomic.Int32
			var reasoningCorrect atomic.Bool
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var request map[string]any
				if json.NewDecoder(r.Body).Decode(&request) == nil {
					reasoning, _ := request["reasoning"].(map[string]any)
					reasoningCorrect.Store(reasoning["effort"] == "low" && request["reasoning_effort"] == nil)
				}
				actions := []map[string]any{
					{"wait": map[string]any{"seconds": 0}},
					{"wait": map[string]any{"seconds": 0}},
					{"wait": map[string]any{"seconds": 0}},
				}
				content, _ := json.Marshal(map[string]any{
					"evaluation_previous_goal": "Starting", "memory": "Keep waiting", "next_goal": "Wait", "action": actions,
				})
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "fixture", "object": "chat.completion", "created": 1, "model": "gpt-5.1-mini",
					"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
						"message": map[string]any{"role": "assistant", "content": string(content)}}},
					"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
				})
			}))
			defer model.Close()
			if err := st.SetPreference(ctx, BrowserEnginePreference, "browser_use", true); err != nil {
				t.Fatal(err)
			}
			if err := st.SetPreference(ctx, "services:llm", map[string]any{
				"base_url": model.URL + "/v1", "api_key": "test-key", "model": "gpt-5.1-mini", "reasoning": "low",
			}, true); err != nil {
				t.Fatal(err)
			}
			result, err := m.RunExternalBrowserTask(ctx, BrowserTaskRequest{
				URL: page.URL, SessionKey: "agent::own", Goal: "Wait for the page", MaxSteps: 2,
			}, st)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "step_limit" || len(result.Steps) != 1 || requests.Load() != 1 || !reasoningCorrect.Load() {
				t.Fatalf("action budget was not enforced: status=%s steps=%d requests=%d", result.Status, len(result.Steps), requests.Load())
			}
			actions, ok := result.Steps[0]["actions"].([]any)
			if !ok || len(actions) != 2 {
				t.Fatal("model's excess actions were not capped")
			}
		})
	}
	if selected := os.Getenv("ASTRORDER_TEST_ENGINE"); selected == "" || selected == "cancel" {
		t.Run("cancel", func(t *testing.T) {
			arrived := make(chan struct{}, 1)
			release := make(chan struct{})
			slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				arrived <- struct{}{}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer slow.Close()
			defer close(release)
			if err := st.SetPreference(ctx, BrowserEnginePreference, "browser_use", true); err != nil {
				t.Fatal(err)
			}
			if err := st.SetPreference(ctx, "services:llm", map[string]any{
				"base_url": slow.URL + "/v1", "api_key": "test-key", "model": "gpt-5.1-mini", "reasoning": "low",
			}, true); err != nil {
				t.Fatal(err)
			}
			taskCtx, taskCancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() {
				_, err := m.RunExternalBrowserTask(taskCtx, BrowserTaskRequest{
					URL: page.URL, SessionKey: "agent::own", Goal: "Read the page", MaxSteps: 10,
				}, st)
				done <- err
			}()
			select {
			case <-arrived:
			case err := <-done:
				taskCancel()
				t.Fatal("task stopped before its model call", err)
			case <-time.After(time.Minute):
				taskCancel()
				t.Fatal("task did not reach its model call")
			}
			taskCancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled task returned success")
				}
			case <-time.After(15 * time.Second):
				t.Fatal("cancelled task did not release its worker")
			}
			if _, err := m.ExtractPageContent(ctx, "agent::other", other.TargetID); err != nil {
				t.Fatal("cancellation affected another browser session", err)
			}
		})
	}
}
