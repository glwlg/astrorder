package grpcapi_test

import (
	"testing"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/runtime/codex"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A confirmed transport control must serve the product's official model
// catalog RPC without treating the control ID as a native thread.
func TestAcceptanceCodexControlSupportsOfficialModelCatalog(t *testing.T) {
	fixture := &grpcControlFixture{}
	adapter := codex.NewSSHAdapter(codex.Config{
		Executable: "/usr/bin/codex", Workspace: "/audit",
		Allowed: []string{"/audit"}, AgentID: "codex-control-agent",
	}, nil, fixture.starter())
	defer adapter.Close()
	f := startFixture(t)
	f.daemon.RegisterRuntime("codex", adapter)
	ref := &pb.SessionRef{SessionId: "probe-codex-local"}
	receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{
		Session: ref, Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX}, RuntimeControl: true,
	})
	if err != nil || !receipt.GetAttached() {
		t.Fatalf("control attach: %v %v", receipt, err)
	}
	if _, err := pb.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &pb.ListModelsRequest{Session: ref}); err != nil {
		t.Fatalf("control model catalog: %v", err)
	}
	if fixture.count("model/list") != 1 {
		t.Fatalf("catalog did not reach native model/list: %v", fixture.snapshot())
	}
	for _, forbidden := range []string{"thread/start", "thread/resume", "session/new", "session/load", "turn/start"} {
		if fixture.count(forbidden) != 0 {
			t.Fatalf("control catalog issued %s: %v", forbidden, fixture.snapshot())
		}
	}

	// The official response must carry the exact native model metadata, not a
	// fabricated placeholder, and a repeated read must reuse the same live
	// transport without starting another native process.
	models, err := pb.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &pb.ListModelsRequest{Session: ref})
	if err != nil || len(models.Items) != 1 {
		t.Fatalf("control model metadata: %+v %v", models, err)
	}
	option := models.Items[0]
	if option.Model != "gpt-5.2-codex" || option.Provider != "openai" || option.GetLabel() != "GPT-5.2 Codex" {
		t.Fatalf("control model identity: %+v", option)
	}
	if len(option.SupportedEfforts) != 1 || option.SupportedEfforts[0] != "high" {
		t.Fatalf("control model efforts: %+v", option.SupportedEfforts)
	}
	if fixture.count("model/list") != 2 || fixture.transportCount() != 1 {
		t.Fatalf("repeated catalog read started another transport: %v transports=%d", fixture.snapshot(), fixture.transportCount())
	}

	// A native app-server EOF must surface as a real error, never a stale
	// success, and the authoritative status must report the failed transport.
	fixture.closeTransports()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, readErr := pb.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &pb.ListModelsRequest{Session: ref})
		if readErr != nil {
			if status.Code(readErr) != codes.Unavailable {
				t.Fatalf("control catalog EOF error code: %v", readErr)
			}
			statusResponse, err := pb.NewDaemonControlClient(f.conn).GetStatus(f.ctx, &pb.GetStatusRequest{})
			if err != nil {
				t.Fatalf("GetStatus after EOF: %v", err)
			}
			failed := false
			for _, session := range statusResponse.Sessions {
				if session.GetSession().GetSessionId() == "probe-codex-local" && session.Status == pb.SessionStatus_SESSION_STATUS_ERROR {
					failed = true
				}
			}
			if failed {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("control catalog still reported success or healthy status after native EOF")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Re-attach must retire the dead transport, initialize a fresh one, and
	// serve the catalog again with zero native threads anywhere in the flow.
	reconnect, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{
		Session: ref, Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX}, RuntimeControl: true,
	})
	if err != nil || !reconnect.GetAttached() {
		t.Fatalf("control re-attach after EOF: %v %v", reconnect, err)
	}
	if fixture.count("initialize") != 2 || fixture.transportCount() != 2 {
		t.Fatalf("control re-attach did not initialize a fresh transport: %v transports=%d", fixture.snapshot(), fixture.transportCount())
	}
	if _, err := pb.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &pb.ListModelsRequest{Session: ref}); err != nil {
		t.Fatalf("control catalog after re-attach: %v", err)
	}
	for _, forbidden := range []string{"thread/start", "thread/resume", "session/new", "session/load", "turn/start"} {
		if fixture.count(forbidden) != 0 {
			t.Fatalf("control re-attach issued %s: %v", forbidden, fixture.snapshot())
		}
	}
}

