package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type observedRequest struct {
	capability    string
	input         map[string]any
	authorization string
}

func TestCompatibleCommandGrammar(t *testing.T) {
	requests := make(chan observedRequest, 20)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		var body struct {
			Capability string         `json:"capability"`
			Input      map[string]any `json:"input"`
		}
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		requests <- observedRequest{body.Capability, body.Input, request.Header.Get("Authorization")}
		_, _ = io.WriteString(response, `{}`)
	}))
	defer server.Close()

	expectedCaller := map[string]any{
		"agent_id":      "hermes",
		"connection_id": "local",
		"profile_name":  "default",
		"runtime":       "hermes",
		"session_id":    "session-1",
	}

	tests := []struct {
		name       string
		args       []string
		capability string
		input      map[string]any
	}{
		{"catalog", []string{"catalog"}, "catalog.list", map[string]any{}},
		{"invoke", []string{"invoke", "swarm.lock.list", "--input", `{"ttl":9007199254740993,"文字":"值"}`}, "swarm.lock.list", map[string]any{"ttl": json.Number("9007199254740993"), "文字": "值"}},
		{"sessions list", []string{"sessions", "list"}, "sessions.list", map[string]any{"caller": expectedCaller}},
		{"sessions search", []string{"sessions", "search", "K8s 集群"}, "sessions.search", map[string]any{"q": "K8s 集群", "caller": expectedCaller}},
		{"sessions read default", []string{"sessions", "read"}, "sessions.read", map[string]any{"limit": json.Number("80"), "caller": expectedCaller}},
		{"sessions read options", []string{"sessions", "read", "--key", "codex::s1", "--agent-id", "codex", "--session-id", "s1", "--limit", "17"}, "sessions.read", map[string]any{"key": "codex::s1", "agent_id": "codex", "session_id": "s1", "limit": json.Number("17"), "caller": expectedCaller}},
		{"agents", []string{"agents"}, "agents.list", map[string]any{}},
		{"machines", []string{"machines"}, "machines.list", map[string]any{}},
		{"projects", []string{"projects"}, "projects.list", map[string]any{}},
	}

	lookup := mapLookup(map[string]string{
		"ASTRORDER_AGENT_TOKEN":          "secret",
		"ASTRORDER_AGENT_URL":            server.URL + "/",
		"ASTRORDER_CALLER_AGENT_ID":      "hermes",
		"ASTRORDER_CALLER_CONNECTION_ID": "local",
		"ASTRORDER_CALLER_PROFILE_NAME":  "default",
		"ASTRORDER_CALLER_RUNTIME":       "hermes",
		"ASTRORDER_CALLER_SESSION_ID":    "session-1",
	})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(context.Background(), test.args, strings.NewReader("ignored"), &stdout, &stderr, lookup); code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			if stdout.String() != "{}\n" || stderr.Len() != 0 {
				t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
			got := <-requests
			if got.capability != test.capability || !reflect.DeepEqual(got.input, test.input) || got.authorization != "Bearer secret" {
				t.Fatalf("request = %#v, want capability=%q input=%#v", got, test.capability, test.input)
			}
		})
	}
}

