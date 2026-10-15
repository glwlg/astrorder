package sessions

import (
	"encoding/json"
	"net/http"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// Answers bypass the ordinary command transcript store. The daemon owns native
// request identity and idempotency; this endpoint never starts another turn.
func (h *Handler) handleUserInput(w http.ResponseWriter, r *http.Request, sid string) {
	var body struct {
		ID        string `json:"id"`
		AgentID   string `json:"agent_id"`
		RequestID string `json:"request_id"`
		Answers   map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	if err := decodeJSON(r.Body, &body); err != nil || body.ID == "" || len(body.ID) > 256 || body.AgentID == "" || body.RequestID == "" || body.Answers == nil {
		writeJSONError(w, 422, "Invalid question response")
		return
	}
	ctx := r.Context()
	session, err := h.store.GetSession(ctx, body.AgentID, sid)
	if err != nil {
		writeJSONError(w, 500, err.Error())
		return
	}
	if session == nil {
		writeJSONError(w, 404, "Session was not found")
		return
	}
	request, err := h.store.Approval(ctx, body.AgentID, sid, body.RequestID)
	if err != nil {
		writeJSONError(w, 500, err.Error())
		return
	}
	// Resolved requests still allow exact operation retries. The daemon ledger
	// verifies payload equality; a fresh operation cannot answer a resolved request.
	if request == nil || request["method"] != "item/tool/requestUserInput" {
		writeJSONError(w, 409, "Question does not belong to this session")
		return
	}
	questions, _ := request["questions"].([]any)
	allowed := map[string]bool{}
	for _, raw := range questions {
		q, _ := raw.(map[string]any)
		id, _ := q["id"].(string)
		if id != "" {
			allowed[id] = true
		}
	}
	for id, answer := range body.Answers {
		if !allowed[id] || len(answer.Answers) > 32 {
			writeJSONError(w, 422, "Invalid question identity")
			return
		}
		for _, text := range answer.Answers {
			if len(text) > 2000000 {
				writeJSONError(w, 422, "Answer is too long")
				return
			}
		}
	}
	// An empty map is the native skip response. Otherwise answer every question.
	if len(body.Answers) != 0 && len(body.Answers) != len(allowed) {
		writeJSONError(w, 422, "Answer every question or skip the request")
		return
	}
	if h.client == nil || h.client.Session == nil {
		writeJSONError(w, 503, "Daemon client is not configured")
		return
	}
	raw, _ := json.Marshal(map[string]any{"answers": body.Answers})
	receipt, err := h.client.Session.RespondApproval(ctx, &pb.RespondApprovalRequest{Meta: newCommandMeta(body.ID, body.ID), Session: buildSessionRef(body.AgentID, sid, stringValue(session, "connection_id")), ApprovalId: &pb.ExactIdentifier{Value: &pb.ExactIdentifier_Text{Text: body.RequestID}}, Decision: pb.ApprovalDecision_APPROVAL_DECISION_ACCEPT, UserInput: &pb.NativeJson{Schema: "codex.user-input.response.v1", Utf8Json: raw}})
	if err != nil {
		writeJSONError(w, 502, "Question response was not confirmed: "+err.Error())
		return
	}
	if err = confirmedReceipt(receipt); err != nil || !receipt.Accepted {
		writeJSONError(w, 502, "Question response was not accepted; do not resubmit with a new identifier")
		return
	}
	writeJSON(w, 200, map[string]any{"accepted": true, "request_id": body.RequestID})
}
