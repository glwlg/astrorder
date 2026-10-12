package grpcapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/grpcapi"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"github.com/coder/websocket"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	_ "modernc.org/sqlite"
)

const operationGRPCSecret = "operation-grpc-secret"

type operationRuntime struct {
	calls atomic.Int32
}

func (runtime *operationRuntime) Create(_ context.Context, request core.Request) (core.Result, error) {
	call := runtime.calls.Add(1)
	if request.Fields["operation_id"] == "uncertain" {
		return core.Result{}, fmt.Errorf("native result unavailable")
	}
	return core.Result{
		SessionID: fmt.Sprintf("native-%d", call),
		Status:    "idle",
		Payload: map[string]any{
			"native_unknown": json.Number("9007199254740993123456789"),
		},
	}, nil
}

type operationServerFixture struct {
	daemon   *protocol.Daemon
	server   *grpc.Server
	listener net.Listener
	conn     *grpc.ClientConn
	ctx      context.Context
}

func startOperationServer(t *testing.T, daemon *protocol.Daemon) *operationServerFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := grpcapi.NewServer(grpcapi.Config{Credential: operationGRPCSecret}, daemon)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	if _, registered := server.GetServiceInfo()["astrorder.sessiond.v1.OperationQuery"]; !registered {
		t.Fatal("OperationQuery is absent from the default daemon server")
	}
	go func() { _ = server.Serve(listener) }()
	dialContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(
		dialContext,
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(grpcapi.MaxWireBytes),
			grpc.MaxCallSendMsgSize(grpcapi.MaxWireBytes),
		),
	)
	if err != nil {
		server.Stop()
		listener.Close()
		t.Fatal(err)
	}
	fixture := &operationServerFixture{
		daemon:   daemon,
		server:   server,
		listener: listener,
		conn:     conn,
		ctx: metadata.NewOutgoingContext(
			context.Background(),
			metadata.Pairs(grpcapi.AuthorizationMetadataKey, "Bearer "+operationGRPCSecret),
		),
	}
	t.Cleanup(func() {
		conn.Close()
		server.Stop()
		listener.Close()
	})
	return fixture
}

func operationNullableValue(value string) *sessiondv1.NullableString {
	return &sessiondv1.NullableString{State: &sessiondv1.NullableString_Value{Value: value}}
}

func operationNullableNull() *sessiondv1.NullableString {
	return &sessiondv1.NullableString{State: &sessiondv1.NullableString_Null{Null: sessiondv1.NullValue_NULL_VALUE_NULL}}
}

func operationKey(id, action string) *sessiondv1.OperationKey {
	return &sessiondv1.OperationKey{
		OperationId:      id,
		LegacyAction:     action,
		Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
	}
}

