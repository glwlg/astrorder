package grpcapi_test

import (
	"context"
	"reflect"
	"sort"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDefaultServerRegistersAndAdvertisesOperationQuery(t *testing.T) {
	fixture := startFixture(t)
	registered := fixture.server.GetServiceInfo()
	if _, ok := registered["astrorder.sessiond.v1.OperationQuery"]; !ok {
		t.Fatal("default daemon server does not register OperationQuery")
	}
	capabilities, err := sessiondv1.NewDaemonControlClient(fixture.conn).GetCapabilities(fixture.ctx, &sessiondv1.GetCapabilitiesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	services := make([]string, 0, len(registered))
	for name := range registered {
		services = append(services, name)
	}
	sort.Strings(services)
	if !reflect.DeepEqual(capabilities.Services, services) || !capabilities.OperationLookup {
		t.Fatalf("capabilities do not describe registered services: %+v, %v", capabilities, services)
	}
	client := sessiondv1.NewOperationQueryClient(fixture.conn)
	request := &sessiondv1.GetOperationRequest{Key: &sessiondv1.OperationKey{
		OperationId: "not-executed", LegacyAction: "session.create",
		Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
	}}
	receipt, err := client.GetOperation(fixture.ctx, request)
	if err != nil || receipt.State != sessiondv1.OperationState_OPERATION_STATE_MISSING || receipt.DaemonId != capabilities.DaemonId {
		t.Fatalf("default query is not reachable: %+v, %v", receipt, err)
	}
	if _, err := client.GetOperation(context.Background(), request); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("default operation query accepted missing authentication: %v", err)
	}
}