func TestInvokePreservesBigIntegersAndUTF8InOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(response, `{"seq":9007199254740993,"文字":"<页面>"}`)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{
		"ASTRORDER_AGENT_TOKEN": "token",
		"ASTRORDER_AGENT_URL":   server.URL,
	}))
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	want := "{\n  \"seq\": 9007199254740993,\n  \"文字\": \"<页面>\"\n}\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestTokenPrecedenceAndInjectedTokenFile(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		want        string
		wantFile    string
	}{
		{
			name: "agent before all", want: "agent",
			environment: map[string]string{"ASTRORDER_AGENT_TOKEN": "agent", "ASTRORDER_BROWSER_SECRET": "browser", "ASTRORDER_CONNECTOR_SECRET": "connector", "ASTRORDER_TOKEN_FILE": "fixture.token"},
		},
		{
			name: "browser before connector", want: "browser",
			environment: map[string]string{"ASTRORDER_BROWSER_SECRET": "browser", "ASTRORDER_CONNECTOR_SECRET": "connector", "ASTRORDER_TOKEN_FILE": "fixture.token"},
		},
		{
			name: "connector before file", want: "connector",
			environment: map[string]string{"ASTRORDER_CONNECTOR_SECRET": "connector", "ASTRORDER_TOKEN_FILE": "fixture.token"},
		},
		{
			name: "explicit fixture file", want: "file-token", wantFile: "fixture.token",
			environment: map[string]string{"ASTRORDER_TOKEN_FILE": "fixture.token"},
		},
		{
			name: "default fixture file", want: "file-token", wantFile: ".token",
			environment: map[string]string{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotAuthorization string
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				gotAuthorization = request.Header.Get("Authorization")
				_, _ = io.WriteString(response, `{}`)
			}))
			defer server.Close()
			test.environment["ASTRORDER_AGENT_URL"] = server.URL
			var gotFile string
			config := Config{ReadFile: func(path string) ([]byte, error) {
				gotFile = path
				return []byte("  file-token\n"), nil
			}}
			var stdout, stderr bytes.Buffer
			code := RunWithConfig(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(test.environment), config)
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			if gotAuthorization != "Bearer "+test.want || gotFile != test.wantFile {
				t.Fatalf("authorization = %q, file = %q", gotAuthorization, gotFile)
			}
		})
	}
}

func TestDefaultURLIsLoopback30001(t *testing.T) {
	if defaultRequestTimeout != 60*time.Second {
		t.Fatalf("default request timeout = %s", defaultRequestTimeout)
	}
	var target string
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		target = request.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    request,
		}, nil
	})}
	var stdout, stderr bytes.Buffer
	code := RunWithConfig(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{"ASTRORDER_AGENT_TOKEN": "token"}), Config{HTTPClient: client})
	if code != 0 || stderr.Len() != 0 || target != "http://127.0.0.1:30001/api/v1/agent/invoke" {
		t.Fatalf("exit = %d, target = %q, stderr = %q", code, target, stderr.String())
	}
}

