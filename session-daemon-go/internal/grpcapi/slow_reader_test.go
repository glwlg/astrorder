package grpcapi

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func TestTCPSlowReaderReleasesBlockedSendWithoutClientCancellation(t *testing.T) {
	daemon, err := protocol.Open("fixture", filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer daemon.Close()
	for i := 0; i < 4; i++ {
		if _, err := daemon.AppendEvent(protocol.EventInput{SessionID: "slow", Event: "message", Status: "idle", Payload: map[string]any{"text": strings.Repeat("x", 1<<20)}}); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	started := make(chan struct{})
	var once sync.Once
	server, err := NewServer(Config{Credential: "fixture-grpc"}, daemon, WithSendTimeout(30*time.Millisecond), WithSendHook(func(kind string) {
		if kind == "replay" {
			once.Do(func() { close(started) })
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	go server.Serve(listener)
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithStaticStreamWindowSize(65535), grpc.WithStaticConnWindowSize(65535), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxWireBytes)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithCancel(metadata.NewOutgoingContext(context.Background(), metadata.Pairs(AuthorizationMetadataKey, "Bearer fixture-grpc")))
	defer cancel()
	stream, err := sessiondv1.NewEventStreamClient(connection).Subscribe(ctx, &sessiondv1.SubscribeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream // Deliberately never receive: HTTP/2 stream flow control must stall.
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("replay never started")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		profile := make([]byte, 1<<20)
		n := runtime.Stack(profile, true)
		dump := string(profile[:n])
		if !strings.Contains(dump, "grpcapi.(*service).Subscribe") && !strings.Contains(dump, "grpcapi.(*service).send.func") && strings.Contains(dump, "http2Client") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow TCP reader kept subscription or sender alive beyond server send deadline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// An independent control RPC remains usable on the same authenticated connection.
	controlCtx, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if _, err := sessiondv1.NewDaemonControlClient(connection).GetStatus(controlCtx, &sessiondv1.GetStatusRequest{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-daemon.ShutdownRequested():
		t.Fatal("slow reader stopped daemon")
	default:
	}
	// Publication still commits after the subscription ended.
	if _, err := daemon.AppendEvent(protocol.EventInput{SessionID: "slow", Event: "after", Status: "idle", Payload: map[string]any{"n": json.Number("9007199254740993")}}); err != nil {
		t.Fatal(err)
	}
}
