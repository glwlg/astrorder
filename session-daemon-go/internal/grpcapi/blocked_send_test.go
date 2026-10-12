package grpcapi

import (
	"context"
	"errors"
	"testing"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type blockedEventStream struct {
	ctx      context.Context
	started  chan struct{}
	finished chan struct{}
}

func (stream *blockedEventStream) Context() context.Context { return stream.ctx }
func (*blockedEventStream) SetHeader(metadata.MD) error     { return nil }
func (*blockedEventStream) SendHeader(metadata.MD) error    { return nil }
func (*blockedEventStream) SetTrailer(metadata.MD)          {}
func (*blockedEventStream) RecvMsg(any) error               { return errors.New("not used") }
func (stream *blockedEventStream) SendMsg(any) error {
	close(stream.started)
	defer close(stream.finished)
	<-stream.ctx.Done()
	return stream.ctx.Err()
}
func (stream *blockedEventStream) Send(frame *sessiondv1.EventStreamFrame) error {
	return stream.SendMsg(frame)
}

func TestBlockedStreamSendReturnsAtServerDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &blockedEventStream{ctx: ctx, started: make(chan struct{}), finished: make(chan struct{})}
	service := &service{options: options{sendTimeout: 20 * time.Millisecond}}
	done := make(chan error, 1)
	go func() { done <- service.send(stream, "live", &sessiondv1.EventStreamFrame{}) }()
	select {
	case <-stream.started:
	case <-time.After(time.Second):
		t.Fatal("send never started")
	}
	select {
	case err := <-done:
		if status.Code(err) != codes.DeadlineExceeded {
			t.Fatalf("blocked send: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("server deadline did not release a blocked stream send")
	}
	// Returning the RPC handler cancels the underlying gRPC stream context.
	cancel()
	select {
	case <-stream.finished:
	case <-time.After(time.Second):
		t.Fatal("transport send did not finish after RPC cancellation")
	}
}
