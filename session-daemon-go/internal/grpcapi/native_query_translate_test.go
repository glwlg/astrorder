package grpcapi

import (
	"encoding/json"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNativeQueryTranslationPreservesPresenceAndExactIntegers(t *testing.T) {
	zero, no, empty := uint32(0), false, ""
	request := &sessiondv1.NativeQueryRequest{
		Target:          &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX},
		TopLevelPayload: &sessiondv1.NativeJson{Utf8Json: []byte(`{"n":9007199254740993}`)},
		TopLevelHash:    &empty,
		Query: &sessiondv1.NativeQueryRequest_CodexModelList{CodexModelList: &sessiondv1.CodexModelListQuery{
			Limit: &zero, IncludeHidden: &no,
			Cursor:  &sessiondv1.NullableString{State: &sessiondv1.NullableString_Null{Null: sessiondv1.NullValue_NULL_VALUE_NULL}},
			Options: &sessiondv1.NativeJson{Utf8Json: []byte(`{"future":9007199254740993123456789}`)},
		}},
	}
	translated, err := translateNativeQuery(request)
	if err != nil {
		t.Fatal(err)
	}
	params := translated.Fields["request_params"].(map[string]any)
	if translated.AgentType != "codex" || translated.Action != "runtime.request" || params["limit"] != uint32(0) || params["includeHidden"] != false || params["future"] != json.Number("9007199254740993123456789") {
		t.Fatalf("lost native params: %#v", translated)
	}
	if value, exists := params["cursor"]; !exists || value != nil {
		t.Fatalf("explicit null cursor lost: %#v", params)
	}
	if value, exists := translated.Fields["hash"]; !exists || value != "" {
		t.Fatalf("present empty hash lost: %#v", translated.Fields)
	}
	if _, exists := translated.Fields["sha256"]; exists {
		t.Fatal("absent sha256 invented")
	}
	if translated.Fields["payload"].(map[string]any)["n"] != json.Number("9007199254740993") {
		t.Fatal("top-level payload integer rounded")
	}
	request.Query = &sessiondv1.NativeQueryRequest_CodexModelList{CodexModelList: &sessiondv1.CodexModelListQuery{}}
	translated, err = translateNativeQuery(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(translated.Fields["request_params"].(map[string]any)) != 0 {
		t.Fatalf("absent params invented: %#v", translated.Fields)
	}
}

func TestNativeQueryTranslationRejectsUnknownAndOptionsOwnedFields(t *testing.T) {
	cases := []struct {
		name  string
		query *sessiondv1.NativeQueryRequest
	}{
		{"wrong-family", &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES}, Query: &sessiondv1.NativeQueryRequest_CodexInitialize{CodexInitialize: &sessiondv1.CodexInitializeQuery{}}}},
		{"option-owned-absent", &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX}, Query: &sessiondv1.NativeQueryRequest_CodexThreadRead{CodexThreadRead: &sessiondv1.CodexThreadReadQuery{ThreadId: "thread", Options: &sessiondv1.NativeJson{Utf8Json: []byte(`{"includeTurns":true}`)}}}}},
		{"trailing-options", &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX}, Query: &sessiondv1.NativeQueryRequest_CodexInitialize{CodexInitialize: &sessiondv1.CodexInitializeQuery{Options: &sessiondv1.NativeJson{Utf8Json: []byte(`{} {}`)}}}}},
		{"null-options", &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX}, Query: &sessiondv1.NativeQueryRequest_CodexInitialize{CodexInitialize: &sessiondv1.CodexInitializeQuery{Options: &sessiondv1.NativeJson{Utf8Json: []byte(`null`)}}}}},
		{"unset-nullable-state", &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX}, Query: &sessiondv1.NativeQueryRequest_CodexModelList{CodexModelList: &sessiondv1.CodexModelListQuery{Cursor: &sessiondv1.NullableString{}}}}},
	}
	unknown := &sessiondv1.NativeQueryRequest{Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX}, Query: &sessiondv1.NativeQueryRequest_CodexInitialize{CodexInitialize: &sessiondv1.CodexInitializeQuery{}}}
	unknown.Target.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	cases = append(cases, struct {
		name  string
		query *sessiondv1.NativeQueryRequest
	}{"nested-unknown", unknown})
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := translateNativeQuery(test.query)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("accepted invalid request: %v", err)
			}
		})
	}
}
