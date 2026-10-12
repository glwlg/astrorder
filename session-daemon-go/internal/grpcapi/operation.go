package grpcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type operationQueryService struct {
	sessiondv1.UnimplementedOperationQueryServer
	daemon *protocol.Daemon
}

// RegisterOperationQuery adds the typed operation reader to the authenticated
// daemon server before it begins serving requests.
func RegisterOperationQuery(server *grpc.Server, daemon *protocol.Daemon) {
	sessiondv1.RegisterOperationQueryServer(server, &operationQueryService{daemon: daemon})
}

func (service *operationQueryService) GetOperation(_ context.Context, request *sessiondv1.GetOperationRequest) (*sessiondv1.OperationReceipt, error) {
	key, lookup, err := operationLookup(request)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	stored, err := service.daemon.ReadStoredOperation(lookup)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	response := &sessiondv1.OperationReceipt{
		DaemonId: stored.DaemonID,
		Key:      key,
		Error:    nullableBusinessError(nil),
		Status:   nullableSessionStatus(nil),
	}
	switch stored.State {
	case protocol.StoredOperationStateMissing:
		response.State = sessiondv1.OperationState_OPERATION_STATE_MISSING
	case protocol.StoredOperationStateUncertain:
		response.State = sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN
		response.Error = nullableBusinessError(&sessiondv1.BusinessError{
			Kind:      sessiondv1.ErrorKind_ERROR_KIND_FAILED_PRECONDITION,
			Message:   "operation pending or outcome uncertain; reconcile native state before retry",
			Retryable: false,
		})
	case protocol.StoredOperationStateCompleted:
		response.State = sessiondv1.OperationState_OPERATION_STATE_COMPLETED
		response.LegacyResponseJson = append([]byte(nil), stored.LegacyResponseJSON...)
		typed, operationStatus, decodeErr := typedLegacyCommandReceipt(stored.LegacyResponseJSON, key, stored.DaemonID, lookup)
		if decodeErr != nil {
			// The ledger already proves completion. Typed projection is optional:
			// an unreadable envelope must not hide its exact stored bytes or
			// turn a completed operation into a transport/storage failure.
			return response, nil
		}
		response.TypedResponse = typed
		if operationStatus != nil {
			response.Status = nullableSessionStatus(operationStatus)
		}
	default:
		return nil, status.Error(codes.Unavailable, "stored operation state is invalid")
	}
	return response, nil
}

func operationLookup(request *sessiondv1.GetOperationRequest) (*sessiondv1.OperationKey, protocol.OperationLookup, error) {
	if request == nil || request.Key == nil {
		return nil, protocol.OperationLookup{}, fmt.Errorf("operation key is required")
	}
	key := request.Key
	if len(request.ProtoReflect().GetUnknown()) != 0 || len(key.ProtoReflect().GetUnknown()) != 0 {
		return nil, protocol.OperationLookup{}, fmt.Errorf("unknown operation identity fields are unsupported")
	}
	if key.OperationId == "" || len(key.OperationId) > 256 || key.LegacyAction == "" {
		return nil, protocol.OperationLookup{}, fmt.Errorf("invalid operation lookup")
	}
	if key.Canonicalization != sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1 {
		return nil, protocol.OperationLookup{}, fmt.Errorf("legacy JSON v1 canonicalization is required")
	}
	agentType, err := typedNullableString("agent_type_compat", key.AgentTypeCompat)
	if err != nil {
		return nil, protocol.OperationLookup{}, err
	}
	connectionID, err := typedNullableString("connection_id", key.ConnectionId)
	if err != nil {
		return nil, protocol.OperationLookup{}, err
	}
	sessionID, err := typedNullableString("session_id", key.SessionId)
	if err != nil {
		return nil, protocol.OperationLookup{}, err
	}
	cloned, ok := proto.Clone(key).(*sessiondv1.OperationKey)
	if !ok {
		return nil, protocol.OperationLookup{}, fmt.Errorf("operation key is invalid")
	}
	return cloned, protocol.OperationLookup{
		Action:       key.LegacyAction,
		OperationID:  key.OperationId,
		AgentType:    agentType,
		ConnectionID: connectionID,
		SessionID:    sessionID,
	}, nil
}

