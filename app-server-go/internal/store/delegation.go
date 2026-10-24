package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// DelegationEdge represents an explicit directed collaboration edge from parent to child session.
type DelegationEdge struct {
	ParentAgentID   string `json:"parent_agent_id"`
	ParentSessionID string `json:"parent_session_id"`
	ChildAgentID    string `json:"child_agent_id"`
	ChildSessionID  string `json:"child_session_id"`
	CreatedAt       string `json:"created_at,omitempty"`
}

// DelegationRoot represents the resolved root/origin session of a delegation tree.
type DelegationRoot struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// OpStage represents the lifecycle stage of a delegation operation.
type OpStage string

const (
	OpStageReserved  OpStage = "reserved"
	OpStageCreating  OpStage = "creating"
	OpStageCreated   OpStage = "created"
	OpStageSending   OpStage = "sending"
	OpStageCompleted OpStage = "completed"
	OpStageUncertain OpStage = "uncertain"
	OpStageFailed    OpStage = "failed"
)

// DelegationOperation tracks an idempotent delegation operation journal entry.
type DelegationOperation struct {
	OperationID     string         `json:"operation_id"`
	OperationType   string         `json:"operation_type"`
	CallerAgentID   string         `json:"caller_agent_id"`
	CallerSessionID string         `json:"caller_session_id"`
	TargetAgentID   string         `json:"target_agent_id"`
	TargetSessionID string         `json:"target_session_id,omitempty"`
	RequestHash     string         `json:"request_hash"`
	Stage           OpStage        `json:"stage"`
	RequestPayload  map[string]any `json:"request_payload"`
	ResultPayload   map[string]any `json:"result_payload,omitempty"`
	ErrorMessage    string         `json:"error_message,omitempty"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
}

// DelegationOperationUpdate specifies partial fields to update during a stage transition.
type DelegationOperationUpdate struct {
	TargetSessionID *string
	ResultPayload   map[string]any
	ErrorMessage    *string
}

// CreateDelegationEdge inserts a directed delegation edge from parent to child session.
// Enforces:
// 1. Non-empty identities.
// 2. No self-delegation.
// 3. Exactly one parent per child session.
// 4. No cycles in the delegation graph.
func (s *Store) CreateDelegationEdge(ctx context.Context, edge DelegationEdge) error {
	if edge.ParentAgentID == "" || edge.ParentSessionID == "" {
		return errors.New("parent agent_id and session_id are required")
	}
	if edge.ChildAgentID == "" || edge.ChildSessionID == "" {
		return errors.New("child agent_id and session_id are required")
	}
	if edge.ParentAgentID == edge.ChildAgentID && edge.ParentSessionID == edge.ChildSessionID {
		return errors.New("cannot create delegation edge to self")
	}

	return s.WithTx(ctx, func(tx *sql.Tx) error {
		// Check if child already has an existing parent
		var existingParentAid, existingParentSid string
		err := tx.QueryRowContext(ctx, "SELECT parent_agent_id, parent_session_id FROM delegation_edges WHERE child_agent_id=? AND child_session_id=?", edge.ChildAgentID, edge.ChildSessionID).Scan(&existingParentAid, &existingParentSid)
		if err == nil {
			if existingParentAid == edge.ParentAgentID && existingParentSid == edge.ParentSessionID {
				return nil // idempotent duplicate
			}
			return fmt.Errorf("child session %s::%s already has a delegation parent (%s::%s)", edge.ChildAgentID, edge.ChildSessionID, existingParentAid, existingParentSid)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check existing delegation parent: %w", err)
		}

		// Cycle detection: walk upward from parent. If child is encountered as ancestor, reject.
		currAid := edge.ParentAgentID
		currSid := edge.ParentSessionID
		visited := make(map[string]bool)
		for {
			key := currAid + "::" + currSid
			if key == edge.ChildAgentID+"::"+edge.ChildSessionID {
				return fmt.Errorf("delegation cycle detected: %s::%s is already an ancestor of %s::%s", edge.ChildAgentID, edge.ChildSessionID, edge.ParentAgentID, edge.ParentSessionID)
			}
			if visited[key] {
				break
			}
			visited[key] = true

			var pAid, pSid string
			qErr := tx.QueryRowContext(ctx, "SELECT parent_agent_id, parent_session_id FROM delegation_edges WHERE child_agent_id=? AND child_session_id=?", currAid, currSid).Scan(&pAid, &pSid)
			if errors.Is(qErr, sql.ErrNoRows) {
				break
			}
			if qErr != nil {
				return fmt.Errorf("detect delegation cycle: %w", qErr)
			}
			currAid, currSid = pAid, pSid
		}

		now := nowDatabaseTime()
		_, insErr := tx.ExecContext(ctx, `INSERT INTO delegation_edges
(child_agent_id, child_session_id, parent_agent_id, parent_session_id, created_at)
VALUES (?, ?, ?, ?, ?)`,
			edge.ChildAgentID, edge.ChildSessionID, edge.ParentAgentID, edge.ParentSessionID, now)
		if insErr != nil {
			return fmt.Errorf("insert delegation edge: %w", insErr)
		}
		return nil
	})
}

// GetDelegationParent returns the direct delegation parent edge of a child session.
func (s *Store) GetDelegationParent(ctx context.Context, childAgentID, childSessionID string) (DelegationEdge, bool, error) {
	row, err := s.QueryRow(ctx, "SELECT child_agent_id, child_session_id, parent_agent_id, parent_session_id, created_at FROM delegation_edges WHERE child_agent_id=? AND child_session_id=?", childAgentID, childSessionID)
	if err != nil {
		return DelegationEdge{}, false, err
	}
	if row == nil {
		return DelegationEdge{}, false, nil
	}
	createdAt, _ := wireTime(row["created_at"])
	return DelegationEdge{
		ChildAgentID:    fmt.Sprint(row["child_agent_id"]),
		ChildSessionID:  fmt.Sprint(row["child_session_id"]),
		ParentAgentID:   fmt.Sprint(row["parent_agent_id"]),
		ParentSessionID: fmt.Sprint(row["parent_session_id"]),
		CreatedAt:       createdAt,
	}, true, nil
}

// ListDelegationChildren returns direct delegation child edges of a parent session.
func (s *Store) ListDelegationChildren(ctx context.Context, parentAgentID, parentSessionID string) ([]DelegationEdge, error) {
	rows, err := s.QueryRows(ctx, "SELECT child_agent_id, child_session_id, parent_agent_id, parent_session_id, created_at FROM delegation_edges WHERE parent_agent_id=? AND parent_session_id=? ORDER BY created_at ASC", parentAgentID, parentSessionID)
	if err != nil {
		return nil, err
	}
	edges := make([]DelegationEdge, 0, len(rows))
	for _, row := range rows {
		createdAt, _ := wireTime(row["created_at"])
		edges = append(edges, DelegationEdge{
			ChildAgentID:    fmt.Sprint(row["child_agent_id"]),
			ChildSessionID:  fmt.Sprint(row["child_session_id"]),
			ParentAgentID:   fmt.Sprint(row["parent_agent_id"]),
			ParentSessionID: fmt.Sprint(row["parent_session_id"]),
			CreatedAt:       createdAt,
		})
	}
	return edges, nil
}

// DeleteDelegationEdge removes the delegation edge for a child session.
func (s *Store) DeleteDelegationEdge(ctx context.Context, childAgentID, childSessionID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM delegation_edges WHERE child_agent_id=? AND child_session_id=?", childAgentID, childSessionID)
	return err
}

// ResolveDelegationRoot climbs the delegation graph from agentID::sessionID to find the root constellation session.
func (s *Store) ResolveDelegationRoot(ctx context.Context, agentID, sessionID string) (DelegationRoot, error) {
	currAid := agentID
	currSid := sessionID
	visited := make(map[string]bool)

	for {
		key := currAid + "::" + currSid
		if visited[key] {
			break
		}
		visited[key] = true

		var pAid, pSid string
		err := s.db.QueryRowContext(ctx, "SELECT parent_agent_id, parent_session_id FROM delegation_edges WHERE child_agent_id=? AND child_session_id=?", currAid, currSid).Scan(&pAid, &pSid)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return DelegationRoot{}, fmt.Errorf("resolve delegation root: %w", err)
		}
		currAid, currSid = pAid, pSid
	}

	return DelegationRoot{AgentID: currAid, SessionID: currSid}, nil
}

// ListDelegationDescendants returns all transitive child edges in the delegation subtree originating at rootAgentID::rootSessionID.
func (s *Store) ListDelegationDescendants(ctx context.Context, rootAgentID, rootSessionID string) ([]DelegationEdge, error) {
	var allEdges []DelegationEdge
	queue := [][2]string{{rootAgentID, rootSessionID}}
	visited := make(map[string]bool)

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		key := curr[0] + "::" + curr[1]
		if visited[key] {
			continue
		}
		visited[key] = true

		children, err := s.ListDelegationChildren(ctx, curr[0], curr[1])
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			allEdges = append(allEdges, child)
			queue = append(queue, [2]string{child.ChildAgentID, child.ChildSessionID})
		}
	}
	return allEdges, nil
}

// ReserveDelegationOperation reserves an operation journal entry.
// Returns:
// - (op, true, nil): successfully reserved as winner.
// - (existing, false, nil): already exists with identical caller and request_hash (idempotent duplicate).
// - (empty, false, error): conflict (different caller or different request_hash), or DB error.
func (s *Store) ReserveDelegationOperation(ctx context.Context, op DelegationOperation) (DelegationOperation, bool, error) {
	if op.OperationID == "" {
		return DelegationOperation{}, false, errors.New("operation_id is required")
	}
	if op.CallerAgentID == "" || op.CallerSessionID == "" {
		return DelegationOperation{}, false, errors.New("caller agent_id and session_id are required")
	}
	if op.TargetAgentID == "" {
		return DelegationOperation{}, false, errors.New("target_agent_id is required")
	}
	if op.RequestHash == "" {
		return DelegationOperation{}, false, errors.New("request_hash is required")
	}

	if op.Stage == "" {
		op.Stage = OpStageReserved
	}

	reqPayloadJSON, err := encodeJSON(op.RequestPayload)
	if err != nil {
		return DelegationOperation{}, false, fmt.Errorf("encode request_payload: %w", err)
	}

	var resPayloadJSON any
	if op.ResultPayload != nil {
		resJSON, mErr := encodeJSON(op.ResultPayload)
		if mErr != nil {
			return DelegationOperation{}, false, fmt.Errorf("encode result_payload: %w", mErr)
		}
		resPayloadJSON = resJSON
	}

	now := nowDatabaseTime()

	var winner bool
	var existing DelegationOperation

	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		res, execErr := tx.ExecContext(ctx, `INSERT INTO delegation_operations
(operation_id, operation_type, caller_agent_id, caller_session_id, target_agent_id, target_session_id, request_hash, stage, request_payload, result_payload, error_message, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(operation_id) DO NOTHING`,
			op.OperationID, op.OperationType, op.CallerAgentID, op.CallerSessionID,
			op.TargetAgentID, nullableString(op.TargetSessionID), op.RequestHash, string(op.Stage),
			reqPayloadJSON, resPayloadJSON, nullableString(op.ErrorMessage), now, now)
		if execErr != nil {
			return fmt.Errorf("insert delegation operation: %w", execErr)
		}

		rows, rErr := res.RowsAffected()
		if rErr != nil {
			return fmt.Errorf("check rows affected: %w", rErr)
		}

		if rows == 1 {
			winner = true
			op.CreatedAt = now
			op.UpdatedAt = now
			return nil
		}

		// Existing operation: query and verify caller and request_hash
		var exOpID, exType, exCallerAid, exCallerSid, exTargetAid string
		var exTargetSid, exReqHash, exStage sql.NullString
		var exReqPayload, exResPayload, exErrMsg sql.NullString
		var exCreatedAt, exUpdatedAt string

		qErr := tx.QueryRowContext(ctx, `SELECT operation_id, operation_type, caller_agent_id, caller_session_id, target_agent_id, target_session_id, request_hash, stage, request_payload, result_payload, error_message, created_at, updated_at
FROM delegation_operations WHERE operation_id=?`, op.OperationID).Scan(
			&exOpID, &exType, &exCallerAid, &exCallerSid, &exTargetAid,
			&exTargetSid, &exReqHash, &exStage, &exReqPayload, &exResPayload, &exErrMsg, &exCreatedAt, &exUpdatedAt,
		)
		if qErr != nil {
			return fmt.Errorf("fetch existing delegation operation: %w", qErr)
		}

		if exCallerAid != op.CallerAgentID || exCallerSid != op.CallerSessionID {
			return fmt.Errorf("operation %s caller conflict: registered by %s::%s, requested by %s::%s", op.OperationID, exCallerAid, exCallerSid, op.CallerAgentID, op.CallerSessionID)
		}

		if exReqHash.String != op.RequestHash {
			return fmt.Errorf("operation %s payload conflict: request hash mismatch", op.OperationID)
		}

		var parsedReqPayload, parsedResPayload map[string]any
		if exReqPayload.Valid && exReqPayload.String != "" {
			_ = json.Unmarshal([]byte(exReqPayload.String), &parsedReqPayload)
		}
		if exResPayload.Valid && exResPayload.String != "" {
			_ = json.Unmarshal([]byte(exResPayload.String), &parsedResPayload)
		}

		createdAtWire, _ := wireTime(exCreatedAt)
		updatedAtWire, _ := wireTime(exUpdatedAt)

		existing = DelegationOperation{
			OperationID:     exOpID,
			OperationType:   exType,
			CallerAgentID:   exCallerAid,
			CallerSessionID: exCallerSid,
			TargetAgentID:   exTargetAid,
			TargetSessionID: exTargetSid.String,
			RequestHash:     exReqHash.String,
			Stage:           OpStage(exStage.String),
			RequestPayload:  parsedReqPayload,
			ResultPayload:   parsedResPayload,
			ErrorMessage:    exErrMsg.String,
			CreatedAt:       createdAtWire,
			UpdatedAt:       updatedAtWire,
		}
		return nil
	})

	if err != nil {
		return DelegationOperation{}, false, err
	}
	if winner {
		return op, true, nil
	}
	return existing, false, nil
}

// GetDelegationOperation retrieves an operation by its operation_id.
func (s *Store) GetDelegationOperation(ctx context.Context, operationID string) (DelegationOperation, bool, error) {
	row, err := s.QueryRow(ctx, `SELECT operation_id, operation_type, caller_agent_id, caller_session_id, target_agent_id, target_session_id, request_hash, stage, request_payload, result_payload, error_message, created_at, updated_at
FROM delegation_operations WHERE operation_id=?`, operationID)
	if err != nil {
		return DelegationOperation{}, false, err
	}
	if row == nil {
		return DelegationOperation{}, false, nil
	}

	var parsedReqPayload, parsedResPayload map[string]any
	if raw, ok := row["request_payload"].(string); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &parsedReqPayload)
	}
	if raw, ok := row["result_payload"].(string); ok && raw != "" {
		_ = json.Unmarshal([]byte(raw), &parsedResPayload)
	}

	createdAtWire, _ := wireTime(row["created_at"])
	updatedAtWire, _ := wireTime(row["updated_at"])

	targetSid := ""
	if val := row["target_session_id"]; val != nil {
		targetSid = fmt.Sprint(val)
	}
	errMsg := ""
	if val := row["error_message"]; val != nil {
		errMsg = fmt.Sprint(val)
	}

	return DelegationOperation{
		OperationID:     fmt.Sprint(row["operation_id"]),
		OperationType:   fmt.Sprint(row["operation_type"]),
		CallerAgentID:   fmt.Sprint(row["caller_agent_id"]),
		CallerSessionID: fmt.Sprint(row["caller_session_id"]),
		TargetAgentID:   fmt.Sprint(row["target_agent_id"]),
		TargetSessionID: targetSid,
		RequestHash:     fmt.Sprint(row["request_hash"]),
		Stage:           OpStage(fmt.Sprint(row["stage"])),
		RequestPayload:  parsedReqPayload,
		ResultPayload:   parsedResPayload,
		ErrorMessage:    errMsg,
		CreatedAt:       createdAtWire,
		UpdatedAt:       updatedAtWire,
	}, true, nil
}

// TransitionDelegationOperation performs an atomic Compare-And-Swap (CAS) transition of an operation's stage.
func (s *Store) TransitionDelegationOperation(ctx context.Context, operationID string, expectedStage, newStage OpStage, update *DelegationOperationUpdate) error {
	now := nowDatabaseTime()

	var targetSidParam any
	var resPayloadParam any
	var errParam any

	if update != nil {
		if update.TargetSessionID != nil {
			targetSidParam = *update.TargetSessionID
		}
		if update.ResultPayload != nil {
			jsonVal, err := encodeJSON(update.ResultPayload)
			if err != nil {
				return fmt.Errorf("encode result_payload: %w", err)
			}
			resPayloadParam = jsonVal
		}
		if update.ErrorMessage != nil {
			errParam = *update.ErrorMessage
		}
	}

	res, err := s.db.ExecContext(ctx, `UPDATE delegation_operations
SET stage=?,
    target_session_id = CASE WHEN ? IS NOT NULL THEN ? ELSE target_session_id END,
    result_payload = CASE WHEN ? IS NOT NULL THEN ? ELSE result_payload END,
    error_message = CASE WHEN ? IS NOT NULL THEN ? ELSE error_message END,
    updated_at=?
WHERE operation_id=? AND stage=?`,
		string(newStage),
		targetSidParam, targetSidParam,
		resPayloadParam, resPayloadParam,
		errParam, errParam,
		now, operationID, string(expectedStage))
	if err != nil {
		return fmt.Errorf("execute CAS transition: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check CAS transition affected: %w", err)
	}
	if affected == 1 {
		return nil
	}

	// CAS failed: query current row for detailed error diagnostics
	curr, found, qErr := s.GetDelegationOperation(ctx, operationID)
	if qErr != nil {
		return fmt.Errorf("CAS failed on operation %s; read diagnostic failed: %w", operationID, qErr)
	}
	if !found {
		return fmt.Errorf("operation %s not found", operationID)
	}
	return fmt.Errorf("operation %s CAS transition failed: expected stage %s, but current stage is %s", operationID, expectedStage, curr.Stage)
}

// FailDelegationOperation marks an operation as failed with an error message.
func (s *Store) FailDelegationOperation(ctx context.Context, operationID string, errorMessage string) error {
	now := nowDatabaseTime()
	_, err := s.db.ExecContext(ctx, `UPDATE delegation_operations
SET stage=?, error_message=?, updated_at=?
WHERE operation_id=?`, string(OpStageFailed), errorMessage, now, operationID)
	return err
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
