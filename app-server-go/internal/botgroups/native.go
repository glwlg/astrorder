package botgroups

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/daemonclient"
	"astrorder.dev/app-server/internal/sessions"
	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NativeController issues typed commands through the daemon and waits for
// correlated completion in the command ledger and results in native history. App cancellation
// only releases the wait; it does not kill or replay the accepted native command.
type NativeController struct {
	Store             *store.Store
	Client            *daemonclient.Client
	Publisher         Publisher
	PollInterval      time.Duration
	DefaultWorkspace  string
	AttachmentsDir    string
	MaxAttachmentSize int64
}

type uncertainDeliveryError struct{ cause error }

func (e *uncertainDeliveryError) Error() string { return e.cause.Error() }
func (e *uncertainDeliveryError) Unwrap() error { return e.cause }

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
	agentConnection, _ := agent["connection_id"].(string)
	if connectionID(agentConnection) != connectionID(member.MachineID) {
		return nil, "", errors.New("group machine and Agent connection disagree")
	}
	target := &pb.RuntimeTarget{AgentId: &member.AgentID}
	if agentConnection != "" && agentConnection != "local" {
		target.ConnectionId = &agentConnection
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
		target.Ssh = &pb.SshConnectConfig{Host: stringField("host"), Port: port, User: ptr("user"), SshConfigAlias: ptr("ssh_config_alias"), IdentityFile: ptr("identity_file"), HermesPath: ptr("hermes_path"), CodexExecutable: ptr("codex_executable"), GrokExecutable: ptr("grok_executable")}
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
	bindingKey := "group_session:" + turn.GroupID + ":" + memberID(turn.Member)
	binding, err := c.Store.GetPreference(ctx, bindingKey)
	if err != nil {
		return nil, err
	}
	if bound, ok := binding.Value.(map[string]any); ok {
		sid, _ := bound["session_id"].(string)
		boundAgent, _ := bound["agent_id"].(string)
		boundConnection, _ := bound["connection_id"].(string)
		if sid == "" || boundAgent != turn.Member.AgentID || connectionID(boundConnection) != connectionID(target.GetConnectionId()) || (turn.Member.ForkSessionID != nil && sid != memberValue(turn.Member.ForkSessionID)) {
			return nil, errors.New("group session binding is invalid")
		}
		stored, err := c.Store.GetSession(ctx, turn.Member.AgentID, sid)
		if err != nil {
			return nil, err
		}
		if stored == nil {
			return nil, errors.New("group session record is missing")
		}
		storedWorkspace, _ := stored["workspace"].(string)
		if turn.Member.Workspace != nil && strings.TrimSpace(storedWorkspace) != strings.TrimSpace(*turn.Member.Workspace) {
			return nil, errors.New("group session workspace changed")
		}
		workspace = storedWorkspace
		if target.Ssh != nil {
			target.Ssh.Workspace = nil
			if workspace != "" {
				target.Ssh.Workspace = &workspace
			}
		}
		ref := &pb.SessionRef{SessionId: sid, AgentId: &turn.Member.AgentID, ConnectionId: target.ConnectionId}
		if c.Client.Runtime == nil {
			return nil, errors.New("group native session query is unavailable")
		}
		model, err := c.Client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
		if err == nil {
			if model == nil || model.Session == nil || model.Session.SessionId != sid || model.Session.GetAgentId() != turn.Member.AgentID || model.Session.GetConnectionId() != target.GetConnectionId() {
				return nil, errors.New("group session identity not confirmed")
			}
			return ref, nil
		}
		if status.Code(err) != codes.NotFound && status.Code(err) != codes.Unavailable {
			return nil, err
		}
		var cwd *string
		if workspace != "" {
			cwd = &workspace
		}
		receipt, err := c.Client.Session.AttachSession(ctx, &pb.AttachSessionRequest{Session: ref, Target: target, Cwd: cwd})
		if err != nil {
			return nil, err
		}
		if receipt == nil || !receipt.Attached || receipt.Session == nil || receipt.Session.SessionId != sid || receipt.Session.GetAgentId() != turn.Member.AgentID || receipt.Session.GetConnectionId() != target.GetConnectionId() {
			return nil, errors.New("group session attach not confirmed")
		}
		return ref, nil
	}
	if binding.Exists {
		return nil, errors.New("group session binding is invalid")
	}
	if turn.Member.ForkSessionID != nil {
		return nil, errors.New("群成员的分叉会话绑定不存在，请重新添加该成员")
	}
	create := &pb.CreateSessionRequest{Meta: meta(turn.CommandID + "-create"), Target: target}
	extras := map[string]any{}
	if workspace != "" {
		extras["cwd"] = workspace
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
	if _, err = c.Store.UpsertSession(ctx, map[string]any{"id": ref.SessionId, "agent_id": turn.Member.AgentID, "connection_id": target.GetConnectionId(), "title": "群聊 " + turn.GroupID, "workspace": workspace, "status": "idle", "control_state": "owned", "native_kind": "rpc", "updated_at": now()}); err != nil {
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

func memberModelMatches(provider, model string, state *pb.ModelState) bool {
	if state == nil || model == "" || state.GetModel() == "" {
		return false
	}
	readProvider := state.GetProvider()
	if provider != "" && readProvider != "" && provider != readProvider {
		return false
	}
	qualification := provider
	if qualification == "" {
		qualification = readProvider
	}
	readModel := state.GetModel()
	if qualification != "" {
		model = strings.TrimPrefix(model, qualification+"/")
		readModel = strings.TrimPrefix(readModel, qualification+"/")
	}
	return model != "" && model == readModel
}

func (c *NativeController) configureMemberSession(ctx context.Context, turn Turn, ref *pb.SessionRef) error {
	model, provider, effort := memberValue(turn.Member.ModelName), memberValue(turn.Member.ModelProvider), memberValue(turn.Member.ThinkingEffort)
	if model != "" || effort != "" {
		if c.Client.Runtime == nil {
			return errors.New("group model settings are unavailable")
		}
		read := func() (*pb.ModelState, error) {
			state, err := c.Client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: ref})
			if err != nil {
				return nil, err
			}
			if state == nil || state.Session == nil || state.Session.SessionId != ref.SessionId || state.Session.GetAgentId() != ref.GetAgentId() || state.Session.GetConnectionId() != ref.GetConnectionId() {
				return nil, errors.New("group model session identity not confirmed")
			}
			return state, nil
		}
		state, err := read()
		if err != nil {
			return err
		}
		if model != "" && !memberModelMatches(provider, model, state) {
			receipt, err := c.Client.Runtime.SetModel(ctx, &pb.SetModelRequest{Meta: meta(turn.CommandID + "-model"), Session: ref, Provider: provider, Model: model})
			if err != nil {
				return err
			}
			if err := confirm(receipt); err != nil {
				return err
			}
			state, err = read()
			if err != nil || !memberModelMatches(provider, model, state) {
				return errors.New("群成员模型未确认，消息未发送")
			}
		}
		if effort != "" && state.GetEffort() != effort {
			receipt, err := c.Client.Runtime.SetReasoning(ctx, &pb.SetReasoningRequest{Meta: meta(turn.CommandID + "-effort"), Session: ref, Effort: effort})
			if err != nil {
				return err
			}
			if err := confirm(receipt); err != nil {
				return err
			}
			state, err = read()
			if err != nil || state.GetEffort() != effort {
				return errors.New("群成员思考强度未确认，消息未发送")
			}
		}
	}
	mode := memberValue(turn.Member.ApprovalPolicy)
	if mode == "" {
		return nil
	}
	if c.Client.Runtime == nil {
		return errors.New("group approval settings are unavailable")
	}
	readApproval := func() (*pb.ApprovalModeState, error) {
		state, err := c.Client.Runtime.ReadApprovalMode(ctx, &pb.ReadApprovalModeRequest{Session: ref})
		if err != nil {
			return nil, err
		}
		if state == nil || state.Session == nil || state.Session.SessionId != ref.SessionId || state.Session.GetAgentId() != ref.GetAgentId() || state.Session.GetConnectionId() != ref.GetConnectionId() {
			return nil, errors.New("group approval session identity not confirmed")
		}
		return state, nil
	}
	state, err := readApproval()
	if err != nil {
		return err
	}
	if state.Mode == mode {
		return nil
	}
	receipt, err := c.Client.Runtime.SetApprovalMode(ctx, &pb.SetApprovalModeRequest{Meta: meta(turn.CommandID + "-approval"), Session: ref, Mode: mode})
	if err != nil {
		return err
	}
	if err := confirm(receipt); err != nil {
		return err
	}
	state, err = readApproval()
	if err != nil || state.Mode != mode {
		return errors.New("群成员审批模式未确认，消息未发送")
	}
	return nil
}

func (c *NativeController) Run(ctx context.Context, turn Turn, bound func(string) error) (Reply, error) {
	ref, err := c.session(ctx, turn)
	if err != nil {
		return Reply{}, err
	}
	if err = bound(ref.SessionId); err != nil {
		return Reply{}, err
	}
	if err = c.configureMemberSession(ctx, turn, ref); err != nil {
		return Reply{}, err
	}
	// Capture native IDs before dispatch in this dedicated member session.
	baseline, err := c.history(ctx, ref)
	if err != nil {
		return Reply{}, err
	}
	seen := make(map[string]bool)
	for _, message := range baseline {
		seen[fmt.Sprint(message["id"])] = true
	}
	if turn.RecordBaseline != nil {
		ids := make([]string, 0, len(seen))
		for id := range seen {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if err := turn.RecordBaseline(ids); err != nil {
			return Reply{}, err
		}
	}
	attachments := make([]any, len(turn.Attachments))
	for i, attachment := range turn.Attachments {
		attachments[i] = attachment
	}
	payload := map[string]any{"id": turn.CommandID, "agent_id": turn.Member.AgentID, "session_id": ref.SessionId, "action": "send", "text": turn.Prompt, "attachments": attachments}
	input, err := sessions.CommandInputs(ctx, sessions.Options{Store: c.Store, AttachmentsDir: c.AttachmentsDir, MaxAttachmentSize: c.MaxAttachmentSize}, payload, nil)
	if err != nil {
		return Reply{}, err
	}
	_, created, err := c.Store.CreateCommand(ctx, payload, "received")
	if err != nil {
		return Reply{}, err
	}
	if !created {
		return Reply{}, errors.New("group command was already submitted; reconciliation required")
	}
	receipt, err := c.Client.Session.Send(ctx, &pb.SendRequest{Meta: meta(turn.CommandID), Session: ref, Input: input, Policy: &pb.SendPolicy{Model: turn.Member.ModelName}})
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
		return Reply{}, &uncertainDeliveryError{updateErr}
	}
	if publishErr := c.publishCommand(ctx, command); publishErr != nil {
		return Reply{}, &uncertainDeliveryError{publishErr}
	}
	if err != nil {
		return Reply{}, &uncertainDeliveryError{err}
	}
	if turn.RecordAccepted != nil {
		if err := turn.RecordAccepted(); err != nil {
			return Reply{}, &uncertainDeliveryError{err}
		}
	}
	return c.waitForReply(ctx, turn, ref, seen)
}

// Resume follows an already-persisted command. It never submits the command
// again, even when its receipt is unknown.
func (c *NativeController) Resume(ctx context.Context, turn Turn, baseline []string) (Reply, error) {
	if turn.SessionID == "" {
		return Reply{}, &uncertainDeliveryError{errors.New("群聊会话未确认")}
	}
	binding, err := c.Store.GetPreference(ctx, "group_session:"+turn.GroupID+":"+memberID(turn.Member))
	if err != nil {
		return Reply{}, &uncertainDeliveryError{err}
	}
	value, ok := binding.Value.(map[string]any)
	if !binding.Exists || !ok || value["session_id"] != turn.SessionID || value["agent_id"] != turn.Member.AgentID {
		return Reply{}, &uncertainDeliveryError{errors.New("群聊会话绑定未确认")}
	}
	ref, err := c.session(ctx, turn)
	if err != nil || ref == nil || ref.SessionId != turn.SessionID {
		if err == nil {
			err = errors.New("群聊会话身份未确认")
		}
		return Reply{}, &uncertainDeliveryError{err}
	}
	seen := make(map[string]bool, len(baseline))
	for _, id := range baseline {
		seen[id] = true
	}
	command, err := c.Store.GetCommand(ctx, turn.Member.AgentID, ref.SessionId, turn.CommandID)
	if err != nil || command == nil || (command["state"] != "accepted" && command["state"] != "running" && command["state"] != "completed") {
		if err == nil {
			err = errors.New("群聊任务结果未确认")
		}
		return Reply{}, &uncertainDeliveryError{err}
	}
	if turn.RecordAccepted != nil {
		if err := turn.RecordAccepted(); err != nil {
			return Reply{}, &uncertainDeliveryError{err}
		}
	}
	return c.waitForReply(ctx, turn, ref, seen)
}

func (c *NativeController) waitForReply(ctx context.Context, turn Turn, ref *pb.SessionRef, seen map[string]bool) (Reply, error) {
	interval := c.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		command, err := c.Store.GetCommand(ctx, turn.Member.AgentID, ref.SessionId, turn.CommandID)
		if err != nil {
			return Reply{}, &uncertainDeliveryError{err}
		}
		if command == nil {
			return Reply{}, &uncertainDeliveryError{errors.New("群聊任务未确认")}
		}
		switch command["state"] {
		case "unknown", "received":
			return Reply{}, &uncertainDeliveryError{errors.New("群聊任务结果未确认")}
		case "failed", "cancelled":
			return Reply{}, errors.New("native group command did not complete: " + fmt.Sprint(command["state"]))
		case "completed":
			prompt, _ := command["text"].(string)
			if prompt == "" {
				return Reply{}, &uncertainDeliveryError{errors.New("群聊任务内容未确认")}
			}
			messages, err := c.historyPages(ctx, ref, nil, prompt)
			if err != nil {
				if ctx.Err() != nil {
					return Reply{}, ctx.Err()
				}
				if status.Code(err) != codes.Unavailable && status.Code(err) != codes.DeadlineExceeded {
					return Reply{}, &uncertainDeliveryError{err}
				}
				break
			}
			parts := []string{}
			ids := []string{}
			start := -1
			for i, message := range messages {
				if message["role"] == "user" && message["text"] == prompt && !seen[fmt.Sprint(message["id"])] {
					start = i
				}
			}
			if start < 0 {
				break
			}
			for _, message := range messages[start+1:] {
				if message["role"] == "user" {
					break
				}
				if !seen[fmt.Sprint(message["id"])] && message["role"] == "assistant" && message["kind"] == "message" && message["group_nonfinal"] != true {
					if text, ok := message["text"].(string); ok && strings.TrimSpace(text) != "" {
						parts = append(parts, text)
						ids = append(ids, fmt.Sprint(message["id"]))
					}
				}
			}
			if len(parts) > 0 {
				return Reply{Text: strings.Join(parts, "\n\n"), NativeIDs: ids}, nil
			}
		}
		select {
		case <-ctx.Done():
			return Reply{}, ctx.Err()
		case <-ticker.C:
		}
	}
}
func (c *NativeController) history(ctx context.Context, ref *pb.SessionRef) ([]map[string]any, error) {
	return c.historyPages(ctx, ref, nil, "")
}

