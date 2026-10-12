package grpcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"astrorder.dev/session-daemon/internal/modelconfig"
	"astrorder.dev/session-daemon/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type modelConfigService struct {
	sessiondv1.UnimplementedModelConfigServer
	daemon *protocol.Daemon
}

// RegisterModelConfig registers the typed ModelConfig RPC service on the provided gRPC server.
func RegisterModelConfig(server *grpc.Server, daemon *protocol.Daemon) {
	sessiondv1.RegisterModelConfigServer(server, &modelConfigService{daemon: daemon})
}

func rejectModelConfigUnknown(message protoreflect.Message) error {
	if !message.IsValid() || len(message.GetUnknown()) != 0 {
		return status.Error(codes.InvalidArgument, "model config request contains unknown fields")
	}
	var err error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() == protoreflect.MessageKind {
			if field.IsList() {
				for i := 0; i < value.List().Len(); i++ {
					if err = rejectModelConfigUnknown(value.List().Get(i).Message()); err != nil {
						return false
					}
				}
			} else {
				err = rejectModelConfigUnknown(value.Message())
			}
		}
		return err == nil
	})
	return err
}

func managedFileToName(f sessiondv1.ManagedFile) (string, error) {
	switch f {
	case sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG:
		return modelconfig.FileCodexConfig, nil
	case sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CATALOG:
		return modelconfig.FileCodexCatalog, nil
	case sessiondv1.ManagedFile_MANAGED_FILE_CODEX_MAGPIE_CATALOG:
		return modelconfig.FileCodexMagpieCatalog, nil
	case sessiondv1.ManagedFile_MANAGED_FILE_GROK_CONFIG:
		return modelconfig.FileGrokConfig, nil
	case sessiondv1.ManagedFile_MANAGED_FILE_HERMES_CONFIG:
		return modelconfig.FileHermesConfig, nil
	default:
		return "", status.Error(codes.InvalidArgument, "unrecognized managed file")
	}
}

func nameToManagedFile(name string) sessiondv1.ManagedFile {
	switch name {
	case modelconfig.FileCodexConfig:
		return sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CONFIG
	case modelconfig.FileCodexCatalog:
		return sessiondv1.ManagedFile_MANAGED_FILE_CODEX_CATALOG
	case modelconfig.FileCodexMagpieCatalog:
		return sessiondv1.ManagedFile_MANAGED_FILE_CODEX_MAGPIE_CATALOG
	case modelconfig.FileGrokConfig:
		return sessiondv1.ManagedFile_MANAGED_FILE_GROK_CONFIG
	case modelconfig.FileHermesConfig:
		return sessiondv1.ManagedFile_MANAGED_FILE_HERMES_CONFIG
	default:
		return sessiondv1.ManagedFile_MANAGED_FILE_UNSPECIFIED
	}
}

func reloadAgentToName(agent sessiondv1.ReloadAgent) (string, error) {
	switch agent {
	case sessiondv1.ReloadAgent_RELOAD_AGENT_CODEX:
		return "codex", nil
	case sessiondv1.ReloadAgent_RELOAD_AGENT_GROK:
		return "grok", nil
	case sessiondv1.ReloadAgent_RELOAD_AGENT_HERMES:
		return "hermes", nil
	default:
		return "", status.Error(codes.InvalidArgument, "unrecognized reload agent")
	}
}

func nameToReloadAgent(name string) sessiondv1.ReloadAgent {
	switch strings.ToLower(name) {
	case "codex":
		return sessiondv1.ReloadAgent_RELOAD_AGENT_CODEX
	case "grok":
		return sessiondv1.ReloadAgent_RELOAD_AGENT_GROK
	case "hermes":
		return sessiondv1.ReloadAgent_RELOAD_AGENT_HERMES
	default:
		return sessiondv1.ReloadAgent_RELOAD_AGENT_UNSPECIFIED
	}
}

