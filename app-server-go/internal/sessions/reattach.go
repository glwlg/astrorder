package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log/slog"
	"sync"
)

var nativeAttachLocks sync.Map

// ReadModel checks the live binding. A missing Hermes binding can be reopened
// from its exact persisted session without resubmitting the previous command.
func (h *Handler) ensureNativeBinding(ctx context.Context, session map[string]any) error {
	err := h.bindNativeSession(ctx, session)
	if err == nil {
		_, err = h.store.DeletePreference(ctx, sleepPreferenceKey(stringValue(session, "agent_id"), stringValue(session, "id")))
	}
	if err == nil {
		err = h.restoreCodexApproval(ctx, session)
	}
	if err == nil {
		err = h.refreshHermesApprovals(ctx, session)
	}
	if err != nil {
		// The HTTP layer reports only a status code; keep the cause findable.
		slog.Warn("native session binding failed", "agent_id", stringValue(session, "agent_id"), "session_id", stringValue(session, "id"), "error", err)
	}
	return err
}

func (h *Handler) bindNativeSession(ctx context.Context, session map[string]any) error {
	aid, sid := stringValue(session, "agent_id"), stringValue(session, "id")
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		return err
	}
	kind := stringValue(agent, "kind")
	if kind != "hermes" && kind != "codex" && kind != "grok" && kind != "antigravity" {
		return nil
	}
	if h.client == nil || h.client.Runtime == nil {
		return fmt.Errorf("daemon runtime is unavailable")
	}
	if err := h.requireAgentConnection(ctx, agent); err != nil {
		return err
	}
	nativeID := stringValue(session, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	ref := buildSessionRef(aid, nativeID, stringValue(session, "connection_id"))
	_, err = h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
	recoverable := func(err error) bool {
		return status.Code(err) == codes.NotFound || (kind == "hermes" || kind == "codex") && status.Code(err) == codes.Unavailable
	}
	if !recoverable(err) {
		return err
	}
	key := h.client.DaemonID() + "\x00" + aid + "\x00" + nativeID
	value, _ := nativeAttachLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()
	_, err = h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
	if !recoverable(err) {
		return err
	}
	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		return err
	}
	cwd := stringValue(session, "workspace")
	receipt, err := h.client.Session.AttachSession(ctx, &pb.AttachSessionRequest{Session: ref, Target: target, Cwd: optString(cwd)})
	if err != nil {
		return err
	}
	if receipt == nil || !receipt.Attached || receipt.Session == nil || receipt.Session.SessionId != nativeID || receipt.Session.GetAgentId() != aid || receipt.Session.GetConnectionId() != ref.GetConnectionId() {
		return fmt.Errorf("native reattachment identity was not confirmed")
	}
	if receipt.Status != pb.SessionStatus_SESSION_STATUS_IDLE && receipt.Status != pb.SessionStatus_SESSION_STATUS_RUNNING && receipt.Status != pb.SessionStatus_SESSION_STATUS_WAITING_APPROVAL {
		return fmt.Errorf("native reattachment status was not confirmed")
	}
	_, err = h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
	return err
}
