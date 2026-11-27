package connections_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrorder.dev/app-server/internal/connections"
	"astrorder.dev/contracts/agycredential"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *fakeDaemonService) ManageAgyAccount(ctx context.Context, req *pb.AgyAccountRequest) (*pb.AgyAccountResponse, error) {
	if err := s.checkAuth(ctx); err != nil {
		return nil, err
	}
	if s.agyAccount == nil {
		return nil, status.Error(codes.Unimplemented, "unused by test")
	}
	return s.agyAccount(req)
}

func TestAgyImportOnceAndApplyToTwoTargets(t *testing.T) {
	const base = "/api/v1/agy-accounts"
	credential := []byte("{\n \"token\": {\"access_token\":\"synthetic-import\"}\n}\n")
	identity, _ := agycredential.Identity(credential)
	oldIdentity, _ := agycredential.Identity([]byte(`{"token":{"access_token":"other-synthetic"}}`))
	active := map[string]string{"": oldIdentity}
	fake := &fakeDaemonService{agyAccount: func(req *pb.AgyAccountRequest) (*pb.AgyAccountResponse, error) {
		target := req.Target.GetConnectionId()
		var payload map[string]any
		switch req.Action {
		case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_STATE:
			payload = map[string]any{"identity": active[target], "quota_supported": true}
		case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_APPLY_CREDENTIAL:
			if !bytes.Equal(req.Credential, credential) {
				return nil, status.Error(codes.FailedPrecondition, "credential did not match original bytes")
			}
			if target == "remote" && req.Target.GetSsh().GetHost() != "remote.example.test" {
				return nil, status.Error(codes.FailedPrecondition, "wrong remote target")
			}
			active[target] = identity
			payload = map[string]any{"account_id": req.AccountId}
		default:
			return nil, status.Error(codes.Unimplemented, "unexpected account operation")
		}
		raw, _ := json.Marshal(payload)
		return &pb.AgyAccountResponse{Result: &pb.NativeJson{Schema: "astrorder.agy-account-vault-result.v1", Utf8Json: raw}}, nil
	}}
	h, st := setupRestoreFixture(t, fake, &recordingResolver{results: map[string]string{"antigravity": "/usr/bin/agy"}})
	request := func(method, path string, payload map[string]any, code int) string {
		t.Helper()
		raw, _ := json.Marshal(payload)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(raw)))
		if w.Code != code {
			t.Fatalf("%s %s returned %d, expected %d: %s", method, path, w.Code, code, w.Body)
		}
		if w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "synthetic-import") || strings.Contains(w.Body.String(), identity) {
			t.Fatal("account API exposed credential data")
		}
		return w.Body.String()
	}
	request(http.MethodPost, base+"/import", map[string]any{"name": "导入账号", "credential": "{}"}, http.StatusUnprocessableEntity)
	if !strings.Contains(request(http.MethodGet, base, nil, http.StatusOK), `"items":[]`) {
		t.Fatal("invalid import created an account")
	}
	response := request(http.MethodPost, base+"/import", map[string]any{"name": "导入账号", "credential": string(credential)}, http.StatusOK)
	var imported struct {
		ID string `json:"account_id"`
	}
	if json.Unmarshal([]byte(response), &imported) != nil || imported.ID == "" {
		t.Fatal("import did not return an account ID")
	}
	if _, err := st.SaveSSHConnection(t.Context(), map[string]any{"id": "remote", "host": "remote.example.test", "user": "fixture"}); err != nil {
		t.Fatal(err)
	}
	for id, kind := range map[string]string{"local": "antigravity", "remote": "antigravity-ssh"} {
		if _, err := st.UpsertAgent(t.Context(), map[string]any{"id": "agy-" + id, "kind": kind, "name": "AGY", "status": "ready", "connection_id": id}); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(request(http.MethodGet, base+"?environment_id=local", nil, http.StatusOK), `"current_account_id":"current"`) {
		t.Fatal("target's unsaved current login was not detected")
	}
	for _, target := range []string{"local", "remote"} {
		path := base + "?environment_id=" + target
		request(http.MethodPost, path, map[string]any{"action": "switch", "account_id": imported.ID}, http.StatusOK)
		if !strings.Contains(request(http.MethodGet, path, nil, http.StatusOK), `"current_account_id":"`+imported.ID+`"`) {
			t.Fatalf("%s did not report the imported account as current", target)
		}
	}
	if items := request(http.MethodGet, base, nil, http.StatusOK); strings.Count(items, imported.ID) != 1 {
		t.Fatal("cross-target application duplicated the saved account")
	}
}

