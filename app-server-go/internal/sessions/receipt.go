package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"errors"
	"fmt"
)

func confirmedReceipt(receipt *pb.CommandReceipt) error {
	if receipt == nil {
		return errors.New("Daemon returned no command receipt")
	}
	if receipt.OperationState != pb.OperationState_OPERATION_STATE_COMPLETED {
		if uncertain := receipt.GetUncertain(); uncertain != nil && uncertain.Message != "" {
			return fmt.Errorf("Native result is unconfirmed: %s", uncertain.Message)
		}
		if rejected := receipt.GetRejected(); rejected != nil && rejected.Message != "" {
			return fmt.Errorf("Native result is unconfirmed: %s", rejected.Message)
		}
		return errors.New("Native result is unconfirmed; command will not retry")
	}
	if rejected := receipt.GetRejected(); rejected != nil {
		return errors.New(rejected.Message)
	}
	if receipt.GetUncertain() != nil {
		return errors.New("Native result is uncertain; command will not retry")
	}
	if receipt.GetCompleted() == nil {
		return errors.New("Daemon returned no completed result")
	}
	return nil
}
func nativeStatus(status pb.SessionStatus) (string, error) {
	switch status {
	case pb.SessionStatus_SESSION_STATUS_IDLE:
		return "idle", nil
	case pb.SessionStatus_SESSION_STATUS_RUNNING:
		return "running", nil
	case pb.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
		return "waiting_approval", nil
	case pb.SessionStatus_SESSION_STATUS_ERROR:
		return "error", nil
	default:
		return "", fmt.Errorf("Native status is unconfirmed")
	}
}
