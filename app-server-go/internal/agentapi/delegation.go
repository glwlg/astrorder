package agentapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// CanonicalDelegateRequest ensures deterministic JSON serialization for request hashing.
type CanonicalDelegateRequest struct {
	CallerAgentID      string `json:"caller_agent_id"`
	CallerConnectionID string `json:"caller_connection_id"`
	CallerProfileName  string `json:"caller_profile_name"`
	CallerRuntime      string `json:"caller_runtime"`
	CallerSessionID    string `json:"caller_session_id"`
	Effort             string `json:"effort"`
	Model              string `json:"model"`
	Prompt             string `json:"prompt"`
	Provider           string `json:"provider"`
	TargetAgentID      string `json:"target_agent_id"`
	Title              string `json:"title"`
	Workspace          string `json:"workspace"`
}

// DelegateInput is the input payload for sessions.delegate capability.
type DelegateInput struct {
	OperationID string  `json:"operation_id"`
	AgentID     string  `json:"agent_id"`
	Title       string  `json:"title"`
	Workspace   string  `json:"workspace"`
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	Effort      string  `json:"effort"`
	Prompt      string  `json:"prompt"`
	Caller      *Caller `json:"caller"`
}

func computeRequestHash(input *DelegateInput, caller *Caller) string {
	canonical := CanonicalDelegateRequest{
		CallerAgentID:      caller.AgentID,
		CallerConnectionID: caller.ConnectionID,
		CallerProfileName:  caller.ProfileName,
		CallerRuntime:      caller.Runtime,
		CallerSessionID:    caller.SessionID, // Exact native ID, never trimmed
		Effort:             input.Effort,
		Model:              input.Model,
		Prompt:             input.Prompt,
		Provider:           input.Provider,
		TargetAgentID:      input.AgentID,
		Title:              input.Title,
		Workspace:          input.Workspace,
	}
	data, _ := json.Marshal(canonical)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ParseDelegateInput validates and extracts DelegateInput from payload.
func ParseDelegateInput(payload map[string]any) (*DelegateInput, error) {
	if payload == nil {
		return nil, NewAgentApiError("invalid_input", "payload is required")
	}

	rawOpID, ok := payload["operation_id"].(string)
	if !ok || rawOpID == "" || hasWhitespace(rawOpID) {
		return nil, NewAgentApiError("invalid_input", "operation_id must be a non-empty string without whitespace")
	}

	rawAgentID, ok := payload["agent_id"].(string)
	if !ok || rawAgentID == "" || hasWhitespace(rawAgentID) {
		return nil, NewAgentApiError("invalid_input", "agent_id must be a non-empty string without whitespace")
	}

	rawPrompt, ok := payload["prompt"].(string)
	if !ok || rawPrompt == "" {
		return nil, NewAgentApiError("invalid_input", "prompt is required and must be a non-empty string")
	}

	caller, err := ParseCaller(payload["caller"])
	if err != nil {
		return nil, err
	}

	parseOptionalString := func(key string) (string, error) {
		if raw, exists := payload[key]; exists {
			if raw == nil {
				return "", NewAgentApiError("invalid_input", fmt.Sprintf("%s cannot be null if provided", key))
			}
			s, ok := raw.(string)
			if !ok {
				return "", NewAgentApiError("invalid_input", fmt.Sprintf("%s must be a string if provided", key))
			}
			return s, nil
		}
		return "", nil
	}

	title, err := parseOptionalString("title")
	if err != nil {
		return nil, err
	}
	workspace, err := parseOptionalString("workspace")
	if err != nil {
		return nil, err
	}
	model, err := parseOptionalString("model")
	if err != nil {
		return nil, err
	}
	provider, err := parseOptionalString("provider")
	if err != nil {
		return nil, err
	}
	effort, err := parseOptionalString("effort")
	if err != nil {
		return nil, err
	}

	return &DelegateInput{
		OperationID: rawOpID,
		AgentID:     rawAgentID,
		Title:       title,
		Workspace:   workspace,
		Model:       model,
		Provider:    provider,
		Effort:      effort,
		Prompt:      rawPrompt,
		Caller:      caller,
	}, nil
}

// invokeSessionsDelegate implements the sessions.delegate HTTP capability.
func (h *Handler) invokeSessionsDelegate(ctx context.Context, payload map[string]any) (any, error) {
	input, err := ParseDelegateInput(payload)
	if err != nil {
		return nil, err
	}

	if h.options.Store == nil {
		return nil, errors.New("store is required")
	}

	// 1. Validate caller identity against store & runtime daemon
	callerSession, vErr := h.ValidateCaller(ctx, input.Caller)
	if vErr != nil {
		return nil, vErr
	}

	// 2. Resolve target agent
	targetAgent, aErr := h.options.Store.GetAgent(ctx, input.AgentID)
	if aErr != nil {
		return nil, fmt.Errorf("failed to get target agent '%s': %w", input.AgentID, aErr)
	}
	if targetAgent == nil {
		return nil, NewAgentApiError("not_found", fmt.Sprintf("target agent '%s' not found", input.AgentID))
	}

	// 3. Normalize workspace and title
	workspace := input.Workspace
	if workspace == "" && input.AgentID == input.Caller.AgentID {
		// The verified caller's workspace is meaningful on the same configured
		// Agent. A different Agent may run on another machine or runtime.
		workspace, _ = callerSession["workspace"].(string)
	}
	if workspace == "" {
		workspace = h.options.DefaultWorkspace
	}
	if workspace == "" {
		return nil, NewAgentApiError("invalid_input", "workspace is required and no default workspace configured")
	}
	title := input.Title
	if title == "" {
		title = fmt.Sprintf("Delegated from %s", input.Caller.SessionID)
	}

	reqHash := computeRequestHash(input, input.Caller)

	rawReqPayload := map[string]any{
		"agent_id":  input.AgentID,
		"title":     title,
		"workspace": workspace,
		"prompt":    input.Prompt,
		"model":     input.Model,
		"provider":  input.Provider,
		"effort":    input.Effort,
	}

	// 4. Reserve operation in store journal (CAS / Single winner)
	opRecord := store.DelegationOperation{
		OperationID:     input.OperationID,
		OperationType:   "sessions.delegate",
		CallerAgentID:   input.Caller.AgentID,
		CallerSessionID: input.Caller.SessionID,
		TargetAgentID:   input.AgentID,
		RequestHash:     reqHash,
		Stage:           store.OpStageReserved,
		RequestPayload:  rawReqPayload,
	}

	existingOp, reserved, rErr := h.options.Store.ReserveDelegationOperation(ctx, opRecord)
	if rErr != nil {
		return nil, fmt.Errorf("failed to reserve operation '%s': %w", input.OperationID, rErr)
	}

	if !reserved {
		// Operation already exists: check conflict or idempotency
		if existingOp.RequestHash != reqHash {
			return nil, NewAgentApiError("conflict", fmt.Sprintf("operation_id '%s' was already registered with different parameters", input.OperationID))
		}
		if existingOp.CallerAgentID != input.Caller.AgentID || existingOp.CallerSessionID != input.Caller.SessionID {
			return nil, NewAgentApiError("conflict", "operation_id was registered by a different caller")
		}

		if existingOp.Stage == store.OpStageCompleted {
			return existingOp.ResultPayload, nil
		}

		if existingOp.Stage == store.OpStageFailed {
			resp := map[string]any{
				"ok":              false,
				"operation_id":    existingOp.OperationID,
				"stage":           string(store.OpStageFailed),
				"target_agent_id": existingOp.TargetAgentID,
				"in_progress":     false,
				"error":           existingOp.ErrorMessage,
			}
			if existingOp.TargetSessionID != "" {
				resp["target_session_id"] = existingOp.TargetSessionID
			}
			return resp, NewAgentApiError("failed", fmt.Sprintf("operation '%s' previously failed, will not retry: %s", existingOp.OperationID, existingOp.ErrorMessage))
		}

		if existingOp.Stage == store.OpStageUncertain {
			resp := map[string]any{
				"ok":              false,
				"operation_id":    existingOp.OperationID,
				"stage":           string(existingOp.Stage),
				"target_agent_id": existingOp.TargetAgentID,
				"uncertain":       true,
				"error":           existingOp.ErrorMessage,
			}
			if existingOp.TargetSessionID != "" {
				resp["target_session_id"] = existingOp.TargetSessionID
			}
			return resp, NewAgentApiError("uncertain", fmt.Sprintf("operation '%s' state is uncertain from previous attempt, will not retry: %s", existingOp.OperationID, existingOp.ErrorMessage))
		}

		return map[string]any{
			"ok":              true,
			"operation_id":    existingOp.OperationID,
			"stage":           string(existingOp.Stage),
			"target_agent_id": existingOp.TargetAgentID,
			"in_progress":     true,
		}, nil
	}

	// 5. Stage 1: Transition to OpStageCreating
	if err := h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageReserved, store.OpStageCreating, nil); err != nil {
		return nil, fmt.Errorf("failed to transition operation to creating: %w", err)
	}

	target, rErr := h.resolveRuntimeTarget(ctx, targetAgent)
	if rErr != nil {
		errMsg := rErr.Error()
		_ = h.options.Store.FailDelegationOperation(ctx, input.OperationID, errMsg)
		return nil, NewAgentApiError("runtime_error", rErr.Error())
	}

	if h.options.Client == nil || h.options.Client.Session == nil {
		errMsg := "daemon client is required"
		_ = h.options.Store.FailDelegationOperation(ctx, input.OperationID, errMsg)
		return nil, NewAgentApiError("unavailable", errMsg)
	}

	createReq := &sessiondv1.CreateSessionRequest{
		Meta:   newCommandMeta(input.OperationID, ""),
		Target: target,
	}

	// Strictly independent child session: NO ParentSessionId or fork!
	targetKind, _ := targetAgent["kind"].(string)
	switch strings.ToLower(targetKind) {
	case "codex":
		codexOpt := &sessiondv1.CodexCreateOptions{
			Cwd: workspace,
		}
		if input.Model != "" {
			codexOpt.Model = &input.Model
		}
		if input.Effort != "" {
			codexOpt.Effort = &input.Effort
		}
		createReq.Options = &sessiondv1.CreateSessionRequest_Codex{Codex: codexOpt}

	case "hermes":
		hermesOpt := &sessiondv1.HermesCreateOptions{}
		if input.Model != "" {
			hermesOpt.Model = &input.Model
		}
		if input.Provider != "" {
			hermesOpt.Provider = &input.Provider
		}
		if input.Effort != "" {
			hermesOpt.Effort = &input.Effort
		}
		cwdJson := fmt.Sprintf(`{"workspace":%q,"cwd":%q}`, workspace, workspace)
		hermesOpt.NativeOptions = &sessiondv1.NativeJson{
			Schema:   "astrorder.hermes.create.options.v1",
			Utf8Json: []byte(cwdJson),
		}
		createReq.Options = &sessiondv1.CreateSessionRequest_Hermes{Hermes: hermesOpt}

	case "antigravity":
		createReq.Options = &sessiondv1.CreateSessionRequest_Antigravity{
			Antigravity: &sessiondv1.AntigravityCreateOptions{Cwd: workspace},
		}

	case "grok":
		createReq.Options = &sessiondv1.CreateSessionRequest_Grok{
			Grok: &sessiondv1.GrokCreateOptions{Cwd: workspace},
		}

	default:
		errMsg := fmt.Sprintf("unsupported agent kind '%s'", targetKind)
		_ = h.options.Store.FailDelegationOperation(ctx, input.OperationID, errMsg)
		return nil, NewAgentApiError("invalid_input", errMsg)
	}

	createReceipt, createErr := h.options.Client.Session.CreateSession(ctx, createReq)
	if createErr != nil {
		// Network/RPC failure: state is UNCERTAIN, do NOT retry blindly
		errMsg := createErr.Error()
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreating, store.OpStageUncertain, &store.DelegationOperationUpdate{
			ErrorMessage: &errMsg,
		})
		return nil, NewAgentApiError("uncertain", fmt.Sprintf("session creation outcome is uncertain: %v", createErr))
	}

	var childSID string
	if createReceipt != nil && createReceipt.Session != nil && createReceipt.Session.SessionId != "" {
		childSID = createReceipt.Session.SessionId
	}

	if createReceipt == nil || createReceipt.OperationState == sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN {
		errMsg := "daemon returned uncertain operation state"
		update := &store.DelegationOperationUpdate{
			ErrorMessage: &errMsg,
		}
		if childSID != "" {
			update.TargetSessionID = &childSID
		}
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreating, store.OpStageUncertain, update)
		return nil, NewAgentApiError("uncertain", errMsg)
	}

	if !createReceipt.Accepted {
		errMsg := "daemon rejected session creation (Accepted=false)"
		if childSID != "" {
			_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreating, store.OpStageUncertain, &store.DelegationOperationUpdate{
				TargetSessionID: &childSID,
				ErrorMessage:    &errMsg,
			})
			return nil, NewAgentApiError("uncertain", errMsg)
		}
		_ = h.options.Store.FailDelegationOperation(ctx, input.OperationID, errMsg)
		return nil, NewAgentApiError("command_rejected", errMsg)
	}

	if createReceipt.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED || childSID == "" {
		errMsg := "daemon rejected session creation"
		if childSID != "" {
			_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreating, store.OpStageUncertain, &store.DelegationOperationUpdate{
				TargetSessionID: &childSID,
				ErrorMessage:    &errMsg,
			})
			return nil, NewAgentApiError("uncertain", errMsg)
		}
		_ = h.options.Store.FailDelegationOperation(ctx, input.OperationID, errMsg)
		return nil, NewAgentApiError("command_rejected", errMsg)
	}

	// Verify agent / connection receipt match strictly
	isForeignAgent := createReceipt.Session.AgentId == nil || *createReceipt.Session.AgentId != input.AgentID
	targetConnID, _ := targetAgent["connection_id"].(string)
	targetConnNorm := NormalizeConnectionID(targetConnID)
	isForeignConn := false
	if targetConnNorm != "local" {
		isForeignConn = createReceipt.Session.ConnectionId == nil || *createReceipt.Session.ConnectionId != targetConnNorm
	}

	if isForeignAgent || isForeignConn {
		errMsg := fmt.Sprintf("daemon returned foreign agent_id (%v) or connection_id (%v) for requested %s/%s",
			createReceipt.Session.AgentId, createReceipt.Session.ConnectionId, input.AgentID, targetConnNorm)
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreating, store.OpStageUncertain, &store.DelegationOperationUpdate{
			TargetSessionID: &childSID,
			ErrorMessage:    &errMsg,
		})
		return nil, NewAgentApiError("uncertain", errMsg)
	}

	childStatus := "idle"
	switch createReceipt.Status {
	case sessiondv1.SessionStatus_SESSION_STATUS_RUNNING:
		childStatus = "running"
	case sessiondv1.SessionStatus_SESSION_STATUS_IDLE:
		childStatus = "idle"
	case sessiondv1.SessionStatus_SESSION_STATUS_WAITING_APPROVAL:
		childStatus = "waiting_approval"
	case sessiondv1.SessionStatus_SESSION_STATUS_ERROR:
		childStatus = "error"
	}

	childKey := input.AgentID + "::" + childSID

	// Persist child session to store (WITHOUT handoff_from)
	childData := map[string]any{
		"id":            childSID,
		"agent_id":      input.AgentID,
		"title":         title,
		"workspace":     workspace,
		"status":        childStatus,
		"source_id":     input.AgentID,
		"connection_id": targetAgent["connection_id"],
		"control_state": "owned",
		"ephemeral":     false,
		"updated_at":    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, sErr := h.options.Store.UpsertSession(ctx, childData); sErr != nil {
		errMsg := fmt.Sprintf("failed to persist child session: %v", sErr)
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreating, store.OpStageUncertain, &store.DelegationOperationUpdate{
			TargetSessionID: &childSID,
			ErrorMessage:    &errMsg,
		})
		return nil, fmt.Errorf("failed to persist child session: %w", sErr)
	}

	// Persist explicit delegation edge in store
	edge := store.DelegationEdge{
		ParentAgentID:   input.Caller.AgentID,
		ParentSessionID: input.Caller.SessionID,
		ChildAgentID:    input.AgentID,
		ChildSessionID:  childSID,
	}
	if edgeErr := h.options.Store.CreateDelegationEdge(ctx, edge); edgeErr != nil {
		errMsg := fmt.Sprintf("failed to persist delegation edge: %v", edgeErr)
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreating, store.OpStageUncertain, &store.DelegationOperationUpdate{
			TargetSessionID: &childSID,
			ErrorMessage:    &errMsg,
		})
		return nil, fmt.Errorf("failed to record delegation edge: %w", edgeErr)
	}

	// Read back canonical child session with joined delegation fields from store and publish
	canonicalChild, getErr := h.options.Store.GetSession(ctx, input.AgentID, childSID)
	if getErr == nil && canonicalChild != nil {
		if h.options.Publisher != nil {
			_ = h.options.Publisher.Publish(ctx, "session.upsert", canonicalChild)
		}
	}

	// Transition to OpStageCreated
	if err := h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreating, store.OpStageCreated, &store.DelegationOperationUpdate{
		TargetSessionID: &childSID,
	}); err != nil {
		return nil, fmt.Errorf("failed to transition operation to created: %w", err)
	}

	// 6. Stage 2: Initial dispatch (send prompt to child session)
	if err := h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageCreated, store.OpStageSending, nil); err != nil {
		return nil, fmt.Errorf("failed to transition operation to sending: %w", err)
	}

	sendOpID := input.OperationID + "-send"
	connID, _ := targetAgent["connection_id"].(string)

	sendReceipt, sendErr := h.options.Client.Session.Send(ctx, &sessiondv1.SendRequest{
		Meta:    newCommandMeta(sendOpID, sendOpID),
		Session: buildSessionRef(input.AgentID, childSID, connID),
		Input: []*sessiondv1.InputPart{
			{Part: &sessiondv1.InputPart_Text{Text: &sessiondv1.TextInput{Text: input.Prompt}}},
		},
	})

	if sendErr != nil {
		errMsg := sendErr.Error()
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageSending, store.OpStageUncertain, &store.DelegationOperationUpdate{
			TargetSessionID: &childSID,
			ErrorMessage:    &errMsg,
		})
		return map[string]any{
			"ok":                false,
			"operation_id":      input.OperationID,
			"stage":             string(store.OpStageUncertain),
			"target_agent_id":   input.AgentID,
			"target_session_id": childSID,
			"uncertain":         true,
			"error":             sendErr.Error(),
		}, NewAgentApiError("uncertain", fmt.Sprintf("initial prompt dispatch outcome is uncertain: %v", sendErr))
	}

	if sendReceipt == nil || sendReceipt.OperationState == sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN {
		errMsg := "daemon returned uncertain send receipt"
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageSending, store.OpStageUncertain, &store.DelegationOperationUpdate{
			TargetSessionID: &childSID,
			ErrorMessage:    &errMsg,
		})
		return map[string]any{
			"ok":                false,
			"operation_id":      input.OperationID,
			"stage":             string(store.OpStageUncertain),
			"target_agent_id":   input.AgentID,
			"target_session_id": childSID,
			"uncertain":         true,
			"error":             errMsg,
		}, NewAgentApiError("uncertain", errMsg)
	}

	isSendForeign := sendReceipt.Session == nil ||
		sendReceipt.Session.SessionId != childSID ||
		(sendReceipt.Session.AgentId != nil && *sendReceipt.Session.AgentId != input.AgentID) ||
		(targetConnNorm != "local" && sendReceipt.Session.ConnectionId != nil && *sendReceipt.Session.ConnectionId != targetConnNorm)

	if isSendForeign {
		errMsg := "daemon returned foreign receipt on send"
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageSending, store.OpStageUncertain, &store.DelegationOperationUpdate{
			TargetSessionID: &childSID,
			ErrorMessage:    &errMsg,
		})
		return map[string]any{
			"ok":                false,
			"operation_id":      input.OperationID,
			"stage":             string(store.OpStageUncertain),
			"target_agent_id":   input.AgentID,
			"target_session_id": childSID,
			"uncertain":         true,
			"error":             errMsg,
		}, NewAgentApiError("uncertain", errMsg)
	}

	if !sendReceipt.Accepted {
		errMsg := "daemon rejected prompt send (Accepted=false)"
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageSending, store.OpStageUncertain, &store.DelegationOperationUpdate{
			TargetSessionID: &childSID,
			ErrorMessage:    &errMsg,
		})
		return map[string]any{
			"ok":                false,
			"operation_id":      input.OperationID,
			"stage":             string(store.OpStageUncertain),
			"target_agent_id":   input.AgentID,
			"target_session_id": childSID,
			"uncertain":         true,
			"error":             errMsg,
		}, NewAgentApiError("command_rejected", errMsg)
	}

	if sendReceipt.OperationState != sessiondv1.OperationState_OPERATION_STATE_COMPLETED {
		errMsg := fmt.Sprintf("daemon returned non-completed operation state in send receipt: %v", sendReceipt.OperationState)
		_ = h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageSending, store.OpStageUncertain, &store.DelegationOperationUpdate{
			TargetSessionID: &childSID,
			ErrorMessage:    &errMsg,
		})
		return map[string]any{
			"ok":                false,
			"operation_id":      input.OperationID,
			"stage":             string(store.OpStageUncertain),
			"target_agent_id":   input.AgentID,
			"target_session_id": childSID,
			"uncertain":         true,
			"error":             errMsg,
		}, NewAgentApiError("uncertain", errMsg)
	}

	receiptMap := map[string]any{
		"accepted":        sendReceipt.Accepted,
		"operation_state": sendReceipt.OperationState.String(),
		"status":          sendReceipt.Status.String(),
	}

	finalResult := map[string]any{
		"ok":           true,
		"operation_id": input.OperationID,
		"stage":        string(store.OpStageCompleted),
		"session": map[string]any{
			"key":                          childKey,
			"id":                           childSID,
			"agent_id":                     input.AgentID,
			"title":                        title,
			"workspace":                    workspace,
			"status":                       childStatus,
			"delegation_parent_agent_id":   input.Caller.AgentID,
			"delegation_parent_session_id": input.Caller.SessionID,
		},
		"receipt": receiptMap,
	}

	// Transition to OpStageCompleted
	if err := h.options.Store.TransitionDelegationOperation(ctx, input.OperationID, store.OpStageSending, store.OpStageCompleted, &store.DelegationOperationUpdate{
		ResultPayload: finalResult,
	}); err != nil {
		return nil, fmt.Errorf("failed to transition operation to completed: %w", err)
	}

	return finalResult, nil
}

