package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Preference carries presence separately so a persisted textual JSON null is a tombstone.
type Preference struct {
	Value  any
	Exists bool
}

// GetPreference returns a JSON-decoded value while preserving tombstone presence.
func (s *Store) GetPreference(ctx context.Context, key string) (Preference, error) {
	row, err := s.QueryRow(ctx, `SELECT "key",value FROM workspace_preferences WHERE "key"=?`, key)
	if err != nil || row == nil {
		return Preference{}, err
	}
	return Preference{Value: row["value"], Exists: true}, nil
}

// ListPreferences returns every workspace preference key and decoded value.
func (s *Store) ListPreferences(ctx context.Context) (map[string]any, error) {
	rows, err := s.QueryRows(ctx, `SELECT "key",value FROM workspace_preferences ORDER BY "key"`)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any, len(rows))
	for _, row := range rows {
		result[row["key"].(string)] = row["value"]
	}
	return result, nil
}

// SetPreference writes JSON. When overwrite is false an existing tombstone or value wins.
func (s *Store) SetPreference(ctx context.Context, key string, value any, overwrite bool) error {
	if key == "" || len(key) > 1100 {
		return errors.New("preference key must be 1-1100 bytes")
	}
	encoded, err := encodeJSON(value)
	if err != nil {
		return err
	}
	conflict := "DO NOTHING"
	if overwrite {
		conflict = "DO UPDATE SET value=excluded.value"
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO workspace_preferences("key",value) VALUES(?,?) ON CONFLICT("key") `+conflict, key, encoded)
	return err
}

// DeletePreference removes one exact key. Preferences tombstones should normally use SetPreference(nil).
func (s *Store) DeletePreference(ctx context.Context, key string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM workspace_preferences WHERE "key"=?`, key)
	if err != nil {
		return false, err
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

// Event is one durable application event returned only after transaction commit.
type Event struct {
	ID        string
	Cursor    int64
	Type      string
	AgentID   string
	SessionID string
	Data      map[string]any
}

// ProjectionFunc is a registered transaction projection callback.
type ProjectionFunc func(*sql.Tx) ([]Event, error)

// LatestCursor returns the current application event cursor.
func (s *Store) LatestCursor(ctx context.Context) (int64, error) {
	var value int64
	err := s.db.QueryRowContext(ctx, "SELECT coalesce(max(cursor),0) FROM events").Scan(&value)
	return value, err
}

// AppendEvent inserts one event unless its nullable-agent scope already contains the event ID.
func (s *Store) AppendEvent(ctx context.Context, event Event) (*Event, error) {
	events, err := s.AppendEvents(ctx, []Event{event})
	if err != nil || len(events) == 0 {
		return nil, err
	}
	return &events[0], nil
}

// AppendEvents commits related application events together, omitting duplicates.
func (s *Store) AppendEvents(ctx context.Context, events []Event) ([]Event, error) {
	var result []Event
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, event := range events {
			inserted, err := insertEventTx(ctx, tx, event, s.eventRetention)
			if err != nil {
				return err
			}
			if inserted != nil {
				result = append(result, *inserted)
			}
		}
		return nil
	})
	return result, err
}

// ReplayEvents returns resync=true when after is outside retained cursor bounds.
func (s *Store) ReplayEvents(ctx context.Context, after int64) (bool, []Event, error) {
	bounds, err := s.QueryRow(ctx, "SELECT coalesce(min(cursor),0) AS oldest,coalesce(max(cursor),0) AS latest FROM events")
	if err != nil {
		return false, nil, err
	}
	oldest, _ := integer(bounds["oldest"], 0)
	latest, _ := integer(bounds["latest"], 0)
	if after > latest || (oldest != 0 && after < oldest-1) {
		return true, []Event{}, nil
	}
	rows, err := s.QueryRows(ctx, "SELECT * FROM events WHERE cursor>? ORDER BY cursor", after)
	if err != nil {
		return false, nil, err
	}
	result := make([]Event, 0, len(rows))
	for _, row := range rows {
		event, err := eventFromRow(row)
		if err != nil {
			return false, nil, err
		}
		if IsTransientEvent(event.Type) {
			// Advance across legacy rows without replaying their transcript copies.
			event.Type = "event.cursor"
			event.Data = map[string]any{}
		}
		result = append(result, event)
	}
	return false, result, nil
}

