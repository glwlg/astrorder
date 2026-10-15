package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// IsPlaceholderSessionTitle identifies runtime defaults, never a meaningful name.
func IsPlaceholderSessionTitle(title, id string) bool {
	value := strings.ToLower(strings.TrimSpace(title))
	if strings.TrimSpace(title) == id && id != "" {
		return true
	}
	switch value {
	case "", "new session", "new conversation", "新会话", "未命名", "未命名会话", "untitled", "untitled session", "none", "null":
		return true
	}
	return false
}

// CompareSessionTitle preserves activity timestamps and concurrent manual/native renames.
func (s *Store) CompareSessionTitle(ctx context.Context, agentID, sessionID, expected, title, source string) (map[string]any, error) {
	changed := false
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if source != "fallback" {
			var markerText string
			if err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE "key"=?`, SessionTitleKey(agentID, sessionID)).Scan(&markerText); err != nil && err != sql.ErrNoRows {
				return err
			}
			var marker struct {
				Title  string `json:"title"`
				Source string `json:"source"`
			}
			_ = json.Unmarshal([]byte(markerText), &marker)
			if !IsPlaceholderSessionTitle(expected, sessionID) && (marker.Title != expected || (marker.Source != "fallback" && marker.Source != "llm")) {
				return nil
			}
		}
		result, err := tx.ExecContext(ctx, `UPDATE sessions SET title=? WHERE agent_id=? AND id=? AND title=?`, title, agentID, sessionID, expected)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil || count == 0 {
			return err
		}
		changed = true
		encoded, err := encodeJSON(map[string]string{"title": title, "source": source})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") DO UPDATE SET value=excluded.value`, SessionTitleKey(agentID, sessionID), encoded)
		return err
	})
	if err != nil || !changed {
		return nil, err
	}
	return s.GetSession(ctx, agentID, sessionID)
}

func SessionTitleKey(agentID, sessionID string) string {
	return "session_title:" + agentID + "::" + sessionID
}
