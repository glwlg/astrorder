package sessions

import (
	"context"
	"fmt"
	"strings"
	"time"

	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func isPreUpgradeAntigravitySSHError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if strings.Contains(msg, "connection is invalid") || strings.Contains(msg, "SSH transport options") {
		return false
	}
	switch status.Code(err) {
	case codes.InvalidArgument, codes.Unimplemented, codes.NotFound:
		return true
	default:
		return strings.Contains(msg, `runtime "antigravity-ssh" is not registered`)
	}
}

// Refresh only metadata; native SQLite remains the transcript source. Existing
// status, user titles, ownership and deletion tombstones are not replaced.
func (h *Handler) refreshAntigravityActivity(ctx context.Context, agent map[string]any, target *pb.RuntimeTarget) (int, error) {
	response, err := h.client.Runtime.QueryNative(ctx, &pb.NativeQueryRequest{Target: target, Query: &pb.NativeQueryRequest_AntigravitySessionList{AntigravitySessionList: &pb.AntigravitySessionListQuery{}}})
	if err != nil {
		if target.GetRuntime() == pb.RuntimeKind_RUNTIME_KIND_ANTIGRAVITY_SSH && isPreUpgradeAntigravitySSHError(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("native Antigravity session list failed: %w", err)
	}
	var parsed struct {
		Items *[]struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Workspace string `json:"workspace"`
			UpdatedAt string `json:"updated_at"`
			Status    string `json:"status"`
			Ephemeral bool   `json:"ephemeral"`
		} `json:"items"`
	}
	if response == nil {
		return 0, fmt.Errorf("native Antigravity session list is absent")
	}
	if err := parseNativeJSON(response.Result, &parsed); err != nil || parsed.Items == nil {
		return 0, fmt.Errorf("native Antigravity session list is invalid")
	}
	// Validate the complete catalog before persisting any part of it.
	for _, native := range *parsed.Items {
		if native.ID == "" {
			return 0, fmt.Errorf("native Antigravity session identity is absent")
		}
		if _, err := time.Parse(time.RFC3339Nano, native.UpdatedAt); err != nil {
			return 0, fmt.Errorf("native Antigravity activity timestamp is invalid")
		}
	}
	aid := stringValue(agent, "id")
	count := 0
	seen := map[string]bool{}
	for _, native := range *parsed.Items {
		if seen[native.ID] {
			continue
		}
		seen[native.ID] = true
		title := strings.TrimSpace(native.Title)
		if title == "" {
			title = native.ID
		}
		if runes := []rune(title); len(runes) > nativeTitleLimit {
			title = string(runes[:nativeTitleLimit])
		}
		at, _ := time.Parse(time.RFC3339Nano, native.UpdatedAt)
		status := native.Status
		if status != "running" && status != "waiting_approval" && status != "error" {
			status = "idle"
		}
		row := map[string]any{"id": native.ID, "agent_id": aid, "title": title, "workspace": optionalText(native.Workspace), "status": status, "source_id": aid, "source_session_id": native.ID, "history_state": "available", "control_state": "unbound", "native_kind": "rpc", "ephemeral": native.Ephemeral, "updated_at": at.UTC().Format(time.RFC3339Nano)}
		if conn := stringValue(agent, "connection_id"); conn != "" && conn != "local" {
			row["connection_id"] = conn
		}
		if project := workspaceBase(native.Workspace); project != "" {
			row["project_name"] = project
		}
		insertedRow, inserted, err := h.store.InsertSessionIfAbsent(ctx, row)
		if err != nil {
			return count, err
		}
		if inserted {
			h.publishEvent(ctx, "session.upsert", insertedRow)
			count++
			continue
		}
		existing, err := h.store.GetSession(ctx, aid, native.ID)
		if err != nil {
			return count, err
		}
		if existing == nil {
			continue
		}
		existingTitle := strings.TrimSpace(stringValue(existing, "title"))
		patch := map[string]any{}
		if title != native.ID && (existingTitle == "" || existingTitle == "New Session" || existingTitle == "新对话" || existingTitle == "新会话" || existingTitle == native.ID) && existingTitle != title {
			patch["title"] = title
		}
		if native.Ephemeral && existing["ephemeral"] != true {
			patch["ephemeral"] = true
		}
		if len(patch) > 0 {
			if upgraded, upErr := h.store.UpdateSession(ctx, aid, native.ID, patch); upErr == nil && upgraded != nil {
				h.publishEvent(ctx, "session.upsert", upgraded)
				count++
			}
		}
		updated, changed, err := h.store.AdvanceSessionActivity(ctx, aid, native.ID, at)
		if err != nil {
			return count, err
		}
		if changed {
			h.publishEvent(ctx, "session.upsert", updated)
			count++
		}
	}
	return count, nil
}
