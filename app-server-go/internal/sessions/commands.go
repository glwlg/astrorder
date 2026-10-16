package sessions

import (
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"context"
	"encoding/json"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"sort"
	"strings"
)

type commandError struct {
	status int
	detail string
}

func (e *commandError) Error() string              { return e.detail }
func (e *commandError) HTTPStatus() int            { return e.status }
func commandFailure(code int, detail string) error { return &commandError{code, detail} }

func (h *Handler) handleCreateCommand(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, 422, "Invalid command submission")
		return
	}
	result, err := h.Submit(r.Context(), payload)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

// Submit persists the exact browser command before one dispatch attempt.
func (h *Handler) Submit(ctx context.Context, payload map[string]any) (map[string]any, error) {
	for key := range payload {
		switch key {
		case "id", "agent_id", "session_id", "action", "text", "attachment_ids", "target_id":
		default:
			return nil, commandFailure(422, "Unknown command field")
		}
	}
	for _, key := range []string{"id", "agent_id", "session_id", "action"} {
		value, ok := payload[key].(string)
		if !ok || value == "" || len(value) > 256 {
			return nil, commandFailure(422, "Invalid command identity")
		}
	}
	aid, sid, action := stringValue(payload, "agent_id"), stringValue(payload, "session_id"), stringValue(payload, "action")
	switch action {
	case "send", "enqueue", "stop", "approve", "cancel":
	default:
		return nil, commandFailure(422, "Unsupported command action")
	}
	if action == "stop" && stringValue(payload, "target_id") != sid {
		return nil, commandFailure(422, "stop must target the current session")
	}
	if (action == "approve" || action == "cancel") && stringValue(payload, "target_id") == "" {
		return nil, commandFailure(422, "Approval target is required")
	}
	if text, present := payload["text"]; present {
		value, ok := text.(string)
		if !ok || len(value) > 2000000 {
			return nil, commandFailure(422, "Invalid command text")
		}
	}
	session, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, commandFailure(404, "Session was not found")
	}
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, commandFailure(404, "Agent was not found")
	}
	ids := []string{}
	if values, present := payload["attachment_ids"]; present {
		list, ok := values.([]any)
		if !ok || len(list) > 32 {
			return nil, commandFailure(422, "Invalid attachment IDs")
		}
		for _, item := range list {
			id, ok := item.(string)
			if !ok || id == "" || len(id) > 256 {
				return nil, commandFailure(422, "Invalid attachment ID")
			}
			ids = append(ids, id)
		}
	}
	attachments, err := h.store.AttachmentRows(ctx, ids)
	if err != nil {
		return nil, commandFailure(404, "Attachment was not found")
	}
	command := map[string]any{}
	for key, value := range payload {
		command[key] = value
	}
	parts := make([]any, len(attachments))
	for i, row := range attachments {
		parts[i] = row
	}
	command["attachments"] = parts
	initial := "received"
	if action == "enqueue" {
		initial = "queued"
	}
	stored, created, err := h.store.CreateCommand(ctx, command, initial)
	if err != nil {
		return nil, commandFailure(409, err.Error())
	}
	if !created {
		return stored, nil
	}
	if err = h.publisher.Publish(ctx, "command.upsert", stored); err != nil {
		return nil, err
	}
	if action == "send" || action == "enqueue" {
		h.ensureSessionTitle(ctx, session, stringValue(stored, "text"))
	}
	if action == "enqueue" {
		return stored, nil
	}
	return h.submitStored(ctx, stored)
}
func (h *Handler) submitStored(ctx context.Context, command map[string]any) (map[string]any, error) {
	aid, sid, cid := stringValue(command, "agent_id"), stringValue(command, "session_id"), stringValue(command, "id")
	setState := func(state string, cause error) (map[string]any, error) {
		var detail any
		if cause != nil {
			detail = cause.Error()
		}
		row, err := h.store.SetCommandState(ctx, aid, sid, cid, state, detail, true)
		if err != nil {
			return nil, err
		}
		if err = h.publisher.Publish(ctx, "command.upsert", row); err != nil {
			return nil, err
		}
		return row, nil
	}
	if h.client == nil || h.client.Session == nil {
		failure := commandFailure(503, "Daemon client is not configured")
		if _, err := setState("failed", failure); err != nil {
			return nil, err
		}
		return nil, failure
	}
	session, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, commandFailure(404, "Session was not found")
	}
	ref := buildSessionRef(aid, sid, stringValue(session, "connection_id"))
	meta := newCommandMeta(cid, cid)
	var receipt *pb.CommandReceipt
	action, text := stringValue(command, "action"), stringValue(command, "text")
	switch action {
	case "send", "enqueue":
		if err = h.ensureNativeBinding(ctx, session); err != nil {
			if _, storageErr := setState("failed", err); storageErr != nil {
				return nil, storageErr
			}
			return nil, commandFailure(502, "Native session binding unavailable: "+err.Error())
		}
		guidance, guidanceErr := h.runningGuidance(ctx, ref)
		if guidanceErr != nil {
			_, _ = setState("failed", guidanceErr)
			return nil, guidanceErr
		}
		if guidance && command["idle_only"] == true {
			failure := commandFailure(409, "Queued send requires an idle session")
			_, _ = setState("failed", failure)
			return nil, failure
		}
		if !guidance {
			err = h.restoreModel(ctx, ref)
		}
		if err != nil {
			_, storageErr := setState("failed", err)
			if storageErr != nil {
				return nil, storageErr
			}
			return nil, err
		}
		input, inputErr := h.commandInputs(ctx, command, session)
		if inputErr != nil {
			if _, storageErr := setState("failed", inputErr); storageErr != nil {
				return nil, storageErr
			}
			return nil, inputErr
		}
		slash := strings.Fields(text)
		name := ""
		if len(slash) > 0 {
			name = slash[0]
		}
		agent, readErr := h.store.GetAgent(ctx, aid)
		if readErr != nil {
			return nil, readErr
		}
		if guidance && agent["kind"] == "hermes" {
			for _, item := range input {
				if item.GetAttachment() != nil {
					failure := commandFailure(409, hermesAttachmentGuidanceRejection)
					if _, storageErr := setState("failed", failure); storageErr != nil {
						return nil, storageErr
					}
					return nil, failure
				}
			}
		}
		if agent["kind"] == "codex" && name == "/compact" {
			receipt, err = h.client.Session.Compact(ctx, &pb.CompactRequest{Meta: meta, Session: ref, Instructions: optString(strings.TrimSpace(strings.TrimPrefix(text, "/compact")))})
		} else if agent["kind"] == "codex" && name == "/review" {
			receipt, err = h.client.Session.StartReview(ctx, &pb.StartReviewRequest{Meta: meta, Session: ref, Delivery: pb.ReviewDelivery_REVIEW_DELIVERY_INLINE, Target: &pb.ReviewTarget{Target: &pb.ReviewTarget_Uncommitted{Uncommitted: "uncommitted"}}, Instructions: optString(strings.TrimSpace(strings.TrimPrefix(text, "/review")))})
		} else {
			request := &pb.SendRequest{Meta: meta, Session: ref, Input: input}
			if command["idle_only"] == true {
				request.Policy = &pb.SendPolicy{NativeOptions: &pb.NativeJson{Schema: "astrorder." + stringValue(agent, "kind") + ".send.options.v1", Utf8Json: []byte(`{"idle_only":true}`)}}
			} else if guidance && agent["kind"] == "hermes" {
				request.Policy = &pb.SendPolicy{NativeOptions: &pb.NativeJson{Schema: "astrorder.hermes.send.options.v1", Utf8Json: []byte(`{"guidance_only":true}`)}}
			}
			receipt, err = h.client.Session.Send(ctx, request)
		}
	case "stop":
		turn, readErr := h.store.ActiveTurn(ctx, aid, sid)
		if readErr != nil {
			return nil, readErr
		}
		receipt, err = h.client.Session.Interrupt(ctx, &pb.InterruptRequest{Meta: meta, Session: ref, TurnId: optString(turn)})
	case "approve", "cancel":
		approvals, readErr := h.store.PendingApprovals(ctx)
		if readErr != nil {
			return nil, readErr
		}
		target := stringValue(command, "target_id")
		found := false
		var pending map[string]any
		for _, approval := range approvals {
			if approval["agent_id"] == aid && approval["session_id"] == sid && approval["id"] == target {
				found = true
				pending = approval
				break
			}
		}
		if !found {
			return nil, commandFailure(409, "Approval does not belong to this pending session")
		}
		if pending["method"] == "item/tool/requestUserInput" {
			return nil, commandFailure(422, "Use the question response endpoint")
		}
		decision := pb.ApprovalDecision_APPROVAL_DECISION_ACCEPT
		if action == "cancel" {
			decision = pb.ApprovalDecision_APPROVAL_DECISION_DECLINE
			if pending["method"] == "session/request_permission" {
				decision = pb.ApprovalDecision_APPROVAL_DECISION_CANCEL
			}
		}
		request := &pb.RespondApprovalRequest{Meta: meta, Session: ref, ApprovalId: &pb.ExactIdentifier{Value: &pb.ExactIdentifier_Text{Text: target}}, Decision: decision}
		if action == "approve" && pending["method"] == "item/permissions/requestApproval" {
			permissions, ok := pending["permissions"].(map[string]any)
			if !ok || len(permissions) == 0 {
				return nil, commandFailure(409, "Requested permissions are unavailable")
			}
			names := make([]string, 0, len(permissions))
			for name := range permissions {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				encoded, err := json.Marshal(permissions[name])
				if err != nil {
					return nil, err
				}
				request.Permissions = append(request.Permissions, &pb.PermissionGrant{Name: name, Native: &pb.NativeJson{Schema: "codex.permission.grant.v1", Utf8Json: encoded}})
			}
		}
		receipt, err = h.client.Session.RespondApproval(ctx, request)
	default:
		return nil, commandFailure(422, "Unsupported command action")
	}
	if err != nil {
		state := "unknown"
		code := status.Code(err)
		if code == codes.InvalidArgument || code == codes.NotFound || code == codes.FailedPrecondition || code == codes.PermissionDenied || code == codes.Unauthenticated {
			state = "failed"
		}
		if _, storageErr := setState(state, err); storageErr != nil {
			return nil, storageErr
		}
		return nil, commandFailure(502, "Native command outcome is "+state+"; submission will not retry")
	}
	if err = confirmedReceipt(receipt); err != nil {
		state := "unknown"
		if receipt != nil && receipt.OperationState == pb.OperationState_OPERATION_STATE_COMPLETED && receipt.GetRejected() != nil {
			state = "failed"
		}
		if _, storageErr := setState(state, err); storageErr != nil {
			return nil, storageErr
		}
		return nil, commandFailure(502, err.Error())
	}
	if (action == "send" || action == "enqueue") && !receipt.Accepted {
		return nil, commandFailure(502, "Native send was not accepted")
	}
	if action == "send" || action == "enqueue" {
		if completed := receipt.GetCompleted(); completed != nil && completed.Native != nil {
			var native map[string]any
			if parseNativeJSON(completed.Native, &native) == nil && native["completed"] == true {
				// A guidance receipt delivers input to an existing turn. It must
				// not restore that turn after a concurrent completion event.
				return setState("completed", nil)
			}
		}
	}
	if receipt.TurnId != nil {
		if err = h.store.RecordAcceptedTurn(ctx, aid, sid, cid, *receipt.TurnId); err != nil {
			return nil, err
		}
	}
	return setState("accepted", nil)
}
func (h *Handler) runningGuidance(ctx context.Context, ref *pb.SessionRef) (bool, error) {
	// Guidance belongs to the current execution and its frozen model policy.
	// Use exact live ownership, since the App status projection can lag events.
	if live, statusErr := h.client.Daemon.GetStatus(ctx, &pb.GetStatusRequest{}); statusErr == nil && live != nil && live.GetDaemonId() == h.client.DaemonID() && live.GetStorageError() == nil {
		agent, agentErr := h.store.GetAgent(ctx, ref.GetAgentId())
		if agentErr != nil {
			return false, agentErr
		}
		for _, snapshot := range live.Sessions {
			if snapshotMatchesOwnedSession(snapshot, agent, ref.GetAgentId(), ref.SessionId, ref.GetConnectionId()) && snapshot.Status == pb.SessionStatus_SESSION_STATUS_RUNNING {
				return true, nil
			}
		}
	}
	return false, nil
}