// ReadReply resolves persisted message references from native history, including older pages.
func (c *NativeController) ReadReply(ctx context.Context, agentID, sessionID string, ids []string) (string, error) {
	agent, err := c.Store.GetAgent(ctx, agentID)
	if err != nil {
		return "", err
	}
	if agent == nil {
		return "", errors.New("group Agent not found")
	}
	ref := &pb.SessionRef{AgentId: &agentID, SessionId: sessionID}
	if connection, ok := agent["connection_id"].(string); ok && connection != "" && connection != "local" {
		ref.ConnectionId = &connection
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	var messages []map[string]any
	kind, _ := agent["kind"].(string)
	if kind == "codex" || kind == "codex-ssh" {
		messages, err = c.readCodexReplyHistory(ctx, ref, wanted)
	} else {
		messages, err = c.historyPages(ctx, ref, wanted, "")
	}
	if err != nil {
		return "", err
	}
	text := map[string]string{}
	for _, message := range messages {
		if message["role"] == "assistant" && message["kind"] == "message" {
			text[fmt.Sprint(message["id"])], _ = message["text"].(string)
		}
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		value, ok := text[id]
		if !ok {
			return "", errors.New("native group reply is unavailable")
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "\n\n"), nil
}

func (c *NativeController) readCodexReplyHistory(ctx context.Context, ref *pb.SessionRef, wanted map[string]bool) ([]map[string]any, error) {
	row, err := c.Store.GetSession(ctx, ref.GetAgentId(), ref.SessionId)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, errors.New("group session record is missing")
	}
	storedConnection, _ := row["connection_id"].(string)
	if connectionID(storedConnection) != connectionID(ref.GetConnectionId()) {
		return nil, errors.New("group session connection changed")
	}
	workspace, _ := row["workspace"].(string)
	target, _, err := c.target(ctx, Member{AgentID: ref.GetAgentId(), MachineID: storedConnection, Workspace: &workspace})
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := c.Client.ReadCodexHistoryPage(ctx, target, ref.SessionId, cursor, 200)
		if err != nil {
			return nil, err
		}
		for _, item := range page.Data {
			projected := sessions.ProjectNativeHistoryItem(item, ref.GetAgentId(), ref.SessionId, ref.SessionId, ref.GetAgentId(), "codex")
			for _, message := range projected {
				if message["role"] == "assistant" && message["kind"] == "message" {
					delete(wanted, fmt.Sprint(message["id"]))
				}
			}
			items = append(items, projected...)
		}
		if len(wanted) == 0 || page.NextCursor == nil {
			return items, nil
		}
		cursor = *page.NextCursor
		if seen[cursor] {
			return nil, errors.New("invalid native group history cursor")
		}
		seen[cursor] = true
	}
}

