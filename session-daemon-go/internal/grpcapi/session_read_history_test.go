package grpcapi_test

import (
	"strings"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestHistoryLargeSingleItemDoesNotDuplicateWirePayload(t *testing.T) {
	f := startFixture(t)
	text := strings.Repeat("x", 9_000_000)
	a := &sessionReadFixture{replies: map[string]map[string]any{"session.history_page": {"items": []any{map[string]any{"id": "large", "content": text}}, "next_cursor": nil, "has_more": false}}}
	f.daemon.RegisterRuntime("hermes", a)
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	r, err := sessiondv1.NewRuntimeQueryClient(f.conn).GetHistoryPage(f.ctx, &sessiondv1.HistoryPageRequest{Session: &sessiondv1.SessionRef{SessionId: "read-owned"}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 1 || !strings.Contains(string(r.Items[0].Utf8Json), text) {
		t.Fatal("large item lost")
	}
}

func TestSessionReadHistoryPreservesCursorPresenceAndItems(t *testing.T) {
	f := startFixture(t)
	a := &sessionReadFixture{replies: map[string]map[string]any{"session.history_page": {"items": []any{map[string]any{"id": 9007199254740993, "role": "assistant", "content": "fixture"}}, "next_cursor": nil, "has_more": false}}}
	f.daemon.RegisterRuntime("hermes", a)
	legacyWSOperation(t, f.daemon, map[string]any{"action": "session.create", "agent_type": "hermes"})
	client := sessiondv1.NewRuntimeQueryClient(f.conn)
	for _, before := range []*sessiondv1.NullableString{nil, {State: &sessiondv1.NullableString_Null{Null: sessiondv1.NullValue_NULL_VALUE_NULL}}, {State: &sessiondv1.NullableString_Value{Value: ""}}} {
		r, err := client.GetHistoryPage(f.ctx, &sessiondv1.HistoryPageRequest{Session: &sessiondv1.SessionRef{SessionId: "read-owned"}, Limit: 20, Before: before})
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Items) != 1 || r.NextCursor == nil || r.NextCursor.GetNull() != sessiondv1.NullValue_NULL_VALUE_NULL || r.HasMore == nil || *r.HasMore || r.Native == nil {
			t.Fatalf("history projection changed: %+v", r)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, request := range a.calls {
		if request.Fields["limit"] != 20 {
			t.Fatalf("history size changed: %v", request.Fields)
		}
		value, exists := request.Fields["before"]
		if i == 0 && exists || i == 1 && (!exists || value != nil) || i == 2 && (!exists || value != "") {
			t.Fatalf("cursor presence changed: index=%d fields=%#v", i, request.Fields)
		}
	}
}
