package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/store"
)

func newServiceFixture(t *testing.T, upstreamURL string) (*Service, *preferenceFixture, *runnerFixture, Target) {
	t.Helper()
	target := Target{ID: "local", Name: "Local", Kind: TargetLocal, Agents: []string{TargetAppCodex}, Home: filepath.ToSlash(t.TempDir())}
	preferences := &preferenceFixture{value: store.Preference{Exists: true, Value: fmt.Sprintf(`{"url":%q,"api_key":"fixture-secret"}`, upstreamURL)}}
	config, err := NewConfigService(ConfigServiceOptions{Preferences: preferences, FallbackPath: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatal(err)
	}
	runner := &runnerFixture{}
	targets := resolverFixture{targets: []Target{target}}
	syncer, err := NewSyncer(SyncerOptions{Resolver: targets, FS: newMemoryFS(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(ServiceOptions{
		Config: config, Client: NewClientFactory(config, OpenVikingClientOptions{}), Targets: targets, Syncer: syncer,
		Authorize: func(request *http.Request) error {
			if request.Header.Get("Authorization") != "Bearer browser-fixture" {
				return fmt.Errorf("Authentication required")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, preferences, runner, target
}

func TestServiceRegistrationAndMuxShareAllTenRoutes(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case "/api/v1/fs/ls", "/api/v1/fs/tree":
			_, _ = w.Write([]byte(`{"result":[]}`))
		case "/api/v1/content/read":
			_, _ = w.Write([]byte(`{"result":"ok"}`))
		case "/api/v1/search/search":
			_, _ = w.Write([]byte(`{"result":{"memories":[]}}`))
		}
	}))
	defer upstream.Close()
	service, _, _, _ := newServiceFixture(t, upstream.URL)
	want := []Registration{
		{RegistrationHTTP, http.MethodGet, "/api/v1/memory/config", AuthBrowser},
		{RegistrationHTTP, http.MethodPost, "/api/v1/memory/config", AuthBrowser},
		{RegistrationHTTP, http.MethodGet, "/api/v1/memory/health", AuthBrowser},
		{RegistrationHTTP, http.MethodGet, "/api/v1/memory/ls", AuthBrowser},
		{RegistrationHTTP, http.MethodGet, "/api/v1/memory/tree", AuthBrowser},
		{RegistrationHTTP, http.MethodGet, "/api/v1/memory/content", AuthBrowser},
		{RegistrationHTTP, http.MethodPost, "/api/v1/memory/search", AuthBrowser},
		{RegistrationHTTP, http.MethodGet, "/api/v1/memory/targets", AuthBrowser},
		{RegistrationHTTP, http.MethodGet, "/api/v1/memory/targets/{target_id}/status", AuthBrowser},
		{RegistrationHTTP, http.MethodPost, "/api/v1/memory/apply", AuthBrowser},
	}
	if !reflect.DeepEqual(service.Registrations(), want) {
		t.Fatalf("registrations = %#v", service.Registrations())
	}
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	client := server.Client()
	for _, registration := range want {
		requestPath := strings.Replace(registration.Path, "{target_id}", "local", 1)
		if registration.Path == "/api/v1/memory/content" {
			requestPath += "?uri=viking%3A%2F%2Fdoc"
		}
		body := ""
		if registration.Method == http.MethodPost {
			switch registration.Path {
			case "/api/v1/memory/config":
				body = fmt.Sprintf(`{"url":%q}`, upstream.URL)
			case "/api/v1/memory/search":
				body = `{"query":"needle","limit":5}`
			case "/api/v1/memory/apply":
				body = `{"target_ids":[]}`
			}
		}
		request, _ := http.NewRequest(registration.Method, server.URL+requestPath, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer browser-fixture")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode == http.StatusServiceUnavailable {
			t.Errorf("%s %s returned %d", registration.Method, registration.Path, response.StatusCode)
		}
	}
}

func TestServiceAuthValidationEnvelopesAndUpstreamWire(t *testing.T) {
	t.Parallel()
	var searchCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Errorf("upstream authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path == "/api/v1/search/search" {
			searchCalls.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"memories":[{"score":9007199254740993}]}}`))
	}))
	defer upstream.Close()
	service, preferences, _, _ := newServiceFixture(t, upstream.URL)
	server := httptest.NewServer(service.Handler())
	defer server.Close()

	unauthorized, err := http.Get(server.URL + "/api/v1/memory/config")
	if err != nil {
		t.Fatal(err)
	}
	defer unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d", unauthorized.StatusCode)
	}

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/memory/search", strings.NewReader(`{"query":"","limit":21}`))
	request.Header.Set("Authorization", "Bearer browser-fixture")
	invalid, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer invalid.Body.Close()
	if invalid.StatusCode != http.StatusUnprocessableEntity || searchCalls.Load() != 0 {
		t.Fatalf("invalid status=%d calls=%d", invalid.StatusCode, searchCalls.Load())
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/v1/memory/search", strings.NewReader(`{"query":"needle","limit":5}`))
	request.Header.Set("Authorization", "Bearer browser-fixture")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var envelope struct {
		Items []map[string]any `json:"items"`
	}
	decoder := json.NewDecoder(response.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || envelope.Items[0]["score"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("response=%d %#v", response.StatusCode, envelope)
	}

	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/v1/memory/config", strings.NewReader(fmt.Sprintf(`{"url":%q,"api_key":""}`, upstream.URL)))
	request.Header.Set("Authorization", "Bearer browser-fixture")
	updated, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = updated.Body.Close()
	if updated.StatusCode != http.StatusOK || len(preferences.writes) != 1 || strings.Contains(fmt.Sprint(preferences.writes), "fixture-secret") {
		t.Fatalf("config status=%d writes=%#v", updated.StatusCode, preferences.writes)
	}
}

func TestServicePropagatesCancellationToUpstream(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	canceled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer upstream.Close()
	service, _, _, _ := newServiceFixture(t, upstream.URL)
	server := httptest.NewServer(service.Handler())
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/memory/ls?uri="+url.QueryEscape("viking://"), nil)
	request.Header.Set("Authorization", "Bearer browser-fixture")
	done := make(chan struct{})
	go func() {
		_, _ = server.Client().Do(request)
		close(done)
	}()
	<-started
	cancel()
	<-canceled
	<-done
}

func TestServicePreservesInjectedAuthStatus(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	service, _, _, _ := newServiceFixture(t, upstream.URL)
	service.authorize = func(*http.Request) error {
		return &auth.Error{Status: http.StatusForbidden, Detail: "Origin is not allowed"}
	}
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/memory/config", nil))
	if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "Origin is not allowed") {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestServiceHealthUnavailableRetainsNullVersion(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "offline", http.StatusServiceUnavailable)
	}))
	upstreamURL := upstream.URL
	upstream.Close()
	service, _, _, _ := newServiceFixture(t, upstreamURL)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/memory/health", nil)
	request.Header.Set("Authorization", "Bearer browser-fixture")
	service.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"version":null`) || !strings.Contains(recorder.Body.String(), `"healthy":false`) {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestServiceEmptyApplyIsNoOp(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	service, _, runner, _ := newServiceFixture(t, upstream.URL)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/memory/apply", strings.NewReader(`{"target_ids":[]}`))
	request.Header.Set("Authorization", "Bearer browser-fixture")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{\"items\":[]}\n" || len(runner.commands) != 0 {
		t.Fatalf("response=%d %s commands=%#v", recorder.Code, recorder.Body.String(), runner.commands)
	}
}

func TestServiceUpstreamErrorDoesNotLeakAPIKey(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fixture-secret", http.StatusBadGateway)
	}))
	defer upstream.Close()
	service, _, _, _ := newServiceFixture(t, upstream.URL)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/memory/ls", nil)
	request.Header.Set("Authorization", "Bearer browser-fixture")
	recorder := httptest.NewRecorder()
	service.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadGateway || bytes.Contains(recorder.Body.Bytes(), []byte("fixture-secret")) {
		t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
	}
}