func parseTarget(target *sessiondv1.ModelConfigTarget) (map[string]any, sessiondv1.ModelConfigTargetSupport, error) {
	if target == nil {
		return map[string]any{"kind": "local"}, sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED, nil
	}
	switch target.Target.(type) {
	case *sessiondv1.ModelConfigTarget_Local:
		if target.GetLocal() == nil {
			return nil, sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_UNSPECIFIED, status.Error(codes.InvalidArgument, "local target is invalid")
		}
		return map[string]any{"kind": "local"}, sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED, nil
	case *sessiondv1.ModelConfigTarget_Wsl:
		if target.GetWsl() == nil || target.GetWsl().Distro == "" {
			return nil, sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_UNSPECIFIED, status.Error(codes.InvalidArgument, "invalid WSL target")
		}
		m := map[string]any{
			"kind":   "wsl",
			"distro": target.GetWsl().Distro,
		}
		return m, sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED, nil
	case *sessiondv1.ModelConfigTarget_Ssh:
		if target.GetSsh() == nil || target.GetSsh().Config == nil {
			return nil, sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_UNSPECIFIED, status.Error(codes.InvalidArgument, "invalid SSH target")
		}
		settings := map[string]any{}
		cfg := target.GetSsh().Config
		settings["host"] = cfg.Host
		if cfg.Port != 0 {
			settings["port"] = int(cfg.Port)
		}
		if cfg.User != nil {
			settings["user"] = *cfg.User
		}
		if cfg.IdentityFile != nil {
			settings["identity_file"] = *cfg.IdentityFile
		}
		if cfg.SshConfigAlias != nil {
			settings["ssh_config_alias"] = *cfg.SshConfigAlias
		}
		if cfg.SshExecutable != nil {
			settings["ssh_executable"] = *cfg.SshExecutable
		}
		m := map[string]any{
			"kind":     "ssh",
			"settings": settings,
		}
		return m, sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_IMPLEMENTED, nil
	default:
		return nil, sessiondv1.ModelConfigTargetSupport_MODEL_CONFIG_TARGET_SUPPORT_UNSPECIFIED, status.Error(codes.InvalidArgument, "model target is invalid")
	}
}

func isStrictSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func sanitizeSecretError(err error) error {
	if err == nil {
		return nil
	}
	if status.Code(err) == codes.Canceled || errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "model configuration operation canceled")
	}
	if status.Code(err) == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "model configuration operation deadline exceeded")
	}
	msg := err.Error()
	if strings.Contains(msg, "already exists") {
		return status.Error(codes.AlreadyExists, "model configuration already exists")
	}
	if strings.Contains(msg, "invalid") || strings.Contains(msg, "outside allowed") || strings.Contains(msg, "escape") || strings.Contains(msg, "digest mismatch") || strings.Contains(msg, "hash mismatch") {
		return status.Error(codes.InvalidArgument, "model configuration request is invalid")
	}
	if strings.Contains(msg, "busy") || strings.Contains(msg, "admission refused") || strings.Contains(msg, "draining") {
		return status.Error(codes.FailedPrecondition, "model configuration admission refused")
	}
	if strings.Contains(msg, "not configured") {
		return status.Error(codes.Unavailable, "model configuration target is not configured")
	}
	return status.Error(codes.Internal, "model configuration execution failed")
}

func (s *modelConfigService) Plan(ctx context.Context, request *sessiondv1.ModelConfigPlanRequest) (*sessiondv1.ModelConfigPlanResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "plan request is required")
	}
	if proto.Size(request) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "model plan exceeds business limit")
	}
	if err := rejectModelConfigUnknown(request.ProtoReflect()); err != nil {
		return nil, err
	}

	targetMap, targetSupport, err := parseTarget(request.Target)
	if err != nil {
		return nil, err
	}

	rawReq := map[string]any{
		"action": "model_config.plan",
		"target": targetMap,
	}

	if len(request.ProposedFiles) > 0 {
		filesMap := make(map[string]any)
		expectedDigests := make(map[string]any)
		for _, pf := range request.ProposedFiles {
			if pf == nil {
				continue
			}
			fname, err := managedFileToName(pf.File)
			if err != nil {
				return nil, err
			}
			if _, exists := filesMap[fname]; exists || !utf8.Valid(pf.Content) {
				return nil, status.Error(codes.InvalidArgument, "duplicate or invalid proposed file")
			}
			filesMap[fname] = string(pf.Content)
			if pf.ExpectedSha256 != nil {
				if !isStrictSHA256(*pf.ExpectedSha256) {
					return nil, status.Error(codes.InvalidArgument, "invalid expected SHA-256")
				}
				expectedDigests[fname] = *pf.ExpectedSha256
			}
		}
		rawReq["files"] = filesMap
		if len(expectedDigests) > 0 {
			rawReq["expected_digests"] = expectedDigests
		}
	}

	resMap, err := s.daemon.ExecuteModelConfig(ctx, rawReq)
	if err != nil {
		return nil, sanitizeSecretError(err)
	}

	resp := &sessiondv1.ModelConfigPlanResponse{
		Target:        request.Target,
		TargetSupport: targetSupport,
	}
	raw, err := json.Marshal(resMap)
	if err != nil || len(raw) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "model plan response exceeds business limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&resMap); err != nil {
		return nil, status.Error(codes.Internal, "model plan response is invalid")
	}
	if proto.Size(request) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "model plan exceeds business limit")
	}

	if home, ok := resMap["_home"].(string); ok && home != "" {
		resp.Home = &home
	}

	// Extract file states
	for key := range modelconfig.ManagedFiles {
		if fileInfo, ok := resMap[key].(map[string]any); ok {
			exists, _ := fileInfo["exists"].(bool)
			sha, _ := fileInfo["sha256"].(string)
			statusStr, _ := fileInfo["status"].(string)
			contentStr, _ := fileInfo["content"].(string)
			valid, _ := fileInfo["valid"].(bool)
			mf := nameToManagedFile(key)
			resp.CurrentFiles = append(resp.CurrentFiles, &sessiondv1.ModelConfigFileState{
				File:    mf,
				Exists:  exists,
				Sha256:  sha,
				Content: []byte(contentStr),
				Status:  statusStr,
				Valid:   valid,
			})
		}
	}

	// Extract dryrun changes if any
	if changesList, ok := resMap["changes"].([]any); ok {
		for _, item := range changesList {
			if chg, ok := item.(map[string]any); ok {
				fileName, _ := chg["file"].(string)
				changed, _ := chg["changed"].(bool)
				currSha, _ := chg["current_sha256"].(string)
				expSha, _ := chg["expected_sha256"].(string)
				st, hasSt := chg["status"].(string)
				var stPtr *string
				if hasSt {
					stPtr = &st
				}
				resp.Changes = append(resp.Changes, &sessiondv1.ModelConfigChange{
					File:           nameToManagedFile(fileName),
					Changed:        changed,
					CurrentSha256:  currSha,
					ExpectedSha256: expSha,
					Status:         stPtr,
				})
			}
		}
	}

	return resp, nil
}