func legacyWSOperation(t *testing.T, daemon *protocol.Daemon, request map[string]any) []byte {
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
	call := func(value map[string]any) []byte {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := socket.Write(ctx, websocket.MessageText, raw); writeErr != nil {
			t.Fatal(writeErr)
		}
		_, raw, readErr := socket.Read(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		return raw
	}
	call(map[string]any{"action": "daemon.handshake", "secret": "legacy-ws-secret"})
	return call(request)
}

func insertStoredOperation(t *testing.T, database, scope, id string, response []byte) {
	t.Helper()
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO operations(scope,id,digest,response) VALUES(?,?,?,?)`, scope, id, "fixture-digest", response); err != nil {
		t.Fatal(err)
	}
}

func TestGetOperationReadsLegacyWSLedgerOverAuthenticatedTCPAndAfterReopen(t *testing.T) {
	database := filepath.Join(t.TempDir(), "daemon.db")
	daemon, err := protocol.Open("legacy-ws-secret", database)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &operationRuntime{}
	daemon.RegisterRuntime("fixture", runtime)
	legacyResponse := legacyWSOperation(t, daemon, map[string]any{
		"action":        "session.create",
		"agent_type":    "fixture",
		"connection_id": "connection-a",
		"session_id":    "scope-session-a",
		"operation_id":  "completed",
		"request_id":    "native-request-9007199254740993123456789",
	})
	if !bytes.Contains(legacyResponse, []byte(`9007199254740993123456789`)) {
		t.Fatalf("legacy response lost large integer: %s", legacyResponse)
	}
	fixture := startOperationServer(t, daemon)
	client := sessiondv1.NewOperationQueryClient(fixture.conn)
	key := operationKey("completed", "session.create")
	key.AgentTypeCompat = operationNullableValue("fixture")
	key.ConnectionId = operationNullableValue("connection-a")
	key.SessionId = operationNullableValue("scope-session-a")
	response, err := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	if response.DaemonId != daemon.DaemonID() || response.State != sessiondv1.OperationState_OPERATION_STATE_COMPLETED {
		t.Fatalf("operation response = %+v", response)
	}
	if !proto.Equal(response.Key, key) || response.Key == key {
		t.Fatalf("returned key is not an equal clone: got=%p want=%p %+v", response.Key, key, response.Key)
	}
	key.OperationId = "caller-mutated"
	if response.Key.OperationId != "completed" {
		t.Fatalf("response key aliases request: %+v", response.Key)
	}
	if response.RequestSha256 != nil {
		t.Fatalf("old reader does not expose request digest: %x", response.RequestSha256)
	}
	if !bytes.Equal(response.LegacyResponseJson, legacyResponse) {
		t.Fatalf("stored response changed\n got: %s\nwant: %s", response.LegacyResponseJson, legacyResponse)
	}
	if response.TypedResponse == nil || response.TypedResponse.DaemonId != daemon.DaemonID() || response.TypedResponse.OperationId != "completed" || response.TypedResponse.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED {
		t.Fatalf("typed response = %+v", response.TypedResponse)
	}
	if response.TypedResponse.Session == nil || response.TypedResponse.Session.SessionId != "native-1" || response.TypedResponse.Status != sessiondv1.SessionStatus_SESSION_STATUS_IDLE {
		t.Fatalf("typed response identity/status = %+v", response.TypedResponse)
	}
	completed := response.TypedResponse.GetCompleted()
	if completed == nil || completed.Native == nil || !bytes.Contains(completed.Native.Utf8Json, []byte(`9007199254740993123456789`)) {
		t.Fatalf("typed native response lost unknown bytes: %+v", response.TypedResponse)
	}
	if response.Error == nil || response.Error.GetNull() != sessiondv1.NullValue_NULL_VALUE_NULL || response.Status == nil || response.Status.GetValue() != sessiondv1.SessionStatus_SESSION_STATUS_IDLE {
		t.Fatalf("receipt error/status = %+v / %+v", response.Error, response.Status)
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("lookup dispatched runtime: calls=%d", runtime.calls.Load())
	}

	missing, err := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: operationKey("missing", "future.mutating.action")})
	if err != nil || missing.State != sessiondv1.OperationState_OPERATION_STATE_MISSING || missing.LegacyResponseJson != nil || missing.TypedResponse != nil {
		t.Fatalf("missing lookup = %+v, %v", missing, err)
	}

	unauthenticated, err := client.GetOperation(context.Background(), &sessiondv1.GetOperationRequest{Key: operationKey("completed", "session.create")})
	if status.Code(err) != codes.Unauthenticated || unauthenticated != nil {
		t.Fatalf("unauthenticated operation lookup = %+v, %v", unauthenticated, err)
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("authenticated/missing reads dispatched runtime: calls=%d", runtime.calls.Load())
	}

	fixture.conn.Close()
	fixture.server.Stop()
	fixture.listener.Close()
	if err = daemon.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := protocol.Open("legacy-ws-secret", database)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replacement := &operationRuntime{}
	reopened.RegisterRuntime("fixture", replacement)
	reopenedFixture := startOperationServer(t, reopened)
	reopenedKey := operationKey("completed", "session.create")
	reopenedKey.AgentTypeCompat = operationNullableValue("fixture")
	reopenedKey.ConnectionId = operationNullableValue("connection-a")
	reopenedKey.SessionId = operationNullableValue("scope-session-a")
	replayed, err := sessiondv1.NewOperationQueryClient(reopenedFixture.conn).GetOperation(reopenedFixture.ctx, &sessiondv1.GetOperationRequest{Key: reopenedKey})
	if err != nil || replayed.State != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || !bytes.Equal(replayed.LegacyResponseJson, legacyResponse) {
		t.Fatalf("reopened lookup = %+v, %v", replayed, err)
	}
	if replacement.calls.Load() != 0 {
		t.Fatalf("reopened lookup re-executed native work: calls=%d", replacement.calls.Load())
	}
}

func TestGetOperationPreservesUncertainAndExactScopeWithoutRedispatch(t *testing.T) {
	daemon, err := protocol.Open("legacy-ws-secret", filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	runtime := &operationRuntime{}
	daemon.RegisterRuntime("fixture", runtime)
	legacyWSOperation(t, daemon, map[string]any{
		"action":       "session.create",
		"agent_type":   "fixture",
		"operation_id": "uncertain",
		"request_id":   "legacy-uncertain",
	})
	fixture := startOperationServer(t, daemon)
	client := sessiondv1.NewOperationQueryClient(fixture.conn)

	key := operationKey("uncertain", "session.create")
	key.AgentTypeCompat = operationNullableValue("fixture")
	for i := 0; i < 2; i++ {
		response, readErr := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: key})
		if readErr != nil || response.State != sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN || response.LegacyResponseJson != nil || response.TypedResponse != nil {
			t.Fatalf("uncertain lookup %d = %+v, %v", i, response, readErr)
		}
		if response.Error == nil || response.Error.GetValue() == nil || response.Error.GetValue().Kind != sessiondv1.ErrorKind_ERROR_KIND_FAILED_PRECONDITION || response.Error.GetValue().Retryable {
			t.Fatalf("uncertain error = %+v", response.Error)
		}
		if response.Status == nil || response.Status.GetNull() != sessiondv1.NullValue_NULL_VALUE_NULL {
			t.Fatalf("uncertain status = %+v", response.Status)
		}
	}
	explicitNull := operationKey("uncertain", "session.create")
	explicitNull.AgentTypeCompat = operationNullableValue("fixture")
	explicitNull.ConnectionId = operationNullableNull()
	explicitNull.SessionId = operationNullableNull()
	nullResponse, err := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: explicitNull})
	if err != nil || nullResponse.State != sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN || !proto.Equal(nullResponse.Key, explicitNull) {
		t.Fatalf("explicit null legacy lookup = %+v, %v", nullResponse, err)
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("uncertain lookup retried native work: calls=%d", runtime.calls.Load())
	}

	collisions := []*sessiondv1.OperationKey{
		operationKey("uncertain", "session.send"),
		operationKey("uncertain", "session.create"),
		operationKey("uncertain", "session.create"),
		operationKey("uncertain", "session.create"),
		operationKey("uncertain", "session.create"),
	}
	collisions[0].AgentTypeCompat = operationNullableValue("fixture")
	collisions[1].AgentTypeCompat = operationNullableValue("other")
	collisions[2].AgentTypeCompat = operationNullableValue("fixture")
	collisions[2].ConnectionId = operationNullableValue("connection-a")
	collisions[3].AgentTypeCompat = operationNullableValue("fixture")
	collisions[3].SessionId = operationNullableValue("session-a")
	collisions[4].AgentTypeCompat = operationNullableValue("")
	for _, collision := range collisions {
		response, readErr := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: collision})
		if readErr != nil || response.State != sessiondv1.OperationState_OPERATION_STATE_MISSING {
			t.Fatalf("scope collision %+v = %+v, %v", collision, response, readErr)
		}
	}
	if runtime.calls.Load() != 1 {
		t.Fatalf("scope reads dispatched runtime: calls=%d", runtime.calls.Load())
	}
}

func TestGetOperationPreservesUntypedAndMalformedCompletedBytes(t *testing.T) {
	database := filepath.Join(t.TempDir(), "daemon.db")
	daemon, err := protocol.Open("legacy-ws-secret", database)
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	fixture := startOperationServer(t, daemon)
	client := sessiondv1.NewOperationQueryClient(fixture.conn)

	untyped := []byte(`{"action":"future.mutating.action.result","request_id":9007199254740993123456789,"daemon_id":"old","unknown":{"exact":9007199254740993123456789}}`)
	insertStoredOperation(t, database, `["future.mutating.action",null,null,null]`, "untyped", untyped)
	response, err := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: operationKey("untyped", "future.mutating.action")})
	if err != nil || response.State != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || response.TypedResponse != nil || !bytes.Equal(response.LegacyResponseJson, untyped) {
		t.Fatalf("valid untyped receipt = %+v, %v", response, err)
	}

	malformed := []byte(`{"unterminated"`)
	insertStoredOperation(t, database, `["session.create",null,null,null]`, "corrupt", malformed)
	response, err = client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: operationKey("corrupt", "session.create")})
	if err != nil || response.State != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || response.TypedResponse != nil || !bytes.Equal(response.LegacyResponseJson, malformed) {
		t.Fatalf("malformed completed response was lost: %+v, %v", response, err)
	}
	if response.Error.GetState() == nil || response.Status.GetState() == nil || response.Error.GetValue() != nil || response.Status.GetValue() != sessiondv1.SessionStatus_SESSION_STATUS_UNSPECIFIED {
		t.Fatalf("unprojectable receipt manufactured error or status: %+v", response)
	}
}

func TestGetOperationRejectsInvalidPresenceCanonicalizationAndUnavailableStorage(t *testing.T) {
	daemon, err := protocol.Open("legacy-ws-secret", filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := startOperationServer(t, daemon)
	client := sessiondv1.NewOperationQueryClient(fixture.conn)

	tooLong := operationKey(strings.Repeat("x", 257), "session.create")
	wrongCanonical := operationKey("id", "session.create")
	wrongCanonical.Canonicalization = sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_ASTRORDER_OPERATION_V1
	unknownCanonical := operationKey("id", "session.create")
	unknownCanonical.Canonicalization = sessiondv1.CanonicalizationVersion(99)
	unsetNullable := operationKey("id", "session.create")
	unsetNullable.AgentTypeCompat = &sessiondv1.NullableString{}
	invalidNull := operationKey("id", "session.create")
	invalidNull.ConnectionId = &sessiondv1.NullableString{State: &sessiondv1.NullableString_Null{Null: sessiondv1.NullValue_NULL_VALUE_UNSPECIFIED}}
	for name, request := range map[string]*sessiondv1.GetOperationRequest{
		"nil request":        nil,
		"missing key":        {},
		"empty operation id": {Key: operationKey("", "session.create")},
		"long operation id":  {Key: tooLong},
		"empty action":       {Key: operationKey("id", "")},
		"wrong canonical":    {Key: wrongCanonical},
		"unknown canonical":  {Key: unknownCanonical},
		"unset nullable":     {Key: unsetNullable},
		"invalid null":       {Key: invalidNull},
	} {
		t.Run(name, func(t *testing.T) {
			response, callErr := client.GetOperation(fixture.ctx, request)
			if status.Code(callErr) != codes.InvalidArgument || response != nil {
				t.Fatalf("invalid lookup = %+v, %v", response, callErr)
			}
		})
	}
	if err = daemon.Close(); err != nil {
		t.Fatal(err)
	}
	response, err := client.GetOperation(fixture.ctx, &sessiondv1.GetOperationRequest{Key: operationKey("missing", "session.create")})
	if status.Code(err) != codes.Unavailable || response != nil {
		t.Fatalf("closed storage lookup = %+v, %v", response, err)
	}
}
