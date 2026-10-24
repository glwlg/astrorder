package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestServer(t *testing.T, requests *atomic.Int32, recorded *[]observedRequest) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		var body struct {
			Capability string         `json:"capability"`
			Input      map[string]any `json:"input"`
		}
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if recorded != nil {
			*recorded = append(*recorded, observedRequest{
				capability:    body.Capability,
				input:         body.Input,
				authorization: r.Header.Get("Authorization"),
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
}

func baseEnv(serverURL string) map[string]string {
	return map[string]string{
		"ASTRORDER_AGENT_TOKEN":          "test-token",
		"ASTRORDER_AGENT_URL":            serverURL,
		"ASTRORDER_CALLER_AGENT_ID":      "hermes",
		"ASTRORDER_CALLER_CONNECTION_ID": "local",
		"ASTRORDER_CALLER_PROFILE_NAME":  "default",
		"HERMES_SESSION_ID":              "hermes-session-42",
	}
}

func TestCallerMissingRequiredVarsFailsBeforeHTTP(t *testing.T) {
	requiredKeys := []string{
		"ASTRORDER_CALLER_AGENT_ID",
		"ASTRORDER_CALLER_CONNECTION_ID",
		"ASTRORDER_CALLER_PROFILE_NAME",
	}

	for _, keyToOmit := range requiredKeys {
		t.Run("missing "+keyToOmit, func(t *testing.T) {
			var requests atomic.Int32
			server := newTestServer(t, &requests, nil)
			defer server.Close()

			env := baseEnv(server.URL)
			delete(env, keyToOmit)

			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
			if code == 0 {
				t.Fatalf("expected non-zero exit code, got 0")
			}
			if requests.Load() != 0 {
				t.Fatalf("HTTP request count = %d; expected 0 requests on missing %s", requests.Load(), keyToOmit)
			}
			if !strings.Contains(stderr.String(), "missing required environment variable "+keyToOmit) {
				t.Fatalf("stderr does not mention missing var: %q", stderr.String())
			}
		})
	}
}

func TestCallerMissingRuntimeFailsBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := newTestServer(t, &requests, nil)
	defer server.Close()

	env := baseEnv(server.URL)
	delete(env, "HERMES_SESSION_ID") // no runtime session vars at all

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if requests.Load() != 0 {
		t.Fatalf("HTTP request count = %d; expected 0", requests.Load())
	}
	if !strings.Contains(stderr.String(), "cannot determine caller runtime") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestCallerMultipleRuntimeVarsWithoutExplicitRuntimeFailsBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := newTestServer(t, &requests, nil)
	defer server.Close()

	env := baseEnv(server.URL)
	env["CODEX_THREAD_ID"] = "codex-thread-10" // both hermes and codex present

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if requests.Load() != 0 {
		t.Fatalf("HTTP request count = %d; expected 0", requests.Load())
	}
	if !strings.Contains(stderr.String(), "multiple runtime session variables detected") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestCallerMultipleRuntimeVarsWithExplicitRuntimeSelection(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	env := baseEnv(server.URL)
	env["CODEX_THREAD_ID"] = "codex-thread-10"
	env["ASTRORDER_CALLER_RUNTIME"] = "codex" // explicitly pick codex

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP request count = %d, expected 1", requests.Load())
	}
	req := recorded[0]
	caller, ok := req.input["caller"].(map[string]any)
	if !ok {
		t.Fatalf("caller not found or not map: %#v", req.input)
	}
	if caller["runtime"] != "codex" || caller["session_id"] != "codex-thread-10" {
		t.Fatalf("expected codex runtime and session_id, got %#v", caller)
	}
}

func TestCallerExplicitSessionIDMismatchFailsBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := newTestServer(t, &requests, nil)
	defer server.Close()

	env := baseEnv(server.URL)
	env["ASTRORDER_CALLER_SESSION_ID"] = "conflicting-session-id"

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if requests.Load() != 0 {
		t.Fatalf("HTTP request count = %d; expected 0", requests.Load())
	}
	if !strings.Contains(stderr.String(), "does not match") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestCallerExplicitSessionIDMatchSucceeds(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	env := baseEnv(server.URL)
	env["ASTRORDER_CALLER_SESSION_ID"] = "hermes-session-42"

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP request count = %d; expected 1", requests.Load())
	}
}

