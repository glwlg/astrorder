package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/agentremote"
)

func TestMCPMainServerWithSyntheticHTTPBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "remote_invoked": true})
	}))
	defer srv.Close()

	cfg := agentremote.Config{
		BaseURL:    srv.URL,
		Token:      "mcp-test-token",
		HTTPClient: srv.Client(),
	}
	client, err := agentremote.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":100,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":101,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":102,"method":"tools/call","params":{"name":"catalog_list","arguments":{}}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = agentremote.RunStdio(ctx, strings.NewReader(input), &out, client)
	if err != nil {
		t.Fatalf("RunStdio: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 responses, got %d:\n%s", len(lines), out.String())
	}

	var callResp map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &callResp); err != nil {
		t.Fatalf("unmarshal call response: %v", err)
	}
	if callResp["id"].(float64) != 102 {
		t.Fatalf("id mismatch: %v", callResp)
	}
	result := callResp["result"].(map[string]any)
	content := result["content"].([]any)
	if len(content) == 0 {
		t.Fatal("empty content in tool response")
	}
}
