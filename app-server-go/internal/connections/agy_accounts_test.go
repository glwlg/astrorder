package connections_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
