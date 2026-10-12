package preferences

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestHTTPPreferencesPersistAcrossClientsAndImportRetainsTombstones(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "preferences.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture-browser"}, nil)})
	request := func(method, route, body, token string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, route, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		r.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		return response
	}
	if got := request(http.MethodPatch, "/api/v1/preferences", `{"session_pins":{"a":true}}`, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized: %d", got.Code)
	}
	legacy := `{"appearance":{"project:a\u0000b":{"icon":"cloud","color":"orange"}},"session_pins":{"a\u0000session":true},"pinned_projects":["p","p"],"project_order":["p","q"]}`
	if got := request(http.MethodPost, "/api/v1/preferences/import", legacy, "fixture-browser"); got.Code != http.StatusOK || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("import: %d %s", got.Code, got.Body.String())
	}
	removed := `{"appearance":{"project:a\u0000b":null},"session_pins":{"a\u0000session":false},"pinned_projects":[],"project_order":["q","p"]}`
	if got := request(http.MethodPatch, "/api/v1/preferences", removed, "fixture-browser"); got.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/v1/preferences/import", legacy, "fixture-browser"); got.Code != http.StatusOK {
		t.Fatalf("reimport: %d", got.Code)
	}
	got := request(http.MethodGet, "/api/v1/preferences", "", "fixture-browser")
	var state struct {
		Appearance     map[string]any  `json:"appearance"`
		SessionPins    map[string]bool `json:"session_pins"`
		PinnedProjects []string        `json:"pinned_projects"`
		ProjectOrder   []string        `json:"project_order"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Appearance) != 0 || state.SessionPins["a\x00session"] || len(state.PinnedProjects) != 0 || strings.Join(state.ProjectOrder, ",") != "q,p" {
		t.Fatalf("tombstones lost: %#v", state)
	}
}

func TestHTTPPreferencesRejectsInvalidInputsWithoutMutation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "preferences.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture-browser"}, nil)})
	invalid := []string{
		`null`, `[]`, `{} {}`, `{"unknown":true}`, `{"appearance":null}`,
		`{"session_pins":null}`, `{"session_pins":{"p":"true"}}`,
		`{"appearance":{"p":{"color":"url(evil)"}}}`, `{"appearance":{"p":{"icon":null}}}`,
		`{"appearance":{"p":{"unknown":true}}}`, `{"project_order":[null]}`, `{"project_order":[""]}`,
	}
	for _, body := range invalid {
		t.Run(body, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPatch, "/api/v1/preferences", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer fixture-browser")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			if response.Code != http.StatusUnprocessableEntity || strings.TrimSpace(response.Body.String()) != `{"detail":"Invalid request"}` {
				t.Fatalf("invalid body accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/preferences", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer fixture-browser")
	r.Header.Set("Origin", "https://untrusted.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusForbidden {
		t.Fatalf("untrusted origin accepted: %d", response.Code)
	}
	values, err := st.ListPreferences(context.Background())
	if err != nil || len(values) != 0 {
		t.Fatalf("rejected request mutated preferences: %#v %v", values, err)
	}
}

func TestHTTPPreferencesMethodNotAllowedAndExplicitNullLists(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "preferences.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	handler := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture-browser"}, nil)})
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/preferences", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status: %d", response.Code)
	}
	r = httptest.NewRequest(http.MethodPatch, "/api/v1/preferences", strings.NewReader(`{"pinned_projects":null,"project_order":null}`))
	r.Header.Set("Authorization", "Bearer fixture-browser")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusOK {
		t.Fatalf("source-compatible null rejected: %d %s", response.Code, response.Body.String())
	}
}
