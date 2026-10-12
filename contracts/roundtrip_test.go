package contracts_test

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/protobuf/proto"
)

func TestPresenceEmptyFalseZeroAndExplicitNullRoundTrip(t *testing.T) {
	empty := ""
	zero := uint32(0)
	falseValue := false
	input := &pb.PresenceFixture{
		OptionalText:  &empty,
		OptionalCount: &zero,
		Enabled:       &falseValue,
		NullableText:  &pb.NullableString{State: &pb.NullableString_Null{Null: pb.NullValue_NULL_VALUE_NULL}},
	}
	raw, err := proto.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output pb.PresenceFixture
	if err := proto.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.OptionalText == nil || *output.OptionalText != "" {
		t.Fatalf("present empty string was not preserved: %+v", output.OptionalText)
	}
	if output.OptionalCount == nil || *output.OptionalCount != 0 {
		t.Fatalf("present zero was not preserved: %+v", output.OptionalCount)
	}
	if output.Enabled == nil || *output.Enabled {
		t.Fatal("present false was not preserved")
	}
	if _, ok := output.NullableText.State.(*pb.NullableString_Null); !ok {
		t.Fatalf("explicit null was not preserved: %T", output.NullableText.State)
	}
	absent := &pb.PresenceFixture{}
	if absent.OptionalText != nil || absent.OptionalCount != nil || absent.Enabled != nil || absent.NullableText != nil {
		t.Fatalf("absent fields unexpectedly present: %+v", absent)
	}
}

func TestEventSeqAndTerminalCursorRoundTripAboveJavaScriptSafeInteger(t *testing.T) {
	const big = int64(9_007_199_254_740_993)
	input := &pb.EventEnvelope{SeqId: big, TimestampUnixMicros: big + 1}
	raw, err := proto.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var event pb.EventEnvelope
	if err := proto.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.SeqId != big || event.TimestampUnixMicros != big+1 {
		t.Fatalf("event integer precision lost: seq_id=%d timestamp_unix_micros=%d", event.SeqId, event.TimestampUnixMicros)
	}
	frame := &pb.TerminalClientFrame{TerminalStreamId: "t", ReconnectAfterChunkSeq: big}
	raw, err = proto.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	var terminal pb.TerminalClientFrame
	if err := proto.Unmarshal(raw, &terminal); err != nil {
		t.Fatal(err)
	}
	if terminal.ReconnectAfterChunkSeq != big {
		t.Fatalf("terminal cursor precision lost: %d", terminal.ReconnectAfterChunkSeq)
	}
}

func TestNativeJSONBytesPreserveLargeIdentifiersExactly(t *testing.T) {
	original := []byte(`{"request_id":9007199254740993,"unsigned":18446744073709551616,"negative":-9223372036854775809}`)
	hash := "native-hash"
	sha := "0123456789abcdef"
	input := &pb.NativeJson{Schema: "fixture.native-identifiers@1", Utf8Json: original, Hash: &hash, Sha256: &sha}
	raw, err := proto.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output pb.NativeJson
	if err := proto.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Utf8Json, original) {
		t.Fatalf("native bytes changed: %s", output.Utf8Json)
	}
	if output.Hash == nil || *output.Hash != hash || output.Sha256 == nil || *output.Sha256 != sha {
		t.Fatalf("native top-level hashes changed: hash=%v sha256=%v", output.Hash, output.Sha256)
	}
	decoder := json.NewDecoder(bytes.NewReader(output.Utf8Json))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"request_id": "9007199254740993",
		"unsigned":   "18446744073709551616",
		"negative":   "-9223372036854775809",
	} {
		got, ok := value[key].(json.Number)
		if !ok || got.String() != want {
			t.Fatalf("%s = %#v, want exact %s", key, value[key], want)
		}
	}
}

func TestUseNumberRejectsTrailingNativeJSON(t *testing.T) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(`{"id":9007199254740993} true`)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(&struct{}{}) == io.EOF {
		t.Fatal("trailing native JSON value was accepted as exactly one value")
	}
}

func TestOperationStatusAndErrorPreserveAbsentNullAndValue(t *testing.T) {
	cases := []*pb.OperationReceipt{
		{},
		{
			Status: &pb.NullableSessionStatus{State: &pb.NullableSessionStatus_Null{Null: pb.NullValue_NULL_VALUE_NULL}},
			Error:  &pb.NullableBusinessError{State: &pb.NullableBusinessError_Null{Null: pb.NullValue_NULL_VALUE_NULL}},
		},
		{
			Status: &pb.NullableSessionStatus{State: &pb.NullableSessionStatus_Value{Value: pb.SessionStatus_SESSION_STATUS_RUNNING}},
			Error: &pb.NullableBusinessError{State: &pb.NullableBusinessError_Value{Value: &pb.BusinessError{
				Kind:    pb.ErrorKind_ERROR_KIND_CONFLICT,
				Message: "conflict",
			}}},
		},
	}
	for _, input := range cases {
		raw, err := proto.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var output pb.OperationReceipt
		if err := proto.Unmarshal(raw, &output); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(input, &output) {
			t.Fatalf("operation status/error presence changed: %v != %v", input, &output)
		}
	}
}

