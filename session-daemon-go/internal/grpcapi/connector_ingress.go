package grpcapi

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	defaultConnectorQueueCapacity = 256
)

type connectorIngressService struct {
	sessiondv1.UnimplementedConnectorIngressServer
	daemon *protocol.Daemon
}

func RegisterConnectorIngress(server *grpc.Server, daemon *protocol.Daemon) {
	sessiondv1.RegisterConnectorIngressServer(server, &connectorIngressService{daemon: daemon})
}

func (s *connectorIngressService) Connect(stream grpc.BidiStreamingServer[sessiondv1.ConnectorIngressFrame, sessiondv1.ConnectorIngressAck]) error {
	var agentID string
	var registeredRollback func()
	defer func() {
		if registeredRollback != nil {
			registeredRollback()
		}
	}()

	sendMu := sync.Mutex{}
	sendAck := func(ack *sessiondv1.ConnectorIngressAck) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		done := make(chan error, 1)
		go func() {
			done <- stream.Send(ack)
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			return status.Error(codes.DeadlineExceeded, "connector stream send timeout")
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}

	for {
		frame, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if frame == nil {
			continue
		}

		switch f := frame.Frame.(type) {
		case *sessiondv1.ConnectorIngressFrame_Hello:
			if err := validateControlRequest(f.Hello); err != nil {
				return err
			}
			if f.Hello == nil || f.Hello.Agent == nil {
				return status.Error(codes.InvalidArgument, "connector hello payload is invalid")
			}
			if proto.Size(f.Hello) > MaxBusinessBytes {
				return status.Error(codes.ResourceExhausted, "connector hello exceeds 16MB business payload limit")
			}
			if f.Hello.ProtocolVersion != 1 {
				return status.Error(codes.InvalidArgument, "unsupported connector protocol version")
			}
			reqAgentID := f.Hello.Agent.Id
			if reqAgentID == "" {
				return status.Error(codes.InvalidArgument, "invalid agent id")
			}
			if agentID != "" && agentID != reqAgentID {
				return status.Error(codes.InvalidArgument, "cannot re-register stream with different agent identity")
			}

			agentMap := map[string]any{
				"id":           reqAgentID,
				"kind":         f.Hello.Agent.Kind,
				"name":         f.Hello.Agent.Name,
				"status":       f.Hello.Agent.Status,
				"capabilities": f.Hello.Agent.Capabilities,
			}
			if f.Hello.Agent.SourceId != nil {
				agentMap["source_id"] = *f.Hello.Agent.SourceId
			}
			if f.Hello.Agent.RuntimeId != nil {
				agentMap["runtime_id"] = *f.Hello.Agent.RuntimeId
			}
			if f.Hello.Agent.ConnectionId != nil {
				agentMap["connection_id"] = *f.Hello.Agent.ConnectionId
			}
			if f.Hello.Agent.Native != nil && len(f.Hello.Agent.Native.Utf8Json) > 0 {
				nativeVal, err := decodeQueryJSON(f.Hello.Agent.Native)
				if err != nil {
					return status.Errorf(codes.InvalidArgument, "invalid native json in agent hello: %v", err)
				}
				// Merge defined native object fields, rejecting collisions
				if nativeObj, ok := nativeVal.(map[string]any); ok {
					for k, v := range nativeObj {
						if _, exists := agentMap[k]; exists {
							return status.Errorf(codes.InvalidArgument, "collision in native agent field %s", k)
						}
						agentMap[k] = v
					}
				} else {
					agentMap["native"] = nativeVal
				}
			}

			rb, err := s.daemon.RegisterConnectorWithRollback(reqAgentID, agentMap)
			if err != nil {
				if errors.Is(err, protocol.ErrConnectorAlreadyRegistered) {
					return status.Errorf(codes.AlreadyExists, "connector already registered: %v", err)
				}
				return status.Errorf(codes.FailedPrecondition, "registration failed: %v", err)
			}
			agentID = reqAgentID
			registeredRollback = rb

			if err := sendAck(&sessiondv1.ConnectorIngressAck{
				State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REGISTERED,
				Ack: &sessiondv1.ConnectorIngressAck_Registered{
					Registered: &sessiondv1.ConnectorRegistered{AgentId: agentID},
				},
			}); err != nil {
				return err
			}

		case *sessiondv1.ConnectorIngressFrame_Heartbeat:
			if agentID == "" {
				return status.Error(codes.FailedPrecondition, "must send hello before heartbeat")
			}
			if err := sendAck(&sessiondv1.ConnectorIngressAck{
				State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_HEARTBEAT,
				Ack: &sessiondv1.ConnectorIngressAck_Heartbeat{
					Heartbeat: &sessiondv1.ConnectorHeartbeatAck{
						ReceivedAtUnixMicros: time.Now().UnixMicro(),
					},
				},
			}); err != nil {
				return err
			}

		case *sessiondv1.ConnectorIngressFrame_Event:
			if agentID == "" {
				return status.Error(codes.FailedPrecondition, "must send hello before event")
			}
			ev := f.Event
			if err := validateControlRequest(ev); err != nil {
				return err
			}
			if ev == nil || ev.EventId == "" || ev.EventType == "" {
				_ = sendAck(&sessiondv1.ConnectorIngressAck{
					State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REJECTED,
					Ack: &sessiondv1.ConnectorIngressAck_Rejected{
						Rejected: &sessiondv1.ConnectorRejected{
							EventId: func() *string {
								if ev != nil && ev.EventId != "" {
									return &ev.EventId
								}
								return nil
							}(),
							Error: businessError(sessiondv1.ErrorKind_ERROR_KIND_INVALID_ARGUMENT, "invalid connector event", false),
						},
					},
				})
				continue
			}
			if proto.Size(ev) > MaxBusinessBytes {
				_ = sendAck(&sessiondv1.ConnectorIngressAck{
					State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REJECTED,
					Ack: &sessiondv1.ConnectorIngressAck_Rejected{
						Rejected: &sessiondv1.ConnectorRejected{
							EventId: &ev.EventId,
							Error:   businessError(sessiondv1.ErrorKind_ERROR_KIND_RESOURCE_EXHAUSTED, "event exceeds 16MB limit", false),
						},
					},
				})
				continue
			}

			if ev.AgentId != agentID {
				_ = sendAck(&sessiondv1.ConnectorIngressAck{
					State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REJECTED,
					Ack: &sessiondv1.ConnectorIngressAck_Rejected{
						Rejected: &sessiondv1.ConnectorRejected{
							EventId: &ev.EventId,
							Error:   businessError(sessiondv1.ErrorKind_ERROR_KIND_PERMISSION_DENIED, "connector event agent identity does not match hello", false),
						},
					},
				})
				continue
			}

			eventPayload := map[string]any{
				"id":       ev.EventId,
				"agent_id": ev.AgentId,
				"type":     ev.EventType,
			}
			if ev.SessionId != nil {
				eventPayload["session_id"] = *ev.SessionId
			}
			if ev.Status != nil {
				if sName, ok := sessionStatusName(*ev.Status); ok {
					eventPayload["status"] = sName
				}
			}
			if ev.Data != nil && len(ev.Data.Utf8Json) > 0 {
				dataVal, err := decodeQueryJSON(ev.Data)
				if err != nil {
					_ = sendAck(&sessiondv1.ConnectorIngressAck{
						State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REJECTED,
						Ack: &sessiondv1.ConnectorIngressAck_Rejected{
							Rejected: &sessiondv1.ConnectorRejected{
								EventId: &ev.EventId,
								Error:   businessError(sessiondv1.ErrorKind_ERROR_KIND_INVALID_ARGUMENT, "invalid native json in event data", false),
							},
						},
					})
					continue
				}
				eventPayload["data"] = dataVal
			}

			committedSession, committedSeq, overflow, err := s.daemon.IngressConnectorEvent(agentID, eventPayload, defaultConnectorQueueCapacity)
			if overflow {
				_ = sendAck(&sessiondv1.ConnectorIngressAck{
					State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_OVERFLOW,
					Ack: &sessiondv1.ConnectorIngressAck_Overflow{
						Overflow: &sessiondv1.ConnectorOverflow{
							QueueCapacity: defaultConnectorQueueCapacity,
						},
					},
				})
				continue
			}
			if err != nil {
				_ = sendAck(&sessiondv1.ConnectorIngressAck{
					State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_REJECTED,
					Ack: &sessiondv1.ConnectorIngressAck_Rejected{
						Rejected: &sessiondv1.ConnectorRejected{
							EventId: &ev.EventId,
							Error:   businessError(sessiondv1.ErrorKind_ERROR_KIND_UNAVAILABLE, err.Error(), false),
						},
					},
				})
				continue
			}

			ackAccepted := &sessiondv1.ConnectorEventAccepted{
				EventId: ev.EventId,
			}
			if committedSession != "" {
				ackAccepted.CommittedSessionId = &committedSession
			}
			if committedSeq > 0 {
				ackAccepted.CommittedSeqId = &committedSeq
			}

			if err := sendAck(&sessiondv1.ConnectorIngressAck{
				State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_ACCEPTED,
				Ack: &sessiondv1.ConnectorIngressAck_EventAccepted{
					EventAccepted: ackAccepted,
				},
			}); err != nil {
				return err
			}

		case *sessiondv1.ConnectorIngressFrame_Disconnect:
			reason := "client disconnect"
			if f.Disconnect != nil && f.Disconnect.Reason != "" {
				reason = f.Disconnect.Reason
			}
			_ = sendAck(&sessiondv1.ConnectorIngressAck{
				State: sessiondv1.ConnectorAckState_CONNECTOR_ACK_STATE_DISCONNECTED,
				Ack: &sessiondv1.ConnectorIngressAck_Disconnected{
					Disconnected: &sessiondv1.ConnectorDisconnected{
						AgentId: agentID,
						Reason:  reason,
					},
				},
			})
			return nil

		default:
			return status.Error(codes.InvalidArgument, "unknown connector ingress frame")
		}
	}
}