func (s *modelConfigService) Apply(ctx context.Context, request *sessiondv1.ModelConfigApplyRequest) (*sessiondv1.ModelConfigApplyResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "apply request is required")
	}
	if proto.Size(request) > MaxBusinessBytes {
		return nil, status.Error(codes.ResourceExhausted, "model apply exceeds business limit")
	}
	if err := rejectModelConfigUnknown(request.ProtoReflect()); err != nil {
		return nil, err
	}

	targetMap, targetSupport, err := parseTarget(request.Target)
	if err != nil {
		return nil, err
	}

	filesMap := make(map[string]any)
	expectedDigests := make(map[string]any)
	for _, f := range request.Files {
		if f == nil {
			return nil, status.Error(codes.InvalidArgument, "model file is invalid")
		}
		fname, err := managedFileToName(f.File)
		if err != nil {
			return nil, err
		}
		if _, exists := filesMap[fname]; exists || !utf8.Valid(f.Content) {
			return nil, status.Error(codes.InvalidArgument, "duplicate or invalid model file")
		}
		filesMap[fname] = string(f.Content)
		if f.ExpectedSha256 != nil {
			if !isStrictSHA256(*f.ExpectedSha256) {
				return nil, status.Error(codes.InvalidArgument, "invalid expected SHA-256")
			}
			expectedDigests[fname] = *f.ExpectedSha256
		}
	}

	rawReq := map[string]any{
		"action": "model_config.apply",
		"target": targetMap,
		"files":  filesMap,
	}
	if len(expectedDigests) > 0 {
		rawReq["expected_digests"] = expectedDigests
	}

	if len(request.EnvironmentValues) > 0 {
		envMap := make(map[string]any)
		for _, ev := range request.EnvironmentValues {
			if ev != nil && ev.Name != "" {
				if _, supported := modelconfig.CredentialFiles[ev.Name]; !supported {
					return nil, status.Error(codes.InvalidArgument, "unsupported environment key")
				}
				if _, exists := envMap[ev.Name]; exists || !utf8.Valid(ev.Value) {
					return nil, status.Error(codes.InvalidArgument, "duplicate or invalid environment value")
				}
				for _, b := range ev.Value {
					if b == 0 || b == '\r' || b == '\n' {
						return nil, status.Error(codes.InvalidArgument, "environment value contains illegal characters")
					}
				}
				envMap[ev.Name] = string(ev.Value)
			}
		}
		rawReq["api_keys"] = envMap
		if token, ok := envMap[modelconfig.EnvTokenKey]; ok {
			rawReq["api_key"] = token
		}
	}

	var opID string
	if err := addCommandMeta(rawReq, request.Meta); err != nil {
		return nil, err
	}
	if request.Meta != nil {
		opID = request.Meta.OperationId
		rawReq["request_id"] = opID
		rawReq["operation_id"] = opID
	}

	resMap, err := s.daemon.ExecuteModelConfig(ctx, rawReq)
	if err != nil {
		return nil, sanitizeSecretError(err)
	}

	resp := &sessiondv1.ModelConfigApplyResponse{
		DaemonId:       s.daemon.DaemonID(),
		OperationId:    opID,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		TargetSupport:  targetSupport,
	}
	if resMap["operation_state"] == "uncertain" {
		resp.OperationState = sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN
		resp.Error = businessError(sessiondv1.ErrorKind_ERROR_KIND_FAILED_PRECONDITION, "model configuration outcome uncertain; reconcile target before retry", false)
		return resp, nil
	}

	if changedList, ok := resMap["changed"].([]any); ok {
		for _, item := range changedList {
			if name, ok := item.(string); ok {
				resp.Changed = append(resp.Changed, nameToManagedFile(name))
			}
		}
	} else if changedStrs, ok := resMap["changed"].([]string); ok {
		for _, name := range changedStrs {
			resp.Changed = append(resp.Changed, nameToManagedFile(name))
		}
	}

	if receipt, ok := resMap["receipt"].(map[string]any); ok {
		if digestsMap, ok := receipt["digests"].(map[string]string); ok {
			for name, d := range digestsMap {
				resp.Digests = append(resp.Digests, &sessiondv1.FileDigest{
					File:   nameToManagedFile(name),
					Sha256: d,
				})
			}
		} else if digestsAnyMap, ok := receipt["digests"].(map[string]any); ok {
			for name, d := range digestsAnyMap {
				if dStr, ok := d.(string); ok {
					resp.Digests = append(resp.Digests, &sessiondv1.FileDigest{
						File:   nameToManagedFile(name),
						Sha256: dStr,
					})
				}
			}
		}
	}

	return resp, nil
}

