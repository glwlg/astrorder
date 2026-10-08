package hermes

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Cursor struct {
	Session string `json:"session"`
	Source  string `json:"source"`
	Before  int64  `json:"before"`
}

func readNativePage(ctx context.Context, dbPath string, sessionID string, sourceID string, before string, limit int) (map[string]any, error) {
	if limit < 1 || limit > 200 {
		return nil, fmt.Errorf("invalid page size")
	}

	var boundary int64
	if before != "" {
		if !strings.HasPrefix(before, "native:") {
			return nil, fmt.Errorf("invalid native cursor")
		}
		raw := before[7:]
		pad := (4 - (len(raw) % 4)) % 4
		rawPadded := raw + strings.Repeat("=", pad)
		decoded, err := base64.URLEncoding.DecodeString(rawPadded)
		if err != nil {
			return nil, fmt.Errorf("invalid native cursor: %w", err)
		}
		var cursor Cursor
		if err := json.Unmarshal(decoded, &cursor); err != nil {
			return nil, fmt.Errorf("invalid native cursor payload: %w", err)
		}
		if cursor.Session != sessionID || cursor.Source != sourceID {
			return nil, fmt.Errorf("foreign native cursor")
		}
		if cursor.Before <= 0 {
			return nil, fmt.Errorf("invalid native boundary")
		}
		boundary = cursor.Before
	}

	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("file:%s?mode=ro", absPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return nil, fmt.Errorf("pragma query_only failed: %w", err)
	}

	var dummy int
	err = db.QueryRowContext(ctx, "SELECT 1 FROM sessions WHERE id = ?", sessionID).Scan(&dummy)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("native session does not exist")
	} else if err != nil {
		return nil, fmt.Errorf("session lookup failed: %w", err)
	}

	rows, err := db.QueryContext(ctx, "PRAGMA table_info(messages)")
	if err != nil {
		return nil, fmt.Errorf("pragma table_info(messages) failed: %w", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err == nil {
			columns[name] = true
		}
	}
	rows.Close()

	for _, reqCol := range []string{"id", "session_id", "role", "content"} {
		if !columns[reqCol] {
			return nil, fmt.Errorf("unsupported native message schema")
		}
	}

	selectCols := []string{"id", "session_id", "role", "content"}
	for _, opt := range []string{"timestamp", "tool_call_id", "tool_calls", "tool_name", "reasoning", "reasoning_content", "reasoning_details", "codex_reasoning_items", "display_kind"} {
		if columns[opt] {
			selectCols = append(selectCols, opt)
		}
	}

	query := "SELECT " + strings.Join(selectCols, ", ") + " FROM messages WHERE session_id = ?"
	args := []any{sessionID}

	if boundary > 0 {
		var checkID int64
		err = db.QueryRowContext(ctx, "SELECT id FROM messages WHERE session_id = ? AND id = ?", sessionID, boundary).Scan(&checkID)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("native cursor anchor is outside this session")
		} else if err != nil {
			return nil, err
		}
		query += " AND id < ?"
		args = append(args, boundary)
	}

	if columns["active"] {
		if columns["compacted"] {
			query += " AND (active=1 OR compacted=1)"
		} else {
			query += " AND active=1"
		}
	}
	if columns["display_kind"] {
		query += " AND COALESCE(display_kind, '') != 'hidden'"
	}

	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)

	msgRows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("messages query failed: %w", err)
	}
	defer msgRows.Close()

	var items []map[string]any
	for msgRows.Next() {
		vals := make([]any, len(selectCols))
		ptrs := make([]any, len(selectCols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := msgRows.Scan(ptrs...); err != nil {
			continue
		}
		item := map[string]any{}
		for i, col := range selectCols {
			val := vals[i]
			if b, ok := val.([]byte); ok {
				item[col] = string(b)
			} else {
				item[col] = val
			}
		}
		items = append(items, item)
	}
	msgRows.Close()

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}

	var nextCursor *string
	if hasMore && len(items) > 0 {
		firstID, _ := items[0]["id"].(int64)
		cur := Cursor{
			Session: sessionID,
			Source:  sourceID,
			Before:  firstID,
		}
		raw, _ := json.Marshal(cur)
		enc := "native:" + strings.TrimRight(base64.URLEncoding.EncodeToString(raw), "=")
		nextCursor = &enc
	}

	res := map[string]any{
		"items": items,
	}
	if nextCursor != nil {
		res["next_cursor"] = *nextCursor
	} else {
		res["next_cursor"] = nil
	}
	return res, nil
}
