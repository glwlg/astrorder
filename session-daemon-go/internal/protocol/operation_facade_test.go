package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	core "astrorder.dev/session-daemon/internal/runtime"
	"github.com/coder/websocket"
)

type operationFacadeRuntime struct {
	calls atomic.Int32
}

func (runtime *operationFacadeRuntime) Create(_ context.Context, request core.Request) (core.Result, error) {
	call := runtime.calls.Add(1)
	if request.Fields["operation_id"] == "uncertain" {
		return core.Result{}, fmt.Errorf("native outcome unavailable")
	}
	return core.Result{
		SessionID: fmt.Sprintf("native-%d", call),
		Status:    "idle",
		Payload: map[string]any{
			"native_unknown": json.Number("9007199254740993123456789"),
		},
	}, nil
}

func legacyOperationCall(t *testing.T, daemon *Daemon, request map[string]any) []byte {
	t.Helper()
	server := httptest.NewServer(daemon.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	socket, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.CloseNow()
	write := func(value map[string]any) {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := socket.Write(ctx, websocket.MessageText, raw); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	read := func() []byte {
		_, raw, readErr := socket.Read(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return raw
	}
	write(map[string]any{"action": "daemon.handshake", "secret": "legacy-secret"})
	if raw := read(); !bytes.Contains(raw, []byte(`"daemon.handshake.result"`)) {
		t.Fatalf("handshake = %s", raw)
	}
	write(request)
	return read()
}

func stringPointer(value string) *string { return &value }

func TestReadStoredOperationUsesExactLegacyScopeAndNeverDispatches(t *testing.T) {
	daemon, err := Open("legacy-secret", filepath.Join(t.TempDir(), "operation.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	runtime := &operationFacadeRuntime{}
	daemon.RegisterRuntime("fixture", runtime)

	request := map[string]any{
		"action":        "session.create",
		"agent_type":    "fixture",
		"connection_id": "connection-a",
		"session_id":    "scope-session-a",
		"operation_id":  "completed",
		"request_id":    "legacy-request",
	}
	legacyResponse := legacyOperationCall(t, daemon, request)
	if !bytes.Contains(legacyResponse, []byte(`9007199254740993123456789`)) {
		t.Fatalf("legacy response lost native integer: %s", legacyResponse)
	}

	lookup := OperationLookup{
		Action:       "session.create",
		OperationID:  "completed",
		AgentType:    stringPointer("fixture"),
		ConnectionID: stringPointer("connection-a"),
		SessionID:    stringPointer("scope-session-a"),
	}
	stored, err := daemon.ReadStoredOperation(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DaemonID != daemon.DaemonID() || stored.State != StoredOperationStateCompleted {
		t.Fatalf("stored operation = %+v", stored)
	}
	if !bytes.Equal(stored.LegacyResponseJSON, legacyResponse) {
		t.Fatalf("stored bytes changed\n got: %s\nwant: %s", stored.LegacyResponseJSON, legacyResponse)
	}
	stored.LegacyResponseJSON[0] = '!'
	again, err := daemon.ReadStoredOperation(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.LegacyResponseJSON, legacyResponse) {
		t.Fatalf("caller mutated stored receipt: %s", again.LegacyResponseJSON)
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("read dispatched runtime: calls=%d", runtime.calls.Load())
	}

	collisions := []OperationLookup{
		{Action: "session.send", OperationID: "completed", AgentType: stringPointer("fixture"), ConnectionID: stringPointer("connection-a"), SessionID: stringPointer("scope-session-a")},
		{Action: "session.create", OperationID: "completed", ConnectionID: stringPointer("connection-a"), SessionID: stringPointer("scope-session-a")},
		{Action: "session.create", OperationID: "completed", AgentType: stringPointer("fixture"), SessionID: stringPointer("scope-session-a")},
		{Action: "session.create", OperationID: "completed", AgentType: stringPointer("fixture"), ConnectionID: stringPointer("connection-a")},
		{Action: "session.create", OperationID: "completed", AgentType: stringPointer("fixture"), ConnectionID: stringPointer(""), SessionID: stringPointer("scope-session-a")},
	}
	for _, collision := range collisions {
		result, readErr := daemon.ReadStoredOperation(collision)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if result.State != StoredOperationStateMissing || result.LegacyResponseJSON != nil {
			t.Fatalf("scope collision for %+v: %+v", collision, result)
		}
	}
	future, err := daemon.ReadStoredOperation(OperationLookup{Action: "future.mutating.action", OperationID: "not-created"})
	if err != nil || future.State != StoredOperationStateMissing {
		t.Fatalf("nonempty legacy action lookup = %+v, %v", future, err)
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("missing reads dispatched runtime: calls=%d", runtime.calls.Load())
	}
}

func TestReadStoredOperationReportsUncertainAndValidatesWithoutMutation(t *testing.T) {
	daemon, err := Open("legacy-secret", filepath.Join(t.TempDir(), "operation.db"))
	if err != nil {
		t.Fatal(err)
	}
	runtime := &operationFacadeRuntime{}
	daemon.RegisterRuntime("fixture", runtime)
	response := legacyOperationCall(t, daemon, map[string]any{
		"action":       "session.create",
		"agent_type":   "fixture",
		"operation_id": "uncertain",
		"request_id":   "legacy-uncertain",
	})
	if !bytes.Contains(response, []byte(`"operation_state":"uncertain"`)) {
		t.Fatalf("legacy uncertain response = %s", response)
	}
	stored, err := daemon.ReadStoredOperation(OperationLookup{
		Action:      "session.create",
		OperationID: "uncertain",
		AgentType:   stringPointer("fixture"),
	})
	if err != nil || stored.State != StoredOperationStateUncertain || stored.LegacyResponseJSON != nil {
		t.Fatalf("uncertain lookup = %+v, %v", stored, err)
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("uncertain lookup dispatched runtime: calls=%d", runtime.calls.Load())
	}

	for _, lookup := range []OperationLookup{
		{Action: "session.create"},
		{OperationID: "id"},
		{Action: "session.create", OperationID: strings.Repeat("x", 257)},
	} {
		if _, readErr := daemon.ReadStoredOperation(lookup); readErr == nil {
			t.Fatalf("invalid lookup accepted: %+v", lookup)
		}
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("invalid lookup dispatched runtime: calls=%d", runtime.calls.Load())
	}
	if err = daemon.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = daemon.ReadStoredOperation(OperationLookup{Action: "session.create", OperationID: "uncertain", AgentType: stringPointer("fixture")}); err == nil {
		t.Fatal("closed operation storage was treated as readable")
	}
}