// GetCheckpoint returns zero when a daemon epoch/stream has no durable checkpoint.
func (s *Store) GetCheckpoint(ctx context.Context, epoch, streamID string) (int64, error) {
	if err := validateCheckpointKey(epoch, streamID); err != nil {
		return 0, err
	}
	row, err := s.QueryRow(ctx, "SELECT seq_id FROM daemon_checkpoints WHERE daemon_id=? AND session_id=?", epoch, streamID)
	if err != nil || row == nil {
		return 0, err
	}
	return integer(row["seq_id"], 0)
}

// ListCheckpoints returns stream checkpoints for one exact daemon epoch.
func (s *Store) ListCheckpoints(ctx context.Context, epoch string) (map[string]int64, error) {
	if err := validateCheckpointKey(epoch, "probe"); err != nil {
		return nil, err
	}
	rows, err := s.QueryRows(ctx, "SELECT session_id,seq_id FROM daemon_checkpoints WHERE daemon_id=? ORDER BY session_id", epoch)
	if err != nil {
		return nil, err
	}
	result := make(map[string]int64, len(rows))
	for _, row := range rows {
		value, err := integer(row["seq_id"], 0)
		if err != nil {
			return nil, err
		}
		result[row["session_id"].(string)] = value
	}
	return result, nil
}

// SetCheckpoint advances but never regresses an exact daemon epoch/stream checkpoint.
func (s *Store) SetCheckpoint(ctx context.Context, epoch, streamID string, seq int64) (int64, error) {
	if err := validateCheckpointKey(epoch, streamID); err != nil {
		return 0, err
	}
	if seq < 0 {
		return 0, errors.New("checkpoint seq must be non-negative")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO daemon_checkpoints(daemon_id,session_id,seq_id,updated_at)
VALUES(?,?,?,?) ON CONFLICT(daemon_id,session_id) DO UPDATE SET seq_id=excluded.seq_id,updated_at=excluded.updated_at
WHERE excluded.seq_id > daemon_checkpoints.seq_id`, epoch, streamID, seq, nowDatabaseTime())
	if err != nil {
		return 0, err
	}
	return s.GetCheckpoint(ctx, epoch, streamID)
}

// ApplyFrame validates exact contiguous sequence, runs one registered projection,
// stores returned events and advances the checkpoint in the same transaction.
// A duplicate current sequence is idempotent and does not re-run fn.
func (s *Store) ApplyFrame(ctx context.Context, epoch, streamID string, seq int64, projector *Projector) ([]Event, error) {
	if err := validateCheckpointKey(epoch, streamID); err != nil {
		return nil, err
	}
	if seq < 1 {
		return nil, errors.New("frame seq must be positive")
	}
	if !s.projectionRegistered(projector) {
		return nil, errors.New("frame projection callback is not registered")
	}
	committed := make([]Event, 0)
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		var current int64
		err := tx.QueryRowContext(ctx, "SELECT seq_id FROM daemon_checkpoints WHERE daemon_id=? AND session_id=?", epoch, streamID).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			current = 0
		} else if err != nil {
			return err
		}
		if seq <= current {
			return nil
		}
		if seq != current+1 {
			return fmt.Errorf("non-contiguous frame: current=%d incoming=%d", current, seq)
		}
		events, err := projector.apply(tx)
		if err != nil {
			return err
		}
		for _, event := range events {
			inserted, err := insertEventTx(ctx, tx, event, s.eventRetention)
			if err != nil {
				return err
			}
			if inserted != nil {
				committed = append(committed, *inserted)
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO daemon_checkpoints(daemon_id,session_id,seq_id,updated_at)
VALUES(?,?,?,?) ON CONFLICT(daemon_id,session_id) DO UPDATE SET seq_id=excluded.seq_id,updated_at=excluded.updated_at`,
			epoch, streamID, seq, nowDatabaseTime())
		return err
	})
	if err != nil {
		return nil, err
	}
	return committed, nil
}

