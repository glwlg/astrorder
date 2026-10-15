package hermes

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func historyTableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var def any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

type historySegment struct{ id, parent, reason, source, config string }

// Follow only verified compression continuations. Parent pointers also describe
// independent branches, delegates and resets and must not merge those chats.
func historyLineage(ctx context.Context, db *sql.DB, sessionID string) ([]string, error) {
	columns, err := historyTableColumns(ctx, db, "sessions")
	if err != nil {
		return nil, err
	}
	if !columns["parent_session_id"] || !columns["end_reason"] {
		return []string{sessionID}, nil
	}
	selectCols := []string{"id", "parent_session_id", "end_reason"}
	for _, name := range []string{"source", "model_config"} {
		if columns[name] {
			selectCols = append(selectCols, name)
		} else {
			selectCols = append(selectCols, "NULL")
		}
	}
	read := func(id string) (historySegment, error) {
		var row historySegment
		var parent, reason, source, config sql.NullString
		err := db.QueryRowContext(ctx, "SELECT "+strings.Join(selectCols, ",")+" FROM sessions WHERE id=?", id).Scan(&row.id, &parent, &reason, &source, &config)
		row.parent, row.reason, row.source, row.config = parent.String, reason.String, source.String, config.String
		return row, err
	}
	fork := func(row historySegment) bool {
		if row.source == "tool" {
			return true
		}
		var cfg map[string]any
		_ = json.Unmarshal([]byte(row.config), &cfg)
		for _, key := range []string{"_branched_from", "_delegate_from", "_reset_from"} {
			if value, ok := cfg[key].(string); ok && value != "" && value == row.parent {
				return true
			}
		}
		return false
	}
	current, err := read(sessionID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{current.id: true}
	chain := []string{current.id}
	for current.parent != "" && !fork(current) {
		parent, err := read(current.parent)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, err
		}
		if parent.reason != "compression" {
			break
		}
		if seen[parent.id] {
			return nil, fmt.Errorf("cyclic native compression lineage")
		}
		if len(chain) >= 1000 {
			return nil, fmt.Errorf("native compression lineage exceeds limit")
		}
		seen[parent.id] = true
		chain = append(chain, parent.id)
		current = parent
	}
	// Start at the requested segment for the forward walk, so an old saved id
	// also includes later continuations. Never traverse siblings of that segment.
	current, err = read(sessionID)
	if err != nil {
		return nil, err
	}
	for current.reason == "compression" {
		preference := "CASE WHEN end_reason='compression' THEN 0 "
		if columns["ended_at"] {
			preference += "WHEN ended_at IS NULL THEN 1 "
		}
		order := []string{preference + "ELSE 2 END"}
		if columns["started_at"] {
			order = append(order, "started_at DESC")
		}
		order = append(order, "id DESC")
		rows, err := db.QueryContext(ctx, "SELECT id FROM sessions WHERE parent_session_id=? ORDER BY "+strings.Join(order, ","), current.id)
		if err != nil {
			return nil, err
		}
		var candidates []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			candidates = append(candidates, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		var next *historySegment
		for _, id := range candidates {
			row, err := read(id)
			if err != nil {
				return nil, err
			}
			if !fork(row) {
				next = &row
				break
			}
		}
		if next == nil {
			break
		}
		if seen[next.id] {
			return nil, fmt.Errorf("cyclic native compression lineage")
		}
		if len(chain) >= 1000 {
			return nil, fmt.Errorf("native compression lineage exceeds limit")
		}
		chain = append(chain, next.id)
		seen[next.id] = true
		current = *next
	}
	return chain, nil
}
