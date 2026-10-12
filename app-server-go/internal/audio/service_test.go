package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
	"github.com/coder/websocket"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testAuthenticator() *auth.Authenticator {
	return auth.New(config.Config{
		BrowserSecret:  "test-secret",
		AllowedOrigins: []string{"http://localhost:30001", "http://127.0.0.1:30001"},
	}, nil)
}

func TestMatches(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		{"POST", "/api/v1/audio/transcriptions", true},
		{"GET", "/api/v1/audio/transcriptions", false},
		{"GET", "/api/v1/audio/local-plugin", true},
		{"PUT", "/api/v1/audio/local-plugin", true},
		{"DELETE", "/api/v1/audio/local-plugin", false},
		{"GET", "/api/v1/audio/local-stream", true},
		{"POST", "/api/v1/audio/local-stream", false},
		{"GET", "/api/v1/other", false},
	}
	for _, c := range cases {
		got := Matches(c.method, c.path)
		if got != c.want {
			t.Errorf("Matches(%s, %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestTranscriptionsRequiresBrowserAuth(t *testing.T) {
	authorizer := testAuthenticator()
	svc, err := NewService(Options{Authorizer: authorizer})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/audio/transcriptions", nil)
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestTranscriptionsValidation(t *testing.T) {
	authorizer := testAuthenticator()
	svc, err := NewService(Options{Authorizer: authorizer})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// 1. Invalid mime type
	{
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreatePart(map[string][]string{
			"Content-Disposition": {`form-data; name="file"; filename="test.txt"`},
			"Content-Type":        {"text/plain"},
		})
		_, _ = part.Write([]byte("not an audio"))
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/audio/transcriptions", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer test-secret")
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("expected 415, got %d", rec.Code)
		}
	}

	// 2. Empty file
	{
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreatePart(map[string][]string{
			"Content-Disposition": {`form-data; name="file"; filename="test.wav"`},
			"Content-Type":        {"audio/wav"},
		})
		_, _ = part.Write([]byte{})
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/audio/transcriptions", body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer test-secret")
		rec := httptest.NewRecorder()
		svc.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	}
}

