package grpcapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
)

func TestOperationNullFieldsNeverBecomeSyntheticTypedValues(t *testing.T) {
	database := filepath.Join(t.TempDir(), "daemon.db")
	daemon, err := protocol.Open("fixture", database)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	fixture := startOperationServer(t, daemon)
	client := sessiondv1.NewOperationQueryClient(fixture.conn)
	for _, name := range []string{"accepted", "termination_confirmed", "turn_id", "title", "model", "provider", "effort"} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"action": "session.create.result", "daemon_id": daemon.DaemonID(), "session_id": "native",
				"result": map[string]any{"status": "idle", name: nil},
			})
			if err != nil {
				t.Fatal(err)
			}
			insertStoredOperation(t, database, `["session.create",null,null,null]`, name, body)
			receipt, err := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: operationKey(name, "session.create")})
			if err != nil {
				t.Fatal(err)
			}
			if receipt.TypedResponse != nil {
				t.Fatalf("stored %s:null fabricated a typed value: %+v", name, receipt.TypedResponse)
			}
			if receipt.State != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || !bytes.Equal(receipt.LegacyResponseJson, body) {
				t.Fatalf("null response bytes/state changed: %+v", receipt)
			}
		})
	}
	for _, value := range []any{false, ""} {
		t.Run(fmt.Sprintf("explicit-%T", value), func(t *testing.T) {
			name := "accepted"
			if _, ok := value.(string); ok {
				name = "title"
			}
			body, err := json.Marshal(map[string]any{
				"action": "session.create.result", "daemon_id": daemon.DaemonID(), "session_id": "native",
				"result": map[string]any{"status": "idle", name: value},
			})
			if err != nil {
				t.Fatal(err)
			}
			id := "explicit-" + name
			insertStoredOperation(t, database, `["session.create",null,null,null]`, id, body)
			receipt, err := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: operationKey(id, "session.create")})
			if err != nil || receipt.TypedResponse == nil {
				t.Fatalf("valid explicit value lost typed receipt: %+v, %v", receipt, err)
			}
			if name == "title" && (receipt.TypedResponse.GetCompleted().Title == nil || *receipt.TypedResponse.GetCompleted().Title != "") {
				t.Fatal("explicit empty string presence was lost")
			}
		})
	}
}