func TestAgyQuotaQueryPausedByDefault(t *testing.T) {
	const base = "/api/v1/agy-accounts"
	var stateHits atomic.Int32
	var quotaHits atomic.Int32
	fake := &fakeDaemonService{agyAccount: func(req *pb.AgyAccountRequest) (*pb.AgyAccountResponse, error) {
		switch req.Action {
		case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_STATE:
			stateHits.Add(1)
			raw, _ := json.Marshal(map[string]any{"identity": "", "quota_supported": true})
			return &pb.AgyAccountResponse{Result: &pb.NativeJson{Schema: "astrorder.agy-account-vault-result.v1", Utf8Json: raw}}, nil
		case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_QUOTA:
			quotaHits.Add(1)
			return nil, status.Error(codes.Internal, "should never call upstream READ_QUOTA while paused")
		default:
			return nil, status.Error(codes.Unimplemented, "unexpected")
		}
	}}
	h, st := setupRestoreFixture(t, fake, &recordingResolver{results: map[string]string{"antigravity": "/usr/bin/agy"}})
	if _, err := st.UpsertAgent(t.Context(), map[string]any{"id": "agy-local", "kind": "antigravity", "name": "AGY", "status": "ready", "connection_id": "local"}); err != nil {
		t.Fatal(err)
	}

	wState := httptest.NewRecorder()
	h.ServeHTTP(wState, httptest.NewRequest(http.MethodGet, base+"?environment_id=local", nil))
	if wState.Code != http.StatusOK {
		t.Fatalf("READ_STATE failed: %d %s", wState.Code, wState.Body.String())
	}
	var stateBody map[string]any
	if err := json.Unmarshal(wState.Body.Bytes(), &stateBody); err != nil {
		t.Fatal(err)
	}
	if stateBody["quota_supported"] != false || stateBody["quota_paused"] != true {
		t.Fatalf("expected quota_supported=false and quota_paused=true, got %s", wState.Body.String())
	}

	wQuota := httptest.NewRecorder()
	h.ServeHTTP(wQuota, httptest.NewRequest(http.MethodGet, base+"?environment_id=local&account_id=current", nil))
	if wQuota.Code != http.StatusServiceUnavailable || !strings.Contains(wQuota.Body.String(), "agy_quota_paused") {
		t.Fatalf("expected HTTP 503 agy_quota_paused, got %d %s", wQuota.Code, wQuota.Body.String())
	}
	if quotaHits.Load() != 0 || stateHits.Load() != 1 {
		t.Fatalf("expected 0 upstream READ_QUOTA calls and 1 READ_STATE call, got quota=%d state=%d", quotaHits.Load(), stateHits.Load())
	}
}

func TestAgyReadStateCacheAndConcurrentQuotaSingleflight(t *testing.T) {
	t.Cleanup(connections.SetAgyQuotaQueryPausedForTest(false))
	const base = "/api/v1/agy-accounts"
	var stateHits atomic.Int32
	var quotaHits atomic.Int32
	quotaStarted := make(chan struct{}, 2)
	releaseQuota := make(chan struct{})
	fake := &fakeDaemonService{agyAccount: func(req *pb.AgyAccountRequest) (*pb.AgyAccountResponse, error) {
		switch req.Action {
		case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_STATE:
			stateHits.Add(1)
			raw, _ := json.Marshal(map[string]any{"identity": "", "quota_supported": true})
			return &pb.AgyAccountResponse{Result: &pb.NativeJson{Schema: "astrorder.agy-account-vault-result.v1", Utf8Json: raw}}, nil
		case pb.AgyAccountAction_AGY_ACCOUNT_ACTION_READ_QUOTA:
			quotaHits.Add(1)
			select {
			case quotaStarted <- struct{}{}:
			default:
			}
			<-releaseQuota
			raw, _ := json.Marshal(map[string]any{"as_of": "2026-11-23T06:00:00Z", "groups": []any{}})
			return &pb.AgyAccountResponse{Result: &pb.NativeJson{Schema: "astrorder.agy-account-vault-result.v1", Utf8Json: raw}}, nil
		default:
			return nil, status.Error(codes.Unimplemented, "unexpected")
		}
	}}
	h, st := setupRestoreFixture(t, fake, &recordingResolver{results: map[string]string{"antigravity": "/usr/bin/agy"}})
	if _, err := st.UpsertAgent(t.Context(), map[string]any{"id": "agy-local", "kind": "antigravity", "name": "AGY", "status": "ready", "connection_id": "local"}); err != nil {
		t.Fatal(err)
	}

	// Sequential READ_STATE calls hit the short-TTL cache.
	for range 3 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"?environment_id=local", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("READ_STATE failed: %d %s", w.Code, w.Body.String())
		}
	}
	if got := stateHits.Load(); got != 1 {
		t.Fatalf("expected 1 upstream READ_STATE call due to TTL cache, got %d", got)
	}

	// Concurrent READ_QUOTA calls coalesce into a single upstream ManageAgyAccount RPC.
	var wg sync.WaitGroup
	codesOut := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"?environment_id=local&account_id=current", nil))
			codesOut[idx] = w.Code
		}(i)
	}
	select {
	case <-quotaStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("quota call never started")
	}
	// Give the second goroutine a brief moment to join the in-flight call.
	time.Sleep(30 * time.Millisecond)
	close(releaseQuota)
	wg.Wait()
	for i, code := range codesOut {
		if code != http.StatusOK {
			t.Fatalf("concurrent quota caller %d got HTTP %d", i, code)
		}
	}
	if got := quotaHits.Load(); got != 1 {
		t.Fatalf("expected 1 upstream READ_QUOTA call due to singleflight coalescing, got %d", got)
	}

	// Subsequent READ_QUOTA call within the short TTL hits cache without spawning another probe.
	wCached := httptest.NewRecorder()
	h.ServeHTTP(wCached, httptest.NewRequest(http.MethodGet, base+"?environment_id=local&account_id=current", nil))
	if wCached.Code != http.StatusOK || quotaHits.Load() != 1 {
		t.Fatalf("expected cached READ_QUOTA hit: status=%d hits=%d", wCached.Code, quotaHits.Load())
	}

	// Explicit fresh=1 bypasses the quota TTL cache.
	wFresh := httptest.NewRecorder()
	h.ServeHTTP(wFresh, httptest.NewRequest(http.MethodGet, base+"?environment_id=local&account_id=current&fresh=1", nil))
	if wFresh.Code != http.StatusOK || quotaHits.Load() != 2 {
		t.Fatalf("expected fresh=1 to bypass quota cache: status=%d hits=%d", wFresh.Code, quotaHits.Load())
	}
}
