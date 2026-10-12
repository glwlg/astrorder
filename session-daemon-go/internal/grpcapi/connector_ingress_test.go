package grpcapi

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type dummyControlAdapter struct {
	core.Adapter
}

func (d *dummyControlAdapter) Spawn(ctx context.Context, req core.Request) (core.Result, error) {
	return core.Result{SessionID: req.SessionID, Status: "idle"}, nil
}

func (d *dummyControlAdapter) Command(ctx context.Context, req core.Request) (core.Result, error) {
	return core.Result{SessionID: req.SessionID, Status: "idle"}, nil
}

func setupTestConnectorServer(t *testing.T, secret string) (*protocol.Daemon, string, func()) {
	t.Helper()
	d := protocol.New("test-secret")
	d.RegisterRuntime("test-control", &dummyControlAdapter{})

	srv, err := NewServer(Config{Credential: secret}, d)
	if err != nil {
		t.Fatalf("failed to create grpc server: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	go srv.Serve(lis)

	teardown := func() {
		srv.Stop()
		lis.Close()
		d.Close()
	}

	return d, lis.Addr().String(), teardown
}

func connectorAuthContext(secret string) context.Context {
	md := metadata.Pairs(AuthorizationMetadataKey, "Bearer "+secret)
	return metadata.NewOutgoingContext(context.Background(), md)
}

func TestConnectorIngressAuthRejection(t *testing.T) {
	_, addr, cleanup := setupTestConnectorServer(t, "valid-token")
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	client := sessiondv1.NewConnectorIngressClient(conn)

	// Test Unauthenticated call to Connect
	stream, err := client.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect failed: %v", err)
	}
	_ = stream.Send(&sessiondv1.ConnectorIngressFrame{
		Frame: &sessiondv1.ConnectorIngressFrame_Hello{
			Hello: &sessiondv1.ConnectorHello{
				ProtocolVersion: 1,
				Agent:           &sessiondv1.ConnectorAgent{Id: "a1", Kind: "test"},
			},
		},
	})
	_, err = stream.Recv()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

func TestConnectorIngressRegistrationDuplicateAndIsolation(t *testing.T) {
	d1, addr1, cleanup1 := setupTestConnectorServer(t, "token-1")
	defer cleanup1()
	d2, addr2, cleanup2 := setupTestConnectorServer(t, "token-2")
	defer cleanup2()

	// Connect to d1
	conn1, err := grpc.Dial(addr1, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial d1: %v", err)
	}
	defer conn1.Close()
	client1 := sessiondv1.NewConnectorIngressClient(conn1)

	ctx1 := connectorAuthContext("token-1")
	s1, err := client1.Connect(ctx1)
	if err != nil {
		t.Fatalf("connect d1: %v", err)
	}

	helloA := &sessiondv1.ConnectorIngressFrame{
		Frame: &sessiondv1.ConnectorIngressFrame_Hello{
			Hello: &sessiondv1.ConnectorHello{
				ProtocolVersion: 1,
				Agent:           &sessiondv1.ConnectorAgent{Id: "agent-dup", Kind: "test"},
			},
		},
	}
	if err := s1.Send(helloA); err != nil {
		t.Fatalf("s1 send: %v", err)
	}
	ack, err := s1.Recv()
	if err != nil || ack.State != sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REGISTERED {
		t.Fatalf("expected registered ack on s1: ack=%v, err=%v", ack, err)
	}

	// Try duplicate registration of agent-dup on same daemon (d1)
	s1Dup, err := client1.Connect(ctx1)
	if err != nil {
		t.Fatalf("connect d1 dup: %v", err)
	}
	if err := s1Dup.Send(helloA); err != nil {
		t.Fatalf("s1Dup send: %v", err)
	}
	_, err = s1Dup.Recv()
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists on duplicate registration, got %v", err)
	}

	// Connect to d2 with the SAME agent ID (verify two daemons don't share global maps)
	conn2, err := grpc.Dial(addr2, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial d2: %v", err)
	}
	defer conn2.Close()
	client2 := sessiondv1.NewConnectorIngressClient(conn2)
	ctx2 := connectorAuthContext("token-2")
	s2, err := client2.Connect(ctx2)
	if err != nil {
		t.Fatalf("connect d2: %v", err)
	}
	if err := s2.Send(helloA); err != nil {
		t.Fatalf("s2 send: %v", err)
	}
	ack2, err := s2.Recv()
	if err != nil || ack2.State != sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REGISTERED {
		t.Fatalf("expected registered on independent daemon d2: ack=%v, err=%v", ack2, err)
	}

	_ = d1
	_ = d2
}

func TestConnectorIngressBigIntAndBindReplay(t *testing.T) {
	d, addr, cleanup := setupTestConnectorServer(t, "token")
	defer cleanup()

	// Spawn control session
	ctrlSid := "ctrl-session-1"
	spawnRes := d.ExecuteSessionControl(context.Background(), map[string]any{
		"action":     "session.spawn",
		"agent_type": "test-control",
		"session_id": ctrlSid,
	})
	if spawnRes["action"] == "error" {
		t.Fatalf("failed to spawn control session: %v", spawnRes)
	}

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := sessiondv1.NewConnectorIngressClient(conn)
	ctx := connectorAuthContext("token")

	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Register hello before bind
	agentID := "agent-bigint"
	if err := stream.Send(&sessiondv1.ConnectorIngressFrame{
		Frame: &sessiondv1.ConnectorIngressFrame_Hello{
			Hello: &sessiondv1.ConnectorHello{
				ProtocolVersion: 1,
				Agent:           &sessiondv1.ConnectorAgent{Id: agentID, Kind: "worker"},
			},
		},
	}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	ack, err := stream.Recv()
	if err != nil || ack.State != sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REGISTERED {
		t.Fatalf("hello ack failed: %v", err)
	}

	// Send an event before bind with a big integer (exceeding JS float precision)
	bigNumStr := `{"big_seq":9007199254740993}`
	if err := stream.Send(&sessiondv1.ConnectorIngressFrame{
		Frame: &sessiondv1.ConnectorIngressFrame_Event{
			Event: &sessiondv1.ConnectorEvent{
				EventId:   "ev-1",
				AgentId:   agentID,
				EventType: "custom.notify",
				Data: &sessiondv1.NativeJson{
					Schema:   "test.v1",
					Utf8Json: []byte(bigNumStr),
				},
			},
		},
	}); err != nil {
		t.Fatalf("send event: %v", err)
	}

	// Event before bind has no session_id -> buffered for control plane, not immediately committed
	// Now bind control session
	bindResp, err := client.BindControlSession(ctx, &sessiondv1.BindControlSessionRequest{
		AgentId: agentID,
		ControlSession: &sessiondv1.SessionRef{
			SessionId: ctrlSid,
		},
	})
	if err != nil {
		t.Fatalf("bind failed: %v", err)
	}
	if !bindResp.Bound || bindResp.FlushedEvents < 1 {
		t.Fatalf("expected flushed events >= 1, got %v", bindResp)
	}

	// Verify journal replay for ctrlSid contains the hello and the big int event (through=0 means through max)
	page, err := d.ReplayPage(ctrlSid, 0, 0, 10)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	foundBigInt := false
	for _, fr := range page.Frames {
		if fr.Event == "connector.event" {
			if data, ok := fr.Payload["data"].(map[string]any); ok {
				if num, ok := data["big_seq"].(json.Number); ok {
					if num.String() == "9007199254740993" {
						foundBigInt = true
					}
				}
			}
		}
	}
	if !foundBigInt {
		t.Fatalf("big integer fidelity was lost; frame payload: %#v", page.Frames)
	}
}

func TestConnectorIngressMalformedJsonAndIdentityMismatch(t *testing.T) {
	_, addr, cleanup := setupTestConnectorServer(t, "token")
	defer cleanup()

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	client := sessiondv1.NewConnectorIngressClient(conn)
	ctx := connectorAuthContext("token")

	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	agentID := "agent-guard"
	if err := stream.Send(&sessiondv1.ConnectorIngressFrame{
		Frame: &sessiondv1.ConnectorIngressFrame_Hello{
			Hello: &sessiondv1.ConnectorHello{
				ProtocolVersion: 1,
				Agent:           &sessiondv1.ConnectorAgent{Id: agentID, Kind: "worker"},
			},
		},
	}); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	_, _ = stream.Recv()

	// 1. Identity mismatch rejected
	if err := stream.Send(&sessiondv1.ConnectorIngressFrame{
		Frame: &sessiondv1.ConnectorIngressFrame_Event{
			Event: &sessiondv1.ConnectorEvent{
				EventId:   "ev-mismatch",
				AgentId:   "different-agent",
				EventType: "some.event",
			},
		},
	}); err != nil {
		t.Fatalf("send event: %v", err)
	}
	ack, err := stream.Recv()
	if err != nil || ack.State != sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REJECTED {
		t.Fatalf("expected REJECTED for identity mismatch, got ack=%v, err=%v", ack, err)
	}

	// 2. Malformed JSON rejected
	if err := stream.Send(&sessiondv1.ConnectorIngressFrame{
		Frame: &sessiondv1.ConnectorIngressFrame_Event{
			Event: &sessiondv1.ConnectorEvent{
				EventId:   "ev-malformed",
				AgentId:   agentID,
				EventType: "some.event",
				Data: &sessiondv1.NativeJson{
					Schema:   "test.v1",
					Utf8Json: []byte(`{not valid json}`),
				},
			},
		},
	}); err != nil {
		t.Fatalf("send malformed: %v", err)
	}
	ack, err = stream.Recv()
	if err != nil || ack.State != sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REJECTED {
		t.Fatalf("expected REJECTED for malformed JSON, got ack=%v, err=%v", ack, err)
	}
}
