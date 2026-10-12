package protocol

import "fmt"

// OperationLookup identifies one row in the legacy durable operation ledger.
// Nil scope pointers preserve a legacy absent field, which operationScope
// canonicalizes as JSON null; a non-nil pointer preserves the exact string,
// including an explicitly present empty string.
type OperationLookup struct {
	Action       string
	OperationID  string
	AgentType    *string
	ConnectionID *string
	SessionID    *string
}

type StoredOperationState string

const (
	StoredOperationStateMissing   StoredOperationState = "missing"
	StoredOperationStateUncertain StoredOperationState = "uncertain"
	StoredOperationStateCompleted StoredOperationState = "completed"
)

type StoredOperation struct {
	DaemonID           string
	State              StoredOperationState
	LegacyResponseJSON []byte
}

// ReadStoredOperation reads the same ledger row used by operation.read. It does
// not reserve, complete, retry, execute, or otherwise mutate an operation.
func (d *Daemon) ReadStoredOperation(lookup OperationLookup) (StoredOperation, error) {
	if lookup.OperationID == "" || len(lookup.OperationID) > 256 || lookup.Action == "" {
		return StoredOperation{}, fmt.Errorf("invalid operation lookup")
	}
	request := make(map[string]any, 3)
	if lookup.AgentType != nil {
		request["agent_type"] = *lookup.AgentType
	}
	if lookup.ConnectionID != nil {
		request["connection_id"] = *lookup.ConnectionID
	}
	if lookup.SessionID != nil {
		request["session_id"] = *lookup.SessionID
	}
	exists, raw, err := d.journal.ReadOperation(operationScope(lookup.Action, request), lookup.OperationID)
	if err != nil {
		return StoredOperation{}, fmt.Errorf("operation storage unavailable: %w", err)
	}
	stored := StoredOperation{DaemonID: d.daemonID, State: StoredOperationStateMissing}
	if !exists {
		return stored, nil
	}
	stored.State = StoredOperationStateUncertain
	if len(raw) == 0 {
		return stored, nil
	}
	stored.State = StoredOperationStateCompleted
	stored.LegacyResponseJSON = append([]byte(nil), raw...)
	return stored, nil
}