// A nil NullableString wrapper is legacy field absence and therefore occupies
// the same operationScope position as JSON null. A present wrapper must select
// a valid oneof branch; explicit null has the same legacy scope value.
func typedNullableString(field string, nullable *sessiondv1.NullableString) (*string, error) {
	if nullable == nil {
		return nil, nil
	}
	if len(nullable.ProtoReflect().GetUnknown()) != 0 {
		return nil, fmt.Errorf("%s has unknown identity fields", field)
	}
	switch state := nullable.State.(type) {
	case *sessiondv1.NullableString_Value:
		value := state.Value
		return &value, nil
	case *sessiondv1.NullableString_Null:
		if state.Null != sessiondv1.NullValue_NULL_VALUE_NULL {
			return nil, fmt.Errorf("%s null value is invalid", field)
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("%s presence is invalid", field)
	}
}

func nullableBusinessError(value *sessiondv1.BusinessError) *sessiondv1.NullableBusinessError {
	if value == nil {
		return &sessiondv1.NullableBusinessError{State: &sessiondv1.NullableBusinessError_Null{Null: sessiondv1.NullValue_NULL_VALUE_NULL}}
	}
	return &sessiondv1.NullableBusinessError{State: &sessiondv1.NullableBusinessError_Value{Value: value}}
}

func nullableSessionStatus(value *sessiondv1.SessionStatus) *sessiondv1.NullableSessionStatus {
	if value == nil {
		return &sessiondv1.NullableSessionStatus{State: &sessiondv1.NullableSessionStatus_Null{Null: sessiondv1.NullValue_NULL_VALUE_NULL}}
	}
	return &sessiondv1.NullableSessionStatus{State: &sessiondv1.NullableSessionStatus_Value{Value: *value}}
}

func typedLegacyCommandReceipt(raw []byte, key *sessiondv1.OperationKey, daemonID string, lookup protocol.OperationLookup) (*sessiondv1.CommandReceipt, *sessiondv1.SessionStatus, error) {
	fields, err := decodeRawObject(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid stored operation response")
	}
	if !typedLegacyAction(key.LegacyAction) {
		return nil, nil, nil
	}
	action, actionOK := rawString(fields["action"])
	storedDaemonID, daemonOK := rawString(fields["daemon_id"])
	sessionID, sessionOK := rawString(fields["session_id"])
	if !actionOK || action != key.LegacyAction+".result" || !daemonOK || storedDaemonID != daemonID || !sessionOK || sessionID == "" || len(sessionID) > 256 {
		return nil, nil, nil
	}
	if key.LegacyAction != "session.create" && lookup.SessionID != nil && sessionID != *lookup.SessionID {
		return nil, nil, nil
	}
	resultRaw, exists := fields["result"]
	if !exists {
		return nil, nil, nil
	}
	result, err := decodeRawObject(resultRaw)
	if err != nil {
		return nil, nil, nil
	}
	statusName, ok := rawString(result["status"])
	if !ok {
		return nil, nil, nil
	}
	mappedStatus, ok := sessionStatus(statusName)
	if !ok {
		return nil, nil, nil
	}
	commandResult := &sessiondv1.CommandResult{
		Native: &sessiondv1.NativeJson{
			Schema:   "astrorder.legacy.operation-result.v1",
			Utf8Json: append([]byte(nil), resultRaw...),
		},
	}
	if commandResult.Accepted, ok = optionalRawBool(result, "accepted"); !ok {
		return nil, nil, nil
	}
	if commandResult.TerminationConfirmed, ok = optionalRawBool(result, "termination_confirmed"); !ok {
		return nil, nil, nil
	}
	optionalStrings := []struct {
		name   string
		target **string
	}{
		{name: "turn_id", target: &commandResult.TurnId},
		{name: "title", target: &commandResult.Title},
		{name: "model", target: &commandResult.Model},
		{name: "provider", target: &commandResult.Provider},
		{name: "effort", target: &commandResult.Effort},
	}
	for _, field := range optionalStrings {
		value, valid := optionalRawString(result, field.name)
		if !valid {
			return nil, nil, nil
		}
		*field.target = value
	}
	session := &sessiondv1.SessionRef{SessionId: sessionID}
	if lookup.ConnectionID != nil {
		connectionID := *lookup.ConnectionID
		session.ConnectionId = &connectionID
	}
	receipt := &sessiondv1.CommandReceipt{
		DaemonId:       daemonID,
		OperationId:    key.OperationId,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Session:        session,
		Status:         mappedStatus,
		Accepted:       commandResult.Accepted,
		TurnId:         commandResult.TurnId,
		Outcome:        &sessiondv1.CommandReceipt_Completed{Completed: commandResult},
	}
	return receipt, &mappedStatus, nil
}

func typedLegacyAction(action string) bool {
	switch action {
	case "session.create", "session.send", "session.steer", "session.review", "session.compact", "session.approve", "session.interrupt", "session.delete", "session.rename", "session.settings":
		return true
	default:
		return false
	}
}

func decodeRawObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("JSON value is not an object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("JSON value contains trailing data")
	}
	return object, nil
}

func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func optionalRawString(object map[string]json.RawMessage, name string) (*string, bool) {
	raw, exists := object[name]
	if !exists {
		return nil, true
	}
	value, ok := rawString(raw)
	if !ok {
		return nil, false
	}
	return &value, true
}

func optionalRawBool(object map[string]json.RawMessage, name string) (bool, bool) {
	raw, exists := object[name]
	if !exists {
		return false, true
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, false
	}
	return value, true
}
