package contracts_test

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"testing"
)

func TestGrokSessionListHasClosedTypedSelector(t *testing.T) {
	request := (&pb.NativeQueryRequest{}).ProtoReflect().Descriptor()
	branch := request.Fields().ByName("grok_session_list")
	if branch == nil {
		t.Fatal("Grok native session discovery has no typed selector")
	}
	if branch.ContainingOneof() == nil || branch.ContainingOneof().Name() != "query" {
		t.Fatal("Grok list must use closed query oneof")
	}
	for _, name := range []protoreflect.Name{"cwd", "cursor"} {
		field := branch.Message().Fields().ByName(name)
		if field == nil || field.Kind() != protoreflect.StringKind || !field.HasPresence() {
			t.Fatalf("%s must preserve optional native string presence", name)
		}
	}
}
