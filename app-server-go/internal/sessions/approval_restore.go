package sessions

import (
	"context"
	"fmt"
	"sync"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// Serialize restores with explicit selections so an older read cannot overwrite
// a newly confirmed choice. The store, rather than a caller's snapshot, owns it.
func (h *Handler) approvalSelectionLock(agentID, sessionID string) *sync.Mutex {
	key := "approval\x00" + h.client.DaemonID() + "\x00" + agentID + "\x00" + sessionID
	value, _ := nativeAttachLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (h *Handler) restoreCodexApproval(ctx context.Context, session map[string]any) error {
	aid, sid := stringValue(session, "agent_id"), stringValue(session, "id")
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		return err
	}
	if kind := stringValue(agent, "kind"); kind != "codex" && kind != "antigravity" {
		return nil
	}
	lock := h.approvalSelectionLock(aid, sid)
	lock.Lock()
	defer lock.Unlock()
	current, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		return err
	}
	if current == nil {
		return fmt.Errorf("approval selection session no longer exists")
	}
	mode := stringValue(current, "selected_approval_mode")
	if mode == "" {
		return nil
	}
	if mode != "manual" && mode != "auto" && mode != "full_access" || stringValue(agent, "kind") == "antigravity" && mode == "auto" {
		return fmt.Errorf("saved approval mode is invalid")
	}
	nativeID := stringValue(current, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	ref := buildSessionRef(aid, nativeID, stringValue(current, "connection_id"))
	state, err := h.client.Runtime.ReadApprovalMode(ctx, &pb.ReadApprovalModeRequest{Session: ref})
	if err != nil {
		return fmt.Errorf("read native approval before restore: %w", err)
	}
	if state != nil && state.Mode == mode {
		return nil
	}
	receipt, err := h.client.Runtime.SetApprovalMode(ctx, &pb.SetApprovalModeRequest{Meta: newCommandMeta("", ""), Session: ref, Mode: mode})
	if err != nil {
		return fmt.Errorf("restore native approval: %w", err)
	}
	if err := confirmedReceipt(receipt); err != nil {
		return fmt.Errorf("native approval restore unconfirmed: %w", err)
	}
	state, err = h.client.Runtime.ReadApprovalMode(ctx, &pb.ReadApprovalModeRequest{Session: ref})
	if err != nil {
		return fmt.Errorf("verify native approval restore: %w", err)
	}
	if state == nil || state.Mode != mode {
		return fmt.Errorf("native approval restore was not confirmed by readback")
	}
	return nil
}
