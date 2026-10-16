package grpcapi_test

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"testing"
)

func TestGrokApprovalCancelPreservesCancelledOutcomeOverGRPC(t *testing.T) {
	f := startFixture(t)
	a := &controlRuntimeFixture{}
	f.daemon.RegisterRuntime("grok", a)
	client := pb.NewSessionControlClient(f.conn)
	_, err := client.AttachSession(f.ctx, &pb.AttachSessionRequest{Session: &pb.SessionRef{SessionId: "control-owned", AgentId: ptr("fixture")}, Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_GROK}})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := client.RespondApproval(f.ctx, &pb.RespondApprovalRequest{Meta: controlMeta("grok-cancel"), Session: &pb.SessionRef{SessionId: "control-owned", AgentId: ptr("fixture")}, ApprovalId: &pb.ExactIdentifier{Value: &pb.ExactIdentifier_Text{Text: "grok:permission-1"}}, Decision: pb.ApprovalDecision_APPROVAL_DECISION_CANCEL})
	if err != nil || receipt.GetCompleted() == nil {
		t.Fatalf("approval failed: %v %v", receipt, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.requests) != 1 || a.requests[0].Fields["cancelled"] != true {
		t.Fatalf("cancel was collapsed into decline: %+v", a.requests)
	}
}