func TestBrowserRunHasLongerDefaultTimeout(t *testing.T) {
	env := mapLookup(map[string]string{
		"ASTRORDER_AGENT_TOKEN":          "secret",
		"ASTRORDER_CALLER_AGENT_ID":      "hermes",
		"ASTRORDER_CALLER_CONNECTION_ID": "local",
		"ASTRORDER_CALLER_PROFILE_NAME":  "default",
		"ASTRORDER_CALLER_RUNTIME":       "hermes",
		"ASTRORDER_CALLER_SESSION_ID":    "session-1",
	})
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		deadline, ok := request.Context().Deadline()
		if !ok || time.Until(deadline) < 29*time.Minute {
			t.Error("browser task received a short request timeout")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})}
	var stdout, stderr bytes.Buffer
	if code := RunWithConfig(context.Background(), []string{"invoke", "browser.run", "--input", `{"url":"https://example.com","goal":"read"}`}, strings.NewReader(""), &stdout, &stderr, env, Config{HTTPClient: client}); code != 0 {
		t.Fatal(code, stderr.String())
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestMissingTokenAndInputErrorsAreHumanAndDoNotCallUpstream(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()

	tests := []struct {
		name        string
		args        []string
		environment map[string]string
		code        int
		want        string
	}{
		{"missing token", []string{"catalog"}, map[string]string{"ASTRORDER_AGENT_URL": server.URL}, 1, "Missing Astrorder agent token (ASTRORDER_AGENT_TOKEN or .token)\n"},
		{"array input", []string{"invoke", "catalog.list", "--input", "[]"}, map[string]string{"ASTRORDER_AGENT_TOKEN": "secret", "ASTRORDER_AGENT_URL": server.URL}, 1, "--input must be a JSON object\n"},
		{"number input", []string{"invoke", "catalog.list", "--input", "1"}, map[string]string{"ASTRORDER_AGENT_TOKEN": "secret", "ASTRORDER_AGENT_URL": server.URL}, 1, "--input must be a JSON object\n"},
		{"invalid JSON", []string{"invoke", "catalog.list", "--input", "{"}, map[string]string{"ASTRORDER_AGENT_TOKEN": "secret", "ASTRORDER_AGENT_URL": server.URL}, 1, "invalid --input JSON:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			config := Config{ReadFile: func(string) ([]byte, error) { return nil, io.ErrUnexpectedEOF }}
			code := RunWithConfig(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, mapLookup(test.environment), config)
			if code != test.code || stdout.Len() != 0 {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
			if test.name == "invalid JSON" {
				if !strings.HasPrefix(stderr.String(), test.want) || !strings.HasSuffix(stderr.String(), "\n") {
					t.Fatalf("stderr = %q", stderr.String())
				}
			} else if stderr.String() != test.want {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.want)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls = %d", calls.Load())
	}
}

func TestHelpUsageAndRegistrationsShareGrammar(t *testing.T) {
	registrations := Registrations()
	commands := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		if registration.Program != programName {
			t.Fatalf("program = %q", registration.Program)
		}
		commands = append(commands, registration.Command)
	}
	wantCommands := []string{
		"catalog", "invoke", "sessions",
		"sessions list", "sessions search", "sessions read",
		"sessions spawn", "sessions delegate", "sessions operation",
		"sessions send", "sessions stop", "sessions delete", "sessions tree",
		"browser", "browser eval", "browser screenshot",
		"agents", "machines", "projects", "bind",
	}
	if !reflect.DeepEqual(commands, wantCommands) {
		t.Fatalf("commands = %#v", commands)
	}
	var readRegistration *Registration
	for index := range registrations {
		if registrations[index].Command == "sessions read" {
			readRegistration = &registrations[index]
		}
	}
	if readRegistration == nil {
		t.Fatal("sessions read registration missing")
	}
	var limit *OptionRegistration
	for index := range readRegistration.Options {
		if readRegistration.Options[index].Name == "limit" {
			limit = &readRegistration.Options[index]
		}
	}
	if limit == nil || !limit.HasDefault || limit.Default != 80 || limit.Type != "integer" || limit.Metavar != "LIMIT" || !reflect.DeepEqual(limit.Flags, []string{"--limit"}) {
		t.Fatalf("limit metadata = %#v", limit)
	}
	readRegistration.Options[0].Flags[0] = "mutated"
	fresh := Registrations()
	if fresh[5].Options[0].Flags[0] != "-h" {
		t.Fatalf("Registrations returned mutable shared metadata: %#v", fresh[5].Options[0])
	}

	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &stderr, nil); code != 0 || stderr.Len() != 0 {
		t.Fatalf("root help exit = %d, stderr = %q", code, stderr.String())
	}
	normalizedHelp := strings.Join(strings.Fields(stdout.String()), " ")
	for _, text := range []string{"usage: astrorder-agent [-h] {catalog,invoke,sessions,browser,agents,machines,projects,bind} ...", "Call Astrorder agent APIs", "catalog", "List capabilities", "invoke", "Call a capability by id"} {
		if !strings.Contains(normalizedHelp, text) {
			t.Fatalf("root help missing %q:\n%s", text, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"invoke", "--help"}, strings.NewReader(""), &stdout, &stderr, nil); code != 0 || stderr.Len() != 0 || !strings.Contains(strings.Join(strings.Fields(stdout.String()), " "), "usage: astrorder-agent invoke [-h] [--input INPUT] [--input-file INPUT_FILE] capability") {
		t.Fatalf("invoke help exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"sessions", "read", "--help"}, strings.NewReader(""), &stdout, &stderr, nil); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "usage: astrorder-agent sessions read") || !strings.Contains(stdout.String(), "--limit LIMIT") {
		t.Fatalf("nested help exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := Run(context.Background(), []string{"sessions", "read", "--limit", "nope"}, strings.NewReader(""), &stdout, &stderr, nil)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage: astrorder-agent sessions read") || !strings.Contains(stderr.String(), "astrorder-agent sessions read: error: argument --limit: invalid int value: 'nope'") {
		t.Fatalf("parse error exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestCancellationStopsOneInFlightRequest(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		<-release
	}))
	defer server.Close()
	defer close(release)

	var stdout, stderr bytes.Buffer
	started := time.Now()
	code := RunWithConfig(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{
		"ASTRORDER_AGENT_TOKEN": "token",
		"ASTRORDER_AGENT_URL":   server.URL,
	}), Config{RequestTimeout: 30 * time.Millisecond})
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "context deadline exceeded") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}
}

