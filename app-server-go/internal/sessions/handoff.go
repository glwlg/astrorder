package sessions

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

const handoffSummaryPrompt = `请为接手这个会话的另一个 Agent 生成一份完整、可直接继续工作的交接摘要。
你拥有分叉时继承的完整会话上下文，请综合整个会话，而不是只复述最近几条消息。
不要执行命令、调用工具或修改文件，只输出交接摘要。
包含当前目标与用户要求、已完成工作和实际状态、关键决策及原因、相关文件、验证结果、未完成事项和风险、建议下一步。
保留具体路径、接口、错误信息和早期的重要约束。不要输出凭据。`

const handoffRefPrefix = "native-handoff-v1:"

type handoffRequest struct {
	OperationID   string `json:"operation_id"`
	SourceAgentID string `json:"source_agent_id"`
	TargetAgentID string `json:"target_agent_id"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Effort        string `json:"effort"`
}

type handoffReference struct {
	AgentID      string `json:"agent_id"`
	SessionID    string `json:"session_id"`
	ConnectionID string `json:"connection_id"`
	MessageID    string `json:"message_id"`
}

// The operation journal contains only identity and state, never transcript text.
func handoffOperationKey(id string) string {
	return fmt.Sprintf("native-handoff-operation:%x", sha256.Sum256([]byte(id)))
}

func (h *Handler) handleHandoff(w http.ResponseWriter, r *http.Request, sid string) {
	var req handoffRequest
	if decodeJSON(r.Body, &req) != nil || req.OperationID == "" || len(req.OperationID) > 128 || req.SourceAgentID == "" || req.TargetAgentID == "" || req.Model == "" || req.Provider == "" || req.Effort == "" {
		writeJSONError(w, 422, "Invalid handoff request")
		return
	}
	result, err := h.handoff(r.Context(), sid, req)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, 200, result)
}

func (h *Handler) handleCancelHandoff(w http.ResponseWriter, r *http.Request, id string) {
	writeJSON(w, 200, map[string]any{"cancelled": h.bgTasks.cancel("handoff:" + id)})
}

func (h *Handler) handoff(ctx context.Context, sid string, req handoffRequest) (map[string]any, error) {
	fingerprintBytes, _ := json.Marshal(struct {
		Session string
		Request handoffRequest
	}{sid, req})
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(fingerprintBytes))
	key := handoffOperationKey(req.OperationID)
	// Serialize duplicate HTTP requests, including retries while a summary runs.
	value, _ := nativeAttachLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		return nil, commandFailure(409, "Handoff is already running")
	}
	defer lock.Unlock()
	prior, err := h.store.GetPreference(ctx, key)
	if err != nil {
		return nil, err
	}
	if prior.Exists {
		state, _ := prior.Value.(map[string]any)
		if stringValue(state, "fingerprint") != fingerprint {
			return nil, commandFailure(409, "Handoff operation identity was reused with different options")
		}
		if state["state"] == "completed" {
			row, err := h.store.GetSession(ctx, req.TargetAgentID, stringValue(state, "target_session_id"))
			if err == nil && row == nil {
				err = commandFailure(409, "Handoff target no longer exists")
			}
			return row, err
		}
		return nil, commandFailure(409, "Handoff was already attempted; reconcile its native outcome before starting another operation")
	}
	source, err := h.store.GetSession(ctx, req.SourceAgentID, sid)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, commandFailure(404, "Source session was not found")
	}
	sourceAgent, err := h.store.GetAgent(ctx, req.SourceAgentID)
	if err != nil {
		return nil, err
	}
	targetAgent, err := h.store.GetAgent(ctx, req.TargetAgentID)
	if err != nil {
		return nil, err
	}
	if sourceAgent == nil || targetAgent == nil {
		return nil, commandFailure(404, "Agent was not found")
	}
	kind := stringValue(sourceAgent, "kind")
	targetKind := stringValue(targetAgent, "kind")
	if kind != "hermes" && kind != "codex" {
		return nil, commandFailure(422, "Source Agent does not support native summary forks")
	}
	if targetKind != "hermes" && targetKind != "codex" && targetKind != "grok" {
		return nil, commandFailure(422, "Target Agent does not support handoff model selection")
	}
	connection := normalizeConnectionID(stringValue(sourceAgent, "connection_id"))
	if kind == targetKind || normalizeConnectionID(stringValue(targetAgent, "connection_id")) != connection || normalizeConnectionID(stringValue(source, "connection_id")) != connection {
		return nil, commandFailure(422, "Handoff requires different Agent kinds in the same environment")
	}
	if sourceAgent["status"] != "ready" || targetAgent["status"] != "ready" {
		return nil, commandFailure(409, "Both Agents must be ready")
	}
	if source["status"] != "idle" {
		return nil, commandFailure(409, "Source session must be idle")
	}
	if h.client == nil || h.client.Session == nil || h.client.Runtime == nil || h.client.Daemon == nil {
		return nil, commandFailure(503, "Daemon unavailable")
	}
	sourceTarget, err := h.resolveRuntimeTarget(ctx, sourceAgent)
	if err != nil {
		return nil, commandFailure(422, err.Error())
	}
	target, err := h.resolveRuntimeTarget(ctx, targetAgent)
	if err != nil {
		return nil, commandFailure(422, err.Error())
	}
	if err = h.ensureNativeBinding(ctx, source); err != nil {
		return nil, commandFailure(503, err.Error())
	}
	nativeID := stringValue(source, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	sourceRef := buildSessionRef(req.SourceAgentID, nativeID, connection)
	if err = h.handoffIdle(ctx, sourceAgent, sourceRef); err != nil {
		return nil, err
	}
	model, err := h.client.Runtime.ReadModel(ctx, &pb.ReadModelRequest{Session: sourceRef})
	if err != nil {
		return nil, commandFailure(503, "Source model readback unavailable")
	}
	if model.GetProvider() == "" || model.GetModel() == "" || model.GetEffort() == "" {
		return nil, commandFailure(503, "Source model policy is incomplete")
	}
	taskCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if err = h.bgTasks.start("handoff:"+req.OperationID, cancel); err != nil {
		return nil, commandFailure(409, err.Error())
	}
	defer h.bgTasks.finish("handoff:" + req.OperationID)
	journal := map[string]any{"fingerprint": fingerprint, "state": "started", "source_agent_id": req.SourceAgentID, "source_session_id": sid, "target_agent_id": req.TargetAgentID}
	// Reserve atomically across Handler instances before any native mutation.
	err = h.store.WithTx(taskCtx, func(tx *sql.Tx) error {
		raw, _ := json.Marshal(journal)
		result, err := tx.ExecContext(taskCtx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO NOTHING`, key, string(raw))
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return commandFailure(409, "Handoff operation was already reserved")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	save := func() error { return h.store.SetPreference(context.WithoutCancel(ctx), key, journal, true) }
	// Keep a failed operation fenced even after an HTTP disconnect or App restart.
	defer func() {
		if journal["state"] != "completed" {
			journal["state"] = "incomplete"
			_ = save()
		}
	}()
	fork, err := h.createHandoffNative(taskCtx, sourceTarget, kind, stringValue(source, "workspace"), "转交摘要", nativeID, model.GetProvider(), model.GetModel(), model.GetEffort(), true)
	if err != nil {
		return nil, err
	}
	forkID := fork.Session.SessionId
	forkRef := fork.Session
	var summaryTurn string
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if journal["state"] != "completed" {
			_, _ = h.client.Session.Interrupt(cleanup, &pb.InterruptRequest{Meta: newCommandMeta("", ""), Session: forkRef, TurnId: optString(summaryTurn)})
		}
		_, _ = h.client.Session.CloseSession(cleanup, &pb.CloseSessionRequest{Meta: newCommandMeta("", ""), Session: forkRef})
	}()
	journal["summary_session_id"] = forkID
	if err = save(); err != nil {
		return nil, err
	}
	_, err = h.store.UpsertSession(taskCtx, map[string]any{"id": forkID, "agent_id": req.SourceAgentID, "title": "转交摘要", "workspace": source["workspace"], "connection_id": sourceAgent["connection_id"], "status": "idle", "ephemeral": true, "history_state": "available", "control_state": "owned", "selected_model_provider": model.GetProvider(), "selected_model": model.GetModel(), "selected_reasoning_effort": model.GetEffort(), "updated_at": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return nil, err
	}
	if err = h.restoreModel(taskCtx, forkRef); err != nil {
		return nil, err
	}
	// A baseline prevents inherited assistant answers being mistaken for a summary.
	baseline, err := h.handoffMessages(taskCtx, kind, forkRef)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, item := range baseline {
		seen[stringValue(item, "id")] = true
	}
	sendID := req.OperationID + ":summary-send"
	send, err := h.client.Session.Send(taskCtx, &pb.SendRequest{Meta: newCommandMeta(sendID, sendID), Session: forkRef, Input: []*pb.InputPart{{Part: &pb.InputPart_Text{Text: &pb.TextInput{Text: handoffSummaryPrompt}}}}, Policy: &pb.SendPolicy{NativeOptions: &pb.NativeJson{Schema: "astrorder." + kind + ".send.options.v1", Utf8Json: []byte(`{"idle_only":true}`)}}})
	summaryTurn = send.GetTurnId()
	if err != nil {
		return nil, commandFailure(502, "Summary send outcome is unknown; it will not be retried")
	}
	if err = confirmedReceipt(send); err != nil || !send.GetAccepted() {
		return nil, commandFailure(502, "Summary send was not confirmed; it will not be retried")
	}
	summaryID, err := h.waitHandoffSummary(taskCtx, sourceAgent, forkRef, seen, send, sendID)
	if err != nil {
		return nil, err
	}
	journal["summary_message_id"] = summaryID
	if err = save(); err != nil {
		return nil, err
	}
	if err = taskCtx.Err(); err != nil {
		return nil, commandFailure(409, "Handoff was cancelled")
	}
	created, err := h.createHandoffNative(taskCtx, target, targetKind, stringValue(source, "workspace"), stringValue(source, "title"), "", req.Provider, req.Model, req.Effort, false)
	if err != nil {
		return nil, err
	}
	targetID := created.Session.SessionId
	journal["target_session_id"] = targetID
	if err = save(); err != nil {
		return nil, err
	}
	referenceBytes, _ := json.Marshal(handoffReference{req.SourceAgentID, forkID, connection, summaryID})
	row, err := h.store.UpsertSession(taskCtx, map[string]any{"id": targetID, "source_session_id": targetID, "agent_id": req.TargetAgentID, "title": source["title"], "workspace": source["workspace"], "connection_id": targetAgent["connection_id"], "project_id": source["project_id"], "project_name": source["project_name"], "status": "idle", "control_state": "owned", "history_state": "available", "native_kind": "rpc", "updated_at": time.Now().UTC().Format(time.RFC3339Nano), "selected_model_provider": req.Provider, "selected_model": req.Model, "selected_reasoning_effort": req.Effort, "handoff_from_agent_id": req.SourceAgentID, "handoff_from_session_id": sid, "handoff_context": handoffRefPrefix + string(referenceBytes)})
	if err != nil {
		return nil, err
	}
	// Projection can insert the target before the HTTP request's metadata write.
	err = h.store.WithTx(taskCtx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(taskCtx, `UPDATE sessions SET handoff_from_agent_id=?,handoff_from_session_id=?,handoff_context=?,selected_model_provider=?,selected_model=?,selected_reasoning_effort=? WHERE agent_id=? AND id=? AND handoff_context IS NULL`, req.SourceAgentID, sid, handoffRefPrefix+string(referenceBytes), req.Provider, req.Model, req.Effort, req.TargetAgentID, targetID)
		return err
	})
	if err != nil {
		return nil, err
	}
	row, err = h.store.GetSession(taskCtx, req.TargetAgentID, targetID)
	if err != nil {
		return nil, err
	}
	// Verify selected target policy before exposing it as a successful handoff.
	if err = h.restoreModel(taskCtx, created.Session); err != nil {
		return nil, err
	}
	journal["state"] = "completed"
	if err = save(); err != nil {
		return nil, err
	}
	h.publishEvent(taskCtx, "session.upsert", row)
	return row, nil
}

