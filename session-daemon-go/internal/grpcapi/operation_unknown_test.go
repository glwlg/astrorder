package grpcapi_test

import (
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestOperationLookupRejectsUnknownIdentityFields(t *testing.T) {
	fixture := startFixture(t)
	client := sessiondv1.NewOperationQueryClient(fixture.conn)
	unknown := protowire.AppendString(protowire.AppendTag(nil, 100, protowire.BytesType), "future-scope")
	for _, field := range []string{"request", "key", "agent", "connection", "session"} {
		t.Run(field, func(t *testing.T) {
			request := &sessiondv1.GetOperationRequest{Key: operationKey("not-executed", "session.create")}
			request.Key.AgentTypeCompat = operationNullableValue("fixture")
			request.Key.ConnectionId = operationNullableValue("connection")
			request.Key.SessionId = operationNullableValue("session")
			switch field {
			case "request":
				request.ProtoReflect().SetUnknown(unknown)
			case "key":
				request.Key.ProtoReflect().SetUnknown(unknown)
			case "agent":
				request.Key.AgentTypeCompat.ProtoReflect().SetUnknown(unknown)
			case "connection":
				request.Key.ConnectionId.ProtoReflect().SetUnknown(unknown)
			case "session":
				request.Key.SessionId.ProtoReflect().SetUnknown(unknown)
			}
			receipt, err := client.GetOperation(fixture.ctx, request)
			if status.Code(err) != codes.InvalidArgument || receipt != nil {
				t.Fatalf("unknown %s identity was silently ignored: %+v, %v", field, receipt, err)
			}
		})
	}
}