func (s *modelConfigService) Reload(ctx context.Context, request *sessiondv1.ModelConfigReloadRequest) (*sessiondv1.ModelConfigReloadResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "reload request is required")
	}
	if err := rejectModelConfigUnknown(request.ProtoReflect()); err != nil {
		return nil, err
	}

	var agentNames []string
	for _, a := range request.Agents {
		name, err := reloadAgentToName(a)
		if err != nil {
			return nil, err
		}
		agentNames = append(agentNames, name)
	}

	var opID string
	rawReq := map[string]any{
		"action": "model_config.reload",
		"agents": agentNames,
	}
	if err := addCommandMeta(rawReq, request.Meta); err != nil {
		return nil, err
	}
	if request.Meta != nil {
		opID = request.Meta.OperationId
		rawReq["request_id"] = opID
		rawReq["operation_id"] = opID
	}

	resMap, err := s.daemon.ExecuteModelConfig(ctx, rawReq)
	if err != nil {
		return nil, sanitizeSecretError(err)
	}

	deferred, _ := resMap["deferred"].(bool)
	statusStr, _ := resMap["status"].(string)

	resp := &sessiondv1.ModelConfigReloadResponse{
		DaemonId:       s.daemon.DaemonID(),
		OperationId:    opID,
		OperationState: sessiondv1.OperationState_OPERATION_STATE_COMPLETED,
		Deferred:       deferred,
		Status:         statusStr,
	}
	if resMap["operation_state"] == "uncertain" {
		resp.OperationState = sessiondv1.OperationState_OPERATION_STATE_UNCERTAIN
		resp.Error = businessError(sessiondv1.ErrorKind_ERROR_KIND_FAILED_PRECONDITION, "model reload outcome uncertain", false)
		return resp, nil
	}

	if reloadedList, ok := resMap["reloaded"].([]any); ok {
		for _, item := range reloadedList {
			if name, ok := item.(string); ok {
				resp.Reloaded = append(resp.Reloaded, nameToReloadAgent(name))
			}
		}
	} else if reloadedStrs, ok := resMap["reloaded"].([]string); ok {
		for _, name := range reloadedStrs {
			resp.Reloaded = append(resp.Reloaded, nameToReloadAgent(name))
		}
	}

	if pendingList, ok := resMap["pending"].([]any); ok {
		for _, item := range pendingList {
			if name, ok := item.(string); ok {
				resp.Pending = append(resp.Pending, nameToReloadAgent(name))
			}
		}
	} else if pendingStrs, ok := resMap["pending"].([]string); ok {
		for _, name := range pendingStrs {
			resp.Pending = append(resp.Pending, nameToReloadAgent(name))
		}
	}

	return resp, nil
}
