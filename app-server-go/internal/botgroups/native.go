package botgroups

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// NativeController issues typed commands through the daemon and waits for
// correlated completion in the App's durable event projection. App cancellation
// only releases the wait; it does not kill or replay the accepted native command.
type NativeController struct {
	Store            *store.Store
	Client           *daemonclient.Client
	Publisher        Publisher
	PollInterval     time.Duration
	DefaultWorkspace string
}

func meta(id string) *pb.CommandMeta {
	return &pb.CommandMeta{OperationId: id, CommandId: &id, Canonicalization: pb.CanonicalizationVersion_CANONICALIZATION_VERSION_LEGACY_JSON_V1}
}
func native(value any) *pb.NativeJson {
	raw, _ := json.Marshal(value)
	return &pb.NativeJson{Schema: "astrorder.bot-group.native-options.v1", Utf8Json: raw}
}
func confirm(receipt *pb.CommandReceipt) error {
	if receipt == nil {
		return errors.New("native command returned no receipt")
	}
	if receipt.OperationState == pb.OperationState_OPERATION_STATE_UNCERTAIN || receipt.GetUncertain() != nil {
		return errors.New("native outcome is uncertain; reconcile before retry")
	}
	if receipt.GetRejected() != nil {
		return errors.New(receipt.GetRejected().Message)
	}
	if receipt.OperationState != pb.OperationState_OPERATION_STATE_COMPLETED || receipt.GetCompleted() == nil {
		return errors.New("native command is not confirmed")
	}
	return nil
}
func (c *NativeController) target(ctx context.Context, member Member) (*pb.RuntimeTarget, string, error) {
	agent, err := c.Store.GetAgent(ctx, member.AgentID)
	if err != nil {
		return nil, "", err
	}
	if agent == nil {
		return nil, "", errors.New("group Agent not found")
	}
	kind, _ := agent["kind"].(string)
	target := &pb.RuntimeTarget{AgentId: &member.AgentID}
	if value, ok := agent["connection_id"].(string); ok && value != "" {
		target.ConnectionId = &value
	}
	if member.MachineID != "local" {
		if target.ConnectionId != nil && *target.ConnectionId != member.MachineID {
			return nil, "", errors.New("group machine and Agent connection disagree")
		}
		target.ConnectionId = &member.MachineID
	}
	remote := target.ConnectionId != nil && *target.ConnectionId != "local"
	switch kind {
	case "codex", "codex-ssh":
		target.Runtime = pb.RuntimeKind_RUNTIME_KIND_CODEX
		if remote {
			target.Runtime = pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH
		}
	case "hermes", "ssh":
		target.Runtime = pb.RuntimeKind_RUNTIME_KIND_HERMES
		if remote {
			target.Runtime = pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES
		}
	case "grok", "grok-ssh":
		target.Runtime = pb.RuntimeKind_RUNTIME_KIND_GROK
		if remote {
			target.Runtime = pb.RuntimeKind_RUNTIME_KIND_GROK_SSH
		}
	default:
		return nil, "", errors.New("group Agent runtime is unsupported")
	}
	workspace := c.DefaultWorkspace
	if member.Workspace != nil {
		workspace = *member.Workspace
	}
	if remote {
		connection, err := c.Store.GetSSHConnection(ctx, *target.ConnectionId)
		if err != nil {
			return nil, "", err
		}
		if connection == nil {
			return nil, "", errors.New("group connection not found")
		}
		settings, ok := connection["settings"].(map[string]any)
		if !ok {
			return nil, "", errors.New("invalid group connection")
		}
		stringField := func(key string) string { value, _ := settings[key].(string); return value }
		ptr := func(key string) *string {
			if value, ok := settings[key].(string); ok {
				return &value
			}
			return nil
		}
		port := uint32(22)
		if value := settings["port"]; value != nil {
			parsed, err := strconv.ParseUint(fmt.Sprint(value), 10, 16)
			if err != nil || parsed == 0 {
				return nil, "", errors.New("invalid SSH port")
			}
			port = uint32(parsed)
		}
		target.Ssh = &pb.SshConnectConfig{Host: stringField("host"), Port: port, User: ptr("user"), SshConfigAlias: ptr("ssh_config_alias"), IdentityFile: ptr("identity_file"), HermesPath: ptr("hermes_path")}
		if workspace == "" {
			workspace = stringField("workspace")
		}
		if workspace != "" {
			target.Ssh.Workspace = &workspace
		}
		if profile, ok := connection["profile_name"].(string); ok && profile != "" {
			target.Ssh.ProfileName = &profile
		}
	}
	return target, workspace, nil
}
func (c *NativeController) session(ctx context.Context, turn Turn) (*pb.SessionRef, error) {
	if c.Store == nil || c.Client == nil || c.Publisher == nil {
		return nil, errors.New("group native controller is unavailable")
	}
	target, workspace, err := c.target(ctx, turn.Member)
	if err != nil {
		return nil, err
	}
	bindingKey := "group_session:" + turn.GroupID + ":" + turn.Member.AgentID
	binding, err := c.Store.GetPreference(ctx, bindingKey)
	if err != nil {
		return nil, err
	}
	if bound, ok := binding.Value.(map[string]any); ok {
		sid, _ := bound["session_id"].(string)
		if sid == "" {
			return nil, errors.New("group session binding is invalid")
		}
		ref := &pb.SessionRef{SessionId: sid, AgentId: &turn.Member.AgentID, ConnectionId: target.ConnectionId}
		receipt, err := c.Client.Session.AttachSession(ctx, &pb.AttachSessionRequest{Session: ref, Target: target})
		if err != nil {
			return nil, err
		}
		if receipt == nil || !receipt.Attached || receipt.Session == nil || receipt.Session.SessionId != sid {
			return nil, errors.New("group session attach not confirmed")
		}
		return ref, nil
	}
	create := &pb.CreateSessionRequest{Meta: meta(turn.CommandID + "-create"), Target: target}
	extras := map[string]any{}
	if workspace != "" {
		extras["cwd"] = workspace
	}
	if turn.Member.ApprovalPolicy != nil {
		extras["approvalPolicy"] = *turn.Member.ApprovalPolicy
	}
	switch target.Runtime {
	case pb.RuntimeKind_RUNTIME_KIND_CODEX, pb.RuntimeKind_RUNTIME_KIND_CODEX_SSH:
		delete(extras, "cwd")
		create.Options = &pb.CreateSessionRequest_Codex{Codex: &pb.CodexCreateOptions{Cwd: workspace, Model: turn.Member.ModelName, Provider: turn.Member.ModelProvider, Effort: turn.Member.ThinkingEffort, NativeOptions: native(extras)}}
	case pb.RuntimeKind_RUNTIME_KIND_HERMES, pb.RuntimeKind_RUNTIME_KIND_SSH_HERMES:
		create.Options = &pb.CreateSessionRequest_Hermes{Hermes: &pb.HermesCreateOptions{Model: turn.Member.ModelName, Provider: turn.Member.ModelProvider, Effort: turn.Member.ThinkingEffort, NativeOptions: native(extras)}}
	default:
		delete(extras, "cwd")
		create.Options = &pb.CreateSessionRequest_Grok{Grok: &pb.GrokCreateOptions{Cwd: workspace, Model: turn.Member.ModelName, Effort: turn.Member.ThinkingEffort, NativeOptions: native(extras)}}
	}
	receipt, err := c.Client.Session.CreateSession(ctx, create)
	if err != nil {
		return nil, err
	}
	if err = confirm(receipt); err != nil {
		return nil, err
	}
	if receipt.Session == nil || receipt.Session.SessionId == "" {
		return nil, errors.New("native group session identity missing")
	}
	if receipt.GetCompleted().Native == nil {
		return nil, errors.New("native group owner identity missing")
	}
	decoder := json.NewDecoder(bytes.NewReader(receipt.GetCompleted().Native.Utf8Json))
	decoder.UseNumber()
	var payload map[string]any
	if err = decoder.Decode(&payload); err != nil {
		return nil, err
	}
	nativeAgent, _ := payload["agent_id"].(string)
	if nativeAgent != turn.Member.AgentID {
		return nil, errors.New("native group owner identity differs from selected Agent")
	}
	ref := &pb.SessionRef{SessionId: receipt.Session.SessionId, AgentId: &turn.Member.AgentID, ConnectionId: target.ConnectionId}
	if _, err = c.Store.UpsertSession(ctx, map[string]any{"id": ref.SessionId, "agent_id": turn.Member.AgentID, "title": "群聊 " + turn.GroupID, "workspace": workspace, "status": "idle", "control_state": "owned", "native_kind": "rpc", "updated_at": now()}); err != nil {
		return nil, err
	}
	err = c.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := readGroupTx(ctx, tx, turn.GroupID); err != nil {
			return err
		}
		if err := putTx(ctx, tx, bindingKey, map[string]any{"agent_id": turn.Member.AgentID, "session_id": ref.SessionId, "connection_id": target.ConnectionId}); err != nil {
			return err
		}
		return putTx(ctx, tx, "blackboard_scope:"+turn.Member.AgentID+"::"+ref.SessionId, "group::"+turn.GroupID)
	})
	if err != nil {
		return nil, err
	}
	return ref, nil
}
func (c *NativeController) Run(ctx context.Context, turn Turn, bound func(string) error) (string, error) {
	ref, err := c.session(ctx, turn)
	if err != nil {
		return "", err
	}
	if err = bound(ref.SessionId); err != nil {
		return "", err
	}
	// Use a stable row boundary in this dedicated group-member session. No
	// timestamps or text heuristics are used to associate native messages.
	row, err := c.Store.QueryRow(ctx, `SELECT COALESCE(MAX(row_id),0) AS cursor FROM messages WHERE agent_id=? AND session_id=?`, turn.Member.AgentID, ref.SessionId)
	if err != nil {
		return "", err
	}
	cursor, err := strconv.ParseInt(fmt.Sprint(row["cursor"]), 10, 64)
	if err != nil {
		return "", err
	}
	payload := map[string]any{"id": turn.CommandID, "agent_id": turn.Member.AgentID, "session_id": ref.SessionId, "action": "send", "text": turn.Prompt, "attachments": []any{}}
	_, created, err := c.Store.CreateCommand(ctx, payload, "received")
	if err != nil {
		return "", err
	}
	if !created {
		return "", errors.New("group command was already submitted; reconciliation required")
	}
	receipt, err := c.Client.Session.Send(ctx, &pb.SendRequest{Meta: meta(turn.CommandID), Session: ref, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: turn.Prompt}}}}, Policy: &pb.SendPolicy{Model: turn.Member.ModelName, ApprovalPolicy: turn.Member.ApprovalPolicy}})
	if err == nil {
		err = confirm(receipt)
	}
	state := "accepted"
	var detail any
	if err != nil {
		state = "unknown"
		detail = "native outcome requires reconciliation"
	}
	command, updateErr := c.Store.SetCommandState(ctx, turn.Member.AgentID, ref.SessionId, turn.CommandID, state, detail, true)
	if updateErr != nil {
		return "", updateErr
	}
	if publishErr := c.publishCommand(ctx, command); publishErr != nil {
		return "", publishErr
	}
	if err != nil {
		return "", err
	}
	interval := c.PollInterval
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		command, err = c.Store.GetCommand(ctx, turn.Member.AgentID, ref.SessionId, turn.CommandID)
		if err != nil {
			return "", err
		}
		if command == nil {
			return "", errors.New("group command disappeared")
		}
		switch command["state"] {
		case "failed", "cancelled", "unknown":
			return "", errors.New("native group command did not complete: " + fmt.Sprint(command["state"]))
		case "completed":
			messages, err := c.Store.QueryRows(ctx, `SELECT role,text,command_id FROM messages WHERE agent_id=? AND session_id=? AND row_id>? ORDER BY row_id LIMIT 200`, turn.Member.AgentID, ref.SessionId, cursor)
			if err != nil {
				return "", err
			}
			parts := []string{}
			for _, message := range messages {
				if message["role"] == "assistant" && (message["command_id"] == nil || message["command_id"] == turn.CommandID) {
					if text, ok := message["text"].(string); ok && strings.TrimSpace(text) != "" {
						parts = append(parts, text)
					}
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n\n"), nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
func (c *NativeController) publishCommand(ctx context.Context, command map[string]any) error {
	var event store.Event
	err := c.Store.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		event, err = appendEventTx(ctx, tx, "command.upsert", command)
		return err
	})
	if err == nil {
		c.Publisher.PublishCommitted([]store.Event{event})
	}
	return err
}
func (c *NativeController) Interrupt(ctx context.Context, turn Turn) error {
	if c.Client == nil || turn.SessionID == "" {
		return errors.New("group native session is unavailable")
	}
	target, _, err := c.target(ctx, turn.Member)
	if err != nil {
		return err
	}
	receipt, err := c.Client.Session.Interrupt(ctx, &pb.InterruptRequest{Meta: meta(turn.CommandID + "-interrupt"), Session: &pb.SessionRef{SessionId: turn.SessionID, AgentId: &turn.Member.AgentID, ConnectionId: target.ConnectionId}})
	if err != nil {
		return err
	}
	return confirm(receipt)
}
