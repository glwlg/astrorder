package settingsapi

import (
	"astrorder.dev/app-server/internal/auth"
	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/kernelcontrol"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKernelOperationsRequireBrowserOriginAndConfirmation(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	h.Authorizer = auth.New(config.Config{BrowserSecret: "fixture", Listen: "127.0.0.1:30001"}, nil)
	launched := 0
	h.Kernels = &kernelcontrol.Service{StateDir: t.TempDir(), Launch: func(string) error { launched++; return nil }}
	for _, row := range []struct {
		body, origin, query, cookie string
		code                        int
	}{
		{`{"target":"app","action":"restart","confirm_active":true}`, "http://localhost:30001", "", "", 401},
		{`{"target":"app","action":"restart","confirm_active":true}`, "", "", "fixture", 403},
		{`{"target":"app","action":"restart","confirm_active":true}`, "https://evil.example", "", "fixture", 403},
		{`{"target":"app","action":"restart","confirm_active":true}`, "http://localhost:30001", "?token=fixture", "fixture", 401},
		{`{"target":"app","action":"restart"}`, "http://localhost:30001", "", "fixture", 422},
		{`{"target":"app","action":"update","confirm_active":true}`, "http://localhost:30001", "", "fixture", 422},
		{`{"target":"app","action":"restart","confirm_active":true} {}`, "http://localhost:30001", "", "fixture", 422},
		{`{"target":"app","action":"restart","confirm_active":true}`, "http://localhost:30001", "", "fixture", 202},
	} {
		r := httptest.NewRequest("POST", "http://localhost:30001/api/v1/services/kernels/operations"+row.query, strings.NewReader(row.body))
		r.Header.Set("Origin", row.origin)
		if row.cookie != "" {
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: row.cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != row.code {
			t.Fatalf("%d want %d: %s", w.Code, row.code, w.Body.String())
		}
	}
	if launched != 1 {
		t.Fatal(launched)
	}
}
func TestKernelStatusRequiresConfiguredAuthorizer(t *testing.T) {
	h := newHandler(t, nil).(*handler)
	h.Kernels = &kernelcontrol.Service{Run: func(context.Context, string, string) (map[string]any, error) {
		return map[string]any{"state": "running"}, nil
	}}
	if w := call(h, "GET", "/api/v1/services/kernels/status", ""); w.Code != 503 {
		t.Fatal(w.Code)
	}
	for _, p := range []struct{ method, path string }{{"GET", "/api/v1/services/kernels/status"}, {"POST", "/api/v1/services/kernels/operations"}, {"GET", "/api/v1/services/kernels/operations/0123456789abcdef0123456789abcdef"}} {
		if !Matches(p.method, p.path) {
			t.Fatal(p)
		}
	}
	if Matches("GET", "/api/v1/services/kernels/operations/../x") {
		t.Fatal("nested route")
	}
}