func TestTranscriptionsSuccessAndUpstreamContract(t *testing.T) {
	t.Setenv("OPENCODEX_API_AUTH_TOKEN", "env-key-12345")

	var capturedReq *http.Request
	var capturedBody []byte

	mockClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			capturedReq = req
			body, _ := io.ReadAll(req.Body)
			capturedBody = body
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"text": "转写测试成功"}`)),
			}
			resp.Header.Set("Content-Type", "application/json")
			return resp, nil
		}),
	}

	authorizer := testAuthenticator()
	svc, err := NewService(Options{
		Authorizer: authorizer,
		HTTPClient: mockClient,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="recording.wav"`},
		"Content-Type":        {"audio/wav"},
	})
	_, _ = part.Write([]byte("RIFF1234WAVEfmt audio bytes"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res["text"] != "转写测试成功" {
		t.Fatalf("expected text '转写测试成功', got %v", res["text"])
	}

	if capturedReq == nil {
		t.Fatal("upstream was not called")
	}
	if capturedReq.Header.Get("X-OpenCodex-API-Key") != "env-key-12345" {
		t.Fatalf("unexpected API key: %s", capturedReq.Header.Get("X-OpenCodex-API-Key"))
	}
	if !strings.Contains(string(capturedBody), "gpt-4o-transcribe") {
		t.Fatalf("upstream payload missing model: %s", string(capturedBody))
	}
}

func TestResolveOpenCodexProfilePreference(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()

	// 1. Configured under gateway_profiles.opencodex even if gateway_type is magpie
	configMap := map[string]any{
		"gateway_type": "magpie",
		"gateway_profiles": map[string]any{
			"opencodex": map[string]any{
				"inference_url": "https://ocx-profile.example/v1",
				"api_key":       "ocx-profile-key",
			},
		},
	}
	if err := st.SetPreference(ctx, "analytics:llm_gateway", configMap, true); err != nil {
		t.Fatalf("SetPreference: %v", err)
	}

	prof := ResolveOpenCodexProfile(ctx, st)
	if prof.InferenceURL != "https://ocx-profile.example/v1" {
		t.Fatalf("unexpected inference URL: %s", prof.InferenceURL)
	}
	if prof.APIKey != "ocx-profile-key" {
		t.Fatalf("unexpected API key: %s", prof.APIKey)
	}
}

func TestLocalPluginGetAndPut(t *testing.T) {
	authorizer := testAuthenticator()
	svc, err := NewService(Options{
		Authorizer:  authorizer,
		ResourceDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// GET status
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audio/local-plugin", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var status map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if status["id"] != "r2t2-local-voice" {
		t.Fatalf("unexpected status payload: %v", status)
	}

	// PUT disabled
	putBody := strings.NewReader(`{"enabled": false}`)
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/audio/local-plugin", putBody)
	putReq.Header.Set("Authorization", "Bearer test-secret")
	putRec := httptest.NewRecorder()
	svc.ServeHTTP(putRec, putReq)

	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", putRec.Code)
	}
	var putStatus map[string]any
	_ = json.Unmarshal(putRec.Body.Bytes(), &putStatus)
	if putStatus["enabled"] != false {
		t.Fatalf("expected enabled=false, got %v", putStatus["enabled"])
	}
}

func TestLocalStreamWebSocket(t *testing.T) {
	authorizer := testAuthenticator()
	resDir := t.TempDir()

	// Create fake resources so manager thinks available=true
	_ = os.MkdirAll(filepath.Join(resDir, "runtime"), 0o755)
	_ = os.MkdirAll(filepath.Join(resDir, "models"), 0o755)
	exeName := "llama-server"
	if os.PathSeparator == '\\' {
		exeName = "llama-server.exe"
	}
	_ = os.WriteFile(filepath.Join(resDir, "runtime", exeName), []byte("fake-exe"), 0o755)
	_ = os.WriteFile(filepath.Join(resDir, "models", ModelName), []byte("fake-model"), 0o644)
	_ = os.WriteFile(filepath.Join(resDir, "models", MMProjName), []byte("fake-mmproj"), 0o644)

	// Upstream llama.cpp mock server
	var upstreamCallCount int
	mockLlamaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			upstreamCallCount++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			res := map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]string{
							"content": "language Chinese<asr_text>测试流式听写成功",
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockLlamaServer.Close()

	svc, err := NewService(Options{
		Authorizer:  authorizer,
		ResourceDir: resDir,
		HTTPClient:  mockLlamaServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Manually set plugin to running with port pointing to mock server
	port := mockLlamaServer.Listener.Addr().(*net.TCPAddr).Port
	svc.plugin.mu.Lock()
	svc.plugin.state = "running"
	svc.plugin.port = port
	svc.plugin.mu.Unlock()

	server := httptest.NewServer(svc.Handler())
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/audio/local-stream?token=test-secret"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	defer conn.CloseNow()

	// 1. First message should be ready
	_, msg, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read ready: %v", err)
	}
	var ready map[string]any
	_ = json.Unmarshal(msg, &ready)
	if ready["type"] != "ready" {
		t.Fatalf("expected ready message, got %v", ready)
	}

	// 2. Send 32000 bytes PCM
	pcmChunk := make([]byte, 32000)
	err = conn.Write(ctx, websocket.MessageBinary, pcmChunk)
	if err != nil {
		t.Fatalf("write pcm: %v", err)
	}

	// 3. Should receive transcript
	_, msg, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	var transcript map[string]any
	_ = json.Unmarshal(msg, &transcript)
	if transcript["type"] != "transcript" || transcript["text"] != "测试流式听写成功" {
		t.Fatalf("expected transcript '测试流式听写成功', got %v", transcript)
	}

	// 4. Send stop
	err = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"stop"}`))
	if err != nil {
		t.Fatalf("write stop: %v", err)
	}

	// 5. Should receive done
	_, msg, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("read done: %v", err)
	}
	var done map[string]any
	_ = json.Unmarshal(msg, &done)
	if done["type"] != "done" || done["text"] != "测试流式听写成功" {
		t.Fatalf("expected done '测试流式听写成功', got %v", done)
	}
}
