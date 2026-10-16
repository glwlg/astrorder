package sessions

import (
	"net/http"
	"sort"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

func (h *Handler) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agents, err := h.store.ListCurrentAgents(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to list agents: "+err.Error())
		return
	}
	// Enrich effective agents (ensure capabilities are sorted and present)
	effectiveAgents := make([]map[string]any, len(agents))
	for i, a := range agents {
		eff := make(map[string]any)
		for k, v := range a {
			eff[k] = v
		}
		caps := make([]string, 0)
		if rawCaps, ok := eff["capabilities"].([]any); ok {
			for _, rc := range rawCaps {
				if s, ok := rc.(string); ok {
					caps = append(caps, s)
				}
			}
		} else if rawCaps, ok := eff["capabilities"].([]string); ok {
			caps = append(caps, rawCaps...)
		}
		// Default agent capabilities by kind if not set
		kind, _ := eff["kind"].(string)
		hasCap := func(c string) bool {
			for _, item := range caps {
				if item == c {
					return true
				}
			}
			return false
		}
		if kind == "hermes" {
			for _, c := range []string{"history", "approvals", "launch", "delete", "queue"} {
				if !hasCap(c) {
					caps = append(caps, c)
				}
			}
		} else if kind == "grok" {
			for _, c := range []string{"history", "launch", "delete", "queue", "approvals", "task_events", "attachments"} {
				if !hasCap(c) {
					caps = append(caps, c)
				}
			}
		} else if kind == "codex" {
			for _, c := range []string{"launch", "task_events"} {
				if !hasCap(c) {
					caps = append(caps, c)
				}
			}
		}
		sort.Strings(caps)
		eff["capabilities"] = caps
		effectiveAgents[i] = eff
	}

	projects, err := h.store.ListProjects(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to list projects: "+err.Error())
		return
	}

	sessionsList, err := h.store.ListSessions(ctx, "")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to list sessions: "+err.Error())
		return
	}

	h.repairSessionTitles(ctx, sessionsList)
	cursor, err := h.store.LatestCursor(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read event cursor")
		return
	}

	approvals, err := h.store.PendingApprovals(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read native approvals")
		return
	}

	if err := h.reconcileHermesAttachmentRejections(ctx); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to reconcile queued commands: "+err.Error())
		return
	}
	commands, err := h.store.QueueCommands(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to read queued commands")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"commands":         commands,
		"protocol_version": 1,
		"agents":           effectiveAgents,
		"projects":         projects,
		"sessions":         sessionsList,
		"cursor":           cursor,
		"approvals":        approvals,
	})
}

func (h *Handler) handleOpenSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	agents, err := h.store.ListAgents(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to list agents: "+err.Error())
		return
	}

	knownAgentIDs := make([]string, 0, len(agents))
	for _, a := range agents {
		if id, ok := a["id"].(string); ok && id != "" {
			knownAgentIDs = append(knownAgentIDs, id)
		}
	}

	items := make([]map[string]string, 0)
	seen := make(map[string]bool)

	// Read active sessions from daemon if available
	if h.client != nil {
		status, err := h.client.Daemon.GetStatus(ctx, &sessiondv1.GetStatusRequest{})
		if err == nil && status != nil {
			for _, snap := range status.Sessions {
				if snap.Session == nil {
					continue
				}
				aid := ""
				if snap.Session.AgentId != nil {
					aid = *snap.Session.AgentId
				}
				sid := snap.Session.SessionId
				key := aid + "::" + sid
				if !seen[key] {
					seen[key] = true
					items = append(items, map[string]string{
						"agent_id": aid,
						"id":       sid,
					})
				}
			}
		}
	}

	// Also check store for sessions currently running or waiting_approval
	sessions, err := h.store.ListSessions(ctx, "")
	if err == nil {
		for _, s := range sessions {
			status, _ := s["status"].(string)
			if status == "running" || status == "waiting_approval" {
				aid, _ := s["agent_id"].(string)
				sid, _ := s["id"].(string)
				key := aid + "::" + sid
				if !seen[key] {
					seen[key] = true
					items = append(items, map[string]string{
						"agent_id": aid,
						"id":       sid,
					})
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"known_agent_ids": knownAgentIDs,
		"items":           items,
		"live":            []any{},
	})
}
