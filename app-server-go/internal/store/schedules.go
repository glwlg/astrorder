package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SessionSchedule persists one scheduled prompt attached to a session.
type SessionSchedule struct {
	ID                  string `json:"id"`
	AgentID             string `json:"agent_id"`
	SessionID           string `json:"session_id"`
	Name                string `json:"name"`
	Prompt              string `json:"prompt"`
	Enabled             bool   `json:"enabled"`
	ScheduleKind        string `json:"schedule_kind"`
	CronExpr            string `json:"cron_expr"`
	IntervalMinutes     int    `json:"interval_minutes"`
	DailyTime           string `json:"daily_time"`
	WeekdayMask         int    `json:"weekday_mask"`
	RunAt               string `json:"run_at,omitempty"`
	Timezone            string `json:"timezone"`
	WhenBusy            string `json:"when_busy"`
	MisfirePolicy       string `json:"misfire_policy"`
	MisfireGraceSec     int    `json:"misfire_grace_sec"`
	NextRunAt           string `json:"next_run_at,omitempty"`
	LastRunAt           string `json:"last_run_at,omitempty"`
	LastStatus          string `json:"last_status"`
	LastError           string `json:"last_error"`
	LastCommandID       string `json:"last_command_id"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	PauseReason         string `json:"pause_reason"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

func nullableDBTime(raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	return databaseTime(trimmed)
}

func wireOptionalTime(raw any) (string, error) {
	if raw == nil {
		return "", nil
	}
	if text, ok := raw.(string); ok && strings.TrimSpace(text) == "" {
		return "", nil
	}
	return wireTime(raw)
}

func decodeSessionScheduleRow(row map[string]any) (SessionSchedule, error) {
	enabled, err := boolValue(row["enabled"], true)
	if err != nil {
		return SessionSchedule{}, err
	}
	intervalMin, err := integer(row["interval_minutes"], 0)
	if err != nil {
		return SessionSchedule{}, err
	}
	weekdayMask, err := integer(row["weekday_mask"], 0)
	if err != nil {
		return SessionSchedule{}, err
	}
	misfireGrace, err := integer(row["misfire_grace_sec"], 900)
	if err != nil {
		return SessionSchedule{}, err
	}
	failures, err := integer(row["consecutive_failures"], 0)
	if err != nil {
		return SessionSchedule{}, err
	}
	runAt, err := wireOptionalTime(row["run_at"])
	if err != nil {
		return SessionSchedule{}, err
	}
	nextRunAt, err := wireOptionalTime(row["next_run_at"])
	if err != nil {
		return SessionSchedule{}, err
	}
	lastRunAt, err := wireOptionalTime(row["last_run_at"])
	if err != nil {
		return SessionSchedule{}, err
	}
	createdAt, err := wireTime(row["created_at"])
	if err != nil {
		return SessionSchedule{}, err
	}
	updatedAt, err := wireTime(row["updated_at"])
	if err != nil {
		return SessionSchedule{}, err
	}
	asStr := func(key string) string {
		v, ok := row[key]
		if !ok || v == nil {
			return ""
		}
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	return SessionSchedule{
		ID:                  asStr("id"),
		AgentID:             asStr("agent_id"),
		SessionID:           asStr("session_id"),
		Name:                asStr("name"),
		Prompt:              asStr("prompt"),
		Enabled:             enabled,
		ScheduleKind:        asStr("schedule_kind"),
		CronExpr:            asStr("cron_expr"),
		IntervalMinutes:     int(intervalMin),
		DailyTime:           asStr("daily_time"),
		WeekdayMask:         int(weekdayMask),
		RunAt:               runAt,
		Timezone:            asStr("timezone"),
		WhenBusy:            asStr("when_busy"),
		MisfirePolicy:       asStr("misfire_policy"),
		MisfireGraceSec:     int(misfireGrace),
		NextRunAt:           nextRunAt,
		LastRunAt:           lastRunAt,
		LastStatus:          asStr("last_status"),
		LastError:           asStr("last_error"),
		LastCommandID:       asStr("last_command_id"),
		ConsecutiveFailures: int(failures),
		PauseReason:         asStr("pause_reason"),
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
	}, nil
}

// CreateSessionSchedule persists a new session schedule and returns the stored wire record.
func (s *Store) CreateSessionSchedule(ctx context.Context, item SessionSchedule) (SessionSchedule, error) {
	if strings.TrimSpace(item.ID) == "" {
		id, err := randomID("sched-", 16)
		if err != nil {
			return SessionSchedule{}, err
		}
		item.ID = id
	}
	runAtDB, err := nullableDBTime(item.RunAt)
	if err != nil {
		return SessionSchedule{}, err
	}
	nextRunAtDB, err := nullableDBTime(item.NextRunAt)
	if err != nil {
		return SessionSchedule{}, err
	}
	lastRunAtDB, err := nullableDBTime(item.LastRunAt)
	if err != nil {
		return SessionSchedule{}, err
	}
	now := nowDatabaseTime()
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		_, execErr := tx.ExecContext(ctx, `INSERT INTO session_schedules (
	id, agent_id, session_id, name, prompt, enabled, schedule_kind, cron_expr,
	interval_minutes, daily_time, weekday_mask, run_at, timezone,
	when_busy, misfire_policy, misfire_grace_sec,
	next_run_at, last_run_at, last_status, last_error, last_command_id,
	consecutive_failures, pause_reason, created_at, updated_at
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			item.ID, item.AgentID, item.SessionID, item.Name, item.Prompt,
			item.Enabled, item.ScheduleKind, item.CronExpr,
			item.IntervalMinutes, item.DailyTime, item.WeekdayMask, runAtDB, item.Timezone,
			item.WhenBusy, item.MisfirePolicy, item.MisfireGraceSec,
			nextRunAtDB, lastRunAtDB, item.LastStatus, item.LastError, item.LastCommandID,
			item.ConsecutiveFailures, item.PauseReason, now, now,
		)
		return execErr
	})
	if err != nil {
		return SessionSchedule{}, err
	}
	stored, err := s.GetSessionSchedule(ctx, item.ID)
	if err != nil {
		return SessionSchedule{}, err
	}
	if stored == nil {
		return SessionSchedule{}, errors.New("created session schedule not found")
	}
	return *stored, nil
}

// GetSessionSchedule fetches one session schedule by ID, returning nil when absent.
func (s *Store) GetSessionSchedule(ctx context.Context, id string) (*SessionSchedule, error) {
	rows, err := s.QueryRows(ctx, `SELECT * FROM session_schedules WHERE id=?`, id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	decoded, err := decodeSessionScheduleRow(rows[0])
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}

// ListSessionSchedules returns schedules for one session, or all schedules when agentID and sessionID are empty.
func (s *Store) ListSessionSchedules(ctx context.Context, agentID, sessionID string) ([]SessionSchedule, error) {
	var (
		rows []map[string]any
		err  error
	)
	if strings.TrimSpace(agentID) != "" && strings.TrimSpace(sessionID) != "" {
		rows, err = s.QueryRows(ctx, `SELECT * FROM session_schedules WHERE agent_id=? AND session_id=? ORDER BY created_at DESC, id ASC`, agentID, sessionID)
	} else {
		rows, err = s.QueryRows(ctx, `SELECT * FROM session_schedules ORDER BY enabled DESC, COALESCE(next_run_at, '9999-12-31 23:59:59.000000') ASC, created_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	items := make([]SessionSchedule, 0, len(rows))
	for _, row := range rows {
		decoded, err := decodeSessionScheduleRow(row)
		if err != nil {
			return nil, err
		}
		items = append(items, decoded)
	}
	return items, nil
}

// ListDueSessionSchedules returns enabled schedules whose next_run_at <= nowUTC.
func (s *Store) ListDueSessionSchedules(ctx context.Context, nowUTC time.Time) ([]SessionSchedule, error) {
	cutoff, err := databaseTime(nowUTC.UTC())
	if err != nil {
		return nil, err
	}
	rows, err := s.QueryRows(ctx, `SELECT * FROM session_schedules
WHERE enabled=1 AND next_run_at IS NOT NULL AND next_run_at != '' AND next_run_at <= ?
ORDER BY next_run_at ASC, id ASC`, cutoff)
	if err != nil {
		return nil, err
	}
	items := make([]SessionSchedule, 0, len(rows))
	for _, row := range rows {
		decoded, err := decodeSessionScheduleRow(row)
		if err != nil {
			return nil, err
		}
		items = append(items, decoded)
	}
	return items, nil
}

// UpdateSessionSchedule replaces mutable fields of an existing schedule by ID.
func (s *Store) UpdateSessionSchedule(ctx context.Context, item SessionSchedule) (*SessionSchedule, error) {
	runAtDB, err := nullableDBTime(item.RunAt)
	if err != nil {
		return nil, err
	}
	nextRunAtDB, err := nullableDBTime(item.NextRunAt)
	if err != nil {
		return nil, err
	}
	lastRunAtDB, err := nullableDBTime(item.LastRunAt)
	if err != nil {
		return nil, err
	}
	now := nowDatabaseTime()
	var affected int64
	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		res, execErr := tx.ExecContext(ctx, `UPDATE session_schedules SET
	name=?, prompt=?, enabled=?, schedule_kind=?, cron_expr=?,
	interval_minutes=?, daily_time=?, weekday_mask=?, run_at=?, timezone=?,
	when_busy=?, misfire_policy=?, misfire_grace_sec=?,
	next_run_at=?, last_run_at=?, last_status=?, last_error=?, last_command_id=?,
	consecutive_failures=?, pause_reason=?, updated_at=?
WHERE id=?`,
			item.Name, item.Prompt, item.Enabled, item.ScheduleKind, item.CronExpr,
			item.IntervalMinutes, item.DailyTime, item.WeekdayMask, runAtDB, item.Timezone,
			item.WhenBusy, item.MisfirePolicy, item.MisfireGraceSec,
			nextRunAtDB, lastRunAtDB, item.LastStatus, item.LastError, item.LastCommandID,
			item.ConsecutiveFailures, item.PauseReason, now, item.ID,
		)
		if execErr != nil {
			return execErr
		}
		affected, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, nil
	}
	return s.GetSessionSchedule(ctx, item.ID)
}

// DeleteSessionSchedule removes one schedule by ID.
func (s *Store) DeleteSessionSchedule(ctx context.Context, id string) (bool, error) {
	var affected int64
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		res, execErr := tx.ExecContext(ctx, `DELETE FROM session_schedules WHERE id=?`, id)
		if execErr != nil {
			return execErr
		}
		affected, _ = res.RowsAffected()
		return nil
	})
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