func (h *Handler) restoreModel(ctx context.Context, ref *pb.SessionRef) error {
	binding, err := h.store.GetSessionModelBinding(ctx, ref.GetAgentId(), ref.SessionId)
	if err != nil || binding == nil {
		return err
	}
	current, err := h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
	if err != nil {
		return commandFailure(502, "Saved model readback unavailable; message was not sent")
	}
	provider, model := stringValue(binding, "provider"), stringValue(binding, "model")
	if modelBindingMatches(provider, model, current.GetProvider(), current.GetModel()) {
		return h.restoreReasoning(ctx, ref, binding, current)
	}
	receipt, err := h.client.Runtime.SetModel(ctx, &pb.SetModelRequest{Meta: newCommandMeta("", ""), Session: ref, Provider: provider, Model: model})
	if err != nil {
		return err
	}
	if err = confirmedReceipt(receipt); err != nil {
		return err
	}
	current, err = h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
	if err != nil || !modelBindingMatches(provider, model, current.GetProvider(), current.GetModel()) {
		return errors.New("Saved model was not confirmed; message was not sent")
	}
	return h.restoreReasoning(ctx, ref, binding, current)
}

func (h *Handler) restoreReasoning(ctx context.Context, ref *pb.SessionRef, binding map[string]any, current *pb.ModelState) error {
	effort := stringValue(binding, "effort")
	if effort == "" || current.GetEffort() == effort {
		return nil
	}
	receipt, err := h.client.Runtime.SetReasoning(ctx, &pb.SetReasoningRequest{Meta: newCommandMeta("", ""), Session: ref, Effort: effort})
	if err != nil {
		return err
	}
	if err := confirmedReceipt(receipt); err != nil {
		return err
	}
	readback, err := h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
	if err != nil || readback.GetEffort() != effort {
		return errors.New("Saved reasoning was not confirmed; message was not sent")
	}
	return nil
}
