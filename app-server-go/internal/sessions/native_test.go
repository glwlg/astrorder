package sessions

import (
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestNativeTargetUsesStoredSSHFields(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.SaveSSHConnection(ctx, map[string]any{"id": "remote", "host": "fixture.invalid", "port": json.Number("2207"), "user": "tester", "workspace": "/srv/work", "state": "disconnected"})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{store: st}
	target, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "a", "kind": "codex", "connection_id": "remote"})
	if err != nil {
		t.Fatal(err)
	}
	if target.Ssh == nil || target.Ssh.Host != "fixture.invalid" || target.Ssh.Port != 2207 || target.Ssh.GetUser() != "tester" || target.GetConnectionId() != "remote" {
		t.Fatalf("SSH fields lost: %+v", target)
	}
}
func TestCommandMetaPreservesStableIdentity(t *testing.T) {
	meta := newCommandMeta("", "provided command")
	if meta.OperationId != "provided command" || meta.GetCommandId() != "provided command" {
		t.Fatalf("non-idempotent metadata: %+v", meta)
	}
	value := " exact identity "
	if stringValue(map[string]any{"id": value}, "id") != value {
		t.Fatal("identity normalized")
	}
}

func TestNativeTargetDefaultsForRemoteAgents(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, err = st.SaveSSHConnection(ctx, map[string]any{
		"id":   "remote-node",
		"host": "192.168.1.100",
		"port": json.Number("22"),
		"user": "luwei",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{store: st}

	codexTarget, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "codex-1", "kind": "codex", "connection_id": "remote-node"})
	if err != nil {
		t.Fatal(err)
	}
	if codexTarget.Ssh.GetCodexExecutable() != "/home/luwei/.vite-plus/bin/codex" {
		t.Fatalf("unexpected codex default: %v", codexTarget.Ssh.GetCodexExecutable())
	}

	grokTarget, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "grok-1", "kind": "grok", "connection_id": "remote-node"})
	if err != nil {
		t.Fatal(err)
	}
	if grokTarget.Ssh.GetGrokExecutable() != "/home/luwei/.local/bin/grok" {
		t.Fatalf("unexpected grok default: %v", grokTarget.Ssh.GetGrokExecutable())
	}

	hermesTarget, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "hermes-1", "kind": "hermes", "connection_id": "remote-node"})
	if err != nil {
		t.Fatal(err)
	}
	if hermesTarget.Ssh.GetHermesPath() != "/home/luwei/.local/bin/hermes" {
		t.Fatalf("unexpected hermes default: %v", hermesTarget.Ssh.GetHermesPath())
	}
	// Custom hermes_path stored in SSH connection
	_, err = st.SaveSSHConnection(ctx, map[string]any{
		"id":          "custom-hermes-node",
		"host":        "192.168.1.100",
		"port":        json.Number("22"),
		"user":        "luwei",
		"hermes_path": "/custom/bin/hermes",
	})
	if err != nil {
		t.Fatal(err)
	}
	customHermesTarget, err := h.resolveRuntimeTarget(ctx, map[string]any{"id": "hermes-2", "kind": "hermes", "connection_id": "custom-hermes-node"})
	if err != nil {
		t.Fatal(err)
	}
	if customHermesTarget.Ssh.GetHermesPath() != "/custom/bin/hermes" {
		t.Fatalf("unexpected custom hermes: %v", customHermesTarget.Ssh.GetHermesPath())
	}
}

func TestConfirmedReceiptTransparency(t *testing.T) {
	uncReceipt := &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_UNCERTAIN,
		Outcome: &pb.CommandReceipt_Uncertain{
			Uncertain: &pb.BusinessError{Message: "remote codex spawn failed"},
		},
	}
	err := confirmedReceipt(uncReceipt)
	if err == nil || err.Error() != "Native result is unconfirmed: remote codex spawn failed" {
		t.Fatalf("expected detailed uncertain error, got: %v", err)
	}

	rejReceipt := &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_UNSPECIFIED,
		Outcome: &pb.CommandReceipt_Rejected{
			Rejected: &pb.BusinessError{Message: "invalid agent state"},
		},
	}
	err = confirmedReceipt(rejReceipt)
	if err == nil || err.Error() != "Native result is unconfirmed: invalid agent state" {
		t.Fatalf("expected detailed rejected error, got: %v", err)
	}

	emptyUncReceipt := &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_UNCERTAIN,
	}
	err = confirmedReceipt(emptyUncReceipt)
	if err == nil || err.Error() != "Native result is unconfirmed; command will not retry" {
		t.Fatalf("expected fallback unconfirmed error, got: %v", err)
	}

	okReceipt := &pb.CommandReceipt{
		OperationState: pb.OperationState_OPERATION_STATE_COMPLETED,
		Outcome: &pb.CommandReceipt_Completed{
			Completed: &pb.CommandResult{},
		},
	}
	if err = confirmedReceipt(okReceipt); err != nil {
		t.Fatalf("expected nil error for completed receipt, got: %v", err)
	}
}