func (c *NativeController) historyPages(ctx context.Context, ref *pb.SessionRef, wanted map[string]bool, throughUser string) ([]map[string]any, error) {
	if c.Client.Runtime == nil {
		return nil, errors.New("native group history is unavailable")
	}
	agent, err := c.Store.GetAgent(ctx, ref.GetAgentId())
	if err != nil {
		return nil, err
	}
	kind, _ := agent["kind"].(string)
	request := &pb.HistoryPageRequest{Session: ref, Limit: 200}
	items := []map[string]any{}
	seenCursors := map[string]bool{}
	for {
		page, err := c.Client.Runtime.GetHistoryPage(ctx, request)
		if err != nil {
			return nil, err
		}
		if page == nil || page.Session == nil || page.Session.SessionId != ref.SessionId || page.Session.GetAgentId() != ref.GetAgentId() || page.Session.GetConnectionId() != ref.GetConnectionId() {
			return nil, errors.New("native group history identity mismatch")
		}
		pageItems := []map[string]any{}
		for _, raw := range page.Items {
			if raw == nil {
				return nil, errors.New("invalid native group history item")
			}
			var item map[string]any
			decoder := json.NewDecoder(bytes.NewReader(raw.Utf8Json))
			decoder.UseNumber()
			if err := decoder.Decode(&item); err != nil {
				return nil, err
			}
			nativeItem := item
			if nested, ok := item["item"].(map[string]any); ok {
				nativeItem = nested
			}
			projected := sessions.ProjectNativeHistoryItem(item, ref.GetAgentId(), ref.SessionId, ref.SessionId, ref.GetAgentId(), kind)
			phase, _ := nativeItem["phase"].(string)
			nonfinal := nativeItem["type"] == "plan" || nativeItem["delivery"] == "async" || (phase != "" && phase != "final_answer")
			if nonfinal {
				for _, row := range projected {
					row["group_nonfinal"] = true
				}
			}
			pageItems = append(pageItems, projected...)
		}
		if throughUser != "" {
			items = append(pageItems, items...)
			for _, item := range pageItems {
				if item["role"] == "user" && item["text"] == throughUser {
					return items, nil
				}
			}
			if !page.GetHasMore() {
				return items, nil
			}
		} else {
			items = append(items, pageItems...)
		}
		if wanted == nil && throughUser == "" {
			return items, nil
		}
		if throughUser == "" {
			for _, item := range pageItems {
				delete(wanted, fmt.Sprint(item["id"]))
			}
			if len(wanted) == 0 || !page.GetHasMore() {
				return items, nil
			}
		}
		cursor := page.GetNextCursor().GetValue()
		if cursor == "" || seenCursors[cursor] {
			return nil, errors.New("invalid native group history cursor")
		}
		seenCursors[cursor] = true
		request.Before = &pb.NullableString{State: &pb.NullableString_Value{Value: cursor}}
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
