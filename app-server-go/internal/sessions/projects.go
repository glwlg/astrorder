package sessions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	sessiondv1 "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("rand-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func (h *Handler) deleteNativeSession(ctx context.Context, agentID, sessionID string) error {
	if agentID == "" || sessionID == "" {
		return errors.New("agent_id and session_id are required")
	}
	existing, err := h.store.GetSession(ctx, agentID, sessionID)
	if err != nil {
		return err
	}
	if existing == nil {
		return errors.New("session not found")
	}
	if h.client == nil || h.client.Session == nil {
		return errors.New("session daemon client is not configured")
	}

	connID, _ := existing["connection_id"].(string)
	ref := buildSessionRef(agentID, sessionID, connID)
	delReq := &sessiondv1.DeleteSessionRequest{
		Meta:    newCommandMeta("", ""),
		Session: ref,
	}
	receipt, err := h.client.Session.DeleteSession(ctx, delReq)
	if err != nil {
		if status.Code(err) != codes.NotFound && !strings.Contains(err.Error(), "session is not daemon-owned") {
			return fmt.Errorf("daemon delete failed: %w", err)
		}
	} else if err := confirmedReceipt(receipt); err != nil {
		if !strings.Contains(err.Error(), "session is not daemon-owned") {
			return err
		}
	}

	if _, err := h.store.DeleteSession(ctx, agentID, sessionID); err != nil {
		return fmt.Errorf("store delete failed: %w", err)
	}
	h.publishEvent(ctx, "session.delete", map[string]any{"id": sessionID, "agent_id": agentID})
	return nil
}

type batchDeletePayload struct {
	Sessions []map[string]string `json:"sessions"`
}

func (h *Handler) handleBatchDeleteSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var payload batchDeletePayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid request payload")
		return
	}

	deleted := make([]map[string]string, 0)
	failed := make([]map[string]string, 0)

	for _, item := range payload.Sessions {
		aid := item["agent_id"]
		sid := item["session_id"]
		if sid == "" {
			sid = item["id"]
		}
		if aid == "" || sid == "" {
			continue
		}
		sess, err := h.store.GetSession(ctx, aid, sid)
		if err != nil || sess == nil {
			continue
		}
		if err := h.deleteNativeSession(ctx, aid, sid); err != nil {
			failed = append(failed, map[string]string{
				"agent_id": aid,
				"id":       sid,
				"error":    err.Error(),
			})
		} else {
			deleted = append(deleted, map[string]string{
				"agent_id": aid,
				"id":       sid,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"deleted": deleted,
		"failed":  failed,
	})
}

type createProjectPayload struct {
	Workspace    string `json:"workspace"`
	Name         string `json:"name"`
	ConnectionID string `json:"connection_id"`
	AgentID      string `json:"agent_id"`
}

func (h *Handler) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var payload createProjectPayload
	if err := decodeJSON(r.Body, &payload); err != nil {
		writeJSONError(w, http.StatusUnprocessableEntity, "Invalid request payload")
		return
	}
	ws := strings.TrimSpace(payload.Workspace)
	if ws == "" {
		ws = h.defaultWorkspace
	}
	if ws == "" {
		writeJSONError(w, http.StatusUnprocessableEntity, "workspace is required")
		return
	}

	normWS := strings.TrimRight(strings.ReplaceAll(ws, "\\", "/"), "/")
	folderName := normWS
	if idx := strings.LastIndex(normWS, "/"); idx >= 0 {
		folderName = normWS[idx+1:]
	}
	pName := strings.TrimSpace(payload.Name)
	if pName == "" {
		pName = folderName
	}
	connID := payload.ConnectionID
	sourceID := "local"
	var connPtr any = nil
	if connID != "" && connID != "local" {
		sourceID = connID
		connPtr = connID
	}
	agentID := payload.AgentID
	if agentID == "" {
		agentID = "codex"
	}
	projectID := randomHex(16)
	now := time.Now().UTC().Format(time.RFC3339)
	projectData := map[string]any{
		"project_id":    projectID,
		"project_name":  pName,
		"workspace":     ws,
		"source_id":     sourceID,
		"connection_id": connPtr,
		"agent_id":      agentID,
		"session_count": 0,
		"updated_at":    now,
	}

	canonical, err := h.store.UpsertProject(ctx, projectData)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to upsert project: "+err.Error())
		return
	}
	h.publishEvent(ctx, "project.upsert", canonical)
	writeJSON(w, http.StatusOK, map[string]any{"project": canonical})
}

type deleteProjectPayload struct {
	ProjectKey     string              `json:"project_key"`
	ProjectID      string              `json:"project_id"`
	SourceID       string              `json:"source_id"`
	Workspace      string              `json:"workspace"`
	SessionKeys    []map[string]string `json:"session_keys"`
	DeleteSessions *bool               `json:"delete_sessions"`
}

