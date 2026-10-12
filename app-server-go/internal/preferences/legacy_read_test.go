package preferences

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestHTTPPreferencesPreservesStoredLegacyJSON(t *testing.T) {
	for name, legacyList := range map[string]string{
		"mixed":  `["a",9007199254740993,"b",null]`,
		"null":   `null`,
		"object": `{"legacy":true,"number":9007199254740993}`,
	} {
		t.Run(name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "legacy.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			fixtures := map[string]string{
				"appearance:":                 `{"legacy":true}`,
				"appearance:gone":             `null`,
				"session_pins:":               `false`,
				"session_pins:legacy":         `"true"`,
				"session_pins:gone":           `null`,
				"pinned_projects":             legacyList,
				"project_order:legacy-suffix": `{"order":"retained"}`,
				"other-domain:keep":           `{"preserved":9007199254740993}`,
			}
			for key, raw := range fixtures {
				if _, err := st.DB().Exec(`INSERT INTO workspace_preferences("key",value) VALUES(?,?)`, key, raw); err != nil {
					t.Fatal(err)
				}
			}
			handler := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture-browser"}, nil)})
			for _, step := range []struct {
				method, path, body string
				fresh              bool
			}{
				{http.MethodGet, "/api/v1/preferences", "", false},
				{http.MethodPatch, "/api/v1/preferences", `{"session_pins":{"fresh":true}}`, true},
				{http.MethodPost, "/api/v1/preferences/import", `{"pinned_projects":["replacement"]}`, true},
			} {
				r := httptest.NewRequest(step.method, step.path, strings.NewReader(step.body))
				r.Header.Set("Authorization", "Bearer fixture-browser")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, r)
				if response.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", step.method, response.Code, response.Body.String())
				}
				wantJSON := `{"appearance":{"":{"legacy":true}},"session_pins":{"":false,"legacy":"true"},"pinned_projects":` + legacyList + `,"project_order":{"order":"retained"}}`
				var want, got map[string]any
				decode := func(raw string, result *map[string]any) {
					t.Helper()
					decoder := json.NewDecoder(strings.NewReader(raw))
					decoder.UseNumber()
					if err := decoder.Decode(result); err != nil {
						t.Fatal(err)
					}
				}
				decode(wantJSON, &want)
				decode(response.Body.String(), &got)
				if step.fresh {
					want["session_pins"].(map[string]any)["fresh"] = true
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s legacy state changed: got %s want %#v", step.method, response.Body.String(), want)
				}
			}
			for key, expected := range fixtures {
				var raw string
				if err := st.DB().QueryRow(`SELECT value FROM workspace_preferences WHERE "key"=?`, key).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if raw != expected {
					t.Fatalf("legacy key %q overwritten: %s", key, raw)
				}
			}
			if _, err := st.DB().Exec(`DELETE FROM workspace_preferences WHERE "key" IN ('appearance:', 'session_pins:')`); err != nil {
				t.Fatal(err)
			}
			for key, raw := range map[string]string{"appearance": `{"no-colon":true}`, "session_pins": `"bare-key"`} {
				if _, err := st.DB().Exec(`INSERT INTO workspace_preferences("key",value) VALUES(?,?)`, key, raw); err != nil {
					t.Fatal(err)
				}
			}
			state, err := Read(context.Background(), st)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var response map[string]any
			if err := json.Unmarshal(raw, &response); err != nil {
				t.Fatal(err)
			}
			if response["appearance"].(map[string]any)[""].(map[string]any)["no-colon"] != true || response["session_pins"].(map[string]any)[""] != "bare-key" {
				t.Fatalf("source partition semantics lost: %s", raw)
			}
		})
	}
}
