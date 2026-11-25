package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/aiprovider"
	"astrorder.dev/app-server/internal/store"
)

func TestTranscriptionsFallbackToOpenAIProvider(t *testing.T) {
	t.Setenv("OPENCODEX_API_AUTH_TOKEN", "")
	var capturedReq *http.Request
	var capturedBody []byte

	mockClient := &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			capturedReq = req
			capturedBody, _ = io.ReadAll(req.Body)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"text": "通用供应商转写成功"}`)),
			}
			resp.Header.Set("Content-Type", "application/json")
			return resp, nil
		}),
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "audio.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	apiKey := "sk-openai-audio-key"
	if _, _, err := aiprovider.UpsertProvider(context.Background(), st, aiprovider.ProviderPatch{
		Name:         "OpenAI Official",
		Protocol:     "openai",
		BaseURL:      "https://api.openai.com/v1",
		APIKey:       &apiKey,
		SetAsDefault: true,
	}); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(Options{
		Authorizer: testAuthenticator(),
		HTTPClient: mockClient,
		Store:      st,
	})
	if err != nil {
		t.Fatal(err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreatePart(map[string][]string{
		"Content-Disposition": {`form-data; name="file"; filename="clip.wav"`},
		"Content-Type":        {"audio/wav"},
	})
	_, _ = part.Write([]byte("RIFF1234WAVEfmt data"))
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/audio/transcriptions", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-secret")
	rec := httptest.NewRecorder()
	svc.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["text"] != "通用供应商转写成功" {
		t.Fatalf("unexpected text: %+v", out)
	}
	if capturedReq == nil || capturedReq.URL.String() != "https://api.openai.com/v1/audio/transcriptions" {
		t.Fatalf("unexpected upstream url: %v", capturedReq)
	}
	if capturedReq.Header.Get("Authorization") != "Bearer sk-openai-audio-key" {
		t.Fatalf("expected Bearer auth header, got %q", capturedReq.Header.Get("Authorization"))
	}
	if !strings.Contains(string(capturedBody), "whisper-1") {
		t.Fatalf("expected whisper-1 model in multipart body: %s", string(capturedBody))
	}
}
