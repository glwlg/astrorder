package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRunCatalogMakesOneRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/agent/invoke" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer agent-token" {
			t.Errorf("authorization = %q", got)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type = %q", got)
		}
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		var body map[string]any
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["capability"] != "catalog.list" {
			t.Errorf("capability = %#v", body["capability"])
		}
		input, ok := body["input"].(map[string]any)
		if !ok || len(input) != 0 {
			t.Errorf("input = %#v", body["input"])
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"message":"你好"}`)
	}))
	defer server.Close()

	environment := map[string]string{
		"ASTRORDER_AGENT_TOKEN": "agent-token",
		"ASTRORDER_AGENT_URL":   server.URL,
	}
	lookup := func(key string) (string, bool) {
		value, ok := environment[key]
		return value, ok
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"catalog"}, strings.NewReader(""), &stdout, &stderr, lookup)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "{\n  \"message\": \"你好\"\n}\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}
