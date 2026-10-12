package httpapi

import (
	"encoding/json"
	"os"
	"testing"

	"astrorder.dev/app-server/internal/agentapi"
	"astrorder.dev/app-server/internal/audio"
	"astrorder.dev/app-server/internal/botgroups"
	"astrorder.dev/app-server/internal/browser"
	"astrorder.dev/app-server/internal/capabilities"
	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/reviewrelay"
	"astrorder.dev/app-server/internal/sessions"
	"astrorder.dev/app-server/internal/settingsapi"
)

// This checks dispatch coverage, not successful execution of every operation.
func TestFrozenRoutesHaveGoDispatch(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/reports/full-go-baseline.json")
	if err != nil {
		t.Fatal(err)
	}
	var baseline struct {
		Inventory struct {
			HTTP []struct {
				Methods []string `json:"methods"`
				Path    string   `json:"path"`
			} `json:"http_routes"`
		} `json:"inventory"`
	}
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatal(err)
	}
	if len(baseline.Inventory.HTTP) == 0 {
		t.Fatal("empty frozen routes")
	}
	for _, route := range baseline.Inventory.HTTP {
		for _, method := range route.Methods {
			p := route.Path
			covered := false
			for _, r := range registeredRoutes {
				if r.Method == method && r.Path == p {
					covered = true
				}
			}
			for _, match := range []func(string, string) bool{sessions.Matches, agentapi.Matches, botgroups.Matches, browser.Matches, audio.Matches, modelgateway.Matches, capabilities.Matches, settingsapi.Matches, connections.Matches, reviewrelay.Matches} {
				covered = covered || match(method, p)
			}
			covered = covered || isFilesRoute(p) || isGitRoute(p) || isAttachmentsRoute(p) || isMemoryRoute(p) || p == "/api/v1/preferences" || p == "/api/v1/preferences/import"
			if !covered {
				t.Errorf("no Go dispatch: %s %s", method, p)
			}
		}
	}
}