func (h *Handler) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var payload deleteProjectPayload
	if r.Body != nil {
		if err := decodeJSON(r.Body, &payload); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusUnprocessableEntity, "Invalid request payload")
			return
		}
	}
	if payload.ProjectKey == "" {
		payload.ProjectKey = r.URL.Query().Get("project_key")
	}
	if payload.ProjectID == "" {
		payload.ProjectID = r.URL.Query().Get("project_id")
	}
	if payload.SourceID == "" {
		payload.SourceID = r.URL.Query().Get("source_id")
	}
	if payload.Workspace == "" {
		payload.Workspace = r.URL.Query().Get("workspace")
	}
	delSessions := true
	if payload.DeleteSessions != nil {
		delSessions = *payload.DeleteSessions
	} else if q := r.URL.Query().Get("delete_sessions"); q == "false" {
		delSessions = false
	}

	if payload.ProjectKey == "" && payload.ProjectID == "" && payload.Workspace == "" && len(payload.SessionKeys) == 0 {
		writeJSONError(w, http.StatusUnprocessableEntity, "Project identity or session_keys is required")
		return
	}
	allProjects, err := h.store.ListProjects(ctx)
	if err != nil {
		writeJSONError(w, 500, "Failed to read projects: "+err.Error())
		return
	}
	projects := make([]map[string]any, 0)
	for _, p := range allProjects {
		matches := false
		switch {
		case payload.ProjectKey != "":
			matches = p["id"] == payload.ProjectKey
		case payload.ProjectID != "":
			matches = p["project_id"] == payload.ProjectID
		case payload.Workspace != "":
			matches = p["workspace"] == payload.Workspace
		}
		if matches && (payload.SourceID == "" || p["source_id"] == payload.SourceID) {
			if payload.ProjectID != "" && p["project_id"] != payload.ProjectID {
				writeJSONError(w, 422, "Conflicting project identity")
				return
			}
			projects = append(projects, p)
		}
	}
	if payload.SourceID == "" && len(projects) > 1 {
		source := projects[0]["source_id"]
		for _, p := range projects[1:] {
			if p["source_id"] != source {
				writeJSONError(w, http.StatusConflict, "Project identity is ambiguous; source_id or project_key is required")
				return
			}
		}
	}
	// Resolve and deduplicate every target before the first native mutation.
	targets := make([][2]string, 0)
	seen := make(map[[2]string]bool)
	addTarget := func(aid, sid string) {
		key := [2]string{aid, sid}
		if !seen[key] {
			seen[key] = true
			targets = append(targets, key)
		}
	}
	if delSessions {
		for _, item := range payload.SessionKeys {
			aid, sid := item["agent_id"], item["id"]
			if sid == "" {
				sid = item["session_id"]
			}
			if aid == "" || sid == "" {
				writeJSONError(w, 422, "session_keys requires exact agent_id and id")
				return
			}
			row, err := h.store.GetSession(ctx, aid, sid)
			if err != nil {
				writeJSONError(w, 500, err.Error())
				return
			}
			if row != nil {
				addTarget(aid, sid)
			}
		}
		sessions, err := h.store.ListSessions(ctx, "")
		if err != nil {
			writeJSONError(w, 500, err.Error())
			return
		}
		for _, row := range sessions {
			for _, p := range projects {
				if row["source_id"] != p["source_id"] {
					continue
				}
				matches := row["project_id"] == p["project_id"] && p["project_id"] != "__no_project__"
				if p["project_id"] == "__no_project__" {
					matches = p["workspace"] != nil && p["workspace"] != "" && row["workspace"] == p["workspace"]
				}
				if matches {
					addTarget(stringValue(row, "agent_id"), stringValue(row, "id"))
				}
			}
		}
	}
	deletedSessions := make([]map[string]string, 0)
	failed := make([]map[string]string, 0)
	for _, key := range targets {
		if err := h.deleteNativeSession(ctx, key[0], key[1]); err != nil {
			failed = append(failed, map[string]string{"agent_id": key[0], "id": key[1], "error": err.Error()})
		} else {
			deletedSessions = append(deletedSessions, map[string]string{"agent_id": key[0], "id": key[1]})
		}
	}
	if len(failed) > 0 {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "detail": "Native session deletion failed; project retained", "deleted_sessions": deletedSessions, "failed": failed})
		return
	}
	ok := len(deletedSessions) > 0
	for _, p := range projects {
		affected, err := h.store.DeleteProject(ctx, stringValue(p, "id"))
		if err != nil {
			writeJSON(w, 500, map[string]any{"ok": false, "detail": "Failed to delete project: " + err.Error(), "deleted_sessions": deletedSessions})
			return
		}
		if affected {
			ok = true
			// Exact keys keep a project event from deleting another source's same path.
			h.publishEvent(ctx, "project.delete", map[string]any{"project_key": p["id"], "deleted_sessions": deletedSessions})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "deleted_sessions": deletedSessions})
}