func TestCallerUnsupportedRuntime(t *testing.T) {
	t.Run("fails without explicit session_id", func(t *testing.T) {
		var requests atomic.Int32
		server := newTestServer(t, &requests, nil)
		defer server.Close()

		env := baseEnv(server.URL)
		delete(env, "HERMES_SESSION_ID")
		env["ASTRORDER_CALLER_RUNTIME"] = "custom_runtime"

		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
		if code == 0 {
			t.Fatalf("expected non-zero exit code")
		}
		if requests.Load() != 0 {
			t.Fatalf("HTTP request count = %d; expected 0", requests.Load())
		}
		if !strings.Contains(stderr.String(), "requires explicit ASTRORDER_CALLER_SESSION_ID") {
			t.Fatalf("unexpected stderr: %q", stderr.String())
		}
	})

	t.Run("succeeds with explicit session_id", func(t *testing.T) {
		var requests atomic.Int32
		var recorded []observedRequest
		server := newTestServer(t, &requests, &recorded)
		defer server.Close()

		env := baseEnv(server.URL)
		delete(env, "HERMES_SESSION_ID")
		env["ASTRORDER_CALLER_RUNTIME"] = "custom_runtime"
		env["ASTRORDER_CALLER_SESSION_ID"] = "custom-sess-99"

		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}
		if requests.Load() != 1 {
			t.Fatalf("HTTP request count = %d; expected 1", requests.Load())
		}
		caller := recorded[0].input["caller"].(map[string]any)
		if caller["runtime"] != "custom_runtime" || caller["session_id"] != "custom-sess-99" {
			t.Fatalf("unexpected caller: %#v", caller)
		}
	})
}

func TestCallerIllegalWhitespaceValidation(t *testing.T) {
	whitespaceCases := []struct {
		name string
		key  string
		val  string
	}{
		{"agent with leading space", "ASTRORDER_CALLER_AGENT_ID", " hermes"},
		{"agent with trailing space", "ASTRORDER_CALLER_AGENT_ID", "hermes "},
		{"conn with tab", "ASTRORDER_CALLER_CONNECTION_ID", "loc\tal"},
		{"profile with newline", "ASTRORDER_CALLER_PROFILE_NAME", "def\nault"},
		{"runtime with space", "ASTRORDER_CALLER_RUNTIME", "her mes"},
		{"explicit session with space", "ASTRORDER_CALLER_SESSION_ID", "sess 1"},
		{"auto session with newline", "HERMES_SESSION_ID", "sess\n1"},
	}

	for _, tc := range whitespaceCases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := newTestServer(t, &requests, nil)
			defer server.Close()

			env := baseEnv(server.URL)
			env[tc.key] = tc.val

			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
			if code == 0 {
				t.Fatalf("expected non-zero exit code for whitespace in %s", tc.key)
			}
			if requests.Load() != 0 {
				t.Fatalf("HTTP requests = %d; expected 0 on illegal whitespace", requests.Load())
			}
			if !strings.Contains(stderr.String(), "illegal whitespace") {
				t.Fatalf("stderr does not report illegal whitespace: %q", stderr.String())
			}
		})
	}
}

func TestCallerPayloadForgeryRejectedBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	server := newTestServer(t, &requests, nil)
	defer server.Close()

	env := baseEnv(server.URL)

	// Invoke sessions.list with forged caller in payload
	forgedInput := `{"caller":{"agent_id":"attacker","connection_id":"local","profile_name":"default","runtime":"hermes","session_id":"hermes-session-42"}}`
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"invoke", "sessions.list", "--input", forgedInput}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code == 0 {
		t.Fatalf("expected non-zero exit code for forged caller")
	}
	if requests.Load() != 0 {
		t.Fatalf("HTTP requests = %d; expected 0 on forged caller", requests.Load())
	}
	if !strings.Contains(stderr.String(), "does not match configured caller") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestMachinesDispatchRequiresCaller(t *testing.T) {
	t.Run("fails before HTTP if caller missing", func(t *testing.T) {
		var requests atomic.Int32
		server := newTestServer(t, &requests, nil)
		defer server.Close()

		env := map[string]string{
			"ASTRORDER_AGENT_TOKEN": "token",
			"ASTRORDER_AGENT_URL":   server.URL,
		}

		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"invoke", "machines.dispatch", "--input", `{"machine_id":"m1"}`}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
		if code == 0 {
			t.Fatalf("expected non-zero exit code")
		}
		if requests.Load() != 0 {
			t.Fatalf("HTTP requests = %d; expected 0", requests.Load())
		}
	})

	t.Run("injects caller when caller present", func(t *testing.T) {
		var requests atomic.Int32
		var recorded []observedRequest
		server := newTestServer(t, &requests, &recorded)
		defer server.Close()

		env := baseEnv(server.URL)
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"invoke", "machines.dispatch", "--input", `{"machine_id":"m1"}`}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}
		if requests.Load() != 1 {
			t.Fatalf("HTTP requests = %d; expected 1", requests.Load())
		}
		caller := recorded[0].input["caller"].(map[string]any)
		if caller["agent_id"] != "hermes" || caller["session_id"] != "hermes-session-42" {
			t.Fatalf("unexpected caller: %#v", caller)
		}
		if recorded[0].input["machine_id"] != "m1" {
			t.Fatalf("missing machine_id: %#v", recorded[0].input)
		}
	})
}