func TestExactIdentifierVariantsRoundTrip(t *testing.T) {
	cases := []*pb.ExactIdentifier{
		{Value: &pb.ExactIdentifier_Text{Text: "codex:9007199254740993"}},
		{Value: &pb.ExactIdentifier_Signed{Signed: -9_007_199_254_740_993}},
		{Value: &pb.ExactIdentifier_Unsigned{Unsigned: 18_446_744_073_709_551_615}},
		{Value: &pb.ExactIdentifier_Decimal{Decimal: "18446744073709551616"}},
		{Value: &pb.ExactIdentifier_NativeJson{NativeJson: []byte(`{"id":9007199254740993}`)}},
	}
	for _, input := range cases {
		raw, err := proto.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var output pb.ExactIdentifier
		if err := proto.Unmarshal(raw, &output); err != nil {
			t.Fatal(err)
		}
		if !proto.Equal(input, &output) {
			t.Fatalf("identifier changed: %v != %v", input, &output)
		}
	}
}

func TestNativeQueryUsesBranchNotMethodString(t *testing.T) {
	query := &pb.NativeQueryRequest{
		Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX},
		Query: &pb.NativeQueryRequest_CodexThreadItemsList{
			CodexThreadItemsList: &pb.CodexThreadItemsListQuery{
				ThreadId: "thread-1",
				Limit:    50,
				Cursor:   &pb.NullableString{State: &pb.NullableString_Null{Null: pb.NullValue_NULL_VALUE_NULL}},
			},
		},
	}
	raw, err := proto.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	var output pb.NativeQueryRequest
	if err := proto.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	branch, ok := output.Query.(*pb.NativeQueryRequest_CodexThreadItemsList)
	if !ok || branch.CodexThreadItemsList.ThreadId != "thread-1" {
		t.Fatalf("typed query branch was not preserved: %T", output.Query)
	}
}

func TestOperationKeyPreservesAbsentNullAndEmptyScopeFields(t *testing.T) {
	empty := ""
	input := &pb.OperationKey{
		OperationId:      "op-scope",
		LegacyAction:     "session.send",
		AgentTypeCompat:  &pb.NullableString{State: &pb.NullableString_Null{Null: pb.NullValue_NULL_VALUE_NULL}},
		ConnectionId:     &pb.NullableString{State: &pb.NullableString_Value{Value: empty}},
		Canonicalization: pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
	}
	raw, err := proto.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var output pb.OperationKey
	if err := proto.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if output.AgentTypeCompat == nil || output.AgentTypeCompat.GetNull() != pb.NullValue_NULL_VALUE_NULL {
		t.Fatalf("explicit null scope lost: %#v", output.AgentTypeCompat)
	}
	if output.ConnectionId == nil || output.ConnectionId.GetValue() != "" {
		t.Fatalf("present-empty scope lost: %#v", output.ConnectionId)
	}
	if output.SessionId != nil {
		t.Fatalf("absent session scope became present: %#v", output.SessionId)
	}
}

func TestSSHConfigIsTypedAndContainsOnlyReferences(t *testing.T) {
	user := "operator"
	alias := "build-prod"
	identity := "C:/Users/example/.ssh/id_ed25519"
	workspace := "/srv/astrorder"
	hermesExecutable := "/opt/hermes/bin/hermes"
	codexExecutable := "/opt/codex/bin/codex"
	grokExecutable := "/opt/grok/bin/grok"
	timeout := uint32(10)
	batchMode := true
	config := &pb.SshConnectConfig{
		Host:             "remote.example",
		Port:             22,
		User:             &user,
		SshConfigAlias:   &alias,
		IdentityFile:     &identity,
		Workspace:        &workspace,
		HermesExecutable: &hermesExecutable,
		CodexExecutable:  &codexExecutable,
		GrokExecutable:   &grokExecutable,
		Options: &pb.SshOptions{
			BatchMode:          &batchMode,
			ConnectTimeoutSecs: &timeout,
		},
	}
	raw, err := proto.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var output pb.SshConnectConfig
	if err := proto.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(config, &output) {
		t.Fatalf("SSH config changed: %v != %v", config, &output)
	}
}

func TestContractConstants(t *testing.T) {
	if int32(pb.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES) != 16_000_000 {
		t.Fatalf("business limit = %d", pb.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES)
	}
	if int32(pb.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES) < 32*1024*1024 {
		t.Fatalf("wire limit = %d, want >= 32 MiB", pb.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES)
	}
	if pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 == 0 || pb.CanonicalizationVersion_CANONICALIZATION_VERSION_ASTRORDER_OPERATION_V1 == 0 || pb.EventStreamVersion_EVENT_STREAM_VERSION_V1 == 0 {
		t.Fatal("fixed contract versions must not be unspecified")
	}
	if pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 == pb.CanonicalizationVersion_CANONICALIZATION_VERSION_ASTRORDER_OPERATION_V1 {
		t.Fatal("new canonicalization must not change the identity of legacy ledger rows")
	}
}
