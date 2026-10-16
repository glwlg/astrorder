package protocol

import (
	storage "astrorder.dev/session-daemon/internal/journal"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func operationScope(action string, request map[string]any) string {
	raw, _ := json.Marshal([]any{action, request["agent_type"], request["connection_id"], request["session_id"]})
	return string(raw)
}

func (d *Daemon) readOperation(request map[string]any) map[string]any {
	id, ok := request["operation_id"].(string)
	action, _ := request["operation_action"].(string)
	if !ok || id == "" || len(id) > 256 || action == "" {
		return errorResponse(request["request_id"], "invalid operation lookup")
	}
	exists, raw, err := d.journal.ReadOperation(operationScope(action, request), id)
	if err != nil {
		return errorResponse(request["request_id"], "operation storage unavailable")
	}
	state := "missing"
	var response map[string]any
	if exists {
		state = "uncertain"
		if len(raw) > 0 {
			state = "completed"
			if err = storage.DecodePayload(raw, &response); err != nil {
				return errorResponse(request["request_id"], "invalid stored operation response")
			}
		}
	}
	return map[string]any{"action": "operation.read.result", "request_id": request["request_id"], "daemon_id": d.daemonID, "operation_id": id, "operation_state": state, "response": response}
}

func (d *Daemon) control(parent context.Context, request map[string]any) map[string]any {
	if err := d.beginControl(); err != nil {
		return errorResponse(request["request_id"], err.Error())
	}
	defer d.endControl()
	action, _ := request["action"].(string)
	switch action {
	case "session.create", "session.send", "session.steer", "session.review", "session.compact", "session.approve", "session.interrupt", "session.delete", "session.rename", "session.settings", "session.goal.set", "session.goal.clear", "model_config.apply", "model_config.reload":
	default:
		return d.controlOnce(parent, request)
	}
	if action != "model_config.apply" && action != "model_config.reload" {
		if message := validateSessionControl(request); message != "" {
			return errorResponse(request["request_id"], message)
		}
	}
	raw, exists := request["operation_id"]
	if !exists {
		raw, exists = request["command_id"]
	}
	if !exists {
		return d.controlOnce(parent, request)
	}
	id, ok := raw.(string)
	if !ok || id == "" || len(id) > 256 {
		return errorResponse(request["request_id"], "invalid operation ID")
	}
	payload := make(map[string]any, len(request))
	for k, v := range request {
		if k != "request_id" {
			payload[k] = v
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return errorResponse(request["request_id"], "invalid operation payload")
	}
	sum := sha256.Sum256(encoded)
	// Scope includes action, connection and native identity; no delimiter ambiguity.
	scope := operationScope(action, request)
	fresh, previous, err := d.journal.ReserveOperation(scope, id, hex.EncodeToString(sum[:]))
	if err != nil {
		return errorResponse(request["request_id"], err.Error())
	}
	if !fresh {
		if len(previous) == 0 {
			response := errorResponse(request["request_id"], "operation pending or outcome uncertain; reconcile native state before retry")
			response["operation_id"], response["operation_state"] = id, "uncertain"
			return response
		}
		var response map[string]any
		if err = storage.DecodePayload(previous, &response); err != nil {
			return errorResponse(request["request_id"], "invalid stored operation response")
		}
		response["request_id"] = request["request_id"]
		return response
	}
	if d.sessionControlTimeout(request) > defaultSessionControlTimeout {
		// A reserved compression belongs to the daemon. Disconnecting the
		// caller only stops its wait; native completion still reaches the ledger.
		// controlOnce supplies the bounded compression execution deadline.
		parent = context.WithoutCancel(parent)
	}
	response := d.controlOnce(parent, request)
	if response["action"] == "error" && response["rejected_before_dispatch"] != true {
		// Native RPC failure does not prove that the side effect did not happen.
		// Retain the reservation and prohibit automatic redispatch.
		response["operation_id"], response["operation_state"] = id, "uncertain"
		return response
	}
	toPersist := response
	if action == "model_config.apply" {
		// Security: sanitize durable completion envelope so file contents and secrets
		// are not retained in WAL / operation storage.
		cleanRes := map[string]any{}
		if resMap, ok := response["result"].(map[string]any); ok {
			cleanRes["changed"] = resMap["changed"]
			if r, ok := resMap["receipt"].(map[string]any); ok {
				cleanRes["receipt"] = map[string]any{
					"status":  r["status"],
					"digests": r["digests"],
				}
			}
		}
		toPersist = map[string]any{
			"action":     response["action"],
			"request_id": response["request_id"],
			"daemon_id":  response["daemon_id"],
			"result":     cleanRes,
		}
	}
	encoded, err = json.Marshal(toPersist)
	if err == nil {
		err = d.journal.CompleteOperation(scope, id, encoded)
	}
	if err != nil {
		failed := errorResponse(request["request_id"], fmt.Sprintf("operation outcome uncertain: %v", err))
		failed["operation_id"], failed["operation_state"] = id, "uncertain"
		return failed
	}
	return response
}
