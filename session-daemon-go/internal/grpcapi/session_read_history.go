package grpcapi

import (
	"context"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (s *runtimeQueryService) GetHistoryPage(ctx context.Context, request *sessiondv1.HistoryPageRequest) (*sessiondv1.HistoryPageResponse, error) {
	if request == nil || request.Limit < 1 || request.Limit > 200 {
		return nil, status.Error(codes.InvalidArgument, "history limit must be between 1 and 200")
	}
	fields := map[string]any{"limit": int(request.Limit)}
	if request.Before != nil {
		before, err := typedNullableString("before", request.Before)
		if err != nil || before != nil && len(*before) > 512 {
			return nil, status.Error(codes.InvalidArgument, "history cursor is invalid")
		}
		if before == nil {
			fields["before"] = nil
		} else {
			fields["before"] = *before
		}
	}
	payload, _, err := s.readSession(ctx, request, request.Session, "session.history_page", fields)
	if err != nil {
		return nil, err
	}
	native, err := nativeReadValue(payload)
	if err != nil {
		return nil, err
	}
	response := &sessiondv1.HistoryPageResponse{Session: proto.Clone(request.Session).(*sessiondv1.SessionRef), Native: native}
	items, err := readItems(payload)
	if err != nil {
		return nil, err
	}
	if len(items) > int(request.Limit) {
		return nil, status.Error(codes.Unavailable, "native history exceeds requested page limit")
	}
	for _, item := range items {
		nativeItem, err := nativeReadValue(item)
		if err != nil {
			return nil, err
		}
		response.Items = append(response.Items, nativeItem)
	}
	if value, present := payload["next_cursor"]; present {
		if value == nil {
			response.NextCursor = &sessiondv1.NullableString{State: &sessiondv1.NullableString_Null{Null: sessiondv1.NullValue_NULL_VALUE_NULL}}
		} else {
			cursor, ok := value.(string)
			if !ok || len(cursor) > 512 {
				return nil, status.Error(codes.Unavailable, "invalid native history cursor")
			}
			response.NextCursor = &sessiondv1.NullableString{State: &sessiondv1.NullableString_Value{Value: cursor}}
		}
	}
	if value, present := payload["has_more"]; present {
		more, ok := value.(bool)
		if !ok {
			return nil, status.Error(codes.Unavailable, "invalid native history continuation")
		}
		response.HasMore = &more
	}
	if response.HasMore != nil && *response.HasMore && (response.NextCursor == nil || response.NextCursor.GetValue() == "") {
		return nil, status.Error(codes.Unavailable, "native history continuation has no cursor")
	}
	if proto.Size(response) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "typed history exceeds business limit")
	}
	return response, nil
}
