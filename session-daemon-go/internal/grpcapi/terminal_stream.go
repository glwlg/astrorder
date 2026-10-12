package grpcapi

import (
	"encoding/base64"
	"errors"
	"io"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/events"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	defaultTerminalQueueCapacity = 256
)

type terminalStreamService struct {
	sessiondv1.UnimplementedTerminalStreamServer
	daemon  *protocol.Daemon
	options options
}

func RegisterTerminalStream(server *grpc.Server, daemon *protocol.Daemon) {
	registerTerminalStream(server, daemon, options{subscriptionQueueCapacity: defaultTerminalQueueCapacity, sendTimeout: defaultSendTimeout})
}

func registerTerminalStream(server *grpc.Server, daemon *protocol.Daemon, settings options) {
	sessiondv1.RegisterTerminalStreamServer(server, &terminalStreamService{daemon: daemon, options: settings})
}

func (s *terminalStreamService) Open(stream grpc.BidiStreamingServer[sessiondv1.TerminalClientFrame, sessiondv1.TerminalServerFrame]) error {
	ctx := stream.Context()
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	if err = validateControlRequest(first); err != nil {
		return err
	}
	if proto.Size(first) > MaxBusinessBytes {
		return status.Error(codes.ResourceExhausted, "terminal open exceeds business limit")
	}
	open := first.GetOpen()
	if open == nil || open.Target == nil || open.Target.Runtime != sessiondv1.RuntimeKind_RUNTIME_KIND_PTY || first.TerminalStreamId == "" || len(first.TerminalStreamId) > 256 || first.ReconnectAfterChunkSeq < 0 {
		return status.Error(codes.InvalidArgument, "terminal open identity is invalid")
	}
	if err = validateSessionRef(open.Session); err != nil {
		return err
	}
	if open.Target.Ssh != nil || open.Target.ConnectionId != nil || open.Target.AgentId != nil {
		return status.Error(codes.InvalidArgument, "PTY target must be local")
	}
	if !terminalDimensions(open.Cols, open.Rows) {
		return status.Error(codes.InvalidArgument, "terminal dimensions are invalid")
	}
	id, streamID := open.Session.SessionId, first.TerminalStreamId
	owner, ownerErr := s.daemon.ResolveSessionOwner(id, controlIdentity(open.Session))
	if ownerErr == nil && owner != "pty" {
		return status.Error(codes.FailedPrecondition, "session belongs to another runtime")
	}
	if ownerErr != nil && status.Code(ownerError(ownerErr)) != codes.NotFound {
		return ownerError(ownerErr)
	}
	if ownerErr != nil && (open.Session.AgentId != nil || open.Session.ConnectionId != nil) {
		return status.Error(codes.InvalidArgument, "new PTY session has no Agent or connection binding")
	}
	plan, err := s.daemon.BeginReplay(nil, []protocol.ReplayCursor{{SessionID: id, After: first.ReconnectAfterChunkSeq}}, s.options.subscriptionQueueCapacity)
	if err != nil {
		return status.Error(codes.Unavailable, "terminal journal is unavailable")
	}
	defer s.daemon.EndReplay(plan)
	snapshot := plan.Sessions[id]
	after, through := first.ReconnectAfterChunkSeq, snapshot.MaxSeqID
	if after > through || snapshot.MinSeqID > 0 && after < snapshot.MinSeqID-1 {
		return status.Error(codes.OutOfRange, "terminal cursor requires resynchronization")
	}
	if ownerErr != nil {
		executable := ""
		if open.Executable != nil {
			executable = *open.Executable
		}
		if err = s.daemon.OpenTerminalSession(ctx, id, open.Cwd, executable, open.Args, open.Cols, open.Rows); err != nil {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
	}
	if err = s.send(stream, &sessiondv1.TerminalServerFrame{TerminalStreamId: streamID, Frame: &sessiondv1.TerminalServerFrame_Opened{Opened: &sessiondv1.TerminalOpened{Session: proto.Clone(open.Session).(*sessiondv1.SessionRef), ReplayThroughChunkSeq: through}}}); err != nil {
		return err
	}
	emit := func(frame events.Frame, replay bool) (bool, error) {
		output, finished, err := terminalEvent(streamID, frame, replay)
		if err != nil {
			return false, err
		}
		if output != nil {
			err = s.send(stream, output)
		}
		return finished, err
	}
	for after < through {
		page, err := s.daemon.ReplayPage(id, after, through, defaultReplayPageSize)
		if err != nil {
			return status.Error(codes.Unavailable, "terminal replay failed")
		}
		if page.MinSeq > 0 && after < page.MinSeq-1 {
			return status.Error(codes.OutOfRange, "terminal retention window changed")
		}
		for _, frame := range page.Frames {
			if frame.SeqID != after+1 {
				return status.Error(codes.DataLoss, "terminal replay is discontinuous")
			}
			done, err := emit(frame, true)
			if err != nil {
				return err
			}
			after = frame.SeqID
			if done {
				return nil
			}
		}
		if page.Next <= after && len(page.Frames) == 0 {
			return status.Error(codes.DataLoss, "terminal replay did not advance")
		}
	}
	type received struct {
		frame *sessiondv1.TerminalClientFrame
		err   error
	}
	incoming := make(chan received, 1)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		for {
			frame, err := stream.Recv()
			select {
			case incoming <- received{frame, err}:
			case <-finished:
				return
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		if !plan.Subscription.Healthy() {
			if err := s.send(stream, &sessiondv1.TerminalServerFrame{TerminalStreamId: streamID, Frame: &sessiondv1.TerminalServerFrame_Overflow{Overflow: &sessiondv1.TerminalOverflow{LastEmittedChunkSeq: after, QueueCapacity: uint32(s.options.subscriptionQueueCapacity)}}}); err != nil {
				return err
			}
			return status.Error(codes.ResourceExhausted, "terminal subscriber overflow; reconnect from durable cursor")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case frame := <-plan.Subscription.Frames():
			if frame.SessionID != id || frame.SeqID <= after {
				continue
			}
			if frame.SeqID != after+1 {
				return status.Error(codes.DataLoss, "terminal live stream is discontinuous")
			}
			done, err := emit(frame, false)
			if err != nil {
				return err
			}
			after = frame.SeqID
			if done {
				return nil
			}
		case incoming := <-incoming:
			if incoming.err != nil {
				if errors.Is(incoming.err, io.EOF) {
					return nil
				}
				return incoming.err
			}
			frame := incoming.frame
			if err = validateControlRequest(frame); err != nil {
				return err
			}
			if frame.TerminalStreamId != streamID || frame.ReconnectAfterChunkSeq != 0 {
				return status.Error(codes.InvalidArgument, "terminal stream identity changed")
			}
			if _, err = s.daemon.ResolveSessionOwner(id, controlIdentity(open.Session)); err != nil {
				return ownerError(err)
			}
			switch input := frame.Frame.(type) {
			case *sessiondv1.TerminalClientFrame_Input:
				if input.Input == nil || len(input.Input.Data) == 0 || len(input.Input.Data) > 65536 {
					return status.Error(codes.InvalidArgument, "terminal input size is invalid")
				}
				err = s.daemon.TerminalInput(ctx, id, input.Input.Data)
			case *sessiondv1.TerminalClientFrame_Resize:
				if input.Resize == nil || !terminalDimensions(input.Resize.Cols, input.Resize.Rows) {
					return status.Error(codes.InvalidArgument, "terminal dimensions are invalid")
				}
				err = s.daemon.TerminalResize(ctx, id, input.Resize.Cols, input.Resize.Rows)
			case *sessiondv1.TerminalClientFrame_Interrupt:
				err = s.daemon.TerminalInterrupt(ctx, id)
			case *sessiondv1.TerminalClientFrame_Close:
				if input.Close == nil {
					return status.Error(codes.InvalidArgument, "terminal close is invalid")
				}
				if err = s.daemon.TerminalClose(ctx, id, input.Close.Reason); err != nil {
					return status.Error(codes.FailedPrecondition, err.Error())
				}
				return s.send(stream, &sessiondv1.TerminalServerFrame{TerminalStreamId: streamID, Frame: &sessiondv1.TerminalServerFrame_Exit{Exit: &sessiondv1.TerminalExit{Cause: sessiondv1.TerminalExitCause_TERMINAL_EXIT_CAUSE_EXPLICIT_CLOSE, RemoteExitConfirmed: true}}})
			default:
				return status.Error(codes.InvalidArgument, "terminal is already opened")
			}
			if err != nil {
				return status.Error(codes.FailedPrecondition, err.Error())
			}
		}
	}
}

func terminalDimensions(cols, rows uint32) bool {
	return cols >= 20 && cols <= 400 && rows >= 10 && rows <= 200
}

func terminalEvent(streamID string, frame events.Frame, replay bool) (*sessiondv1.TerminalServerFrame, bool, error) {
	switch frame.Event {
	case "pty.output":
		var data []byte
		if raw, exists := frame.Payload["data_base64"]; exists {
			encoded, ok := raw.(string)
			if !ok {
				return nil, false, status.Error(codes.DataLoss, "terminal bytes are invalid")
			}
			var err error
			data, err = base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, false, status.Error(codes.DataLoss, "terminal bytes are invalid")
			}
		} else {
			legacy, ok := frame.Payload["data"].(string)
			if !ok {
				return nil, false, status.Error(codes.DataLoss, "terminal output is absent")
			}
			data = []byte(legacy)
		}
		if len(data) > int(sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_TERMINAL_CHUNK_BYTES) {
			return nil, false, status.Error(codes.ResourceExhausted, "terminal chunk exceeds contract limit")
		}
		return &sessiondv1.TerminalServerFrame{TerminalStreamId: streamID, Frame: &sessiondv1.TerminalServerFrame_Output{Output: &sessiondv1.TerminalOutput{Data: data, ChunkSeq: frame.SeqID, Replay: replay}}}, false, nil
	case "pty.closed":
		if frame.Status == "error" {
			return nil, false, status.Error(codes.FailedPrecondition, "native terminal cleanup or output failed")
		}
		return &sessiondv1.TerminalServerFrame{TerminalStreamId: streamID, Frame: &sessiondv1.TerminalServerFrame_Exit{Exit: &sessiondv1.TerminalExit{Cause: sessiondv1.TerminalExitCause_TERMINAL_EXIT_CAUSE_NATIVE_EXIT, RemoteExitConfirmed: true}}}, true, nil
	default:
		return nil, false, nil
	}
}

func (s *terminalStreamService) send(stream grpc.BidiStreamingServer[sessiondv1.TerminalClientFrame, sessiondv1.TerminalServerFrame], frame *sessiondv1.TerminalServerFrame) error {
	done := make(chan error, 1)
	go func() { done <- stream.Send(frame) }()
	timer := time.NewTimer(s.options.sendTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-timer.C:
		return status.Error(codes.DeadlineExceeded, "terminal send timed out")
	}
}
