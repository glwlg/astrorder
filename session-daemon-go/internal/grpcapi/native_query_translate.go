package grpcapi

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	core "astrorder.dev/session-daemon/internal/runtime"
	sshtrans "astrorder.dev/session-daemon/internal/transport/ssh"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// This closed table supplies every method name; JSON options can only supply
// native parameters. It cannot choose a daemon action or native method.
type nativeQuerySpec struct {
	family   string
	method   string
	fields   map[protoreflect.Name]string
	required map[protoreflect.Name]bool
}

var nativeQuerySpecs = map[protoreflect.Name]nativeQuerySpec{
	"codex_initialize":         {family: "codex", method: "initialize"},
	"codex_account_read":       {family: "codex", method: "account/read"},
	"codex_hooks_list":         {family: "codex", method: "hooks/list"},
	"codex_model_list":         {family: "codex", method: "model/list", fields: map[protoreflect.Name]string{"limit": "limit", "cursor": "cursor", "include_hidden": "includeHidden"}},
	"codex_skills_list":        {family: "codex", method: "skills/list", fields: map[protoreflect.Name]string{"cwds": "cwds"}},
	"codex_thread_items_list":  {family: "codex", method: "thread/items/list", fields: map[protoreflect.Name]string{"thread_id": "threadId", "limit": "limit", "cursor": "cursor", "sort_direction": "sortDirection"}, required: map[protoreflect.Name]bool{"thread_id": true, "limit": true}},
	"codex_thread_list":        {family: "codex", method: "thread/list", fields: map[protoreflect.Name]string{"limit": "limit", "cursor": "cursor", "cwd": "cwd", "archived": "archived"}},
	"codex_thread_loaded_list": {family: "codex", method: "thread/loaded/list"},
	"codex_thread_read":        {family: "codex", method: "thread/read", fields: map[protoreflect.Name]string{"thread_id": "threadId", "include_turns": "includeTurns"}, required: map[protoreflect.Name]bool{"thread_id": true}},
	"hermes_session_list":      {family: "hermes", method: "session.list", fields: map[protoreflect.Name]string{"offset": "offset", "limit": "limit", "source_id": "source_id"}},
	"hermes_active_list":       {family: "hermes", method: "session.active_list"},
	"hermes_projects_tree":     {family: "hermes", method: "projects.tree"},
	"hermes_project_sessions":  {family: "hermes", method: "projects.project_sessions", fields: map[protoreflect.Name]string{"project_id": "project_id", "session_limit": "session_limit"}, required: map[protoreflect.Name]bool{"project_id": true}},
	"hermes_session_history":   {family: "hermes", method: "session.history", fields: map[protoreflect.Name]string{"session_id": "session_id", "before": "before", "limit": "limit"}, required: map[protoreflect.Name]bool{"session_id": true}},
	"hermes_models_list":       {family: "hermes", method: "models.list"},
	"hermes_commands_list":     {family: "hermes", method: "commands.list"},
	"grok_initialize":          {family: "grok", method: "initialize"},
	"grok_session_list":        {family: "grok", method: "session/list", fields: map[protoreflect.Name]string{"cwd": "cwd", "cursor": "cursor"}},
}

