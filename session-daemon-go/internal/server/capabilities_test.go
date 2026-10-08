package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"astrorder.dev/session-daemon/internal/parity"
	"astrorder.dev/session-daemon/internal/server"
)

func TestStatusAdvertisesEveryContractedRuntimeBeforeAuthentication(t *testing.T) {
	handler := server.New(server.Config{Secret: "test-secret"}).Handler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/internal/capabilities", nil)

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated capability probe status = %d, want 401", response.Code)
	}
	request.Header.Set("Authorization", "Bearer test-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("authenticated capability probe status = %d body=%s", response.Code, body)
	}
	var got struct {
		RuntimeTypes []string `json:"runtime_types"`
		Implemented  bool     `json:"implemented"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	var manifest struct {
		RuntimeTypes []string `json:"runtime_types"`
	}
	if err := json.Unmarshal(parity.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if !got.Implemented || len(got.RuntimeTypes) != len(manifest.RuntimeTypes) {
		t.Fatalf("daemon is not advertising the complete runtime set: %+v", got)
	}
}
