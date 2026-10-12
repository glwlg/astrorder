package grpcapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestSessionReadModelPreservesNativeNullAndExactFields(t *testing.T) {
	f := startFixture(t)
	adapter := &sessionReadFixture{replies: map[string]map[string]any{"session.model.read": {"provider": "", "model": nil, "effort": "high", "branch": "", "future": json.Number("9007199254740993123456789")}}}
	f.daemon.RegisterRuntime("hermes", adapter)
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	response, err := sessiondv1.NewRuntimeQueryClient(f.conn).ReadModel(f.ctx, &sessiondv1.ReadModelRequest{Session: &sessiondv1.SessionRef{SessionId: "read-owned"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider == nil || *response.Provider != "" || response.Model != nil || response.Effort == nil || *response.Effort != "high" || response.Branch == nil {
		t.Fatalf("model presence changed: %+v", response)
	}
	decoder := json.NewDecoder(strings.NewReader(string(response.Native.Utf8Json)))
	decoder.UseNumber()
	var native map[string]any
	if err = decoder.Decode(&native); err != nil || native["future"] != json.Number("9007199254740993123456789") {
		t.Fatalf("native metadata lost: %v %v", native, err)
	}
	if model, exists := native["model"]; !exists || model != nil {
		t.Fatal("explicit null removed")
	}
	authority, err := f.daemon.AuthorityStatus()
	if err != nil || authority.Sessions[0].Status != "waiting_approval" {
		t.Fatalf("idle read receipt changed activity: %+v %v", authority, err)
	}
}