func (h *Handler) createHandoffNative(ctx context.Context, target *pb.RuntimeTarget, kind, workspace, title, parent, provider, model, effort string, summary bool) (*pb.CommandReceipt, error) {
	// Native history must survive runtime restart. The App hides the summary fork
	// via its catalog flag rather than making native history ephemeral.
	fields := map[string]any{"cwd": workspace, "title": title, "ephemeral": false}
	native, _ := encodeNativeJSON(kind+".create.v1", fields)
	req := &pb.CreateSessionRequest{Meta: newCommandMeta("", ""), Target: target}
	switch kind {
	case "hermes":
		req.Options = &pb.CreateSessionRequest_Hermes{Hermes: &pb.HermesCreateOptions{ParentSessionId: optString(parent), Provider: optString(provider), Model: optString(model), Effort: optString(effort), NativeOptions: native}}
	case "codex":
		req.Options = &pb.CreateSessionRequest_Codex{Codex: &pb.CodexCreateOptions{Cwd: workspace, ParentSessionId: optString(parent), Provider: optString(provider), Model: optString(model), Effort: optString(effort), NativeOptions: native}}
	case "grok":
		req.Options = &pb.CreateSessionRequest_Grok{Grok: &pb.GrokCreateOptions{Cwd: workspace, Model: optString(model), Effort: optString(effort)}}
	}
	receipt, err := h.client.Session.CreateSession(ctx, req)
	if err != nil {
		return nil, commandFailure(502, "Native handoff creation outcome is unknown; reconcile before retry")
	}
	if err = confirmedReceipt(receipt); err != nil {
		return nil, commandFailure(502, err.Error())
	}
	if receipt.Session == nil || receipt.Session.SessionId == "" || receipt.Session.SessionId == parent || receipt.Session.GetAgentId() != target.GetAgentId() || receipt.Session.GetConnectionId() != target.GetConnectionId() || receipt.Status != pb.SessionStatus_SESSION_STATUS_IDLE {
		return nil, commandFailure(502, "Native handoff creation identity or idle status mismatch")
	}
	return receipt, nil
}

