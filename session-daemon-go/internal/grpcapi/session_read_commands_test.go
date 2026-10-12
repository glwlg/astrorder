package grpcapi_test

import (
	"encoding/json"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestSessionReadCommandsPreservesHintsAndNativeExtensions(t *testing.T) {
	f := startFixture(t)
	a := &sessionReadFixture{replies: map[string]map[string]any{"session.commands": {"items": []any{map[string]any{"name": "plan", "description": "", "input_hint": nil, "extension": json.Number("9007199254740993")}, map[string]any{"name": "inspect", "input_hint": ""}}}}}
	f.daemon.RegisterRuntime("hermes", a)
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	r, err := sessiondv1.NewRuntimeQueryClient(f.conn).ListCommands(f.ctx, &sessiondv1.ListCommandsRequest{Session: &sessiondv1.SessionRef{SessionId: "read-owned"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 2 || r.Items[0].Name != "plan" || r.Items[0].Description == nil || *r.Items[0].Description != "" || r.Items[0].InputHint != nil || r.Items[1].InputHint == nil || *r.Items[1].InputHint != "" {
		t.Fatalf("command presence lost: %+v", r)
	}
	if string(r.Items[0].Native.Utf8Json) != `{"description":"","extension":9007199254740993,"input_hint":null,"name":"plan"}` {
		t.Fatalf("native row lost: %s", r.Items[0].Native.Utf8Json)
	}
}