func TestSessionsSpawnParametersMapping(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	env := baseEnv(server.URL)
	args := []string{
		"sessions", "spawn",
		"--operation-id", "op-test-1",
		"--agent-id", "codex",
		"--title", "Refactor Task",
		"--workspace", "/workspace/project",
		"--model", "gpt-5-preview",
		"--provider", "openai",
		"--effort", "high",
		"--prompt", "Complete refactoring",
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d; expected 1", requests.Load())
	}
	req := recorded[0]
	if req.capability != "sessions.delegate" {
		t.Fatalf("capability = %q, want 'sessions.delegate'", req.capability)
	}
	input := req.input
	if input["operation_id"] != "op-test-1" {
		t.Fatalf("operation_id = %v", input["operation_id"])
	}
	if input["agent_id"] != "codex" {
		t.Fatalf("agent_id = %v", input["agent_id"])
	}
	if input["title"] != "Refactor Task" {
		t.Fatalf("title = %v", input["title"])
	}
	if input["workspace"] != "/workspace/project" {
		t.Fatalf("workspace = %v", input["workspace"])
	}
	if input["model"] != "gpt-5-preview" {
		t.Fatalf("model = %v", input["model"])
	}
	if input["provider"] != "openai" {
		t.Fatalf("provider = %v", input["provider"])
	}
	if input["effort"] != "high" {
		t.Fatalf("effort = %v", input["effort"])
	}
	if input["prompt"] != "Complete refactoring" {
		t.Fatalf("prompt = %v", input["prompt"])
	}
	caller := input["caller"].(map[string]any)
	if caller["agent_id"] != "hermes" || caller["session_id"] != "hermes-session-42" {
		t.Fatalf("caller = %#v", caller)
	}
}

func TestSessionsDelegateAlias(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	env := baseEnv(server.URL)
	args := []string{
		"sessions", "delegate",
		"--operation-id", "op-del-1",
		"--agent", "codex",
		"--prompt", "Task via delegate",
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d", requests.Load())
	}
	req := recorded[0]
	if req.capability != "sessions.delegate" {
		t.Fatalf("capability = %q", req.capability)
	}
	if req.input["operation_id"] != "op-del-1" || req.input["agent_id"] != "codex" {
		t.Fatalf("unexpected input: %#v", req.input)
	}
}

func TestSessionsSpawnPromptFilePriorityAndUTF8(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	env := baseEnv(server.URL)
	utf8Prompt := "这是中文指令：\n1. 执行检查；\n2. 输出 \"JSON\" 格式；\n'单引号' 和 \"双引号\" 混合。"

	config := Config{
		ReadFile: func(path string) ([]byte, error) {
			if path == "instructions.txt" {
				return []byte(utf8Prompt), nil
			}
			return nil, io.EOF
		},
	}

	args := []string{
		"sessions", "spawn",
		"--operation-id", "op-utf8",
		"--agent-id", "codex",
		"--prompt", "This should be overridden by file",
		"--prompt-file", "instructions.txt",
	}

	var stdout, stderr bytes.Buffer
	code := RunWithConfig(context.Background(), args, strings.NewReader(""), &stdout, &stderr, mapLookup(env), config)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d", requests.Load())
	}
	if got := recorded[0].input["prompt"]; got != utf8Prompt {
		t.Fatalf("prompt = %q, want %q", got, utf8Prompt)
	}
}

func TestSessionsOperation(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	env := baseEnv(server.URL)
	args := []string{"sessions", "operation", "--operation-id", "op-check-99"}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d", requests.Load())
	}
	req := recorded[0]
	if req.capability != "sessions.operation" {
		t.Fatalf("capability = %q", req.capability)
	}
	if req.input["operation_id"] != "op-check-99" {
		t.Fatalf("operation_id = %v", req.input["operation_id"])
	}
	if req.input["caller"] == nil {
		t.Fatalf("caller not injected into sessions.operation")
	}
}

