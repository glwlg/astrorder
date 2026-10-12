package grpcapi_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sessionReadFixture struct {
	mu      sync.Mutex
	calls   []core.Request
	replies map[string]map[string]any
}

func (a *sessionReadFixture) Create(_ context.Context, r core.Request) (core.Result, error) {
	return core.Result{SessionID: "read-owned", Status: "waiting_approval", Payload: map[string]any{"agent_id": "agent-fixture"}}, nil
}
func (a *sessionReadFixture) Command(_ context.Context, r core.Request) (core.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, r)
	if payload, exists := a.replies[r.Action]; exists {
		return core.Result{SessionID: r.SessionID, Status: "idle", Payload: payload}, nil
	}
	return core.Result{SessionID: r.SessionID, Status: "waiting_approval", Payload: map[string]any{"items": []map[string]string{{"provider": "fixture", "model": "literal/model", "label": ""}}}}, nil
}

func TestSessionReadListModelsUsesOwnedRuntimeWithoutChangingState(t *testing.T) {
	f := startFixture(t)
	adapter := &sessionReadFixture{}
	f.daemon.RegisterRuntime("hermes", adapter)
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	request := &sessiondv1.ListModelsRequest{Session: &sessiondv1.SessionRef{SessionId: "read-owned", AgentId: ptr("agent-fixture")}}
	if _, err := client.ListModels(context.Background(), request); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("authentication: %v", err)
	}
	before, err := f.daemon.AuthorityStatus()
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.ListModels(f.ctx, request)
	if err != nil {
		t.Fatalf("typed model list: %v", err)
	}
	if len(response.Items) != 1 || response.Items[0].Provider != "fixture" || response.Items[0].Model != "literal/model" || response.Items[0].Label == nil || *response.Items[0].Label != "" {
		t.Fatalf("catalog changed: %+v", response)
	}
	var native map[string]any
	if err = json.Unmarshal(response.Items[0].Native.Utf8Json, &native); err != nil || native["model"] != "literal/model" {
		t.Fatalf("native row lost: %v %v", native, err)
	}
	after, err := f.daemon.AuthorityStatus()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Sessions) != 1 || after.Sessions[0].Status != "waiting_approval" || after.Sessions[0].MaxSeqID != before.Sessions[0].MaxSeqID || after.PendingOperations != before.PendingOperations {
		t.Fatalf("read changed durable state: before=%+v after=%+v", before, after)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if len(adapter.calls) != 1 || adapter.calls[0].Action != "session.models" || adapter.calls[0].SessionID != "read-owned" {
		t.Fatalf("wrong runtime read: %+v", adapter.calls)
	}
}