func insertEventTx(ctx context.Context, tx *sql.Tx, event Event, retention int64) (*Event, error) {
	if event.ID == "" || event.Type == "" {
		return nil, errors.New("event ID and type are required")
	}
	if IsTransientEvent(event.Type) {
		event.Cursor = 0
		return &event, nil
	}
	if err := resolveSessionUpsertIdentity(&event); err != nil {
		return nil, err
	}
	var count int
	var err error
	if event.AgentID == "" {
		err = tx.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE event_id=? AND agent_id IS NULL", event.ID).Scan(&count)
	} else {
		err = tx.QueryRowContext(ctx, "SELECT count(*) FROM events WHERE event_id=? AND agent_id=?", event.ID, event.AgentID).Scan(&count)
	}
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, nil
	}
	if event.Type == "session.upsert" {
		dataCopy := make(map[string]any, len(event.Data)+2)
		for k, v := range event.Data {
			dataCopy[k] = v
		}
		var parentAid, parentSid sql.NullString
		rowErr := tx.QueryRowContext(ctx, "SELECT parent_agent_id, parent_session_id FROM delegation_edges WHERE child_agent_id=? AND child_session_id=?", event.AgentID, event.SessionID).Scan(&parentAid, &parentSid)
		if rowErr == nil {
			if parentAid.Valid {
				dataCopy["delegation_parent_agent_id"] = parentAid.String
			} else {
				dataCopy["delegation_parent_agent_id"] = nil
			}
			if parentSid.Valid {
				dataCopy["delegation_parent_session_id"] = parentSid.String
			} else {
				dataCopy["delegation_parent_session_id"] = nil
			}
		} else if errors.Is(rowErr, sql.ErrNoRows) {
			dataCopy["delegation_parent_agent_id"] = nil
			dataCopy["delegation_parent_session_id"] = nil
		} else {
			return nil, rowErr
		}
		event.Data = dataCopy
	}
	if err := persistApprovalTx(ctx, tx, event); err != nil {
		return nil, err
	}
	data, err := encodeJSON(event.Data)
	if err != nil {
		return nil, err
	}
	var agentID any
	if event.AgentID != "" {
		agentID = event.AgentID
	}
	var sessionID any
	if event.SessionID != "" {
		sessionID = event.SessionID
	}
	result, err := tx.ExecContext(ctx, "INSERT INTO events(event_id,type,agent_id,session_id,data,created_at) VALUES(?,?,?,?,?,?)",
		event.ID, event.Type, agentID, sessionID, data, nowDatabaseTime())
	if err != nil {
		return nil, err
	}
	cursor, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	event.Cursor = cursor
	cutoff := cursor - retention + 1
	if retention > 0 && cutoff > 1 {
		if _, err := tx.ExecContext(ctx, "DELETE FROM events WHERE cursor<?", cutoff); err != nil {
			return nil, err
		}
	}
	return &event, nil
}

func eventFromRow(row map[string]any) (Event, error) {
	data, ok := row["data"].(map[string]any)
	if !ok && row["data"] != nil {
		return Event{}, errors.New("persisted event data must be an object or null")
	}
	cursor, _ := integer(row["cursor"], 0)
	return Event{
		ID: fmt.Sprint(row["event_id"]), Cursor: cursor, Type: fmt.Sprint(row["type"]),
		AgentID: stringOrEmpty(row["agent_id"]), SessionID: stringOrEmpty(row["session_id"]),
		Data: data,
	}, nil
}

func validateCheckpointKey(epoch, streamID string) error {
	if epoch == "" || len(epoch) > 128 {
		return errors.New("epoch must be a non-empty string up to 128 bytes")
	}
	if streamID == "" || len(streamID) > 256 {
		return errors.New("stream ID must be a non-empty string up to 256 bytes")
	}
	return nil
}

func stringOrEmpty(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func resolveSessionUpsertIdentity(event *Event) error {
	if event.Type != "session.upsert" {
		return nil
	}
	agentID := event.AgentID
	if event.Data != nil {
		if v, ok := event.Data["agent_id"]; ok && v != nil {
			s, valid := v.(string)
			if !valid {
				return errors.New("session.upsert agent_id must be a string")
			}
			if s != "" {
				if agentID != "" && agentID != s {
					return fmt.Errorf("session.upsert agent ID conflict: event=%q data=%q", agentID, s)
				}
				agentID = s
			}
		}
	}
	if agentID == "" {
		return errors.New("session.upsert requires agent ID")
	}

	sessionID := event.SessionID
	if event.Data != nil {
		if v, ok := event.Data["id"]; ok && v != nil {
			s, valid := v.(string)
			if !valid {
				return errors.New("session.upsert id must be a string")
			}
			if s != "" {
				if sessionID != "" && sessionID != s {
					return fmt.Errorf("session.upsert session ID conflict: event=%q data id=%q", sessionID, s)
				}
				sessionID = s
			}
		}
		if v, ok := event.Data["session_id"]; ok && v != nil {
			s, valid := v.(string)
			if !valid {
				return errors.New("session.upsert session_id must be a string")
			}
			if s != "" {
				if sessionID != "" && sessionID != s {
					return fmt.Errorf("session.upsert session ID conflict: event=%q data session_id=%q", sessionID, s)
				}
				sessionID = s
			}
		}
	}
	if sessionID == "" {
		return errors.New("session.upsert requires session ID")
	}

	event.AgentID = agentID
	event.SessionID = sessionID
	return nil
}
