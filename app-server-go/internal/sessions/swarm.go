package sessions

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func (h *Handler) handleGetSessionSwarm(w http.ResponseWriter, r *http.Request, sid string) {
	ctx := r.Context()
	aid := strings.TrimSpace(r.URL.Query().Get("agent_id"))
	if aid == "" {
		writeJSONError(w, 422, "agent_id is required")
		return
	}
	agent, err := h.store.GetAgent(ctx, aid)
	if err != nil {
		writeJSONError(w, 500, "Agent storage unavailable")
		return
	}
	if agent == nil {
		writeJSONError(w, 404, "Agent was not found")
		return
	}
	row, err := h.store.GetSession(ctx, aid, sid)
	if err != nil {
		writeJSONError(w, 500, "Session storage unavailable")
		return
	}
	if row == nil {
		writeJSONError(w, 404, "Session was not found")
		return
	}

	kind := stringValue(agent, "kind")
	if kind != "antigravity" && kind != "antigravity-ssh" {
		writeJSON(w, http.StatusOK, map[string]any{
			"supported":              false,
			"available":              false,
			"active":                 false,
			"kind":                   "none",
			"title":                  "",
			"coordinator_session_id": "",
			"current_session_id":     sid,
			"is_coordinator":         true,
			"summary": map[string]int{
				"total":   0,
				"working": 0,
				"waiting": 0,
				"stopped": 0,
				"killed":  0,
			},
			"members":    []any{},
			"board":      []any{},
			"updated_at": "",
		})
		return
	}

	if h.client == nil || h.client.Runtime == nil {
		writeJSONError(w, 503, "Session daemon runtime query is unavailable")
		return
	}

	memberConvID := strings.TrimSpace(r.URL.Query().Get("member_conversation_id"))
	var memberLimitPtr *int32
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("member_limit")); rawLimit != "" {
		parsed, convErr := strconv.Atoi(rawLimit)
		if convErr != nil || parsed < 1 || parsed > 60 {
			writeJSONError(w, 422, "member_limit must be between 1 and 60")
			return
		}
		v := int32(parsed)
		memberLimitPtr = &v
	}

	target, err := h.resolveRuntimeTarget(ctx, agent)
	if err != nil {
		writeJSONError(w, 503, err.Error())
		return
	}

	nativeID := stringValue(row, "source_session_id")
	if nativeID == "" {
		nativeID = sid
	}
	readonly := stringValue(row, "control_state") == "readonly"
	query := &pb.AntigravitySessionSwarmQuery{
		ConversationId: nativeID,
		Readonly:       &readonly,
	}
	if memberConvID != "" {
		query.MemberConversationId = &memberConvID
	}
	if memberLimitPtr != nil {
		query.MemberLimit = memberLimitPtr
	}

	resp, err := h.client.Runtime.QueryNative(ctx, &pb.NativeQueryRequest{
		Target: target,
		Query:  &pb.NativeQueryRequest_AntigravitySessionSwarm{AntigravitySessionSwarm: query},
	})
	if err != nil {
		// Gracefully degrade if the running small kernel hasn't been upgraded yet.
		if isPreUpgradeAntigravitySSHError(err) || strings.Contains(err.Error(), "unsupported Antigravity read query") || strings.Contains(err.Error(), "native query request is invalid") {
			writeJSON(w, http.StatusOK, map[string]any{
				"supported":              true,
				"available":              false,
				"active":                 false,
				"needs_daemon_upgrade":   true,
				"kind":                   "swarm",
				"title":                  "",
				"coordinator_session_id": nativeID,
				"current_session_id":     sid,
				"is_coordinator":         true,
				"summary": map[string]int{
					"total":   0,
					"working": 0,
					"waiting": 0,
					"stopped": 0,
					"killed":  0,
				},
				"members":    []any{},
				"board":      []any{},
				"updated_at": "",
			})
			return
		}
		if strings.Contains(err.Error(), "does not belong to this Swarm") {
			writeJSONError(w, 404, err.Error())
			return
		}
		writeJSONError(w, 503, "Antigravity Swarm query failed: "+err.Error())
		return
	}

	var payload map[string]any
	if err := parseNativeJSON(resp.GetResult(), &payload); err != nil || payload == nil {
		writeJSONError(w, 502, "Invalid Antigravity Swarm response")
		return
	}

	// Enrich member rows with agent_id, live owned status reconciliation, catalog presence, and priority sorting.
	// Also mark non-coordinator Swarm/subagent child sessions ephemeral so they stay out of the main sidebar rail.
	if rawMembers, ok := payload["members"].([]any); ok {
		type memberEntry struct {
			data        map[string]any
			origIndex   int
			ownedActive bool
			isCoord     bool
		}
		entries := make([]memberEntry, 0, len(rawMembers))
		nativeWorking := 0

		for idx, item := range rawMembers {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			m["agent_id"] = aid
			cid := stringValue(m, "conversation_id")
			isCoord, _ := m["is_coordinator"].(bool)
			state := strings.TrimSpace(stringValue(m, "state"))
			if state == "working" {
				nativeWorking++
			}
			inCatalog := false
			ownedActive := false
			if cid != "" {
				var existing map[string]any
				if cid == sid || cid == nativeID {
					existing = row
					inCatalog = true
				} else if found, getErr := h.store.GetSession(ctx, aid, cid); getErr == nil && found != nil {
					existing = found
					inCatalog = true
				}
				if existing != nil {
					if isOwnedSessionRunning(existing) {
						ownedActive = true
					}
					if !isCoord {
						patch := map[string]any{}
						if existing["ephemeral"] != true {
							patch["ephemeral"] = true
						}
						memberName := strings.TrimSpace(stringValue(m, "name"))
						existingTitle := strings.TrimSpace(stringValue(existing, "title"))
						if memberName != "" && (existingTitle == "" || existingTitle == cid || existingTitle == "New Session" || existingTitle == "新会话") {
							patch["title"] = "@" + memberName
						}
						if len(patch) > 0 {
							if updated, upErr := h.store.UpdateSession(ctx, aid, cid, patch); upErr == nil && updated != nil {
								h.publishEvent(ctx, "session.upsert", updated)
							}
						}
					}
				}
			}
			m["in_catalog"] = inCatalog
			entries = append(entries, memberEntry{
				data:        m,
				origIndex:   idx,
				ownedActive: ownedActive,
				isCoord:     isCoord,
			})
		}

		// Reconcile live owned ACP runtime state when native conversation_summaries.db lags
		// (e.g. coordinator woke up to synthesize finished subagent reports while not_fully_idle=0).
		for _, e := range entries {
			state := strings.TrimSpace(stringValue(e.data, "state"))
			if state == "killed" || state == "working" || !e.ownedActive {
				continue
			}
			if !e.isCoord || nativeWorking == 0 {
				e.data["state"] = "working"
				nativeWorking++
			}
		}

		// Sort members so running ("working") always appear first, followed by waiting, stopped, killed.
		// Within the same state, keep coordinator first, then most recently updated first.
		sort.SliceStable(entries, func(i, j int) bool {
			ri := swarmStateRank(stringValue(entries[i].data, "state"))
			rj := swarmStateRank(stringValue(entries[j].data, "state"))
			if ri != rj {
				return ri < rj
			}
			if entries[i].isCoord != entries[j].isCoord {
				return entries[i].isCoord
			}
			ui := strings.TrimSpace(stringValue(entries[i].data, "updated_at"))
			uj := strings.TrimSpace(stringValue(entries[j].data, "updated_at"))
			if ui != uj {
				return ui > uj
			}
			return entries[i].origIndex < entries[j].origIndex
		})

		summary := map[string]int{
			"total":   len(entries),
			"working": 0,
			"waiting": 0,
			"stopped": 0,
			"killed":  0,
		}
		sortedMembers := make([]any, 0, len(entries))
		for _, e := range entries {
			sortedMembers = append(sortedMembers, e.data)
			switch strings.TrimSpace(stringValue(e.data, "state")) {
			case "working":
				summary["working"]++
			case "waiting":
				summary["waiting"]++
			case "killed":
				summary["killed"]++
			default:
				summary["stopped"]++
			}
		}
		payload["members"] = sortedMembers
		payload["summary"] = summary
		available, _ := payload["available"].(bool)
		payload["active"] = available && summary["working"] > 0
	}

	writeJSON(w, http.StatusOK, payload)
}

func isOwnedSessionRunning(sess map[string]any) bool {
	if sess == nil {
		return false
	}
	if strings.TrimSpace(stringValue(sess, "control_state")) != "owned" {
		return false
	}
	status := strings.TrimSpace(stringValue(sess, "status"))
	return status == "running" || status == "waiting_approval"
}

func swarmStateRank(state string) int {
	switch strings.TrimSpace(state) {
	case "working":
		return 0
	case "waiting":
		return 1
	case "stopped":
		return 2
	case "killed":
		return 3
	default:
		return 4
	}
}
