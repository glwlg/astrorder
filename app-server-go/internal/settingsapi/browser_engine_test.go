package settingsapi

import (
	"context"
	"strings"
	"testing"

	"astrorder.dev/app-server/internal/browser"
)

func TestBrowserEngineConfiguration(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	if w := call(h, "GET", "/api/v1/services/browser/config", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"engine":"browser_use"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call(h, "POST", "/api/v1/services/browser/config", `{"engine":"jev_ultrafast"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if engine, err := browser.GetBrowserEngine(context.Background(), h.Store); err != nil || engine != "jev_ultrafast" {
		t.Fatal(engine, err)
	}
	if w := call(h, "POST", "/api/v1/services/browser/config", `{"engine":"other"}`); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	if engine, err := browser.GetBrowserEngine(context.Background(), h.Store); err != nil || engine != "jev_ultrafast" {
		t.Fatal(engine, err)
	}
}
