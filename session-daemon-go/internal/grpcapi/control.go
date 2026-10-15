package grpcapi

import (
	"context"
	"sort"
	"strings"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type service struct {
	sessiondv1.UnimplementedDaemonControlServer
	sessiondv1.UnimplementedEventStreamServer
	daemon  *protocol.Daemon
	options options
}

func (service *service) GetCapabilities(context.Context, *sessiondv1.GetCapabilitiesRequest) (*sessiondv1.GetCapabilitiesResponse, error) {
	return &sessiondv1.GetCapabilitiesResponse{
		ProtocolMajor: 1,
		ProtocolMinor: 1, // idle_only send admission is enforced by native adapters.
		DaemonId:      service.daemon.DaemonID(),
		Services: []string{
			"astrorder.sessiond.v1.ConnectorIngress",
			"astrorder.sessiond.v1.DaemonControl",
			"astrorder.sessiond.v1.EventStream",
			"astrorder.sessiond.v1.ModelConfig",
			"astrorder.sessiond.v1.OperationQuery",
			"astrorder.sessiond.v1.RuntimeQuery",
			"astrorder.sessiond.v1.SessionControl",
			"astrorder.sessiond.v1.TerminalStream",
		},
		CanonicalizationVersions: []sessiondv1.CanonicalizationVersion{sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1, sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_ASTRORDER_OPERATION_V1},
		MaxBusinessBytes:         sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_COMPAT_BUSINESS_BYTES,
		MaxWireBytes:             sessiondv1.ContractLimit_CONTRACT_LIMIT_MAX_GRPC_WIRE_BYTES,
		EventStreamVersion:       sessiondv1.EventStreamVersion_EVENT_STREAM_VERSION_V1,
		OperationLookup:          true,
	}, nil
}

func (service *service) GetStatus(context.Context, *sessiondv1.GetStatusRequest) (*sessiondv1.GetStatusResponse, error) {
	authority, err := service.daemon.AuthorityStatus()
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	pending := authority.PendingOperations
	response := &sessiondv1.GetStatusResponse{
		DaemonId:          authority.DaemonID,
		ConnectorIds:      authority.ConnectorIDs,
		PendingOperations: &pending,
	}
	for _, runtime := range authority.Runtimes {
		kind, ok := runtimeKind(runtime.Kind)
		if !ok {
			continue
		}
		response.Runtimes = append(response.Runtimes, &sessiondv1.RuntimeStatus{Runtime: kind, Registered: runtime.Registered, OwnedSessions: runtime.OwnedSessions, Busy: runtime.Busy})
	}
	for _, session := range authority.Sessions {
		mapped, ok := sessionStatus(session.Status)
		if !ok {
			return nil, status.Error(codes.Unavailable, "authoritative session status is invalid")
		}
		snapshot := &sessiondv1.SessionSnapshot{
			Session:            &sessiondv1.SessionRef{SessionId: session.SessionID},
			Status:             mapped,
			MinSeqId:           session.MinSeqID,
			ReplayThroughSeqId: session.MaxSeqID,
		}
		if session.RuntimeOwned {
			kind, ok := runtimeKind(session.RuntimeKind)
			if !ok {
				// A live-owned session whose kind cannot be mapped must fail
				// closed: it must never be reported under a default kind.
				return nil, status.Error(codes.Unavailable, "authoritative session runtime kind is invalid")
			}
			owned := true
			snapshot.RuntimeOwned = &owned
			snapshot.Runtime = &kind
			snapshot.Session.AgentId = session.Identity.AgentID
			snapshot.Session.ConnectionId = session.Identity.ConnectionID
		} else {
			// Journal-only rows explicitly report not live-owned so clients
			// cannot mistake durable history for an attachment.
			owned := false
			snapshot.RuntimeOwned = &owned
		}
		response.Sessions = append(response.Sessions, snapshot)
	}
	if authority.ModelConfigError != "" {
		response.ModelConfigError = businessError(sessiondv1.ErrorKind_ERROR_KIND_UNAVAILABLE, authority.ModelConfigError, true)
	}
	return response, nil
}

func (service *service) Shutdown(_ context.Context, request *sessiondv1.ShutdownRequest) (*sessiondv1.ShutdownResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "shutdown request is required")
	}
	decision, err := service.daemon.AuthorizeShutdown(request.ExpectedDaemonId, request.ConfirmActive)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	response := &sessiondv1.ShutdownResponse{DaemonId: decision.DaemonID, Accepted: decision.Accepted, GracefulDrainStarted: decision.Accepted}
	return response, nil
}

func runtimeKind(kind string) (sessiondv1.RuntimeKind, bool) {
	switch kind {
	case "codex":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX, true
	case "pty":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_PTY, true
	case "hermes":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES, true
	case "ssh":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_SSH_HERMES, true
	case "codex-ssh":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX_SSH, true
	case "grok":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_GROK, true
	case "grok-ssh":
		return sessiondv1.RuntimeKind_RUNTIME_KIND_GROK_SSH, true
	default:
		return sessiondv1.RuntimeKind_RUNTIME_KIND_UNSPECIFIED, false
	}
}

func sessionStatus(value string) (sessiondv1.SessionStatus, bool) {
	switch value {
	case "idle":
		return sessiondv1.SessionStatus_SESSION_STATUS_IDLE, true
	case "running":
		return sessiondv1.SessionStatus_SESSION_STATUS_RUNNING, true
	case "waiting_approval":
		return sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL, true
	case "error":
		return sessiondv1.SessionStatus_SESSION_STATUS_ERROR, true
	default:
		return sessiondv1.SessionStatus_SESSION_STATUS_UNSPECIFIED, false
	}
}

func sessionStatusName(value sessiondv1.SessionStatus) (string, bool) {
	switch value {
	case sessiondv1.SessionStatus_SESSION_STATUS_IDLE:
		return "idle", true
	case sessiondv1.SessionStatus_SESSION_STATUS_RUNNING:
		return "running", true
	case sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
		return "waiting_approval", true
	case sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
		return "error", true
	default:
		return "", false
	}
}

func businessError(kind sessiondv1.ErrorKind, message string, retryable bool) *sessiondv1.BusinessError {
	return &sessiondv1.BusinessError{Kind: kind, Message: message, Retryable: retryable}
}

func sortedSessionIDs[T any](sessions map[string]T) []string {
	ids := make([]string, 0, len(sessions))
	for id := range sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func classifyProtocolError(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	message := err.Error()
	if strings.Contains(message, "invalid") || strings.Contains(message, "duplicate") {
		return codes.InvalidArgument
	}
	return codes.Unavailable
}