// invokeSessionsOperation implements the sessions.operation HTTP capability.
func (h *Handler) invokeSessionsOperation(ctx context.Context, payload map[string]any) (any, error) {
	if payload == nil {
		return nil, NewAgentApiError("invalid_input", "payload is required")
	}

	opID, _ := payload["operation_id"].(string)
	if opID == "" {
		return nil, NewAgentApiError("invalid_input", "operation_id is required")
	}

	caller, err := ParseCaller(payload["caller"])
	if err != nil {
		return nil, err
	}

	_, vErr := h.ValidateCaller(ctx, caller)
	if vErr != nil {
		return nil, vErr
	}

	if h.options.Store == nil {
		return nil, errors.New("store is required")
	}

	op, found, err := h.options.Store.GetDelegationOperation(ctx, opID)
	if err != nil {
		return nil, fmt.Errorf("failed to query operation '%s': %w", opID, err)
	}
	if !found {
		return nil, NewAgentApiError("not_found", fmt.Sprintf("operation '%s' not found", opID))
	}

	// Caller must match
	if op.CallerAgentID != caller.AgentID || op.CallerSessionID != caller.SessionID {
		return nil, NewAgentApiError("forbidden", "operation does not belong to caller")
	}

	resp := map[string]any{
		"ok":              op.Stage == store.OpStageCompleted,
		"operation_id":    op.OperationID,
		"stage":           string(op.Stage),
		"target_agent_id": op.TargetAgentID,
		"created_at":      op.CreatedAt,
		"updated_at":      op.UpdatedAt,
	}

	if op.TargetSessionID != "" {
		resp["target_session_id"] = op.TargetSessionID
		resp["session"] = map[string]any{
			"key":                          op.TargetAgentID + "::" + op.TargetSessionID,
			"id":                           op.TargetSessionID,
			"agent_id":                     op.TargetAgentID,
			"delegation_parent_agent_id":   op.CallerAgentID,
			"delegation_parent_session_id": op.CallerSessionID,
		}
	}

	if op.ResultPayload != nil {
		for k, v := range op.ResultPayload {
			resp[k] = v
		}
	}

	if op.ErrorMessage != "" {
		resp["error"] = op.ErrorMessage
	}

	if op.Stage == store.OpStageUncertain {
		resp["uncertain"] = true
	}

	return resp, nil
}
