package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type SessionCheckpointFile struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

type SessionCheckpoint struct {
	ID           string                  `json:"id"`
	AgentID      string                  `json:"agent_id"`
	SessionID    string                  `json:"session_id"`
	TurnNumber   int                     `json:"turn_number"`
	Kind         string                  `json:"kind"`
	CommandID    string                  `json:"command_id,omitempty"`
	Label        string                  `json:"label"`
	Workspace    string                  `json:"workspace"`
	RepoRoot     string                  `json:"repo_root"`
	Branch       string                  `json:"branch"`
	HeadSHA      string                  `json:"head_sha"`
	TreeSHA      string                  `json:"tree_sha"`
	CommitSHA    string                  `json:"commit_sha"`
	RefName      string                  `json:"ref_name"`
	PrevTreeSHA  string                  `json:"prev_tree_sha,omitempty"`
	FilesChanged int                     `json:"files_changed"`
	Insertions   int                     `json:"insertions"`
	Deletions    int                     `json:"deletions"`
	Files        []SessionCheckpointFile `json:"files"`
	CreatedAt    string                  `json:"created_at"`
}

func (s *Store) ListSessionCheckpoints(ctx context.Context, agentID, sessionID string, limit int) ([]SessionCheckpoint, error) {
	if limit <= 0 || limit > 250 {
		limit = 60
	}
	var rows *sql.Rows
	var err error
	if strings.TrimSpace(agentID) != "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, agent_id, session_id, turn_number, kind, command_id, label,
				workspace, repo_root, branch, head_sha, tree_sha, commit_sha, ref_name,
				prev_tree_sha, files_changed, insertions, deletions, files_json, created_at
			FROM session_checkpoints
			WHERE agent_id = ? AND session_id = ?
			ORDER BY turn_number DESC, created_at DESC
			LIMIT ?`, agentID, sessionID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, agent_id, session_id, turn_number, kind, command_id, label,
				workspace, repo_root, branch, head_sha, tree_sha, commit_sha, ref_name,
				prev_tree_sha, files_changed, insertions, deletions, files_json, created_at
			FROM session_checkpoints
			WHERE session_id = ?
			ORDER BY turn_number DESC, created_at DESC
			LIMIT ?`, sessionID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]SessionCheckpoint, 0)
	for rows.Next() {
		item, scanErr := scanSessionCheckpoint(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) GetSessionCheckpoint(ctx context.Context, agentID, sessionID, checkpointID string) (*SessionCheckpoint, error) {
	var row *sql.Row
	if strings.TrimSpace(agentID) != "" {
		row = s.db.QueryRowContext(ctx, `
			SELECT id, agent_id, session_id, turn_number, kind, command_id, label,
				workspace, repo_root, branch, head_sha, tree_sha, commit_sha, ref_name,
				prev_tree_sha, files_changed, insertions, deletions, files_json, created_at
			FROM session_checkpoints
			WHERE agent_id = ? AND session_id = ? AND id = ?`, agentID, sessionID, checkpointID)
	} else {
		row = s.db.QueryRowContext(ctx, `
			SELECT id, agent_id, session_id, turn_number, kind, command_id, label,
				workspace, repo_root, branch, head_sha, tree_sha, commit_sha, ref_name,
				prev_tree_sha, files_changed, insertions, deletions, files_json, created_at
			FROM session_checkpoints
			WHERE session_id = ? AND id = ?`, sessionID, checkpointID)
	}
	item, err := scanSessionCheckpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) LatestSessionCheckpoint(ctx context.Context, agentID, sessionID string) (*SessionCheckpoint, error) {
	items, err := s.ListSessionCheckpoints(ctx, agentID, sessionID, 1)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (s *Store) InsertSessionCheckpoint(ctx context.Context, item SessionCheckpoint) (SessionCheckpoint, error) {
	if item.CreatedAt == "" {
		item.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if item.Files == nil {
		item.Files = []SessionCheckpointFile{}
	}
	encodedFiles, err := json.Marshal(item.Files)
	if err != nil {
		return SessionCheckpoint{}, err
	}
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		if item.TurnNumber <= 0 {
			var maxTurn int
			_ = tx.QueryRowContext(ctx, `
				SELECT COALESCE(MAX(turn_number), 0) FROM session_checkpoints
				WHERE agent_id = ? AND session_id = ?`, item.AgentID, item.SessionID).Scan(&maxTurn)
			item.TurnNumber = maxTurn + 1
		}
		_, execErr := tx.ExecContext(ctx, `
			INSERT INTO session_checkpoints (
				id, agent_id, session_id, turn_number, kind, command_id, label,
				workspace, repo_root, branch, head_sha, tree_sha, commit_sha, ref_name,
				prev_tree_sha, files_changed, insertions, deletions, files_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.AgentID, item.SessionID, item.TurnNumber, item.Kind, item.CommandID, item.Label,
			item.Workspace, item.RepoRoot, item.Branch, item.HeadSHA, item.TreeSHA, item.CommitSHA, item.RefName,
			item.PrevTreeSHA, item.FilesChanged, item.Insertions, item.Deletions, string(encodedFiles), item.CreatedAt,
		)
		return execErr
	})
	if err != nil {
		return SessionCheckpoint{}, err
	}
	return item, nil
}

func (s *Store) DeleteSessionCheckpoint(ctx context.Context, agentID, sessionID, checkpointID string) (bool, error) {
	var res sql.Result
	var err error
	if strings.TrimSpace(agentID) != "" {
		res, err = s.db.ExecContext(ctx, `DELETE FROM session_checkpoints WHERE agent_id = ? AND session_id = ? AND id = ?`, agentID, sessionID, checkpointID)
	} else {
		res, err = s.db.ExecContext(ctx, `DELETE FROM session_checkpoints WHERE session_id = ? AND id = ?`, sessionID, checkpointID)
	}
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

// ListPrunableAutoCheckpoints returns automatic turn checkpoints (`auto_before`
// / `auto_after`) older than the newest keepAuto automatic rows. Manual and
// pre_restore safety backups are never returned for pruning.
func (s *Store) ListPrunableAutoCheckpoints(ctx context.Context, agentID, sessionID string, keepAuto int) ([]SessionCheckpoint, error) {
	if keepAuto <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_id, session_id, turn_number, kind, command_id, label,
			workspace, repo_root, branch, head_sha, tree_sha, commit_sha, ref_name,
			prev_tree_sha, files_changed, insertions, deletions, files_json, created_at
		FROM session_checkpoints
		WHERE agent_id = ? AND session_id = ? AND kind IN ('auto_before', 'auto_after')
		ORDER BY turn_number DESC, created_at DESC
		LIMIT -1 OFFSET ?`, agentID, sessionID, keepAuto)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []SessionCheckpoint
	for rows.Next() {
		item, scanErr := scanSessionCheckpoint(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// HealSleptErrorSessions resets historical sessions stuck in status='error'
// purely because idle sleep intentionally closed their Codex child process.
func (s *Store) HealSleptErrorSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET status = 'idle'
		WHERE status = 'error'
		  AND EXISTS (
		    SELECT 1 FROM workspace_preferences p
		    WHERE p."key" = 'session_sleep:' || json_array(sessions.agent_id, sessions.id)
		      AND lower(trim(p.value)) = 'true'
		  )`)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return affected, nil
}

type checkpointScanner interface {
	Scan(dest ...any) error
}

func scanSessionCheckpoint(scanner checkpointScanner) (SessionCheckpoint, error) {
	var item SessionCheckpoint
	var filesJSON string
	var createdAt any
	err := scanner.Scan(
		&item.ID, &item.AgentID, &item.SessionID, &item.TurnNumber, &item.Kind, &item.CommandID, &item.Label,
		&item.Workspace, &item.RepoRoot, &item.Branch, &item.HeadSHA, &item.TreeSHA, &item.CommitSHA, &item.RefName,
		&item.PrevTreeSHA, &item.FilesChanged, &item.Insertions, &item.Deletions, &filesJSON, &createdAt,
	)
	if err != nil {
		return SessionCheckpoint{}, err
	}
	switch v := createdAt.(type) {
	case time.Time:
		item.CreatedAt = v.UTC().Format(time.RFC3339Nano)
	case string:
		item.CreatedAt = v
	case []byte:
		item.CreatedAt = string(v)
	}
	if filesJSON != "" {
		_ = json.Unmarshal([]byte(filesJSON), &item.Files)
	}
	if item.Files == nil {
		item.Files = []SessionCheckpointFile{}
	}
	return item, nil
}
