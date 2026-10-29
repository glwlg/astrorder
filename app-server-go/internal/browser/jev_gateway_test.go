package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/modelgateway"
	"astrorder.dev/app-server/internal/store"
)

func TestBrowserPlannerUsesSavedJevGatewayWithoutOfficialKey(t *testing.T) {
	t.Setenv("JEV_API_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer upstream.Close()
	if _, err := modelgateway.SaveGatewayConfig(context.Background(), st, map[string]any{"gateway_type": "magpie", "inference_url": upstream.URL + "/v1", "api_key": "gateway-secret"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(context.Background(), JevConfigPreference, JevConfig{Mode: "gateway", Model: "typesafe/jev-preview"}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPreference(context.Background(), BrowserEnginePreference, "jev_ultrafast", true); err != nil {
		t.Fatal(err)
	}
	fake := newFakeCDPServer(t)
	defer fake.server.Close()
	mgr := NewSessionManager(fake.server.URL, true)
	mgr.engineRunner = func(_ context.Context, request engineRequest) (engineResult, error) {
		if request.Engine != "jev_ultrafast" || request.Jev.URL != upstream.URL+"/v1/systemone" ||
			request.Jev.APIKey != "gateway-secret" || request.Jev.Model != "typesafe/jev-preview" {
			t.Fatal("Jev upstream ignored the saved gateway")
		}
		return engineResult{OK: true, Status: "done", TargetID: request.TargetID}, nil
	}
	result, err := mgr.RunExternalBrowserTask(context.Background(), BrowserTaskRequest{URL: "https://example.com/test", Goal: "Read the page", SessionKey: fixtureSessionKey}, st)
	if err != nil || result == nil || !result.OK {
		t.Fatal(result, err)
	}
}

func TestJevRejectsRedirectWithoutForwardingGatewayKey(t *testing.T) {
	secondCalls := 0
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { secondCalls++; w.Write([]byte(`{}`)) }))
	defer second.Close()
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL, http.StatusTemporaryRedirect)
	}))
	defer first.Close()
	p := JevPlanner{JevKey: "gateway-secret", JevURL: first.URL, JevModel: "decision"}
	if _, err := p.InvokeJevHTTP(context.Background(), map[string]any{}, map[string]any{}); err == nil || secondCalls != 0 {
		t.Fatal("redirect followed", err, secondCalls)
	}
}
