package sessions

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func newCommandMeta(operationID, commandID string) *sessiondv1.CommandMeta {
	if operationID == "" {
		operationID = commandID
		if operationID == "" {
			operationID = "op-" + rand.Text()
		}
	}
	meta := &sessiondv1.CommandMeta{
		OperationId:      operationID,
		Canonicalization: sessiondv1.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1,
	}
	if commandID != "" {
		meta.CommandId = &commandID
	}
	return meta
}

func buildSessionRef(agentID, sessionID, connectionID string) *sessiondv1.SessionRef {
	ref := &sessiondv1.SessionRef{
		SessionId: sessionID,
	}
	if agentID != "" {
		ref.AgentId = &agentID
	}
	if connectionID != "" && connectionID != "local" {
		ref.ConnectionId = &connectionID
	}
	return ref
}

func (h *Handler) requireAgentConnection(ctx context.Context, agent map[string]any) error {
	connID := stringValue(agent, "connection_id")
	if connID == "" {
		connID = "local"
	}
	enabled, present, err := h.store.GetAgentConnectionChoice(ctx, connID+":"+stringValue(agent, "kind"))
	if err != nil {
		return err
	}
	if present && !enabled {
		return fmt.Errorf("Agent 已主动断联，请在连接管理中重新接入")
	}
	return nil
}

func (h *Handler) resolveRuntimeTarget(ctx context.Context, agent map[string]any) (*sessiondv1.RuntimeTarget, error) {
	aid, _ := agent["id"].(string)
	kind, _ := agent["kind"].(string)
	connID, _ := agent["connection_id"].(string)
	if err := h.requireAgentConnection(ctx, agent); err != nil {
		return nil, err
	}

	target := &sessiondv1.RuntimeTarget{
		AgentId: &aid,
	}
	if connID != "" && connID != "local" {
		target.ConnectionId = &connID
	}

	isSSH := connID != "" && connID != "local"
	switch kind {
	case "codex":
		if isSSH {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX_SSH
		} else {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_CODEX
		}
	case "hermes":
		if isSSH {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_SSH_HERMES
		} else {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_HERMES
		}
	case "antigravity":
		if isSSH {
			return nil, fmt.Errorf("Antigravity SSH runtime is not supported yet")
		}
		target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY
	case "grok":
		if isSSH {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_GROK_SSH
		} else {
			target.Runtime = sessiondv1.RuntimeKind_RUNTIME_KIND_GROK
		}
	default:
		return nil, fmt.Errorf("unsupported agent kind: %s", kind)
	}

	if isSSH {
		sshRow, err := h.store.GetSSHConnection(ctx, connID)
		if err != nil {
			return nil, err
		}
		if sshRow == nil {
			return nil, fmt.Errorf("ssh connection %s not found", connID)
		}
		settings, _ := sshRow["settings"].(map[string]any)
		host, _ := settings["host"].(string)
		portNum := 22
		if pVal, ok := settings["port"]; ok {
			switch p := pVal.(type) {
			case int:
				portNum = p
			case int64:
				if p < 1 || p > 65535 {
					return nil, fmt.Errorf("invalid SSH port")
				}
				portNum = int(p)
			case json.Number:
				if n, err := strconv.Atoi(p.String()); err == nil {
					portNum = n
				} else {
					return nil, fmt.Errorf("invalid SSH port")
				}
			case string:
				if n, err := strconv.Atoi(p); err == nil {
					portNum = n
				} else {
					return nil, fmt.Errorf("invalid SSH port")
				}
			default:
				return nil, fmt.Errorf("invalid SSH port")
			}
		}
		if portNum < 1 || portNum > 65535 {
			return nil, fmt.Errorf("invalid SSH port")
		}
		if host == "" && stringValue(settings, "ssh_config_alias") == "" {
			return nil, fmt.Errorf("SSH host or alias is required")
		}
		sshConf := &sessiondv1.SshConnectConfig{
			Host: host,
			Port: uint32(portNum),
		}
		if u, ok := settings["user"].(string); ok && u != "" {
			sshConf.User = &u
		}
		if a, ok := settings["ssh_config_alias"].(string); ok && a != "" {
			sshConf.SshConfigAlias = &a
		}
		if idf, ok := settings["identity_file"].(string); ok && idf != "" {
			sshConf.IdentityFile = &idf
		}
		if ws, ok := settings["workspace"].(string); ok && ws != "" {
			sshConf.Workspace = &ws
		} else {
			defaultWs := "/home/luwei"
			sshConf.Workspace = &defaultWs
		}
		if ce, ok := settings["codex_executable"].(string); ok && ce != "" {
			sshConf.CodexExecutable = &ce
		} else if kind == "codex" {
			defaultCodex := "/home/luwei/.vite-plus/bin/codex"
			sshConf.CodexExecutable = &defaultCodex
		}
		if ge, ok := settings["grok_executable"].(string); ok && ge != "" {
			sshConf.GrokExecutable = &ge
		} else if kind == "grok" {
			defaultGrok := "/home/luwei/.local/bin/grok"
			sshConf.GrokExecutable = &defaultGrok
		}
		if hp, ok := settings["hermes_path"].(string); ok && hp != "" {
			sshConf.HermesPath = &hp
		} else if kind == "hermes" {
			defaultHermes := "/home/luwei/.local/bin/hermes"
			sshConf.HermesPath = &defaultHermes
		}
		if profile, ok := sshRow["profile_name"].(string); ok && profile != "" {
			sshConf.ProfileName = &profile
		}
		target.Ssh = sshConf
	}
	return target, nil
}

func parseNativeJSON(nj *sessiondv1.NativeJson, dst any) error {
	if nj == nil || len(nj.Utf8Json) == 0 {
		return fmt.Errorf("native JSON is missing")
	}
	dec := json.NewDecoder(bytes.NewReader(nj.Utf8Json))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing native JSON")
	}
	return nil
}

func encodeNativeJSON(schema string, v any) (*sessiondv1.NativeJson, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &sessiondv1.NativeJson{
		Schema:   schema,
		Utf8Json: b,
	}, nil
}

func stringValue(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