func translateNativeQuery(request *sessiondv1.NativeQueryRequest) (core.Request, error) {
	invalid := func() (core.Request, error) {
		return core.Request{}, status.Error(codes.InvalidArgument, "native query request is invalid")
	}
	if request == nil || request.Target == nil {
		return invalid()
	}
	if proto.Size(request) > MaxWireBytes {
		return core.Request{}, status.Error(codes.ResourceExhausted, "native query exceeds transport limit")
	}
	if err := rejectNativeQueryUnknown(request.ProtoReflect()); err != nil {
		return core.Request{}, err
	}
	message := request.ProtoReflect()
	branch := message.WhichOneof(message.Descriptor().Oneofs().ByName("query"))
	if branch == nil {
		return invalid()
	}
	spec, ok := nativeQuerySpecs[branch.Name()]
	if !ok {
		return invalid()
	}
	kind, family := "", ""
	switch request.Target.Runtime {
	case sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX:
		kind, family = "codex", "codex"
	case sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES:
		kind, family = "hermes", "hermes"
	case sessiondv1.RuntimeKind_RUNTIME_KIND_GROK:
		kind, family = "grok", "grok"
	case sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX_SSH:
		kind, family = "codex-ssh", "codex"
	case sessiondv1.RuntimeKind_RUNTIME_KIND_SSH_HERMES:
		kind, family = "ssh", "hermes"
	case sessiondv1.RuntimeKind_RUNTIME_KIND_GROK_SSH:
		kind, family = "grok-ssh", "grok"
	default:
		return invalid()
	}
	if spec.family != family {
		return invalid()
	}
	query := message.Get(branch).Message()
	if !query.IsValid() {
		return invalid()
	}
	params := map[string]any{}
	options := query.Descriptor().Fields().ByName("options")
	if options != nil && query.Has(options) {
		decoded, err := decodeQueryJSON(query.Get(options).Message().Interface().(*sessiondv1.NativeJson))
		if err != nil {
			return core.Request{}, err
		}
		var object bool
		params, object = decoded.(map[string]any)
		if !object || params == nil {
			return invalid()
		}
	}
	for field, key := range spec.fields {
		if _, exists := params[key]; exists {
			return invalid()
		}
		descriptor := query.Descriptor().Fields().ByName(field)
		value := query.Get(descriptor)
		if spec.required[field] && (!query.Has(descriptor) || descriptor.Kind() == protoreflect.StringKind && value.String() == "" || descriptor.Kind() == protoreflect.Uint32Kind && value.Uint() == 0) {
			return invalid()
		}
		if !query.Has(descriptor) {
			continue
		}
		if descriptor.IsList() {
			values := make([]string, value.List().Len())
			for i := range values {
				values[i] = value.List().Get(i).String()
			}
			params[key] = values
			continue
		}
		switch descriptor.Kind() {
		case protoreflect.StringKind:
			params[key] = value.String()
		case protoreflect.BoolKind:
			params[key] = value.Bool()
		case protoreflect.Uint32Kind:
			params[key] = uint32(value.Uint())
		case protoreflect.MessageKind:
			nullable := value.Message().Interface().(*sessiondv1.NullableString)
			switch state := nullable.State.(type) {
			case *sessiondv1.NullableString_Value:
				params[key] = state.Value
			case *sessiondv1.NullableString_Null:
				if state.Null != sessiondv1.NullValue_NULL_VALUE_NULL {
					return invalid()
				}
				params[key] = nil
			default:
				return invalid()
			}
		default:
			return invalid()
		}
	}
	connection, err := nativeQueryConnection(request.Target, kind != family)
	if err != nil {
		return core.Request{}, err
	}
	fields := map[string]any{"action": "runtime.request", "agent_type": kind, "method": spec.method, "request_params": params, "params": connection}
	if request.Target.AgentId != nil {
		fields["agent_id"] = *request.Target.AgentId
	}
	if request.Target.ConnectionId != nil {
		fields["connection_id"] = *request.Target.ConnectionId
	}
	if request.TopLevelPayload != nil {
		fields["payload"], err = decodeQueryJSON(request.TopLevelPayload)
		if err != nil {
			return core.Request{}, err
		}
	}
	if request.TopLevelHash != nil {
		fields["hash"] = *request.TopLevelHash
	}
	if request.TopLevelSha256 != nil {
		fields["sha256"] = *request.TopLevelSha256
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return invalid()
	}
	if len(raw) > MaxBusinessBytes {
		return core.Request{}, status.Error(codes.ResourceExhausted, "native query exceeds business limit")
	}
	return core.Request{AgentType: kind, Action: "runtime.request", Fields: fields}, nil
}

func decodeQueryJSON(native *sessiondv1.NativeJson) (any, error) {
	if native == nil || !utf8.Valid(native.Utf8Json) {
		return nil, status.Error(codes.InvalidArgument, "native query JSON must be UTF-8")
	}
	if len(native.Utf8Json) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "native query JSON exceeds business limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(native.Utf8Json))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, status.Error(codes.InvalidArgument, "native query JSON is invalid")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, status.Error(codes.InvalidArgument, "native query JSON must contain one value")
	}
	return value, nil
}

func rejectNativeQueryUnknown(message protoreflect.Message) error {
	if !message.IsValid() || len(message.GetUnknown()) != 0 {
		return status.Error(codes.InvalidArgument, "native query contains unknown fields")
	}
	var err error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() == protoreflect.MessageKind {
			if field.IsList() {
				for i := 0; i < value.List().Len(); i++ {
					if err = rejectNativeQueryUnknown(value.List().Get(i).Message()); err != nil {
						return false
					}
				}
			} else {
				err = rejectNativeQueryUnknown(value.Message())
			}
		}
		return err == nil
	})
	return err
}

func nativeQueryConnection(target *sessiondv1.RuntimeTarget, remote bool) (map[string]any, error) {
	invalid := func() (map[string]any, error) {
		return nil, status.Error(codes.InvalidArgument, "native query connection is invalid")
	}
	params := map[string]any{}
	if !remote {
		if target.ConnectionId != nil || target.Ssh != nil {
			return invalid()
		}
		return params, nil
	}
	if target.ConnectionId == nil || *target.ConnectionId == "" || len(*target.ConnectionId) > 128 || target.Ssh == nil {
		return invalid()
	}
	settings := target.Ssh
	if settings.Port > 65535 {
		return invalid()
	}
	cfg := sshtrans.Config{Host: settings.Host, Port: int(settings.Port), User: settings.GetUser(), IdentityFile: settings.GetIdentityFile(), Binary: settings.GetSshExecutable()}
	if err := cfg.Validate(); err != nil {
		return invalid()
	}
	// Existing remote factories do not consume these transport options yet.
	// Refuse them explicitly rather than returning a successful partial mapping.
	if settings.Options != nil || settings.SshConfigAlias != nil || settings.SshExecutable != nil {
		return nil, status.Error(codes.Unimplemented, "native query SSH transport options are not implemented")
	}
	values := map[string]any{"host": settings.Host}
	if settings.Port != 0 {
		values["port"] = int(settings.Port)
	}
	settings.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() == protoreflect.StringKind && field.Name() != "host" {
			values[string(field.Name())] = value.String()
		}
		return true
	})
	params["connection_id"] = *target.ConnectionId
	params["ssh_settings"] = values
	return params, nil
}