func TestSessionsReadWithBeforePagination(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	env := baseEnv(server.URL)
	args := []string{
		"sessions", "read",
		"--key", "codex::thread-xyz",
		"--before", "cursor-msg-50",
		"--limit", "30",
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d", requests.Load())
	}
	req := recorded[0]
	if req.capability != "sessions.read" {
		t.Fatalf("capability = %q", req.capability)
	}
	if req.input["key"] != "codex::thread-xyz" {
		t.Fatalf("key = %v", req.input["key"])
	}
	if req.input["before"] != "cursor-msg-50" {
		t.Fatalf("before = %v", req.input["before"])
	}
	if !reflect.DeepEqual(req.input["limit"], json.Number("30")) {
		t.Fatalf("limit = %#v", req.input["limit"])
	}
}

func TestSessionsSendStopDeleteTree(t *testing.T) {
	testCases := []struct {
		name       string
		args       []string
		capability string
		checkInput func(t *testing.T, input map[string]any)
	}{
		{
			name:       "sessions send",
			args:       []string{"sessions", "send", "--key", "codex::s1", "--text", "hello agent"},
			capability: "sessions.send",
			checkInput: func(t *testing.T, input map[string]any) {
				if input["key"] != "codex::s1" || input["text"] != "hello agent" {
					t.Fatalf("unexpected input: %#v", input)
				}
			},
		},
		{
			name:       "sessions stop",
			args:       []string{"sessions", "stop", "--key", "codex::s1"},
			capability: "sessions.stop",
			checkInput: func(t *testing.T, input map[string]any) {
				if input["key"] != "codex::s1" {
					t.Fatalf("unexpected input: %#v", input)
				}
			},
		},
		{
			name:       "sessions delete",
			args:       []string{"sessions", "delete", "--key", "codex::s1"},
			capability: "sessions.delete",
			checkInput: func(t *testing.T, input map[string]any) {
				if input["key"] != "codex::s1" {
					t.Fatalf("unexpected input: %#v", input)
				}
			},
		},
		{
			name:       "sessions tree",
			args:       []string{"sessions", "tree", "--key", "codex::root"},
			capability: "sessions.tree",
			checkInput: func(t *testing.T, input map[string]any) {
				if input["key"] != "codex::root" {
					t.Fatalf("unexpected input: %#v", input)
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			var recorded []observedRequest
			server := newTestServer(t, &requests, &recorded)
			defer server.Close()

			env := baseEnv(server.URL)
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), tc.args, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
			if code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			if requests.Load() != 1 {
				t.Fatalf("HTTP requests = %d", requests.Load())
			}
			req := recorded[0]
			if req.capability != tc.capability {
				t.Fatalf("capability = %q, want %q", req.capability, tc.capability)
			}
			if req.input["caller"] == nil {
				t.Fatalf("caller was not injected")
			}
			tc.checkInput(t, req.input)
		})
	}
}

func TestCallerConfigFileExplicitPath(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	configFileContent := `{"agent_id":"hermes-worker","connection_id":"local","profile_name":"default","runtime":"hermes"}`
	config := Config{
		ReadFile: func(path string) ([]byte, error) {
			if path == "/mock/caller.json" {
				return []byte(configFileContent), nil
			}
			return nil, io.EOF
		},
	}

	// Environment has only the auth token, server URL, config path, and session ID (no static caller env vars!)
	env := map[string]string{
		"ASTRORDER_AGENT_TOKEN":   "test-token",
		"ASTRORDER_AGENT_URL":     server.URL,
		"ASTRORDER_CALLER_CONFIG": "/mock/caller.json",
		"HERMES_SESSION_ID":       "hermes-auto-session-88",
	}

	var stdout, stderr bytes.Buffer
	code := RunWithConfig(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env), config)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d; expected 1", requests.Load())
	}
	caller := recorded[0].input["caller"].(map[string]any)
	if caller["agent_id"] != "hermes-worker" || caller["runtime"] != "hermes" || caller["session_id"] != "hermes-auto-session-88" {
		t.Fatalf("unexpected caller: %#v", caller)
	}
}

