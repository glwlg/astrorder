package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestBootstrapExposesExplicitDisconnectWithSavedSession(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, err = st.UpsertAgent(ctx, map[string]any{
		"id": "remote-agy", "name": "Antigravity", "kind": "antigravity-ssh", "connection_id": "saved", "status": "ready",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.UpsertSession(ctx, map[string]any{
		"id": "retained", "agent_id": "remote-agy", "title": "Saved session", "status": "idle", "updated_at": "2026-10-12T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := st.RecordAgentConnectionChoice(ctx, "saved:antigravity", false)
	if err != nil || len(events) != 1 {
		t.Fatalf("record disconnect: events=%v err=%v", events, err)
	}
	handler, err := NewHandler(Options{
		Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: application.NewEventHub(st),
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	r.Header.Set("Authorization", "Bearer fixture")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var snapshot struct {
		Disabled []string         `json:"disabled_agent_connections"`
		Cursor   int64            `json:"cursor"`
		Agents   []map[string]any `json:"agents"`
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); w.Code != http.StatusOK || err != nil {
		t.Fatalf("bootstrap HTTP=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
	if !reflect.DeepEqual(snapshot.Disabled, []string{"saved:antigravity"}) || snapshot.Cursor != events[0].Cursor {
		t.Fatalf("disconnect state lost: %+v", snapshot)
	}
	if len(snapshot.Agents) != 1 || snapshot.Agents[0]["status"] != "disconnected" {
		t.Fatalf("disconnect status lost: %v", snapshot.Agents)
	}
	if len(snapshot.Sessions) != 1 || snapshot.Sessions[0]["id"] != "retained" {
		t.Fatalf("saved session lost: %v", snapshot.Sessions)
	}
}

func TestBootstrapRetainsExactPendingApprovalIdentity(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := application.NewEventHub(st)
	if err = hub.Publish(context.Background(), "approval.upsert", map[string]any{"id": "codex:9007199254740993", "agent_id": "a", "session_id": "native", "state": "pending", "title": "授权"}); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: hub})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	r.Header.Set("Authorization", "Bearer fixture")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 || !stringsContains(w.Body.String(), "codex:9007199254740993") {
		t.Fatalf("exact approval lost: %d %s", w.Code, w.Body.String())
	}
}
func stringsContains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