func (s *connectorIngressService) BindControlSession(ctx context.Context, req *sessiondv1.BindControlSessionRequest) (*sessiondv1.BindControlSessionResponse, error) {
	if req == nil || req.AgentId == "" || req.ControlSession == nil || req.ControlSession.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid bind control session request")
	}
	if err := validateControlRequest(req); err != nil {
		return nil, err
	}
	if err := validateSessionRef(req.ControlSession); err != nil {
		return nil, err
	}
	if _, err := s.daemon.ResolveSessionOwner(req.ControlSession.SessionId, controlIdentity(req.ControlSession)); err != nil {
		if req.ControlSession.SessionId != protocol.ConnectorEventStreamID(req.AgentId) ||
			req.ControlSession.GetAgentId() != req.AgentId || req.ControlSession.ConnectionId != nil ||
			status.Code(ownerError(err)) != codes.NotFound {
			return nil, ownerError(err)
		}
	}

	flushed, err := s.daemon.BindConnectorControlSession(req.AgentId, req.ControlSession.SessionId)
	if err != nil {
		if errors.Is(err, protocol.ErrConnectorNotRegistered) {
			return nil, status.Error(codes.NotFound, "connector agent is not registered")
		}
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	return &sessiondv1.BindControlSessionResponse{
		Bound:         true,
		FlushedEvents: flushed,
	}, nil
}

func (s *connectorIngressService) DisconnectConnector(ctx context.Context, req *sessiondv1.DisconnectConnectorRequest) (*sessiondv1.DisconnectConnectorResponse, error) {
	if req == nil || req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	disconnected, err := s.daemon.DisconnectConnectorExplicit(req.AgentId)
	if err != nil {
		return &sessiondv1.DisconnectConnectorResponse{
			DaemonId:       s.daemon.DaemonID(),
			Disconnected:   false,
			OperationState: sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN,
			Error:          businessError(sessiondv1.ErrorKind_ERROR_KIND_FAILED_PRECONDITION, err.Error(), false),
		}, nil
	}
	return &sessiondv1.DisconnectConnectorResponse{
		DaemonId:       s.daemon.DaemonID(),
		Disconnected:   disconnected,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
	}, nil
}
