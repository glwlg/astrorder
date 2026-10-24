package projection

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

// sessionUpsertEvent builds the live event for a session row. updated_at is
// normalised to the same RFC3339 form that the HTTP list returns, so the
// browser compares event and list timestamps in one format.
func sessionUpsertEvent(agentID, sessionID string, row map[string]any) store.Event {
	data := make(map[string]any, len(row))
	for key, value := range row {
		data[key] = value
	}
	if stamp, err := store.WireTime(data["updated_at"]); err == nil {
		data["updated_at"] = stamp
	}
	return store.Event{
		ID:        fmt.Sprintf("sess-%s-%d", sessionID, time.Now().UnixNano()),
		Type:      "session.upsert",
		AgentID:   agentID,
		SessionID: sessionID,
		Data:      data,
	}
}

// projectNativeActivityTx is projectNativeActivity without the unknown-session
// report; it is the form the projector tests exercise.
func projectNativeActivityTx(ctx context.Context, tx *sql.Tx, streamID, agentID string, payload map[string]any) ([]store.Event, error) {
	events, _, err := projectNativeActivity(ctx, tx, streamID, agentID, payload)
	return events, err
}

// unknownNative names a native session that activity was reported for but that
// Astrorder does not list. The Agent id is the one the event resolved to: the
// envelope often carries none and the payload supplies it.
type unknownNative struct {
	AgentID  string
	NativeID string
}

// projectNativeActivity records that a native session had activity at a given
// instant. It only moves updated_at forward and never changes status. It does
// not create a session itself: a native event for a session Astrorder does not
// track is reported back as unknown (its Agent and native id), so the caller can register
// it from the native store after the transaction instead of from the event.
// A stale or duplicate event produces no session.upsert.
func projectNativeActivity(ctx context.Context, tx *sql.Tx, streamID, agentID string, payload map[string]any) ([]store.Event, *unknownNative, error) {
	nativeID, _ := payload["session_id"].(string)
	if nativeID == "" {
		nativeID = streamID
	}
	if agentID == "" {
		agentID, _ = payload["agent_id"].(string)
	}
	if nativeID == "" || agentID == "" {
		return nil, nil, nil
	}
	observed, ok := activityInstant(payload["observed_at"])
	if !ok {
		return nil, nil, nil
	}
	observed = observed.Truncate(time.Microsecond)
	rows, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND (id=? OR source_session_id=?)", agentID, nativeID, nativeID)
	if err != nil {
		return nil, nil, fmt.Errorf("query native activity session: %w", err)
	}
	if len(rows) == 0 {
		return nil, &unknownNative{AgentID: agentID, NativeID: nativeID}, nil
	}
	var events []store.Event
	for _, row := range rows {
		// Hook activity announces changed native history independently of the
		// session timestamp. A native tick may already have advanced that clock.
		if hook, _ := payload["hook_event"].(string); hook != "" {
			id := fmt.Sprint(row["id"])
			events = append(events, store.Event{
				ID:   fmt.Sprintf("hook-transcript-%s-%d", id, time.Now().UnixNano()),
				Type: "transcript.invalidate", AgentID: agentID, SessionID: id,
				Data: map[string]any{"hook_event": hook},
			})
		}
		// Process startup/shutdown can happen during app updates or reconnects
		// without any new conversation. Keep transcript invalidation above, but
		// do not let lifecycle hooks (including replay) advance activity time.
		hook, _ := payload["hook_event"].(string)
		if hook == "SessionStart" || hook == "SessionEnd" {
			continue
		}
		current, err := store.ParseTime(row["updated_at"])
		if err == nil && !observed.After(current) {
			continue
		}
		id := fmt.Sprint(row["id"])
		stamp := observed.UTC().Format("2006-01-02 15:04:05.000000")
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET updated_at=? WHERE agent_id=? AND id=?`, stamp, agentID, id); err != nil {
			return nil, nil, fmt.Errorf("update native activity: %w", err)
		}
		updated, err := queryRows(ctx, tx, "SELECT * FROM sessions WHERE agent_id=? AND id=?", agentID, id)
		if err != nil || len(updated) == 0 {
			return nil, nil, fmt.Errorf("query native activity session: %w", err)
		}
		events = append(events, sessionUpsertEvent(agentID, id, updated[0]))
	}
	return events, nil, nil
}

// activityInstant accepts Unix seconds (float or json.Number) or an RFC3339 string.
func activityInstant(value any) (time.Time, bool) {
	var seconds float64
	switch typed := value.(type) {
	case float64:
		seconds = typed
	case int64:
		seconds = float64(typed)
	case interface{ Float64() (float64, error) }:
		parsed, err := typed.Float64()
		if err != nil {
			return time.Time{}, false
		}
		seconds = parsed
	case string:
		if strings.TrimSpace(typed) == "" {
			return time.Time{}, false
		}
		parsed, err := store.ParseTime(typed)
		if err != nil {
			return time.Time{}, false
		}
		return parsed, true
	default:
		return time.Time{}, false
	}
	if seconds <= 0 || seconds != seconds {
		return time.Time{}, false
	}
	whole := int64(seconds)
	return time.Unix(whole, int64((seconds-float64(whole))*1e9)).UTC(), true
}
