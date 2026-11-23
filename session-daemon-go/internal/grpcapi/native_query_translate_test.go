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

func TestNativeQueryTranslationSupportsAntigravityInt64UsageSummaryAndSessionUsage(t *testing.T) {
	sinceMs := int64(1795000000000)
	untilMs := int64(1795086400000)
	connID := "ssh-1"

	localReq := &sessiondv1.NativeQueryRequest{
		Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY},
		Query: &sessiondv1.NativeQueryRequest_AntigravityUsageSummary{
			AntigravityUsageSummary: &sessiondv1.AntigravityUsageSummaryQuery{
				SinceMs: &sinceMs,
				UntilMs: &untilMs,
			},
		},
	}
	translated, err := translateNativeQuery(localReq)
	if err != nil {
		t.Fatalf("local AntigravityUsageSummary failed: %v", err)
	}
	params := translated.Fields["request_params"].(map[string]any)
	if translated.AgentType != "antigravity" || translated.Fields["method"] != "usage.summary" {
		t.Fatalf("unexpected route metadata: %#v", translated)
	}
	if params["since_ms"] != sinceMs || params["until_ms"] != untilMs {
		t.Fatalf("expected exact int64 since_ms/until_ms, got %#v", params)
	}

	sshReq := &sessiondv1.NativeQueryRequest{
		Target: &sessiondv1.RuntimeTarget{
			Runtime:      sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH,
			ConnectionId: &connID,
			Ssh:          &sessiondv1.SshConnectConfig{Host: "synthetic.invalid", Port: 22},
		},
		Query: &sessiondv1.NativeQueryRequest_AntigravityUsageSummary{
			AntigravityUsageSummary: &sessiondv1.AntigravityUsageSummaryQuery{
				SinceMs: &sinceMs,
			},
		},
	}
	translatedSSH, err := translateNativeQuery(sshReq)
	if err != nil {
		t.Fatalf("ssh AntigravityUsageSummary with since_ms failed: %v", err)
	}
	sshParams := translatedSSH.Fields["request_params"].(map[string]any)
	if translatedSSH.AgentType != "antigravity-ssh" || sshParams["since_ms"] != sinceMs {
		t.Fatalf("unexpected ssh usage summary translation: %#v", translatedSSH)
	}
	if _, hasUntil := sshParams["until_ms"]; hasUntil {
		t.Fatalf("absent until_ms invented: %#v", sshParams)
	}

	emptyReq := &sessiondv1.NativeQueryRequest{
		Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY},
		Query: &sessiondv1.NativeQueryRequest_AntigravityUsageSummary{
			AntigravityUsageSummary: &sessiondv1.AntigravityUsageSummaryQuery{},
		},
	}
	translatedEmpty, err := translateNativeQuery(emptyReq)
	if err != nil {
		t.Fatalf("empty AntigravityUsageSummary failed: %v", err)
	}
	if len(translatedEmpty.Fields["request_params"].(map[string]any)) != 0 {
		t.Fatalf("expected empty request_params for all-time usage summary, got %#v", translatedEmpty.Fields)
	}

	readonly := true
	sessionReq := &sessiondv1.NativeQueryRequest{
		Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY},
		Query: &sessiondv1.NativeQueryRequest_AntigravitySessionUsage{
			AntigravitySessionUsage: &sessiondv1.AntigravitySessionUsageQuery{
				ConversationId: "52794208-5f2e-4120-a6b1-99e5ee29c7f1",
				Readonly:       &readonly,
			},
		},
	}
	translatedSess, err := translateNativeQuery(sessionReq)
	if err != nil {
		t.Fatalf("AntigravitySessionUsage failed: %v", err)
	}
	sessParams := translatedSess.Fields["request_params"].(map[string]any)
	if sessParams["conversation_id"] != "52794208-5f2e-4120-a6b1-99e5ee29c7f1" || sessParams["readonly"] != true {
		t.Fatalf("lost session usage params: %#v", sessParams)
	}

	badSessReq := &sessiondv1.NativeQueryRequest{
		Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY},
		Query: &sessiondv1.NativeQueryRequest_AntigravitySessionUsage{
			AntigravitySessionUsage: &sessiondv1.AntigravitySessionUsageQuery{},
		},
	}
	if _, err := translateNativeQuery(badSessReq); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty conversation_id, got %v", err)
	}

	memberConvID := "62794208-5f2e-4120-a6b1-99e5ee29c7f2"
	memberLimit := int32(25)
	swarmReq := &sessiondv1.NativeQueryRequest{
		Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY},
		Query: &sessiondv1.NativeQueryRequest_AntigravitySessionSwarm{
			AntigravitySessionSwarm: &sessiondv1.AntigravitySessionSwarmQuery{
				ConversationId:       "52794208-5f2e-4120-a6b1-99e5ee29c7f1",
				Readonly:             &readonly,
				MemberConversationId: &memberConvID,
				MemberLimit:          &memberLimit,
			},
		},
	}
	translatedSwarm, err := translateNativeQuery(swarmReq)
	if err != nil {
		t.Fatalf("AntigravitySessionSwarm failed: %v", err)
	}
	swarmParams := translatedSwarm.Fields["request_params"].(map[string]any)
	if translatedSwarm.Fields["method"] != "session.swarm" ||
		swarmParams["conversation_id"] != "52794208-5f2e-4120-a6b1-99e5ee29c7f1" ||
		swarmParams["readonly"] != true ||
		swarmParams["member_conversation_id"] != memberConvID ||
		swarmParams["member_limit"] != int32(25) {
		t.Fatalf("lost session swarm params: %#v", swarmParams)
	}

	badSwarmReq := &sessiondv1.NativeQueryRequest{
		Target: &sessiondv1.RuntimeTarget{Runtime: sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY},
		Query: &sessiondv1.NativeQueryRequest_AntigravitySessionSwarm{
			AntigravitySessionSwarm: &sessiondv1.AntigravitySessionSwarmQuery{},
		},
	}
	if _, err := translateNativeQuery(badSwarmReq); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty swarm conversation_id, got %v", err)
	}
}