func TestUpstreamFailuresPreserveBoundedBodyAndNeverRetryOrRedirect(t *testing.T) {
	t.Run("original body", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			requests.Add(1)
			response.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(response, "上游失败: exact body")
		}))
		defer server.Close()
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{"ASTRORDER_AGENT_TOKEN": "token", "ASTRORDER_AGENT_URL": server.URL}))
		if code != 1 || stdout.Len() != 0 || stderr.String() != "Astrorder agent API 502: 上游失败: exact body\n" || requests.Load() != 1 {
			t.Fatalf("exit = %d, requests = %d, stdout = %q, stderr = %q", code, requests.Load(), stdout.String(), stderr.String())
		}
	})

	t.Run("bounded body", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(response, "abcdefgh")
		}))
		defer server.Close()
		var stdout, stderr bytes.Buffer
		code := RunWithConfig(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{"ASTRORDER_AGENT_TOKEN": "token", "ASTRORDER_AGENT_URL": server.URL}), Config{MaxErrorBodyBytes: 4})
		if code != 1 || stderr.String() != "Astrorder agent API 500: abcd... (truncated)\n" {
			t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
		}
	})

	t.Run("redirect rejected", func(t *testing.T) {
		var first, second atomic.Int32
		secondServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			second.Add(1)
			_, _ = io.WriteString(response, `{}`)
		}))
		defer secondServer.Close()
		firstServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			first.Add(1)
			response.Header().Set("Location", secondServer.URL+invokePath)
			response.WriteHeader(http.StatusTemporaryRedirect)
		}))
		defer firstServer.Close()
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{"ASTRORDER_AGENT_TOKEN": "token", "ASTRORDER_AGENT_URL": firstServer.URL}))
		if code != 1 || first.Load() != 1 || second.Load() != 0 || !strings.HasPrefix(stderr.String(), "Astrorder agent API 307:") {
			t.Fatalf("exit = %d, first = %d, second = %d, stderr = %q", code, first.Load(), second.Load(), stderr.String())
		}
	})
}

func TestInvalidOrOversizedSuccessResponseFailsWithoutPartialOutput(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		config Config
		want   string
	}{
		{"invalid JSON", "not-json", Config{}, "Astrorder agent API returned invalid JSON:"},
		{"oversized", `{"long":"value"}`, Config{MaxBodyBytes: 5}, "Astrorder agent API response exceeds 5 bytes\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				_, _ = io.WriteString(response, test.body)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			code := RunWithConfig(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{"ASTRORDER_AGENT_TOKEN": "token", "ASTRORDER_AGENT_URL": server.URL}), test.config)
			if code != 1 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), test.want) {
				t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestErrorsAndRegistrationsDoNotExposeSecrets(t *testing.T) {
	const secret = "never-print-this-token"
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, mapLookup(map[string]string{
		"ASTRORDER_AGENT_TOKEN": secret,
		"ASTRORDER_AGENT_URL":   "://bad-url",
	}))
	registrations, err := json.Marshal(Registrations())
	if err != nil {
		t.Fatal(err)
	}
	combined := stdout.String() + stderr.String() + string(registrations)
	if code != 1 || strings.Contains(combined, secret) || strings.Contains(combined, "ASTRORDER_AGENT_TOKEN") {
		t.Fatalf("exit = %d, unsafe output = %q", code, combined)
	}
}

func mapLookup(values map[string]string) EnvLookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
