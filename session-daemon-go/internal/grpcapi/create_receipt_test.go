package grpcapi

import (
	"encoding/json"
	"testing"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func TestCommandReceiptCreateMissingAcceptedDeducesTrue(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-create-1"}
	envelope := map[string]any{
		"action":     "session.create.result",
		"session_id": "sid-create-1",
		"result": map[string]any{
			"agent_id":   "fixture",
			"session_id": "sid-create-1",
			"status":     "idle",
			"title":      "Test Session",
			"workspace":  "P:/work",
		},
	}
	receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !receipt.Accepted {
		t.Fatalf("expected receipt.Accepted=true, got %v", receipt.Accepted)
	}
	completed := receipt.GetCompleted()
	if completed == nil || !completed.Accepted {
		t.Fatalf("expected completed.Accepted=true, got %+v", completed)
	}
	var native map[string]any
	if err := json.Unmarshal(completed.Native.Utf8Json, &native); err != nil {
		t.Fatalf("unmarshal native payload: %v", err)
	}
	if native["accepted"] != true {
		t.Fatalf("expected native accepted=true, got %v", native["accepted"])
	}
}

func TestCommandReceiptLiveCapturedEnvelope(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "star-cli-live-20261019-r1-A-to-B"}
	envelope := map[string]any{
		"action":     "session.create.result",
		"session_id": "local-cfd6e1a1d921e46b666191792cd4d077f7ec5119196d7cfdb0a855b09754bf8f",
		"result": map[string]any{
			"agent_id":   "local-hermes-default",
			"session_id": "local-cfd6e1a1d921e46b666191792cd4d077f7ec5119196d7cfdb0a855b09754bf8f",
			"status":     "idle",
			"title":      "新会话",
			"workspace":  "P:\\workspace\\glwlg\\ai\\astrorder",
		},
	}
	receipt, err := commandReceipt("daemon-live", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !receipt.Accepted {
		t.Fatalf("expected receipt.Accepted=true for live captured envelope, got %v", receipt.Accepted)
	}
	completed := receipt.GetCompleted()
	if completed == nil || !completed.Accepted {
		t.Fatalf("expected completed.Accepted=true, got %+v", completed)
	}
	var native map[string]any
	if err := json.Unmarshal(completed.Native.Utf8Json, &native); err != nil {
		t.Fatalf("unmarshal native payload: %v", err)
	}
	if native["accepted"] != true {
		t.Fatalf("expected native accepted=true, got %v", native["accepted"])
	}
}

func TestCommandReceiptCreateMissingPayloadSessionIDAllowed(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-create-no-payload-sid"}
	envelope := map[string]any{
		"action":     "session.create.result",
		"session_id": "control-owned",
		"result": map[string]any{
			"agent_id": "fixture",
			"status":   "idle",
			"created":  true,
		},
	}
	receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !receipt.Accepted {
		t.Fatalf("expected receipt.Accepted=true when payload session_id is omitted, got %v", receipt.Accepted)
	}
	completed := receipt.GetCompleted()
	if completed == nil || !completed.Accepted {
		t.Fatalf("expected completed.Accepted=true, got %+v", completed)
	}
}

func TestCommandReceiptCreateValidStatusVariants(t *testing.T) {
	statuses := []string{"idle", "running", "waiting_approval"}
	for _, st := range statuses {
		t.Run(st, func(t *testing.T) {
			meta := &pb.CommandMeta{OperationId: "op-" + st}
			envelope := map[string]any{
				"action":     "session.create.result",
				"session_id": "sid-" + st,
				"result": map[string]any{
					"status": st,
				},
			}
			receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !receipt.Accepted {
				t.Fatalf("status %s expected accepted=true, got %v", st, receipt.Accepted)
			}
		})
	}
}

func TestCommandReceiptCreateExplicitFalsePreserved(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-create-false"}
	envelope := map[string]any{
		"action":     "session.create.result",
		"session_id": "sid-create-false",
		"result": map[string]any{
			"agent_id":   "fixture",
			"session_id": "sid-create-false",
			"status":     "idle",
			"accepted":   false,
		},
	}
	receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt.Accepted {
		t.Fatalf("expected receipt.Accepted=false, got %v", receipt.Accepted)
	}
	completed := receipt.GetCompleted()
	if completed == nil || completed.Accepted {
		t.Fatalf("expected completed.Accepted=false, got %+v", completed)
	}
}

func TestCommandReceiptCreateExplicitTruePreserved(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-create-true"}
	envelope := map[string]any{
		"action":     "session.create.result",
		"session_id": "sid-create-true",
		"result": map[string]any{
			"agent_id":   "fixture",
			"session_id": "sid-create-true",
			"status":     "idle",
			"accepted":   true,
		},
	}
	receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !receipt.Accepted {
		t.Fatalf("expected receipt.Accepted=true, got %v", receipt.Accepted)
	}
	completed := receipt.GetCompleted()
	if completed == nil || !completed.Accepted {
		t.Fatalf("expected completed.Accepted=true, got %+v", completed)
	}
}

func TestCommandReceiptMalformedOrNullAcceptedRejected(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-malformed"}
	cases := []struct {
		name     string
		accepted any
	}{
		{"null", nil},
		{"string", "true"},
		{"int", 1},
		{"slice", []any{true}},
		{"map", map[string]any{"ok": true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envelope := map[string]any{
				"action":     "session.create.result",
				"session_id": "sid-malformed",
				"result": map[string]any{
					"agent_id":   "fixture",
					"session_id": "sid-malformed",
					"status":     "idle",
					"accepted":   tc.accepted,
				},
			}
			receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
			if err == nil {
				t.Fatalf("expected error for accepted=%#v, got receipt=%+v", tc.accepted, receipt)
			}
		})
	}
}

func TestCommandReceiptSendMissingAcceptedDoesNotBecomeTrue(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-send"}
	envelope := map[string]any{
		"action":     "session.send.result",
		"session_id": "sid-send",
		"result": map[string]any{
			"status":  "running",
			"turn_id": "turn-1",
		},
	}
	sessionRef := &pb.SessionRef{SessionId: "sid-send"}
	receipt, err := commandReceipt("daemon-1", sessionRef, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt.Accepted {
		t.Fatalf("send missing accepted should NOT become true, got %v", receipt.Accepted)
	}
	completed := receipt.GetCompleted()
	if completed == nil || completed.Accepted {
		t.Fatalf("send missing completed.Accepted should NOT become true, got %+v", completed)
	}
}

func TestCommandReceiptUnknownActionMissingAcceptedDoesNotBecomeTrue(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-unknown"}
	envelope := map[string]any{
		"action":     "unknown.custom.result",
		"session_id": "sid-unknown",
		"result": map[string]any{
			"status": "idle",
		},
	}
	receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt.Accepted {
		t.Fatalf("unknown action missing accepted should NOT become true, got %v", receipt.Accepted)
	}
	completed := receipt.GetCompleted()
	if completed == nil || completed.Accepted {
		t.Fatalf("unknown action missing completed.Accepted should NOT become true, got %+v", completed)
	}
}

func TestCommandReceiptCreateIdentityMalformedNotConfirmed(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-bad-id"}

	t.Run("empty envelope session_id", func(t *testing.T) {
		envelope := map[string]any{
			"action":     "session.create.result",
			"session_id": "",
			"result": map[string]any{
				"status": "idle",
			},
		}
		receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Accepted {
			t.Fatalf("empty session_id should not confirm accepted, got %v", receipt.Accepted)
		}
	})

	t.Run("missing envelope session_id", func(t *testing.T) {
		envelope := map[string]any{
			"action": "session.create.result",
			"result": map[string]any{
				"status": "idle",
			},
		}
		receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Accepted {
			t.Fatalf("missing session_id should not confirm accepted, got %v", receipt.Accepted)
		}
	})

	t.Run("mismatched payload session_id", func(t *testing.T) {
		envelope := map[string]any{
			"action":     "session.create.result",
			"session_id": "sid-envelope",
			"result": map[string]any{
				"session_id": "sid-payload-different",
				"status":     "idle",
			},
		}
		receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Accepted {
			t.Fatalf("mismatched session_id should not confirm accepted, got %v", receipt.Accepted)
		}
	})

	t.Run("caller session ref mismatch", func(t *testing.T) {
		envelope := map[string]any{
			"action":     "session.create.result",
			"session_id": "sid-envelope",
			"result": map[string]any{
				"session_id": "sid-envelope",
				"status":     "idle",
			},
		}
		callerSession := &pb.SessionRef{SessionId: "sid-caller"}
		receipt, err := commandReceipt("daemon-1", callerSession, meta, envelope)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Accepted {
			t.Fatalf("caller session mismatch should not confirm accepted, got %v", receipt.Accepted)
		}
	})

	t.Run("non-string payload session_id", func(t *testing.T) {
		envelope := map[string]any{
			"action":     "session.create.result",
			"session_id": "sid-envelope",
			"result": map[string]any{
				"session_id": 12345,
				"status":     "idle",
			},
		}
		receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if receipt.Accepted {
			t.Fatalf("non-string payload session_id should not confirm accepted, got %v", receipt.Accepted)
		}
	})
}

func TestCommandReceiptCreateErrorStatusNotConfirmed(t *testing.T) {
	meta := &pb.CommandMeta{OperationId: "op-create-error"}
	envelope := map[string]any{
		"action":     "session.create.result",
		"session_id": "sid-create-error",
		"result": map[string]any{
			"agent_id":   "fixture",
			"session_id": "sid-create-error",
			"status":     "error",
		},
	}
	receipt, err := commandReceipt("daemon-1", nil, meta, envelope)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if receipt.Accepted {
		t.Fatalf("error status should not confirm accepted, got %v", receipt.Accepted)
	}
	completed := receipt.GetCompleted()
	if completed == nil || completed.Accepted {
		t.Fatalf("error status completed.Accepted should not be true, got %+v", completed)
	}
}
