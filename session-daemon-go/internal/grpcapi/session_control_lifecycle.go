package grpcapi

import (
	"context"
	"encoding/base64"
	"strings"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func controlTarget(target *pb.RuntimeTarget) (string, map[string]any, error) {
	if target == nil {
		return "", nil, status.Error(codes.InvalidArgument, "runtime target is required")
	}
	var kind string
	switch target.Runtime {
	case pb.RuntimeKind_RUNTIME_KIND_CODEX:
		kind = "codex"
	case pb.RuntimeKind_RUNTIME_KIND_HERMES:
		kind = "hermes"
	case pb.RuntimeKind_RUNTIME_KIND_GROK:
		kind = "grok"
	case pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH:
		kind = "codex-ssh"
	case pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES:
		kind = "ssh"
	case pb.RuntimeKind_RUNTIME_KIND_GROK_SSH:
		kind = "grok-ssh"
	case pb.RuntimeKind_RUNTIME_KIND_PTY:
		kind = "pty"
	default:
		return "", nil, status.Error(codes.InvalidArgument, "runtime target is invalid")
	}
	params, err := nativeQueryConnection(target, kind == "ssh" || strings.HasSuffix(kind, "-ssh"))
	return kind, params, err
}

func (s *sessionControlService) CreateSession(ctx context.Context, r *pb.CreateSessionRequest) (*pb.CommandReceipt, error) {
	if err := validateControlRequest(r); err != nil {
		return nil, err
	}
	kind, params, err := controlTarget(r.Target)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{"action": "session.create", "agent_type": kind, "params": params}
	if r.Target.ConnectionId != nil {
		fields["connection_id"] = *r.Target.ConnectionId
	}
	switch options := r.Options.(type) {
	case *pb.CreateSessionRequest_Codex:
		if kind != "codex" && kind != "codex-ssh" || options.Codex == nil {
			return nil, status.Error(codes.InvalidArgument, "create options do not match runtime")
		}
		o := options.Codex
		native, err := nativeControlOptions(o.NativeOptions, "cwd", "parent_session_id", "model", "modelProvider", "effort")
		if err != nil {
			return nil, err
		}
		for k, v := range native {
			params[k] = v
		}
		fields["cwd"] = o.Cwd
		if o.ParentSessionId != nil {
			fields["parent_session_id"] = *o.ParentSessionId
		}
		if o.Model != nil {
			params["model"] = *o.Model
		}
		if o.Provider != nil {
			params["modelProvider"] = *o.Provider
		}
		if o.Effort != nil {
			params["effort"] = *o.Effort
		}
	case *pb.CreateSessionRequest_Hermes:
		if kind != "hermes" && kind != "ssh" || options.Hermes == nil {
			return nil, status.Error(codes.InvalidArgument, "create options do not match runtime")
		}
		o := options.Hermes
		native, err := nativeControlOptions(o.NativeOptions, "action", "agent_type", "session_id", "operation_id", "command_id", "params", "model", "provider", "effort", "parent_session_id")
		if err != nil {
			return nil, err
		}
		for k, v := range native {
			fields[k] = v
		}
		if o.ParentSessionId != nil {
			fields["parent_session_id"] = *o.ParentSessionId
		}
		if o.Model != nil {
			fields["model"] = *o.Model
		}
		if o.Provider != nil {
			fields["provider"] = *o.Provider
		}
		if o.Effort != nil {
			fields["effort"] = *o.Effort
		}
	case *pb.CreateSessionRequest_Grok:
		if kind != "grok" && kind != "grok-ssh" || options.Grok == nil {
			return nil, status.Error(codes.InvalidArgument, "create options do not match runtime")
		}
		o := options.Grok
		native, err := nativeControlOptions(o.NativeOptions, "action", "agent_type", "session_id", "operation_id", "command_id", "params", "cwd", "model", "effort")
		if err != nil {
			return nil, err
		}
		for k, v := range native {
			fields[k] = v
		}
		fields["cwd"] = o.Cwd
		if o.Model != nil {
			fields["model"] = *o.Model
		}
		if o.Effort != nil {
			fields["effort"] = *o.Effort
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "create options are required")
	}
	receipt, err := s.execute(ctx, r, nil, r.Meta, fields)
	if err != nil || receipt == nil || receipt.OperationState != pb.OperationState_OPERATION_STATE_COMPLETED {
		return receipt, err
	}
	if receipt.Session == nil {
		return nil, status.Error(codes.Unavailable, "created native identity is missing; reconcile before retry")
	}
	identity, err := s.daemon.SessionIdentity(receipt.Session.SessionId)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "created native ownership is unconfirmed; reconcile before retry")
	}
	if r.Target.AgentId != nil && (identity.AgentID == nil || *identity.AgentID != *r.Target.AgentId) {
		return nil, status.Error(codes.FailedPrecondition, "created native owner differs from target; reconcile before retry")
	}
	if r.Target.ConnectionId != nil && (identity.ConnectionID == nil || *identity.ConnectionID != *r.Target.ConnectionId) {
		return nil, status.Error(codes.FailedPrecondition, "created native connection differs from target; reconcile before retry")
	}
	receipt.Session.AgentId = identity.AgentID
	receipt.Session.ConnectionId = identity.ConnectionID
	return receipt, nil
}

