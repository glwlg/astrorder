package contracts_test

import (
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestEventEnvelopePreservesNativePayloadAlongsideTypedEvent(t *testing.T) {
	message := (&sessiondv1.EventEnvelope{}).ProtoReflect().Descriptor()
	field := message.Fields().ByName("native_payload")
	if field == nil { t.Fatal("typed event envelopes have no field preserving complete native payload") }
	if field.Number() != 7 || field.Kind() != protoreflect.MessageKind || field.Message().Name() != "NativeJson" || !field.HasPresence() || field.ContainingOneof() != nil && !field.ContainingOneof().IsSynthetic() {
		t.Fatalf("native payload cannot coexist with typed event: %v", field)
	}
}
