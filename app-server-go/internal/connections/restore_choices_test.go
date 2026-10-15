package connections_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyConnectionRoutesPersistAutoConnectChoice(t *testing.T) {
	for _, tc := range []struct {
		path, key string
		enabled   bool
	}{
		{"/api/v1/connections/local/connect", "local:hermes", true},
		{"/api/v1/connections/local/disconnect", "local:hermes", false},
		{"/api/v1/connections/codex/connect", "local:codex", true},
		{"/api/v1/connections/codex/disconnect", "local:codex", false},
		{"/api/v1/connections/ssh/saved/connect", "saved:hermes", true},
		{"/api/v1/connections/ssh/saved/disconnect", "saved:hermes", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			h, st, _ := receiptFixture(t, &fakeDaemonService{})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, tc.path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			enabled, present, err := st.GetAgentConnectionChoice(context.Background(), tc.key)
			if err != nil || !present || enabled != tc.enabled {
				t.Fatalf("choice = %v, present=%v, err=%v", enabled, present, err)
			}
		})
	}
}