func (s *sessionControlService) AttachSession(ctx context.Context, r *pb.AttachSessionRequest) (*pb.SessionReceipt, error) {
	if err := validateControlRequest(r); err != nil {
		return nil, err
	}
	if err := validateSessionRef(r.Session); err != nil {
		return nil, err
	}
	kind, params, err := controlTarget(r.Target)
	if err != nil {
		return nil, err
	}
	if r.Session.ConnectionId != nil && (r.Target.ConnectionId == nil || *r.Session.ConnectionId != *r.Target.ConnectionId) || r.Session.AgentId != nil && r.Target.AgentId != nil && *r.Session.AgentId != *r.Target.AgentId {
		return nil, status.Error(codes.InvalidArgument, "attach identities disagree")
	}
	if _, err := s.daemon.ResolveSessionOwner(r.Session.SessionId, controlIdentity(r.Session)); err != nil && status.Code(ownerError(err)) != codes.NotFound {
		return nil, ownerError(err)
	}
	options, err := nativeControlOptions(r.NativeOptions, "runtime_control", "connection_id", "ssh_settings")
	if err != nil {
		return nil, err
	}
	for k, v := range options {
		params[k] = v
	}
	params["runtime_control"] = r.RuntimeControl
	fields := controlFields("session.spawn", r.Session)
	fields["agent_type"] = kind
	fields["params"] = params
	if r.Cwd != nil {
		fields["cwd"] = *r.Cwd
	}
	result := s.daemon.ExecuteSessionControl(ctx, fields)
	receipt, err := commandReceipt(s.daemon.DaemonID(), r.Session, &pb.CommandMeta{}, result)
	if err != nil {
		return nil, err
	}
	return &pb.SessionReceipt{DaemonId: receipt.DaemonId, Session: receipt.Session, Status: receipt.Status, Attached: true, Native: receipt.GetCompleted().Native}, nil
}

func inputFields(parts []*pb.InputPart) (map[string]any, error) {
	if len(parts) == 0 || len(parts) > 1000 {
		return nil, status.Error(codes.InvalidArgument, "input parts are required")
	}
	items := make([]any, 0, len(parts))
	attachments := []any{}
	texts := []string{}
	for _, part := range parts {
		if part == nil {
			return nil, status.Error(codes.InvalidArgument, "input part is invalid")
		}
		switch p := part.Part.(type) {
		case *pb.InputPart_Text:
			if p.Text == nil {
				return nil, status.Error(codes.InvalidArgument, "text input is invalid")
			}
			items = append(items, map[string]any{"type": "text", "text": p.Text.Text})
			texts = append(texts, p.Text.Text)
		case *pb.InputPart_ImageUrl:
			if p.ImageUrl == nil || p.ImageUrl.Url == "" {
				return nil, status.Error(codes.InvalidArgument, "image input is invalid")
			}
			items = append(items, map[string]any{"type": "image", "url": p.ImageUrl.Url})
		case *pb.InputPart_LocalImage:
			if p.LocalImage == nil || p.LocalImage.Path == "" {
				return nil, status.Error(codes.InvalidArgument, "image input is invalid")
			}
			items = append(items, map[string]any{"type": "localImage", "path": p.LocalImage.Path})
		case *pb.InputPart_Skill:
			if p.Skill == nil || p.Skill.Name == "" || p.Skill.Path == "" {
				return nil, status.Error(codes.InvalidArgument, "skill input is invalid")
			}
			items = append(items, map[string]any{"type": "skill", "name": p.Skill.Name, "path": p.Skill.Path})
		case *pb.InputPart_Mention:
			if p.Mention == nil || p.Mention.Name == "" || p.Mention.Path == "" {
				return nil, status.Error(codes.InvalidArgument, "mention input is invalid")
			}
			items = append(items, map[string]any{"type": "mention", "name": p.Mention.Name, "path": p.Mention.Path})
		case *pb.InputPart_Attachment:
			a := p.Attachment
			if a == nil || a.Name == "" || a.MediaType == "" || len(a.Content) == 0 {
				return nil, status.Error(codes.InvalidArgument, "attachment is invalid")
			}
			item := map[string]any{"name": a.Name, "media_type": a.MediaType, "content_base64": base64.StdEncoding.EncodeToString(a.Content)}
			if a.SourceId != nil {
				item["source_id"] = *a.SourceId
			}
			attachments = append(attachments, item)
		default:
			return nil, status.Error(codes.InvalidArgument, "input type is invalid")
		}
	}
	return map[string]any{"input": items, "text": strings.Join(texts, "\n"), "prompt": strings.Join(texts, "\n"), "attachments": attachments}, nil
}

