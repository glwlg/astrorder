package sessions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
	pb "astrorder.dev/contracts/gen/go/astrorder/sessiond/v1"
)

// grokNativeSession contains ACP session metadata only. Transcripts remain native.
type grokNativeSession struct {
	ID        string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

func (h *Handler) grokSessions(ctx context.Context, target *pb.RuntimeTarget) ([]grokNativeSession, error) {
	result := []grokNativeSession{}
	seenIDs, seenCursors := map[string]bool{}, map[string]bool{}
	var cursor *string
	for page := 0; page < activityMaxPages; page++ {
		response, err := h.client.Runtime.QueryNative(ctx, &pb.NativeQueryRequest{Target: target, Query: &pb.NativeQueryRequest_GrokSessionList{GrokSessionList: &pb.GrokSessionListQuery{Cursor: cursor}}})
		if err != nil {
			return nil, fmt.Errorf("native Grok session list failed: %w", err)
		}
		var parsed struct {
			Sessions   *[]grokNativeSession `json:"sessions"`
			NextCursor *string              `json:"nextCursor"`
			Meta       struct {
				Partial map[string]bool `json:"x.ai/partial"`
			} `json:"_meta"`
		}
		if response == nil {
			return nil, fmt.Errorf("native Grok session list is absent")
		}
		if err := parseNativeJSON(response.Result, &parsed); err != nil || parsed.Sessions == nil {
			return nil, fmt.Errorf("native Grok session list is invalid")
		}
		if parsed.Meta.Partial["conversations"] {
			return nil, fmt.Errorf("native Grok session catalog is partial")
		}
		for _, session := range *parsed.Sessions {
			if session.ID == "" {
				return nil, fmt.Errorf("native Grok session identity is absent")
			}
			if !seenIDs[session.ID] {
				result = append(result, session)
				seenIDs[session.ID] = true
			}
		}
		if parsed.NextCursor == nil || *parsed.NextCursor == "" {
			return result, nil
		}
		if seenCursors[*parsed.NextCursor] {
			return nil, fmt.Errorf("native Grok session cursor repeated")
		}
		seenCursors[*parsed.NextCursor] = true
		cursor = parsed.NextCursor
	}
	return nil, fmt.Errorf("native Grok session list exceeds page limit")
}

func (h *Handler) refreshGrokActivity(ctx context.Context, agent map[string]any, target *pb.RuntimeTarget) (int, error) {
	sessions, err := h.grokSessions(ctx, target)
	if err != nil {
		return 0, err
	}
	count := 0
	aid := stringValue(agent, "id")
	for _, native := range sessions {
		title := strings.TrimSpace(native.Title)
		if title == "" {
			title = native.ID
		}
		if runes := []rune(title); len(runes) > nativeTitleLimit {
			title = string(runes[:nativeTitleLimit])
		}
		row := map[string]any{"id": native.ID, "agent_id": aid, "title": title, "workspace": optionalText(native.Cwd), "status": "idle", "source_id": aid, "source_session_id": native.ID, "history_state": "available", "control_state": "owned", "native_kind": "cli"}
		if conn := stringValue(agent, "connection_id"); conn != "" && conn != "local" {
			row["connection_id"] = conn
		}
		if project := workspaceBase(native.Cwd); project != "" {
			row["project_name"] = project
		}
		at, timeErr := time.Parse(time.RFC3339Nano, native.UpdatedAt)
		if timeErr == nil {
			row["updated_at"] = at.UTC().Format(time.RFC3339Nano)
		} else {
			row["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
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
		} // Respect native removal tombstones.
		if !store.IsPlaceholderSessionTitle(title, native.ID) && title != stringValue(existing, "title") {
			named, err := h.store.CompareSessionTitle(ctx, aid, native.ID, stringValue(existing, "title"), title, "native")
			if err != nil {
				return count, err
			}
			if named != nil {
				h.publishEvent(ctx, "session.upsert", named)
			}
		}
		if timeErr == nil {
			updated, changed, err := h.store.AdvanceSessionActivity(ctx, aid, native.ID, at)
			if err != nil {
				return count, err
			}
			if changed {
				h.publishEvent(ctx, "session.upsert", updated)
				count++
			}
		}
	}
	return count, nil
}
