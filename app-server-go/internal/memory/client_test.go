package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenVikingClientWireContractAndUseNumber(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	seen := make([]string, 0, 5)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" "+r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			if r.Header.Get("Authorization") != "" {
				t.Errorf("health unexpectedly sent authorization")
			}
			_, _ = w.Write([]byte(`{"status":"ready","version":"0.4.17"}`))
		case "/api/v1/fs/ls":
			if r.URL.Query().Get("uri") != "viking://user/a b" || r.URL.Query().Get("recursive") != "false" {
				t.Errorf("ls query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"result":[{"uri":"viking://large","size":9007199254740993}]}`))
		case "/api/v1/fs/tree":
			if r.URL.Query().Get("level_limit") != "10" || r.URL.Query().Get("node_limit") != "5000" {
				t.Errorf("tree query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"result":[{"uri":"viking://tree"}]}`))
		case "/api/v1/content/read":
			_, _ = w.Write([]byte(`{"result":"# exact"}`))
		case "/api/v1/search/search":
			var payload map[string]any
			decoder := json.NewDecoder(r.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 2 || payload["query"] != "needle" || payload["scope"] != "viking://user" {
				t.Errorf("search payload = %#v", payload)
			}
			_, _ = w.Write([]byte(`{"result":{"memories":[{"uri":"viking://hit","seq":9007199254740995}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewOpenVikingClient(OpenVikingClientOptions{BaseURL: server.URL + "/", APIKey: "fixture-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	health := client.Health(ctx)
	if !health.Healthy || health.Status != "ready" || health.Version == nil || *health.Version != "0.4.17" {
		t.Fatalf("health = %#v", health)
	}
	items, err := client.ListDir(ctx, "viking://user/a b")
	if err != nil {
		t.Fatal(err)
	}
	if items[0]["size"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("large integer lost: %#v", items)
	}
	if _, err = client.Tree(ctx, "viking://"); err != nil {
		t.Fatal(err)
	}
	if content, readErr := client.ReadContent(ctx, "viking://doc"); readErr != nil || content != "# exact" {
		t.Fatalf("content=%q err=%v", content, readErr)
	}
	results, err := client.Search(ctx, "needle", "viking://user", 20)
	if err != nil || results[0]["seq"].(json.Number).String() != "9007199254740995" {
		t.Fatalf("search=%#v err=%v", results, err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, request := range seen[1:] {
		if !strings.HasSuffix(request, "Bearer fixture-secret") {
			t.Fatalf("missing bearer header in %q", request)
		}
	}
}

func TestOpenVikingClientErrorsTimeoutAndCancellationAreSanitized(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			<-r.Context().Done()
		case "/api/v1/fs/ls":
			http.Error(w, "upstream leaked fixture-secret", http.StatusBadGateway)
		case "/api/v1/fs/tree":
			_, _ = w.Write([]byte(`{"result":`))
		}
	}))
	defer server.Close()

	client, err := NewOpenVikingClient(OpenVikingClientOptions{
		BaseURL: server.URL, APIKey: "fixture-secret", Timeout: 25 * time.Millisecond, TreeTimeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	health := client.Health(context.Background())
	if health.Healthy || health.Status != "unreachable" || strings.Contains(health.Error, "fixture-secret") {
		t.Fatalf("health = %#v", health)
	}
	_, err = client.ListDir(context.Background(), "viking://")
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatalf("status error = %v", err)
	}
	_, err = client.Tree(context.Background(), "viking://")
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("json error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.ListDir(ctx, "viking://")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestNewOpenVikingClientRejectsInvalidBaseURL(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"", "localhost:1933", "ftp://example.test", "http://"} {
		if _, err := NewOpenVikingClient(OpenVikingClientOptions{BaseURL: raw}); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, err := url.Parse(DefaultOpenVikingURL); err != nil {
		t.Fatal(err)
	}
}
