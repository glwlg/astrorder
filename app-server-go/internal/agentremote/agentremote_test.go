package agentremote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConfigResolvePriority(t *testing.T) {
	// 1. Missing token fails
	_, err := ResolveConfig(nil, nil)
	if err == nil {
		t.Fatal("expected error on missing token")
	}

	// 2. Token file fallback
	envMap := map[string]string{
		"ASTRORDER_TOKEN_FILE": "my.token",
	}
	lookup := func(k string) (string, bool) {
		v, ok := envMap[k]
		return v, ok
	}
	fileReader := func(path string) ([]byte, error) {
		if path == "my.token" {
			return []byte("file-token-value\n"), nil
		}
		return nil, io.EOF
	}

	cfg, err := ResolveConfig(lookup, fileReader)
	if err != nil || cfg.Token != "file-token-value" {
		t.Fatalf("expected token file read, got: %+v err: %v", cfg, err)
	}

	// 3. Env priority: AGENT_TOKEN > BROWSER_SECRET
	envMap["ASTRORDER_BROWSER_SECRET"] = "browser-secret"
	cfg, err = ResolveConfig(lookup, fileReader)
	if err != nil || cfg.Token != "browser-secret" {
		t.Fatalf("expected browser secret, got: %+v", cfg)
	}

	envMap["ASTRORDER_AGENT_TOKEN"] = "agent-token"
	cfg, err = ResolveConfig(lookup, fileReader)
	if err != nil || cfg.Token != "agent-token" {
		t.Fatalf("expected agent token priority, got: %+v", cfg)
	}
}

func TestValidateURLAndRedirectSecurity(t *testing.T) {
	badURLs := []string{
		"",
		"ftp://127.0.0.1:30001",
		"http://user:pass@127.0.0.1:30001",
		"http://127.0.0.1:30001?token=secret",
		"http://127.0.0.1:30001#fragment",
	}
	for _, u := range badURLs {
		if _, err := ValidateURL(u); err == nil {
			t.Fatalf("expected error for bad url %q", u)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://attacker.com", http.StatusFound)
			return
		}
	}))
	defer srv.Close()

	injectedClient := &http.Client{}
	cfg := Config{
		BaseURL:    srv.URL,
		Token:      "my-secret-token",
		HTTPClient: injectedClient,
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/redirect", nil)
	resp, err := client.httpClient.Do(req)
	if err != nil && !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("unexpected redirect error: %v", err)
	}
	if resp != nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("expected 302 not followed, got %d", resp.StatusCode)
		}
	}
}

func TestClientInvokePreservesBigIntegers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		_ = dec.Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"seq":   9007199254740993,
			"cap":   req["capability"],
			"input": req["input"],
		})
	}))
	defer srv.Close()

	client, err := NewClient(Config{BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := client.Invoke(context.Background(), "custom.test", map[string]any{"big": 9007199254740993})
	if err != nil {
		t.Fatal(err)
	}

	data, _ := json.Marshal(res)
	if !strings.Contains(string(data), "9007199254740993") {
		t.Fatalf("big integer lost or formatted in scientific notation: %s", string(data))
	}
}

func TestRunStdioIsRetired(t *testing.T) {
	var out bytes.Buffer
	err := RunStdio(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), &out, nil)
	if !errors.Is(err, ErrMCPRetired) {
		t.Fatalf("expected ErrMCPRetired, got %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("retired stdio must not write protocol output, got %q", out.String())
	}
}

func TestRunCLIRouting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "routed": req["capability"]})
	}))
	defer srv.Close()

	lookup := func(k string) (string, bool) {
		switch k {
		case "ASTRORDER_AGENT_URL":
			return srv.URL, true
		case "ASTRORDER_AGENT_TOKEN":
			return "tok-1", true
		default:
			return "", false
		}
	}

	var out bytes.Buffer
	var errOut bytes.Buffer

	// 1. catalog
	code := RunCLI(context.Background(), []string{"catalog"}, nil, &out, &errOut, lookup, nil)
	if code != 0 {
		t.Fatalf("catalog failed: code=%d err=%s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "catalog.list") {
		t.Fatalf("output missing routed capability: %s", out.String())
	}

	// 2. sessions search
	out.Reset()
	errOut.Reset()
	code = RunCLI(context.Background(), []string{"sessions", "search", "keyword"}, nil, &out, &errOut, lookup, nil)
	if code != 0 {
		t.Fatalf("sessions search failed: %s", errOut.String())
	}
	if !strings.Contains(out.String(), "sessions.search") {
		t.Fatalf("output missing routed capability: %s", out.String())
	}
}
