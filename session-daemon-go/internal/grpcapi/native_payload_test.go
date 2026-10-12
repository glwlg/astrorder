package grpcapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"astrorder.dev/session-daemon/internal/events"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestTypedEventsKeepAllNativeFieldsAndExactNumbers(t *testing.T) {
	for _, event := range []string{"runtime.owner", "runtime.ownership_lost", "pty.output", "extension.event"} {
		t.Run(event, func(t *testing.T) {
			payload := map[string]any{"owner_pid": json.Number("1234"), "runtime_id": "exact", "data": "片段", "chunk_seq": json.Number("9007199254740993"), "adopted": false, "previous_status": "running", "orphan_alive": true, "reason": "fixture", "unknown": map[string]any{"n": json.Number("18446744073709551617"), "literal": `\u2028`}}
			wire, err := (&service{}).eventEnvelope("epoch", events.Frame{SessionID: "exact-session", SeqID: 1, Event: event, Status: "idle", Payload: payload})
			if err != nil {
				t.Fatal(err)
			}
			field := wire.ProtoReflect().Descriptor().Fields().ByName("native_payload")
			if field == nil || !wire.ProtoReflect().Has(field) {
				t.Fatal("typed conversion discarded native payload fields")
			}
			native := wire.ProtoReflect().Get(field).Message()
			jsonField := native.Descriptor().Fields().ByName(protoreflect.Name("utf8_json"))
			var got map[string]any
			decoder := json.NewDecoder(bytes.NewReader(native.Get(jsonField).Bytes()))
			decoder.UseNumber()
			if err := decoder.Decode(&got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, payload) {
				t.Fatalf("native payload changed: %#v", got)
			}
		})
	}
}
