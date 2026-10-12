package grpcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (s *runtimeQueryService) readSession(ctx context.Context, request proto.Message, session *sessiondv1.SessionRef, action string, fields map[string]any) (map[string]any, string, error) {
	if session == nil || session.SessionId == "" || len(session.SessionId) > 256 {
		return nil, "", status.Error(codes.InvalidArgument, "session identity is invalid")
	}
	if err := rejectNativeQueryUnknown(request.ProtoReflect()); err != nil {
		return nil, "", err
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["action"] = action
	fields["session_id"] = session.SessionId
	result, kind, err := s.daemon.ReadSession(ctx, core.Request{SessionID: session.SessionId, Action: action, Fields: fields}, core.SessionReadIdentity{AgentID: session.AgentId, ConnectionID: session.ConnectionId})
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return nil, kind, status.FromContextError(err).Err()
		case errors.Is(err, core.ErrSessionNotOwned):
			return nil, kind, status.Error(codes.NotFound, "session is not daemon-owned")
		case errors.Is(err, core.ErrSessionIdentity):
			return nil, kind, status.Error(codes.FailedPrecondition, "session identity does not match owner")
		case errors.Is(err, core.ErrSessionReadUnsupported):
			return nil, kind, status.Error(codes.Unimplemented, "runtime does not support this session read")
		case errors.Is(err, core.ErrRuntimeBusy):
			return nil, kind, status.Error(codes.FailedPrecondition, "native runtime admission is closed")
		default:
			return nil, kind, status.Error(codes.Unavailable, "native session read failed")
		}
	}
	raw, err := json.Marshal(result.Payload)
	if err != nil {
		return nil, kind, status.Error(codes.Internal, "native session response is not valid JSON")
	}
	if len(raw) > MaxBusinessBytes {
		return nil, kind, status.Error(codes.ResourceExhausted, "native session response exceeds business limit")
	}
	// Normalize concrete Go slice/map shapes without losing exact integer values.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err = decoder.Decode(&payload); err != nil {
		return nil, kind, status.Error(codes.Internal, "invalid native session response")
	}
	return payload, kind, nil
}

func nativeReadValue(value any) (*sessiondv1.NativeJson, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, status.Error(codes.Internal, "native read value is not valid JSON")
	}
	return &sessiondv1.NativeJson{Schema: "astrorder.runtime.session-read.v1", Utf8Json: raw}, nil
}
func readString(object map[string]any, key string) (*string, error) {
	value, exists := object[key]
	if !exists || value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, status.Error(codes.Unavailable, "invalid native string field")
	}
	return &text, nil
}
func readItems(payload map[string]any) ([]any, error) {
	value, exists := payload["items"]
	if !exists {
		return nil, status.Error(codes.Unavailable, "native item list is absent")
	}
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, status.Error(codes.Unavailable, "native items are not an array")
	}
	return items, nil
}

func (s *runtimeQueryService) ListModels(ctx context.Context, request *sessiondv1.ListModelsRequest) (*sessiondv1.ListModelsResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "model list request is required")
	}
	payload, _, err := s.readSession(ctx, request, request.Session, "session.models", nil)
	if err != nil {
		return nil, err
	}
	items, err := readItems(payload)
	if err != nil {
		return nil, err
	}
	response := &sessiondv1.ListModelsResponse{}
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, status.Error(codes.Unavailable, "invalid native model row")
		}
		provider, err := readString(row, "provider")
		if err != nil {
			return nil, err
		}
		model, err := readString(row, "model")
		if err != nil {
			return nil, err
		}
		if model == nil {
			model, err = readString(row, "id")
			if err != nil {
				return nil, err
			}
		}
		if model == nil || *model == "" {
			return nil, status.Error(codes.Unavailable, "native model identity is absent")
		}
		label, err := readString(row, "label")
		if err != nil {
			return nil, err
		}
		if label == nil {
			label, err = readString(row, "displayName")
			if err != nil {
				return nil, err
			}
		}
		native, err := nativeReadValue(row)
		if err != nil {
			return nil, err
		}
		option := &sessiondv1.ModelOption{Model: *model, Label: label, Native: native}
		if provider != nil {
			option.Provider = *provider
		}
		option.SupportedEfforts, err = modelEfforts(row)
		if err != nil {
			return nil, err
		}
		response.Items = append(response.Items, option)
	}
	if proto.Size(response) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "typed model list exceeds business limit")
	}
	return response, nil
}

func modelEfforts(row map[string]any) ([]string, error) {
	for _, key := range []string{"supported_efforts", "efforts", "supportedReasoningEfforts"} {
		value, present := row[key]
		if !present || value == nil {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return nil, status.Error(codes.Unavailable, "invalid model effort list")
		}
		efforts := make([]string, 0, len(items))
		for _, item := range items {
			if key == "supportedReasoningEfforts" {
				object, ok := item.(map[string]any)
				if !ok {
					return nil, status.Error(codes.Unavailable, "invalid model effort")
				}
				item = object["reasoningEffort"]
			}
			effort, ok := item.(string)
			if !ok || effort == "" {
				return nil, status.Error(codes.Unavailable, "invalid model effort")
			}
			efforts = append(efforts, effort)
		}
		return efforts, nil
	}
	return nil, nil
}

func (s *runtimeQueryService) ListCommands(ctx context.Context, request *sessiondv1.ListCommandsRequest) (*sessiondv1.ListCommandsResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "commands request is required")
	}
	payload, _, err := s.readSession(ctx, request, request.Session, "session.commands", nil)
	if err != nil {
		return nil, err
	}
	items, err := readItems(payload)
	if err != nil {
		return nil, err
	}
	response := &sessiondv1.ListCommandsResponse{}
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, status.Error(codes.Unavailable, "invalid native command row")
		}
		name, err := readString(row, "name")
		if err != nil {
			return nil, err
		}
		if name == nil || *name == "" {
			return nil, status.Error(codes.Unavailable, "native command name is absent")
		}
		description, err := readString(row, "description")
		if err != nil {
			return nil, err
		}
		hint, err := readString(row, "input_hint")
		if err != nil {
			return nil, err
		}
		native, err := nativeReadValue(row)
		if err != nil {
			return nil, err
		}
		response.Items = append(response.Items, &sessiondv1.CommandOption{Name: *name, Description: description, InputHint: hint, Native: native})
	}
	if proto.Size(response) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "typed commands exceed business limit")
	}
	return response, nil
}

func (s *runtimeQueryService) ReadModel(ctx context.Context, request *sessiondv1.ReadModelRequest) (*sessiondv1.ModelState, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "model read request is required")
	}
	payload, _, err := s.readSession(ctx, request, request.Session, "session.model.read", nil)
	if err != nil {
		return nil, err
	}
	response := &sessiondv1.ModelState{Session: proto.Clone(request.Session).(*sessiondv1.SessionRef)}
	for key, destination := range map[string]**string{"provider": &response.Provider, "model": &response.Model, "branch": &response.Branch, "effort": &response.Effort} {
		*destination, err = readString(payload, key)
		if err != nil {
			return nil, err
		}
	}
	response.Native, err = nativeReadValue(payload)
	if err != nil {
		return nil, err
	}
	if proto.Size(response) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "typed model state exceeds business limit")
	}
	return response, nil
}