func (s *sessionControlService) Send(ctx context.Context, r *pb.SendRequest) (*pb.CommandReceipt, error) {
	if err := validateControlRequest(r); err != nil {
		return nil, err
	}
	if err := validateSessionRef(r.Session); err != nil {
		return nil, err
	}
	fields, err := inputFields(r.Input)
	if err != nil {
		return nil, err
	}
	for k, v := range controlFields("session.send", r.Session) {
		fields[k] = v
	}
	kind, err := s.daemon.ResolveSessionOwner(r.Session.SessionId, controlIdentity(r.Session))
	if err != nil {
		return nil, ownerError(err)
	}
	if kind == "codex" || kind == "codex-ssh" {
		delete(fields, "text")
		delete(fields, "prompt")
		if len(fields["attachments"].([]any)) > 0 {
			return nil, status.Error(codes.InvalidArgument, "Codex attachments must be staged into input references")
		}
		delete(fields, "attachments")
	} else {
		delete(fields, "input")
	}
	if r.Policy != nil {
		p := r.Policy
		params, err := nativeControlOptions(p.NativeOptions, "model", "approvalPolicy", "sandboxPolicy", "approvalsReviewer")
		if err != nil {
			return nil, err
		}
		if p.Model != nil {
			params["model"] = *p.Model
		}
		if p.ApprovalPolicy != nil {
			params["approvalPolicy"] = *p.ApprovalPolicy
		}
		if p.SandboxPolicy != nil {
			params["sandboxPolicy"] = *p.SandboxPolicy
		}
		if p.ApprovalsReviewer != nil {
			params["approvalsReviewer"] = *p.ApprovalsReviewer
		}
		fields["params"] = params
	}
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}

func (s *sessionControlService) Steer(ctx context.Context, r *pb.SteerRequest) (*pb.CommandReceipt, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "steer request is required")
	}
	fields, err := inputFields(r.Input)
	if err != nil {
		return nil, err
	}
	delete(fields, "text")
	delete(fields, "prompt")
	delete(fields, "attachments")
	for k, v := range controlFields("session.steer", r.Session) {
		fields[k] = v
	}
	fields["turn_id"] = r.TurnId
	return s.execute(ctx, r, r.Session, r.Meta, fields)
}

func (s *sessionControlService) ReportObservedStatus(_ context.Context, r *pb.ReportObservedStatusRequest) (*pb.SessionReceipt, error) {
	if err := validateControlRequest(r); err != nil {
		return nil, err
	}
	if err := validateSessionRef(r.Session); err != nil {
		return nil, err
	}
	activity := ""
	switch r.ObservedActiveStatus {
	case pb.ObservedActiveStatus_OBSERVED_ACTIVE_STATUS_RUNNING:
		activity = "running"
	case pb.ObservedActiveStatus_OBSERVED_ACTIVE_STATUS_WAITING_APPROVAL:
		activity = "waiting_approval"
	default:
		return nil, status.Error(codes.InvalidArgument, "only positive activity observations are supported")
	}
	result, err := s.daemon.ObserveSession(r.Session.SessionId, controlIdentity(r.Session), activity, r.Observer, r.ObservedAtUnixMicros)
	if err != nil {
		return nil, ownerError(err)
	}
	mapped, _ := sessionStatus(result.Status)
	return &pb.SessionReceipt{DaemonId: s.daemon.DaemonID(), Session: proto.Clone(r.Session).(*pb.SessionRef), Status: mapped, Attached: true}, nil
}
