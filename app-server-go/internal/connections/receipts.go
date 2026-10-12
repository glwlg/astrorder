package connections

import (
	"errors"
	"fmt"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func (h *Handler) disconnectReceipt(response *pb.DisconnectRuntimeResponse, err error) error {
	if err != nil {
		return err
	}
	if response != nil && response.Error != nil {
		return fmt.Errorf("runtime disconnect refused: %s", response.Error.Message)
	}
	if response == nil || !response.Disconnected || response.OperationState != pb.OperationState_OPERATION_STATE_COMPLETED || response.DaemonId != h.client.DaemonID() {
		return errors.New("Runtime disconnect was not confirmed by daemon; reconcile before retry")
	}
	return nil
}

// attachReceipt validates a SessionReceipt before a confirmed attach may
// mutate persisted connection state. The daemon epoch and the exact control
// session identity must match, and only a confirmed live session status may
// count as success. ERROR is a known failure, UNSPECIFIED means no confirmed
// outcome, and any unknown enum value is not a confirmed attach; none of them
// may mutate persisted state, and a rejected attach is never retried.
func (h *Handler) attachReceipt(receipt *pb.SessionReceipt, expected *pb.SessionRef) error {
	if receipt == nil || !receipt.Attached {
		return errors.New("Runtime attach was not confirmed by daemon")
	}
	if h.client != nil && receipt.DaemonId != h.client.DaemonID() {
		return errors.New("Runtime attach receipt belongs to another daemon epoch; reconcile before retry")
	}
	if expected != nil {
		if receipt.Session == nil || receipt.Session.SessionId != expected.SessionId {
			return errors.New("Runtime attach receipt identity does not match the requested control session")
		}
		if expected.ConnectionId != nil && (receipt.Session.ConnectionId == nil || *receipt.Session.ConnectionId != *expected.ConnectionId) {
			return errors.New("Runtime attach receipt does not match the requested connection")
		}
	}
	switch receipt.Status {
	case pb.SessionStatus_SESSION_STATUS_IDLE, pb.SessionStatus_SESSION_STATUS_RUNNING, pb.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
		return nil
	case pb.SessionStatus_SESSION_STATUS_ERROR:
		return errors.New("Runtime attach reported an error session; the attach failed and must not be treated as success")
	default:
		return errors.New("Runtime attach outcome is uncertain: no confirmed session status; reconcile before retry")
	}
}
