package httpapi

import (
	"context"
	"encoding/json"

	"astrorder.dev/app-server/internal/config"
	"astrorder.dev/app-server/internal/store"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormalSessionAndAgentRoutesUseSharedStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h, err := NewHandlerChecked(Options{Store: st, Config: config.Config{BrowserSecret: "fixture", ConnectorSecret: "agent-fixture", DefaultWorkspace: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string) map[string]any {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("formal %s=%d %s", path, w.Code, w.Body.String())
		}
		var value map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	call("POST", "/api/v1/agent/invoke", `{"capability":"blackboard.set","input":{"namespace":"group:正式","key":"result","value":{"x":1}}}`, "agent-fixture")
	value, err := st.GetPreference(context.Background(), "blackboard:group:正式:result")
	if err != nil || !value.Exists {
		t.Fatal("agent writes not persistent")
	}
	call("GET", "/api/v1/bootstrap", "", "fixture")
	call("GET", "/api/v1/sessions", "", "fixture")
}
