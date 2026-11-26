package connections_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/agentaccounts"
	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/envtarget"
	"astrorder.dev/app-server/internal/envtarget/envtargettest"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

type fakeEnvOpener struct {
	envs map[string]*envtargettest.Env
}

func (f *fakeEnvOpener) Open(_ context.Context, id string) (envtarget.Env, error) {
	if env, ok := f.envs[id]; ok {
		return env, nil
	}
	return nil, envtarget.Errorf(http.StatusNotFound, "target not found: %s", id)
}

func setupPlatformAccountsFixture(
	t *testing.T,
	daemon *fakeDaemonService,
	envs map[string]*envtargettest.Env,
	quotaFn connections.PlatformQuotaQuerier,
) (*connections.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "platform-accounts-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	daemon.token = "platform-accounts-token"
	addr, closeServer := startFakeDaemon(t, daemon)
	t.Cleanup(closeServer)

	client, err := daemonclient.Dial(context.Background(), daemonclient.Config{
		Target:         addr,
		Credential:     daemon.token,
		RequestTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	h, err := connections.NewHandler(connections.Options{
		Store:            st,
		Client:           client,
		RemoteExecutable: &recordingResolver{results: map[string]string{"codex": "/usr/bin/codex", "grok": "/usr/bin/grok"}},
		EnvResolver:      &fakeEnvOpener{envs: envs},
		PlatformQuota:    quotaFn,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h, st
}

func TestCodexAndGrokAccountLifecycle(t *testing.T) {
	localEnv := envtargettest.New("local")
	localEnv.Info.Kind = "local"
	remoteEnv := envtargettest.New("ssh:dev")

	codexCredLive := []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-codex-live-secret"}`)
	codexCredImported := []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"sk-codex-import-secret"}`)
	grokCredLive := []byte(`{"xai::api_key":{"key":"xai-grok-live-secret"}}`)
	grokCredImported := []byte(`{"xai::api_key":{"key":"xai-grok-import-secret"}}`)

	localEnv.Files["/home/test/.codex/auth.json"] = codexCredLive
	remoteEnv.Files["/home/test/.codex/auth.json"] = codexCredLive
	localEnv.Files["/home/test/.grok/auth.json"] = grokCredLive
	remoteEnv.Files["/home/test/.grok/auth.json"] = grokCredLive

	var quotaCalls atomic.Int32

	daemon := &fakeDaemonService{}

	h, st := setupPlatformAccountsFixture(t, daemon, map[string]*envtargettest.Env{
		"local": localEnv,
		"dev":   remoteEnv,
	}, func(_ context.Context, platform string, _ envtarget.Env, _ string, _ []byte) (agentaccounts.QuotaResult, error) {
		quotaCalls.Add(1)
		if platform == "codex" {
			return agentaccounts.QuotaResult{
				Plan: "ChatGPT Plus",
				Groups: []agentaccounts.QuotaGroup{{
					Name: "Codex",
					Buckets: []agentaccounts.QuotaBucket{
						{ID: "5h", Name: "5 小时", Window: "5h", RemainingFraction: 0.92, ResetAt: 1795675485000},
					},
				}},
				AsOf: "2026-11-26T02:00:00Z",
			}, nil
		}
		return agentaccounts.QuotaResult{
			Plan:   "SuperGrok",
			Detail: "已认证",
			Groups: []agentaccounts.QuotaGroup{},
			AsOf:   "2026-11-26T02:00:00Z",
		}, nil
	})

	if _, err := st.SaveSSHConnection(t.Context(), map[string]any{"id": "dev", "display_name": "开发机", "host": "dev.example.test", "user": "luwei"}); err != nil {
		t.Fatal(err)
	}
	for _, agent := range []map[string]any{
		{"id": "codex-local", "kind": "codex", "name": "Codex", "status": "ready", "connection_id": "local"},
		{"id": "codex-dev", "kind": "codex-ssh", "name": "Codex · 开发机", "status": "ready", "connection_id": "dev"},
		{"id": "grok-local", "kind": "grok", "name": "Grok", "status": "ready", "connection_id": "local"},
		{"id": "grok-dev", "kind": "grok-ssh", "name": "Grok · 开发机", "status": "ready", "connection_id": "dev"},
	} {
		if _, err := st.UpsertAgent(t.Context(), agent); err != nil {
			t.Fatal(err)
		}
	}

	reqJSON := func(method, path string, payload any, wantStatus int) string {
		t.Helper()
		var body *bytes.Reader
		if payload != nil {
			raw, _ := json.Marshal(payload)
			body = bytes.NewReader(raw)
		} else {
			body = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, path, body)
		if !connections.Matches(method, req.URL.Path) {
			t.Fatalf("connections.Matches(%s, %s) = false", method, req.URL.Path)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != wantStatus {
			t.Fatalf("%s %s = %d (want %d): %s", method, path, w.Code, wantStatus, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("missing Cache-Control: no-store on %s %s", method, path)
		}
		// Never leak secrets in HTTP responses.
		for _, secret := range []string{"sk-codex-live-secret", "sk-codex-import-secret", "xai-grok-live-secret", "xai-grok-import-secret"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("secret leaked in response for %s %s", method, path)
			}
		}
		return w.Body.String()
	}

	// Verify Codex & Grok lifecycle identically.
	for _, tc := range []struct {
		platform   string
		authPath   string
		importCred []byte
		wantPlan   string
	}{
		{"codex", "/home/test/.codex/auth.json", codexCredImported, "ChatGPT Plus"},
		{"grok", "/home/test/.grok/auth.json", grokCredImported, "SuperGrok"},
	} {
		base := "/api/v1/agent-accounts/" + tc.platform

		// 1. Targets include local + dev.
		targetsOut := reqJSON(http.MethodGet, base+"/targets", nil, http.StatusOK)
		if !strings.Contains(targetsOut, `"id":"local"`) || !strings.Contains(targetsOut, `"id":"dev"`) {
			t.Fatalf("%s targets missing expected machines: %s", tc.platform, targetsOut)
		}

		// 2. Unsaved live credential shows as "current".
		stateOut := reqJSON(http.MethodGet, base+"?environment_id=local", nil, http.StatusOK)
		if !strings.Contains(stateOut, `"current_account_id":"current"`) || !strings.Contains(stateOut, `"quota_supported":true`) {
			t.Fatalf("%s state unexpected: %s", tc.platform, stateOut)
		}

		// 3. Save current account from target.
		saveOut := reqJSON(http.MethodPost, base+"?environment_id=local", map[string]any{"action": "save", "name": "主力账号"}, http.StatusOK)
		var saved struct {
			AccountID string `json:"account_id"`
		}
		if json.Unmarshal([]byte(saveOut), &saved) != nil || saved.AccountID == "" {
			t.Fatalf("%s save missing account_id: %s", tc.platform, saveOut)
		}

		// State now recognizes the saved account ID.
		stateOut = reqJSON(http.MethodGet, base+"?environment_id=local", nil, http.StatusOK)
		if !strings.Contains(stateOut, `"current_account_id":"`+saved.AccountID+`"`) {
			t.Fatalf("%s state did not match saved account: %s", tc.platform, stateOut)
		}

		// 4. Reject invalid credential import, accept valid import.
		reqJSON(http.MethodPost, base+"/import", map[string]any{"name": "坏凭证", "credential": `{}`}, http.StatusUnprocessableEntity)
		impOut := reqJSON(http.MethodPost, base+"/import", map[string]any{"name": "备用账号", "credential": string(tc.importCred)}, http.StatusOK)
		var imported struct {
			AccountID string `json:"account_id"`
		}
		if json.Unmarshal([]byte(impOut), &imported) != nil || imported.AccountID == "" {
			t.Fatalf("%s import missing account_id: %s", tc.platform, impOut)
		}

		// 5. Quota query works for saved account and caches.
		before := quotaCalls.Load()
		quotaOut := reqJSON(http.MethodGet, base+"?environment_id=dev&account_id="+imported.AccountID, nil, http.StatusOK)
		if !strings.Contains(quotaOut, tc.wantPlan) {
			t.Fatalf("%s quota missing plan %q: %s", tc.platform, tc.wantPlan, quotaOut)
		}
		// Second call hits cache.
		reqJSON(http.MethodGet, base+"?environment_id=dev&account_id="+imported.AccountID, nil, http.StatusOK)
		if quotaCalls.Load() != before+1 {
			t.Fatalf("%s quota was not cached", tc.platform)
		}

		// 6. Switch refuses when disconnect is refused (without overwriting auth.json),
		// and succeeds when disconnect completes with confirm_active forwarded to daemon.
		daemon.disconnectResponse = &pb.DisconnectRuntimeResponse{
			DaemonId:       "daemon-test-123",
			OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
			Disconnected:   false,
			Error:          &pb.BusinessError{Message: "存在活动会话，请确认强制重启"},
		}
		reqJSON(http.MethodPost, base+"?environment_id=dev", map[string]any{"action": "switch", "account_id": imported.AccountID, "confirm_active": false}, http.StatusConflict)
		if bytes.Equal(remoteEnv.Files[tc.authPath], tc.importCred) {
			t.Fatalf("%s auth.json was overwritten despite disconnect refusal", tc.platform)
		}

		daemon.disconnectResponse = nil
		switchOut := reqJSON(http.MethodPost, base+"?environment_id=dev", map[string]any{"action": "switch", "account_id": imported.AccountID, "confirm_active": true}, http.StatusOK)
		if !strings.Contains(switchOut, `"restarted":true`) {
			t.Fatalf("%s switch response unexpected: %s", tc.platform, switchOut)
		}
		if req := daemon.disconnectRequest.Load(); req == nil || !req.GetConfirmActive() {
			t.Fatalf("%s switch did not forward confirm_active=true to daemon", tc.platform)
		}
		if !bytes.Equal(remoteEnv.Files[tc.authPath], tc.importCred) {
			t.Fatalf("%s remote auth.json was not updated", tc.platform)
		}

		// Dev state now reports the imported account.
		devState := reqJSON(http.MethodGet, base+"?environment_id=dev", nil, http.StatusOK)
		if !strings.Contains(devState, `"current_account_id":"`+imported.AccountID+`"`) {
			t.Fatalf("%s dev state after switch unexpected: %s", tc.platform, devState)
		}

		// 7. Delete saved account from vault.
		reqJSON(http.MethodPost, base, map[string]any{"action": "delete", "account_id": saved.AccountID}, http.StatusOK)
		listOut := reqJSON(http.MethodGet, base, nil, http.StatusOK)
		if strings.Contains(listOut, saved.AccountID) || !strings.Contains(listOut, imported.AccountID) {
			t.Fatalf("%s list after delete unexpected: %s", tc.platform, listOut)
		}
	}
}
