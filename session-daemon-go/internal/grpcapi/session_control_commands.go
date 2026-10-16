package grpcapi

import (
	"context"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *sessionControlService) StartReview(ctx context.Context, r *pb.StartReviewRequest) (*pb.CommandReceipt, error) {
	if r == nil || r.Target == nil {
		return nil, status.Error(codes.InvalidArgument, "review target is required")
	}
	var target map[string]any
	switch value := r.Target.Target.(type) {
	case *pb.ReviewTarget_Branch:
		if value.Branch == "" {
			return nil, status.Error(codes.InvalidArgument, "review branch is empty")
		}
		target = map[string]any{"type": "baseBranch", "branch": value.Branch}
	case *pb.ReviewTarget_Commit:
		if value.Commit == "" {
			return nil, status.Error(codes.InvalidArgument, "review commit is empty")
		}
		target = map[string]any{"type": "commit", "sha": value.Commit}
	case *pb.ReviewTarget_Uncommitted:
		target = map[string]any{"type": "uncommittedChanges"}
	case *pb.ReviewTarget_Native:
		var err error
		target, err = nativeControlOptions(value.Native)
		if err != nil {
			return nil, err
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "review target is invalid")
	}
	delivery := ""
	switch r.Delivery {
	case pb.ReviewDelivery_REVIEW_DELIVERY_INLINE:
		delivery = "inline"
	case pb.ReviewDelivery_REVIEW_DELIVERY_DETACHED:
		delivery = "detached"
	default:
		return nil, status.Error(codes.InvalidArgument, "review delivery is invalid")
	}
	fields := controlFields("session.review", r.Session)
	fields["target"] = target
	fields["delivery"] = delivery
	if r.Instructions != nil {
		fields["instructions"] = *r.Instructions
	}
	if r.Model != nil {
		fields["model"] = *r.Model
	}
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *sessionControlService) Compact(ctx context.Context, r *pb.CompactRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "compact request is required")
	}
	fields := controlFields("session.compact", r.Session)
	if r.Instructions != nil {
		fields["instructions"] = *r.Instructions
	}
	if r.Model != nil {
		fields["model"] = *r.Model
	}
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *sessionControlService) Interrupt(ctx context.Context, r *pb.InterruptRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "interrupt request is required")
	}
	fields := controlFields("session.interrupt", r.Session)
	if r.TurnId != nil {
		fields["turn_id"] = *r.TurnId
	}
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
func approvalIdentifier(id *pb.ExactIdentifier) (string, error) {
	if id == nil {
		return "", status.Error(codes.InvalidArgument, "approval identity is required")
	}
	// The daemon's emitted approval ID is a string key containing its native
	// namespace. Raw native IDs must not be guessed into a different key.
	if text, ok := id.Value.(*pb.ExactIdentifier_Text); ok && text.Text != "" && len(text.Text) <= 256 {
		return text.Text, nil
	}
	return "", status.Error(codes.InvalidArgument, "use the exact emitted daemon approval identity")
}
func (s *sessionControlService) RespondApproval(ctx context.Context, r *pb.RespondApprovalRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "approval request is required")
	}
	id, err := approvalIdentifier(r.ApprovalId)
	if err != nil {
		return nil, err
	}
	fields := controlFields("session.approve", r.Session)
	fields["approval_id"] = id
	switch r.Decision {
	case pb.ApprovalDecision_APPROVAL_DECISION_ACCEPT:
		fields["decision"] = "accept"
		fields["choice"] = "once"
	case pb.ApprovalDecision_APPROVAL_DECISION_DECLINE:
		fields["decision"] = "decline"
		fields["choice"] = "deny"
	case pb.ApprovalDecision_APPROVAL_DECISION_CANCEL:
		fields["decision"] = "decline"
		fields["choice"] = "deny"
		fields["cancelled"] = true
	case pb.ApprovalDecision_APPROVAL_DECISION_ACCEPT_FOR_SESSION:
		fields["decision"] = "accept"
		fields["choice"] = "session"
	default:
		return nil, status.Error(codes.InvalidArgument, "approval decision is invalid")
	}
	if r.UserInput != nil {
		if len(r.Permissions) != 0 || r.Decision != pb.ApprovalDecision_APPROVAL_DECISION_ACCEPT {
			return nil, status.Error(codes.InvalidArgument, "user input must be an explicit answer without permission grants")
		}
		value, err := decodeQueryJSON(r.UserInput)
		if err != nil {
			return nil, err
		}
		fields["user_input"] = value
	}
	if len(r.Permissions) > 0 {
		permissions := map[string]any{}
		for _, grant := range r.Permissions {
			if grant == nil || grant.Name == "" {
				return nil, status.Error(codes.InvalidArgument, "permission grant is invalid")
			}
			if _, exists := permissions[grant.Name]; exists {
				return nil, status.Error(codes.InvalidArgument, "duplicate permission grant")
			}
			if grant.Native != nil {
				value, err := decodeQueryJSON(grant.Native)
				if err != nil {
					return nil, err
				}
				permissions[grant.Name] = value
			} else if grant.Scope != nil {
				permissions[grant.Name] = *grant.Scope
			} else {
				permissions[grant.Name] = true
			}
		}
		fields["permissions"] = permissions
	}
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *sessionControlService) UpdateSettings(ctx context.Context, r *pb.UpdateSettingsRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "settings request is required")
	}
	fields := controlFields("session.settings", r.Session)
	if r.Model != nil {
		fields["model"] = *r.Model
	}
	if r.Provider != nil {
		fields["provider"] = *r.Provider
	}
	if r.Effort != nil {
		fields["effort"] = *r.Effort
	}
	if r.Model == nil && r.Provider == nil && r.Effort == nil {
		return nil, status.Error(codes.InvalidArgument, "settings request is empty")
	}
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *sessionControlService) RenameSession(ctx context.Context, r *pb.RenameSessionRequest) (*pb.CommandReceipt, error) {
	if r == nil || r.Title == "" {
		return nil, status.Error(codes.InvalidArgument, "title is required")
	}
	fields := controlFields("session.rename", r.Session)
	fields["title"] = r.Title
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *sessionControlService) DeleteSession(ctx context.Context, r *pb.DeleteSessionRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "delete request is required")
	}
	return s.execute(ctx, r, r.Session, r.Meta, controlFields("session.delete", r.Session))
}
func (s *sessionControlService) CloseSession(ctx context.Context, r *pb.CloseSessionRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "close request is required")
	}
	return s.execute(ctx, r, r.Session, r.Meta, controlFields("session.close", r.Session))
}
func (s *sessionControlService) DetachSession(ctx context.Context, r *pb.DetachSessionRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "detach request is required")
	}
	if !r.KeepRuntime {
		return nil, status.Error(codes.InvalidArgument, "detach must keep runtime; use explicit close for termination")
	}
	fields := controlFields("session.disconnect", r.Session)
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *sessionControlService) SubmitDesktop(ctx context.Context, r *pb.SubmitDesktopRequest) (*pb.CommandReceipt, error) {
	if r == nil || len(r.Input) == 0 {
		return nil, status.Error(codes.InvalidArgument, "desktop input is required")
	}
	parts := make([]any, 0, len(r.Input))
	for _, item := range r.Input {
		if item == nil {
			return nil, status.Error(codes.InvalidArgument, "desktop input is invalid")
		}
		switch value := item.Part.(type) {
		case *pb.DesktopInputPart_Text:
			if value.Text == nil {
				return nil, status.Error(codes.InvalidArgument, "desktop text is invalid")
			}
			parts = append(parts, map[string]any{"type": "text", "text": value.Text.Text})
		case *pb.DesktopInputPart_Image:
			if value.Image == nil || value.Image.Url == "" {
				return nil, status.Error(codes.InvalidArgument, "desktop image is invalid")
			}
			parts = append(parts, map[string]any{"type": "image", "url": value.Image.Url})
		default:
			return nil, status.Error(codes.InvalidArgument, "desktop input is invalid")
		}
	}
	fields := controlFields("session.desktop.submit", r.Session)
	fields["input"] = parts
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
func (s *sessionControlService) UpdateDesktopSettings(ctx context.Context, r *pb.UpdateDesktopSettingsRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "desktop settings request is required")
	}
	fields := controlFields("session.desktop.settings", r.Session)
	updates := map[string]any{}
	if r.Model != nil {
		updates["model"] = *r.Model
	}
	if r.ModelLabel != nil {
		updates["model_label"] = *r.ModelLabel
	}
	if r.Effort != nil {
		updates["effort"] = *r.Effort
	}
	if r.EffortIndex != nil {
		updates["effort_index"] = int(*r.EffortIndex)
	}
	if len(updates) == 0 {
		return nil, status.Error(codes.InvalidArgument, "desktop settings request is empty")
	}
	fields["updates"] = updates
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}
