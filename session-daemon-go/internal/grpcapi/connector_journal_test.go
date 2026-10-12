package grpcapi

import (
	"context"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestConnectorIngressBindsJournalStreamWithoutNativeRuntime(t *testing.T) {
	d, addr, cleanup := setupTestConnectorServer(t, "fixture")
	defer cleanup()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(connectorAuthContext("fixture"), 3*time.Second)
	defer cancel()
	client := sessiondv1.NewConnectorIngressClient(conn)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Send(&sessiondv1.ConnectorIngressFrame{Frame: &sessiondv1.ConnectorIngressFrame_Hello{Hello: &sessiondv1.ConnectorHello{ProtocolVersion: 1, Agent: &sessiondv1.ConnectorAgent{Id: "external", Kind: "hermes", Name: "External", Status: "connected"}}}}); err != nil {
		t.Fatal(err)
	}
	if ack, err := stream.Recv(); err != nil || ack.GetRegistered() == nil {
		t.Fatalf("registration failed: %v", err)
	}
	id := protocol.ConnectorEventStreamID("external")
	agent := "external"
	bound, err := client.BindControlSession(ctx, &sessiondv1.BindControlSessionRequest{AgentId: agent, ControlSession: &sessiondv1.SessionRef{SessionId: id, AgentId: &agent}})
	if err != nil || !bound.Bound || bound.FlushedEvents != 1 {
		t.Fatalf("journal binding failed: %v %v", bound, err)
	}
	page, err := d.ReplayPage(id, 0, 1, 8)
	if err != nil || len(page.Frames) != 1 || page.Frames[0].Event != "connector.hello" {
		t.Fatalf("hello not durable: %v", err)
	}
	wrong := "other"
	if _, err = client.BindControlSession(ctx, &sessiondv1.BindControlSessionRequest{AgentId: agent, ControlSession: &sessiondv1.SessionRef{SessionId: protocol.ConnectorEventStreamID(wrong), AgentId: &agent}}); err == nil {
		t.Fatal("foreign connector stream accepted")
	}
}