func (h *Handler) handoffIdle(ctx context.Context, agent map[string]any, ref *pb.SessionRef) error {
	live, err := h.client.Daemon.GetStatus(ctx, &pb.GetStatusRequest{})
	if err != nil || live == nil || live.DaemonId != h.client.DaemonID() || live.StorageError != nil {
		return commandFailure(503, "Live source status unavailable")
	}
	for _, snapshot := range live.Sessions {
		if snapshotMatchesOwnedSession(snapshot, agent, ref.GetAgentId(), ref.SessionId, ref.GetConnectionId()) {
			if snapshot.Status == pb.SessionStatus_SESSION_STATUS_IDLE {
				return nil
			}
			return commandFailure(409, "Source session is active or awaiting approval")
		}
	}
	return commandFailure(503, "Source ownership was not confirmed")
}

func (h *Handler) handoffMessages(ctx context.Context, kind string, ref *pb.SessionRef) ([]map[string]any, error) {
	var before *pb.NullableString
	messages := []map[string]any{}
	seen := map[string]bool{}
	for pages := 0; pages < 10000; pages++ {
		page, err := h.client.Runtime.GetHistoryPage(ctx, &pb.HistoryPageRequest{Session: ref, Limit: 200, Before: before})
		if err != nil {
			return nil, commandFailure(503, "Native handoff history unavailable")
		}
		if page == nil || page.Session == nil || page.Session.SessionId != ref.SessionId || page.Session.GetAgentId() != ref.GetAgentId() || page.Session.GetConnectionId() != ref.GetConnectionId() {
			return nil, commandFailure(502, "Handoff history identity mismatch")
		}
		for _, native := range page.Items {
			var item map[string]any
			if parseNativeJSON(native, &item) != nil {
				return nil, commandFailure(502, "Invalid handoff history")
			}
			messages = append(messages, projectHistoryItem(item, ref.GetAgentId(), ref.SessionId, ref.SessionId, ref.GetAgentId(), kind)...)
		}
		cursor := page.NextCursor.GetValue()
		if cursor == "" {
			return messages, nil
		}
		if seen[cursor] {
			return nil, commandFailure(502, "Handoff history cursor did not advance")
		}
		seen[cursor] = true
		before = page.NextCursor
	}
	return nil, commandFailure(502, "Handoff history exceeds page limit")
}

