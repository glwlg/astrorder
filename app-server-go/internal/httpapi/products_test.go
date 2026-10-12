package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/botgroups"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

type groupAgents struct{}

func (groupAgents) Run(context.Context, botgroups.Turn, func(string) error) (string, error) {
	return "", nil
}
func (groupAgents) Interrupt(context.Context, botgroups.Turn) error { return nil }
func TestFormalRouterMountsGroupProduct(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := config.Config{BrowserSecret: "fixture"}
	group, err := botgroups.New(context.Background(), botgroups.Options{Store: st, Controller: groupAgents{}, Authorizer: auth.New(cfg, nil), Publisher: application.NewEventHub(st)})
	if err != nil {
		t.Fatal(err)
	}
	defer group.Close()
	handler, err := NewHandlerChecked(Options{Config: cfg, Store: st, BotGroups: group})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/bot-groups", strings.NewReader(`{"name":"群聊","members":[{"machine_id":"local","agent_id":"agent"}]}`))
	request.Header.Set("Authorization", "Bearer fixture")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("formal route %d: %s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["group"].(map[string]any)["name"] != "群聊" {
		t.Fatal("group not created")
	}
}