func TestCallerConfigFileWithSessionIDRejected(t *testing.T) {
	var requests atomic.Int32
	server := newTestServer(t, &requests, nil)
	defer server.Close()

	configFileContent := `{"agent_id":"hermes","connection_id":"local","profile_name":"default","runtime":"hermes","session_id":"should-not-exist"}`
	config := Config{
		ReadFile: func(path string) ([]byte, error) {
			return []byte(configFileContent), nil
		},
	}

	env := map[string]string{
		"ASTRORDER_AGENT_TOKEN":   "test-token",
		"ASTRORDER_AGENT_URL":     server.URL,
		"ASTRORDER_CALLER_CONFIG": "/mock/caller.json",
		"HERMES_SESSION_ID":       "sess-1",
	}

	var stdout, stderr bytes.Buffer
	code := RunWithConfig(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env), config)
	if code == 0 {
		t.Fatalf("expected non-zero exit code when config contains session_id")
	}
	if requests.Load() != 0 {
		t.Fatalf("HTTP requests = %d; expected 0", requests.Load())
	}
	if !strings.Contains(stderr.String(), "must not contain session_id") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestCallerConfigEnvConflictRejected(t *testing.T) {
	var requests atomic.Int32
	server := newTestServer(t, &requests, nil)
	defer server.Close()

	configFileContent := `{"agent_id":"hermes","connection_id":"local","profile_name":"default","runtime":"hermes"}`
	config := Config{
		ReadFile: func(path string) ([]byte, error) {
			return []byte(configFileContent), nil
		},
	}

	env := map[string]string{
		"ASTRORDER_AGENT_TOKEN":     "test-token",
		"ASTRORDER_AGENT_URL":       server.URL,
		"ASTRORDER_CALLER_CONFIG":   "/mock/caller.json",
		"ASTRORDER_CALLER_AGENT_ID": "codex", // conflicts with config's hermes!
		"HERMES_SESSION_ID":         "sess-1",
	}

	var stdout, stderr bytes.Buffer
	code := RunWithConfig(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env), config)
	if code == 0 {
		t.Fatalf("expected non-zero exit code on conflict")
	}
	if requests.Load() != 0 {
		t.Fatalf("HTTP requests = %d; expected 0", requests.Load())
	}
	if !strings.Contains(stderr.String(), "conflict between environment") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestCallerHermesFixedPathFallback(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	configFileContent := `{"agent_id":"hermes-fixed","connection_id":"local","profile_name":"default","runtime":"hermes"}`
	config := Config{
		ReadFile: func(path string) ([]byte, error) {
			if strings.HasSuffix(path, "astrorder-caller.json") {
				return []byte(configFileContent), nil
			}
			return nil, io.EOF
		},
	}

	// In Hermes session, no ASTRORDER_* env vars at all except auth & URL
	env := map[string]string{
		"ASTRORDER_AGENT_TOKEN": "test-token",
		"ASTRORDER_AGENT_URL":   server.URL,
		"HERMES_HOME":           "/mock/hermes",
		"HERMES_SESSION_ID":     "hermes-auto-session-fixed",
	}

	var stdout, stderr bytes.Buffer
	code := RunWithConfig(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env), config)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d", requests.Load())
	}
	caller := recorded[0].input["caller"].(map[string]any)
	if caller["agent_id"] != "hermes-fixed" || caller["session_id"] != "hermes-auto-session-fixed" {
		t.Fatalf("unexpected caller: %#v", caller)
	}
}

func TestGrokExperimentalRuntimeBehavior(t *testing.T) {
	t.Run("fails without explicit runtime", func(t *testing.T) {
		var requests atomic.Int32
		server := newTestServer(t, &requests, nil)
		defer server.Close()

		env := map[string]string{
			"ASTRORDER_AGENT_TOKEN":          "test-token",
			"ASTRORDER_AGENT_URL":            server.URL,
			"ASTRORDER_CALLER_AGENT_ID":      "grok-agent",
			"ASTRORDER_CALLER_CONNECTION_ID": "local",
			"ASTRORDER_CALLER_PROFILE_NAME":  "default",
			"GROK_SESSION_ID":                "grok-sess-1",
		}

		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
		if code == 0 {
			t.Fatalf("expected non-zero exit code")
		}
		if requests.Load() != 0 {
			t.Fatalf("HTTP requests = %d; expected 0", requests.Load())
		}
		if !strings.Contains(stderr.String(), "grok runtime is experimental") {
			t.Fatalf("unexpected stderr: %q", stderr.String())
		}
	})

	t.Run("succeeds with explicit runtime", func(t *testing.T) {
		var requests atomic.Int32
		var recorded []observedRequest
		server := newTestServer(t, &requests, &recorded)
		defer server.Close()

		env := map[string]string{
			"ASTRORDER_AGENT_TOKEN":          "test-token",
			"ASTRORDER_AGENT_URL":            server.URL,
			"ASTRORDER_CALLER_AGENT_ID":      "grok-agent",
			"ASTRORDER_CALLER_CONNECTION_ID": "local",
			"ASTRORDER_CALLER_PROFILE_NAME":  "default",
			"ASTRORDER_CALLER_RUNTIME":       "grok",
			"GROK_SESSION_ID":                "grok-sess-1",
		}

		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"sessions", "list"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
		}
		if requests.Load() != 1 {
			t.Fatalf("HTTP requests = %d", requests.Load())
		}
		caller := recorded[0].input["caller"].(map[string]any)
		if caller["runtime"] != "grok" || caller["session_id"] != "grok-sess-1" {
			t.Fatalf("unexpected caller: %#v", caller)
		}
	})
}

