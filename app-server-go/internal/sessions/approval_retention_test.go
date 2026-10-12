package sessions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestBootstrapKeepsApprovalAcrossRetentionAndResolution(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenWithOptions(filepath.Join(t.TempDir(), "app.db"), store.Options{EventRetention: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hub := application.NewEventHub(st)
	for _, aid := range []string{"a", "b"} {
		if err = hub.Publish(ctx, "approval.upsert", map[string]any{"id": "codex:9007199254740993", "agent_id": aid, "session_id": "same", "state": "pending", "title": "授权"}); err != nil {
			t.Fatal(err)
		}
	}
	if err = hub.Publish(ctx, "approval.upsert", map[string]any{"id": "codex:9007199254740993", "agent_id": "a", "session_id": "same", "state": "approved"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err = hub.Publish(ctx, "unrelated", map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewHandler(Options{Store: st, Authorizer: auth.New(config.Config{BrowserSecret: "fixture"}, nil), Publisher: hub})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	r.Header.Set("Authorization", "Bearer fixture")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	var body struct {
		Approvals []map[string]any `json:"approvals"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(body.Approvals) != 1 || body.Approvals[0]["agent_id"] != "b" || body.Approvals[0]["title"] != "授权" {
		t.Fatalf("retained identity lost: %d %+v", w.Code, body)
	}
}