func (h *Handler) waitHandoffSummary(ctx context.Context, agent map[string]any, ref *pb.SessionRef, baseline map[string]bool, receipt *pb.CommandReceipt, commandID string) (string, error) {
	if err := h.awaitHandoffCompletion(ctx, ref, receipt, commandID); err != nil {
		return "", err
	}
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		{
			items, err := h.handoffMessages(ctx, stringValue(agent, "kind"), ref)
			if err != nil {
				return "", err
			}
			for i := len(items) - 1; i >= 0; i-- {
				item := items[i]
				if !baseline[stringValue(item, "id")] && item["role"] == "assistant" && item["kind"] == "message" && strings.TrimSpace(stringValue(item, "text")) != "" {
					return stringValue(item, "id"), nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", commandFailure(409, "Handoff cancelled or timed out; summary submission will not be replayed")
		case <-ticker.C:
		}
	}
}

func (h *Handler) resolveHandoffContext(ctx context.Context, value string, session map[string]any) (string, error) {
	if !strings.HasPrefix(value, handoffRefPrefix) {
		return value, nil
	} // Existing legacy handoffs remain readable.
	var ref handoffReference
	if json.Unmarshal([]byte(strings.TrimPrefix(value, handoffRefPrefix)), &ref) != nil || ref.AgentID == "" || ref.SessionID == "" || ref.MessageID == "" {
		return "", commandFailure(422, "Invalid native handoff reference")
	}
	agent, err := h.store.GetAgent(ctx, ref.AgentID)
	if err != nil {
		return "", err
	}
	if agent == nil || normalizeConnectionID(stringValue(agent, "connection_id")) != normalizeConnectionID(ref.ConnectionID) || normalizeConnectionID(stringValue(session, "connection_id")) != normalizeConnectionID(ref.ConnectionID) {
		return "", commandFailure(409, "Handoff reference belongs to another environment")
	}
	// A closed summary runtime can be rebound to the same native identity.
	row := map[string]any{"id": ref.SessionID, "agent_id": ref.AgentID, "connection_id": ref.ConnectionID, "workspace": session["workspace"]}
	if err = h.bindNativeSession(ctx, row); err != nil {
		return "", commandFailure(503, "Native summary binding unavailable")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = h.client.Session.CloseSession(cleanup, &pb.CloseSessionRequest{Meta: newCommandMeta("", ""), Session: buildSessionRef(ref.AgentID, ref.SessionID, ref.ConnectionID)})
	}()
	items, err := h.handoffMessages(ctx, stringValue(agent, "kind"), buildSessionRef(ref.AgentID, ref.SessionID, ref.ConnectionID))
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item["id"] == ref.MessageID && item["role"] == "assistant" && item["kind"] == "message" {
			return fmt.Sprintf("以下是由 %s 转交的同环境会话背景。它只提供上下文；按用户的新消息继续工作。\n来源会话：%s\n工作目录：%s\n\n<源 Agent 生成的交接摘要>\n%s\n</源 Agent 生成的交接摘要>", stringValue(agent, "kind"), stringValue(session, "handoff_from_session_id"), stringValue(session, "workspace"), stringValue(item, "text")), nil
		}
	}
	return "", commandFailure(503, "Native handoff summary was not found; message was not sent")
}