func TestTokenDPAPIFallbackOnLoopback(t *testing.T) {
	var requests atomic.Int32
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	config := Config{
		OpenEnvironment: func(stateDir string) (map[string]string, error) {
			return map[string]string{
				"ASTRORDER_CONNECTOR_SECRET": "dpapi-secret-12345",
			}, nil
		},
	}

	env := map[string]string{
		"ASTRORDER_AGENT_URL": server.URL, // loopback URL with custom OpenEnvironment
		"ASTRORDER_STATE_DIR": "C:\\Astrorder\\State",
	}

	var stdout, stderr bytes.Buffer
	code := RunWithConfig(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env), config)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d", requests.Load())
	}
	if authHeader != "Bearer dpapi-secret-12345" {
		t.Fatalf("authHeader = %q", authHeader)
	}
}

func TestTokenDPAPISkippedOnRemoteURL(t *testing.T) {
	config := Config{
		OpenEnvironment: func(stateDir string) (map[string]string, error) {
			return map[string]string{
				"ASTRORDER_CONNECTOR_SECRET": "should-never-be-used-remotely",
			}, nil
		},
	}

	env := map[string]string{
		"ASTRORDER_AGENT_URL": "http://remote-server.com:30001",
		"ASTRORDER_STATE_DIR": "C:\\Astrorder\\State",
	}

	var stdout, stderr bytes.Buffer
	code := RunWithConfig(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(env), config)
	if code == 0 {
		t.Fatalf("expected non-zero exit code on remote URL without token")
	}
	if !strings.Contains(stderr.String(), "Missing Astrorder agent token") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestSessionsSendCommandID(t *testing.T) {
	var requests atomic.Int32
	var recorded []observedRequest
	server := newTestServer(t, &requests, &recorded)
	defer server.Close()

	env := baseEnv(server.URL)
	args := []string{
		"sessions", "send",
		"--key", "codex::s1",
		"--command-id", "cmd-unique-999",
		"--text", "instruction",
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if requests.Load() != 1 {
		t.Fatalf("HTTP requests = %d", requests.Load())
	}
	req := recorded[0]
	if req.input["command_id"] != "cmd-unique-999" {
		t.Fatalf("command_id = %v", req.input["command_id"])
	}
	if req.input["text"] != "instruction" {
		t.Fatalf("text = %v", req.input["text"])
	}
}

func TestBindCommandWritesValidCallerConfig(t *testing.T) {
	scratchFile := filepath.Join(t.TempDir(), "caller-bind-test.json")
	args := []string{
		"bind",
		"--agent-id", "hermes",
		"--connection-id", "local",
		"--profile-name", "default",
		"--runtime", "hermes",
		"--output", scratchFile,
	}

	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}

	data, err := os.ReadFile(scratchFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal output file: %v", err)
	}
	if parsed["agent_id"] != "hermes" || parsed["runtime"] != "hermes" || parsed["connection_id"] != "local" || parsed["profile_name"] != "default" {
		t.Fatalf("unexpected content: %#v", parsed)
	}
	if _, ok := parsed["session_id"]; ok {
		t.Fatalf("session_id must not be written!")
	}
}

func TestFindHermesCallerConfigExplicitHomeDoesNotFallback(t *testing.T) {
	t.Run("explicit home with corrupt file reports error and does not fallback", func(t *testing.T) {
		config := Config{
			ReadFile: func(path string) ([]byte, error) {
				if path == filepath.Join("/custom/hermes", "astrorder-caller.json") {
					return []byte("{corrupt-json"), nil
				}
				if path == filepath.Join("/default/hermes", "astrorder-caller.json") {
					return []byte(`{"agent_id":"hermes","connection_id":"local","profile_name":"default","runtime":"hermes"}`), nil
				}
				return nil, os.ErrNotExist
			},
		}

		env := map[string]string{
			"HERMES_HOME":       "/custom/hermes",
			"LOCALAPPDATA":      "/default",
			"HERMES_SESSION_ID": "sess-1",
		}

		_, err := resolveCaller(mapLookup(env), config)
		if err == nil {
			t.Fatalf("expected error on corrupt config in explicit HERMES_HOME")
		}
		if !strings.Contains(err.Error(), "parse caller config") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("explicit home when file missing returns unbound error without fallback", func(t *testing.T) {
		config := Config{
			ReadFile: func(path string) ([]byte, error) {
				if path == filepath.Join("/default", "hermes", "astrorder-caller.json") {
					return []byte(`{"agent_id":"hermes-default","connection_id":"local","profile_name":"default","runtime":"hermes"}`), nil
				}
				return nil, os.ErrNotExist
			},
		}

		env := map[string]string{
			"HERMES_HOME":       "/empty/hermes", // missing file
			"LOCALAPPDATA":      "/default",
			"HERMES_SESSION_ID": "sess-1",
		}

		_, err := resolveCaller(mapLookup(env), config)
		if err == nil {
			t.Fatalf("expected error because explicit home has no config, must not fallback to default")
		}
		if !strings.Contains(err.Error(), "missing required environment variable ASTRORDER_CALLER_AGENT_ID") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestFindHermesCallerConfigProfileMatching(t *testing.T) {
	t.Run("profile mismatch with HERMES_PROFILE reports error", func(t *testing.T) {
		config := Config{
			ReadFile: func(path string) ([]byte, error) {
				return []byte(`{"agent_id":"hermes","connection_id":"local","profile_name":"default","runtime":"hermes"}`), nil
			},
		}

		env := map[string]string{
			"LOCALAPPDATA":      "/default",
			"HERMES_PROFILE":    "staging", // expects staging but file has default
			"HERMES_SESSION_ID": "sess-1",
		}

		_, err := resolveCaller(mapLookup(env), config)
		if err == nil {
			t.Fatalf("expected profile mismatch error")
		}
		if !strings.Contains(err.Error(), "does not match HERMES_PROFILE") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("profile matches HERMES_PROFILE succeeds", func(t *testing.T) {
		config := Config{
			ReadFile: func(path string) ([]byte, error) {
				return []byte(`{"agent_id":"hermes","connection_id":"local","profile_name":"staging","runtime":"hermes"}`), nil
			},
		}

		env := map[string]string{
			"LOCALAPPDATA":      "/default",
			"HERMES_PROFILE":    "staging",
			"HERMES_SESSION_ID": "sess-1",
		}

		caller, err := resolveCaller(mapLookup(env), config)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if caller.ProfileName != "staging" {
			t.Fatalf("profile = %q, want staging", caller.ProfileName)
		}
	})

	t.Run("default home with non-default profile reports error", func(t *testing.T) {
		config := Config{
			ReadFile: func(path string) ([]byte, error) {
				return []byte(`{"agent_id":"hermes","connection_id":"local","profile_name":"custom-team","runtime":"hermes"}`), nil
			},
		}

		env := map[string]string{
			"LOCALAPPDATA":      "/default",
			"HERMES_SESSION_ID": "sess-1",
		}

		_, err := resolveCaller(mapLookup(env), config)
		if err == nil {
			t.Fatalf("expected error for non-default profile in default home")
		}
		if !strings.Contains(err.Error(), "must have profile_name \"default\"") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestCallerConfigUnknownAndEmptyFieldsRejected(t *testing.T) {
	t.Run("unknown field in config", func(t *testing.T) {
		config := Config{
			ReadFile: func(string) ([]byte, error) {
				return []byte(`{"agent_id":"hermes","connection_id":"local","profile_name":"default","runtime":"hermes","extra":"value"}`), nil
			},
		}
		env := map[string]string{
			"ASTRORDER_CALLER_CONFIG": "/mock.json",
			"HERMES_SESSION_ID":       "sess-1",
		}
		_, err := resolveCaller(mapLookup(env), config)
		if err == nil || !strings.Contains(err.Error(), "unknown field \"extra\"") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty field in config", func(t *testing.T) {
		config := Config{
			ReadFile: func(string) ([]byte, error) {
				return []byte(`{"agent_id":"","connection_id":"local","profile_name":"default","runtime":"hermes"}`), nil
			},
		}
		env := map[string]string{
			"ASTRORDER_CALLER_CONFIG": "/mock.json",
			"HERMES_SESSION_ID":       "sess-1",
		}
		_, err := resolveCaller(mapLookup(env), config)
		if err == nil || !strings.Contains(err.Error(), "is required and must not be empty") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestCallerEmptyEnvVarsCannotBeCoveredByFallback(t *testing.T) {
	testVars := []string{
		"ASTRORDER_CALLER_CONFIG",
		"ASTRORDER_CALLER_AGENT_ID",
		"ASTRORDER_CALLER_CONNECTION_ID",
		"ASTRORDER_CALLER_PROFILE_NAME",
		"ASTRORDER_CALLER_RUNTIME",
		"ASTRORDER_CALLER_SESSION_ID",
	}

	for _, v := range testVars {
		t.Run("explicit empty "+v, func(t *testing.T) {
			env := map[string]string{
				"HERMES_SESSION_ID": "sess-1",
				v:                   "", // explicitly empty
			}
			_, err := resolveCaller(mapLookup(env), Config{})
			if err == nil {
				t.Fatalf("expected error when %s is explicitly empty", v)
			}
			if !strings.Contains(err.Error(), v+" is set to empty string") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestMultiRuntimeFixedPathCannotSelect(t *testing.T) {
	config := Config{
		ReadFile: func(string) ([]byte, error) {
			return []byte(`{"agent_id":"hermes","connection_id":"local","profile_name":"default","runtime":"hermes"}`), nil
		},
	}

	env := map[string]string{
		"HERMES_SESSION_ID": "hermes-1",
		"CODEX_THREAD_ID":   "codex-1",
		"LOCALAPPDATA":      "/default",
	}

	_, err := resolveCaller(mapLookup(env), config)
	if err == nil {
		t.Fatalf("expected multiple runtime error, fixed path must not automatically select hermes")
	}
	if !strings.Contains(err.Error(), "multiple runtime session variables detected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIsAllowedLocalOriginStrictValidation(t *testing.T) {
	valid := []string{
		"http://127.0.0.1:30001",
		"http://127.0.0.1:30001/",
		"http://localhost:30001",
		"http://[::1]:30001",
	}
	for _, u := range valid {
		if !isAllowedLocalOrigin(u, false) {
			t.Errorf("expected %q to be allowed", u)
		}
	}

	invalid := []struct {
		url    string
		reason string
	}{
		{"https://127.0.0.1:30001", "scheme not http"},
		{"http://user:pass@127.0.0.1:30001", "userinfo present"},
		{"http://127.0.0.1:30001/foo", "subpath present"},
		{"http://127.0.0.1:30001?a=1", "query present"},
		{"http://127.0.0.1:30001#fragment", "fragment present"},
		{"http://127.0.0.1", "no port is port 80"},
		{"http://127.0.0.1:8080", "port not 30001"},
		{"http://192.168.1.100:30001", "remote IP"},
		{"http://remote.host:30001", "remote host"},
	}
	for _, tc := range invalid {
		if isAllowedLocalOrigin(tc.url, false) {
			t.Errorf("expected %q to be rejected: %s", tc.url, tc.reason)
		}
	}
}

func TestRequiresCallerBlackboardAndTelemetry(t *testing.T) {
	callerRequired := []string{
		"sessions.list",
		"sessions.read",
		"sessions.delegate",
		"machines.dispatch",
		"swarm.telemetry.report",
		"swarm.lock.acquire",
		"swarm.lock.release",
		"swarm.sos.escalate",
		"plugins.open",
		"plugins.close",
		"blackboard.get",
		"blackboard.set",
		"blackboard.delete",
		"blackboard.auto_render",
	}
	for _, cap := range callerRequired {
		if !requiresCaller(cap) {
			t.Errorf("capability %s should require caller", cap)
		}
	}

	noCaller := []string{
		"catalog.list",
		"agents.list",
		"machines.list",
		"projects.list",
		"swarm.lock.list",
		"swarm.sos.list",
		"plugins.list",
		"blackboard.components.list",
		"blackboard.component.schema",
	}
	for _, cap := range noCaller {
		if requiresCaller(cap) {
			t.Errorf("capability %s should not require caller", cap)
		}
	}
}

func TestInjectCallerExplicitNullRejected(t *testing.T) {
	var requests atomic.Int32
	server := newTestServer(t, &requests, nil)
	defer server.Close()

	env := baseEnv(server.URL)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"invoke", "sessions.list", "--input", `{"caller":null}`}, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
	if code == 0 {
		t.Fatalf("expected exit code != 0 when caller is explicitly null")
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d, expected 0", requests.Load())
	}
	if !strings.Contains(stderr.String(), "caller in payload must not be null") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestSessionsSpawnWhitespaceInIDsRejected(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "whitespace in operation-id",
			args: []string{"sessions", "spawn", "--operation-id", "op 1", "--agent-id", "codex"},
			want: "operation_id contains illegal whitespace",
		},
		{
			name: "whitespace in agent-id",
			args: []string{"sessions", "spawn", "--operation-id", "op1", "--agent-id", "cod ex"},
			want: "agent_id contains illegal whitespace",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := newTestServer(t, &requests, nil)
			defer server.Close()

			env := baseEnv(server.URL)
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), tc.args, strings.NewReader(""), &stdout, &stderr, mapLookup(env))
			if code == 0 {
				t.Fatalf("expected exit code != 0")
			}
			if requests.Load() != 0 {
				t.Fatalf("requests = %d, expected 0", requests.Load())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
		})
	}
}
