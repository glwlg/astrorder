package agentapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/application"
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
)

func TestTelemetryHTTPInvokeJsonNumber(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	h, err := NewHandler(Options{
		Store:      st,
		Authorizer: auth.New(config.Config{BrowserSecret: "fixture", ConnectorSecret: "connector-fixture"}, nil),
		Publisher:  application.NewEventHub(st),
	})
	if err != nil {
		t.Fatal(err)
	}

	call := func(body string) (int, map[string]any) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/invoke", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer connector-fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("json unmarshal failed: %v, raw: %s", err, w.Body.String())
		}
		return w.Code, result
	}

	// 1. Report telemetry with integer 100 in JSON payload (decoded via decodeAgent UseNumber() as json.Number)
	status, result := call(`{"capability":"swarm.telemetry.report","input":{"key":"agent-http::session-1","progress":100,"phase":"executing","status":"ok","summary":"testing 100"}}`)
	if status != 200 {
		t.Fatalf("expected status 200, got %d: %+v", status, result)
	}
	rep, ok := result["report"].(map[string]any)
	if !ok {
		t.Fatalf("expected report in invoke result: %+v", result)
	}
	// Verify progress is 100, not 0
	if rep["progress"] != float64(100) && rep["progress"] != 100 {
		t.Fatalf("expected report progress to be 100, got %v (%T)", rep["progress"], rep["progress"])
	}

	// 2. Read back via swarm.telemetry.get
	status, getResult := call(`{"capability":"swarm.telemetry.get","input":{"key":"agent-http::session-1"}}`)
	if status != 200 {
		t.Fatalf("expected status 200 for get, got %d: %+v", status, getResult)
	}
	getRep, ok := getResult["report"].(map[string]any)
	if !ok {
		t.Fatalf("expected report in get result: %+v", getResult)
	}
	if getRep["progress"] != float64(100) && getRep["progress"] != 100 {
		t.Fatalf("expected get progress to be 100, got %v (%T)", getRep["progress"], getRep["progress"])
	}

	// 3. Test clamp > 100 (e.g. 150) -> clamps to 100
	status, clampResult := call(`{"capability":"swarm.telemetry.report","input":{"key":"agent-http::session-clamp","progress":150}}`)
	if status != 200 {
		t.Fatalf("expected status 200, got %d: %+v", status, clampResult)
	}
	clampRep := clampResult["report"].(map[string]any)
	if clampRep["progress"] != float64(100) && clampRep["progress"] != 100 {
		t.Fatalf("expected clamp progress to be 100, got %v", clampRep["progress"])
	}

	// 4. Test clamp < 0 (e.g. -20) -> clamps to 0
	status, clampZeroResult := call(`{"capability":"swarm.telemetry.report","input":{"key":"agent-http::session-clamp-zero","progress":-20}}`)
	if status != 200 {
		t.Fatalf("expected status 200, got %d: %+v", status, clampZeroResult)
	}
	clampZeroRep := clampZeroResult["report"].(map[string]any)
	if clampZeroRep["progress"] != float64(0) && clampZeroRep["progress"] != 0 {
		t.Fatalf("expected clamp progress to be 0, got %v", clampZeroRep["progress"])
	}
}
