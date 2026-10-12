package httpapi

import (
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormalProjectRoutesPersistAndPublishExactDeletion(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := NewHandlerChecked(Options{Store: st, Config: config.Config{BrowserSecret: "fixture", DefaultWorkspace: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %s code=%d body=%s", method, path, w.Code, w.Body.String())
		}
		var data map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	created := call(http.MethodPost, "/api/v1/projects", `{"workspace":"C:/fixture","name":"fixture project"}`)["project"].(map[string]any)
	bootstrap := call(http.MethodGet, "/api/v1/bootstrap", "")
	if len(bootstrap["projects"].([]any)) != 1 {
		t.Fatal("created project missing from bootstrap")
	}
	body, _ := json.Marshal(map[string]any{"project_key": created["id"], "delete_sessions": false})
	deleted := call(http.MethodPost, "/api/v1/projects/delete", string(body))
	if deleted["ok"] != true {
		t.Fatalf("delete result=%v", deleted)
	}
	remaining, err := st.ListProjects(context.Background())
	if err != nil || len(remaining) != 0 {
		t.Fatalf("project delete not persisted: %v", err)
	}
	resync, events, err := st.ReplayEvents(context.Background(), 0)
	if err != nil || resync {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "project.delete" {
			found = true
			if event.Data["project_key"] != created["id"] || event.Data["workspace"] != nil || event.Data["project_id"] != nil {
				t.Fatalf("broad delete event=%v", event.Data)
			}
		}
	}
	if !found {
		t.Fatal("missing durable project deletion event")
	}
}
