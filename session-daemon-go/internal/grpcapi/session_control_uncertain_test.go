package grpcapi

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"testing"
)

func TestCommandReceiptUncertainWithDetail(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-1"}
	envelope := map[string]any{
		"action":          "error",
		"operation_state": "uncertain",
		"detail":          "remote codex failed to spawn: /path/not/found",
	}
	receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt.OperationState != pb.OperationState_OPERATION_STATE_UNCERTAIN {
		t.Fatalf("expected uncertain, got %v", receipt.OperationState)
	}
	uncertain := receipt.GetUncertain()
	if uncertain == nil {
		t.Fatal("expected uncertain outcome")
	}
	expected := "native command outcome is uncertain: remote codex failed to spawn: /path/not/found"
	if uncertain.Message != expected {
		t.Fatalf("expected %q, got %q", expected, uncertain.Message)
	}
}

func TestCommandReceiptUncertainWithoutDetail(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-2"}
	envelope := map[string]any{
		"action":          "error",
		"operation_state": "uncertain",
	}
	receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt.OperationState != pb.OperationState_OPERATION_STATE_UNCERTAIN {
		t.Fatalf("expected uncertain, got %v", receipt.OperationState)
	}
	uncertain := receipt.GetUncertain()
	if uncertain == nil {
		t.Fatal("expected uncertain outcome")
	}
	expected := "native command outcome is uncertain; reconcile before retry"
	if uncertain.Message != expected {
		t.Fatalf("expected %q, got %q", expected, uncertain.Message)
	}
}