// Native-session actions on a control-only binding must be refused explicitly
// with zero native requests and zero new threads, and the binding must keep
// serving the read-only catalog afterwards.
func TestAcceptanceCodexControlRejectsNativeSessionActions(t *testing.T) {
	fixture := &grpcControlFixture{}
	adapter := codex.NewSSHAdapter(codex.Config{
		Executable: "/usr/bin/codex", Workspace: "/audit",
		Allowed: []string{"/audit"}, AgentID: "codex-control-agent",
	}, nil, fixture.starter())
	defer adapter.Close()
	f := startFixture(t)
	f.daemon.RegisterRuntime("codex", adapter)
	ref := &pb.SessionRef{SessionId: "probe-codex-local"}
	receipt, err := pb.NewSessionControlClient(f.conn).AttachSession(f.ctx, &pb.AttachSessionRequest{
		Session: ref, Target: &pb.RuntimeTarget{Runtime: pb.RuntimeKind_RUNTIME_KIND_CODEX}, RuntimeControl: true,
	})
	if err != nil || !receipt.GetAttached() {
		t.Fatalf("control attach: %v %v", receipt, err)
	}
	control := pb.NewSessionControlClient(f.conn)
	reject := func(name string, err error, receipt *pb.CommandReceipt) {
		t.Helper()
		if err != nil {
			return
		}
		if receipt == nil || receipt.OperationState == pb.OperationState_OPERATION_STATE_COMPLETED || receipt.Accepted || receipt.GetUncertain() == nil {
			t.Fatalf("%s was not explicitly refused on a control-only binding: %+v", name, receipt)
		}
	}
	send, sendErr := control.Send(f.ctx, &pb.SendRequest{Meta: controlMeta("control-reject-send"), Session: ref, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: "synthetic"}}}}})
	reject("session.send", sendErr, send)
	settings, settingsErr := control.UpdateSettings(f.ctx, &pb.UpdateSettingsRequest{Meta: controlMeta("control-reject-settings"), Session: ref, Effort: ptr("high")})
	reject("session.settings", settingsErr, settings)
	rename, renameErr := control.RenameSession(f.ctx, &pb.RenameSessionRequest{Meta: controlMeta("control-reject-rename"), Session: ref, Title: "synthetic"})
	reject("session.rename", renameErr, rename)
	deleted, deleteErr := control.DeleteSession(f.ctx, &pb.DeleteSessionRequest{Meta: controlMeta("control-reject-delete"), Session: ref})
	reject("session.delete", deleteErr, deleted)
	approval, approvalErr := control.RespondApproval(f.ctx, &pb.RespondApprovalRequest{Meta: controlMeta("control-reject-approval"), Session: ref, ApprovalId: &pb.ExactIdentifier{Value: &pb.ExactIdentifier_Text{Text: "codex:1"}}, Decision: pb.ApprovalDecision_APPROVAL_DECISION_ACCEPT})
	reject("session.approve", approvalErr, approval)
	if _, err := pb.NewRuntimeQueryClient(f.conn).GetHistoryPage(f.ctx, &pb.HistoryPageRequest{Session: ref, Limit: 10}); status.Code(err) != codes.Unavailable {
		t.Fatalf("control history read was not refused: %v", err)
	}
	// Zero native requests: only the attach initialization frames may exist.
	for _, method := range fixture.snapshot() {
		if method != "initialize" && method != "initialized" {
			t.Fatalf("rejected control actions issued native request %s: %v", method, fixture.snapshot())
		}
	}
	for _, forbidden := range []string{"thread/start", "thread/resume", "session/new", "session/load", "turn/start"} {
		if fixture.count(forbidden) != 0 {
			t.Fatalf("rejected control actions issued %s: %v", forbidden, fixture.snapshot())
		}
	}
	// The control binding stays healthy and bound after the refusals.
	if _, err := pb.NewRuntimeQueryClient(f.conn).ListModels(f.ctx, &pb.ListModelsRequest{Session: ref}); err != nil {
		t.Fatalf("control catalog after refusals: %v", err)
	}
	if fixture.count("model/list") != 1 {
		t.Fatalf("control catalog after refusals did not reach model/list: %v", fixture.snapshot())
	}
}
