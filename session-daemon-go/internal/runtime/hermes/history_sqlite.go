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

	lineage, err := historyLineage(ctx, db, sessionID)
	if err != nil {
		return nil, err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(lineage)), ",")
	args := make([]any, 0, len(lineage)+2)
	for _, id := range lineage {
		args = append(args, id)
	}
	selectCols := []string{"id", "session_id", "role", "content"}
	for _, opt := range []string{"timestamp", "tool_call_id", "tool_calls", "tool_name", "reasoning", "reasoning_content", "reasoning_details", "codex_reasoning_items", "display_kind", "display_metadata"} {
		if columns[opt] {
			selectCols = append(selectCols, opt)
		}
	}

	where := "session_id IN (" + placeholders + ")"
	if columns["active"] {
		if columns["compacted"] {
			where += " AND (active=1 OR compacted=1)"
		} else {
			where += " AND active=1"
		}
	}
	if columns["display_kind"] {
		where += " AND COALESCE(display_kind, '') != 'hidden'"
	}
	if columns["display_metadata"] {
		where += " AND COALESCE(json_extract(CASE WHEN json_valid(display_metadata) THEN display_metadata ELSE '{}' END, '$.model_only'), 0)=0"
	}
	// A compaction can clone the protected tail into a newer generation. Page
	// logical display rows, ordered at their first occurrence, before LIMIT.
	identity := "id"
	if columns["timestamp"] {
		keys := []string{"role", "content", "timestamp"}
		for _, name := range []string{"tool_call_id", "tool_calls", "tool_name"} {
			if columns[name] {
				keys = append(keys, name)
			} else {
				keys = append(keys, "NULL")
			}
		}
		identity = "CASE WHEN timestamp IS NULL THEN 'row:'||id ELSE 'message:'||json_array(" + strings.Join(keys, ",") + ") END"
		if columns["display_identity"] {
			identity = "CASE WHEN display_identity IS NOT NULL THEN 'native:'||hex(display_identity) ELSE " + identity + " END"
		}
	}
	priority := "id DESC"
	if columns["active"] {
		priority = "active DESC, " + priority
	}
	query := "WITH visible AS (SELECT *, " + identity + " AS logical_identity FROM messages WHERE " + where + "), ranked AS (SELECT *, MIN(id) OVER (PARTITION BY logical_identity) AS history_order, ROW_NUMBER() OVER (PARTITION BY logical_identity ORDER BY " + priority + ") AS representative FROM visible) SELECT " + strings.Join(selectCols, ", ") + ", history_order FROM ranked WHERE representative=1"
	if boundary > 0 {
		var checkID int64
		anchorArgs := append(append([]any{}, args...), boundary)
		err = db.QueryRowContext(ctx, "SELECT id FROM messages WHERE session_id IN ("+placeholders+") AND id=?", anchorArgs...).Scan(&checkID)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("native cursor anchor is outside this session")
		} else if err != nil {
			return nil, err
		}
		query += " AND history_order < ?"
		args = append(args, boundary)
	}
	query += " ORDER BY history_order DESC LIMIT ?"
	args = append(args, limit+1)
	selectCols = append(selectCols, "history_order")

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
			return nil, fmt.Errorf("native history row failed: %w", err)
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
	if err := msgRows.Err(); err != nil {
		return nil, fmt.Errorf("native history read failed: %w", err)
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
		firstID, _ := items[0]["history_order"].(int64)
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
