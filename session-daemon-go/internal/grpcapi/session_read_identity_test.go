package grpcapi_test

import (
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSessionReadRejectsWrongIdentityAndUnknownFieldsBeforeDispatch(t *testing.T) {
	f := startFixture(t)
	a := &sessionReadFixture{}
	f.daemon.RegisterRuntime("hermes", a)
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	for _, test := range []struct {
		session *sessiondv1.SessionRef
		code    codes.Code
	}{
		{&sessiondv1.SessionRef{SessionId: "read-owned", AgentId: ptr("wrong")}, codes.FailedPrecondition},
		{&sessiondv1.SessionRef{SessionId: "read-owned", ConnectionId: ptr("")}, codes.FailedPrecondition},
		{&sessiondv1.SessionRef{SessionId: "missing"}, codes.NotFound},
		{&sessiondv1.SessionRef{}, codes.InvalidArgument},
	} {
		if _, err := client.ListModels(f.ctx, &sessiondv1.ListModelsRequest{Session: test.session}); status.Code(err) != test.code {
			t.Fatalf("identity: %v wanted %v", err, test.code)
		}
	}
	unknown := &sessiondv1.SessionRef{SessionId: "read-owned"}
	unknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if _, err := client.ListModels(f.ctx, &sessiondv1.ListModelsRequest{Session: unknown}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown identity accepted: %v", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.calls) != 0 {
		t.Fatalf("invalid identity reached runtime: %v", a.calls)
	}
}

func TestSessionReadModelsPreservesEffortsFromNativeFamilies(t *testing.T) {
	for _, kind := range []string{"grok", "codex"} {
		t.Run(kind, func(t *testing.T) {
			f := startFixture(t)
			row := map[string]any{"provider": kind, "model": "literal", "efforts": []string{"low", "high"}}
			if kind == "codex" {
				delete(row, "efforts")
				row["supportedReasoningEfforts"] = []any{map[string]any{"reasoningEffort": "low"}, map[string]any{"reasoningEffort": "high"}}
			}
			a := &sessionReadFixture{replies: map[string]map[string]any{"session.models": {"items": []any{row}}}}
			f.daemon.RegisterRuntime(kind, a)
			legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": kind})
			r, err := sessiondv1.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &sessiondv1.ListModelsRequest{Session: &sessiondv1.SessionRef{SessionId: "read-owned"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Items) != 1 || len(r.Items[0].SupportedEfforts) != 2 || r.Items[0].SupportedEfforts[1] != "high" {
				t.Fatalf("lost efforts: %+v", r)
			}
		})
	}
}
