package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AgentInvocation records only usage metadata, never command inputs or results.
type AgentInvocation struct {
	Capability string
	AgentID    string
	Runtime    string
	Success    bool
	Duration   time.Duration
	OccurredAt time.Time
}

type AgentUsageSummary struct {
	Calls        int64 `json:"calls"`
	Succeeded    int64 `json:"succeeded"`
	Failed       int64 `json:"failed"`
	ActiveAgents int64 `json:"active_agents"`
}

type AgentUsageAgent struct {
	AgentID   string `json:"agent_id"`
	Name      string `json:"name"`
	Runtime   string `json:"runtime"`
	Calls     int64  `json:"calls"`
	Succeeded int64  `json:"succeeded"`
	Failed    int64  `json:"failed"`
}

type AgentUsageCapability struct {
	Capability string `json:"capability"`
	Calls      int64  `json:"calls"`
	Succeeded  int64  `json:"succeeded"`
	Failed     int64  `json:"failed"`
}

type AgentUsageRecent struct {
	Capability string    `json:"capability"`
	AgentID    string    `json:"agent_id"`
	Name       string    `json:"name"`
	Runtime    string    `json:"runtime"`
	Success    bool      `json:"success"`
	DurationMS int64     `json:"duration_ms"`
	OccurredAt time.Time `json:"occurred_at"`
}

type AgentUsageReport struct {
	Range        string                 `json:"range"`
	Summary      AgentUsageSummary      `json:"summary"`
	Agents       []AgentUsageAgent      `json:"agents"`
	Capabilities []AgentUsageCapability `json:"capabilities"`
	Recent       []AgentUsageRecent     `json:"recent"`
}

func (s *Store) RecordAgentInvocation(ctx context.Context, invocation AgentInvocation) error {
	if invocation.Capability == "" || len(invocation.Capability) > 128 || len(invocation.AgentID) > 256 || len(invocation.Runtime) > 32 || invocation.OccurredAt.IsZero() || invocation.Duration < 0 {
		return fmt.Errorf("invalid agent invocation metadata")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_invocation_usage
		(capability, agent_id, runtime, success, duration_ms, created_at_ms) VALUES (?, ?, ?, ?, ?, ?)`,
		invocation.Capability, invocation.AgentID, invocation.Runtime, invocation.Success, invocation.Duration.Milliseconds(), invocation.OccurredAt.UnixMilli())
	if err != nil {
		return fmt.Errorf("record agent invocation: %w", err)
	}
	return nil
}

// AgentUsage provides a consistent snapshot over an exact rolling window.
func (s *Store) AgentUsage(ctx context.Context, period string, now time.Time) (AgentUsageReport, error) {
	report := AgentUsageReport{Range: period, Agents: []AgentUsageAgent{}, Capabilities: []AgentUsageCapability{}, Recent: []AgentUsageRecent{}}
	var since time.Time
	switch period {
	case "24h":
		since = now.Add(-24 * time.Hour)
	case "7d":
		since = now.Add(-7 * 24 * time.Hour)
	case "30d":
		since = now.Add(-30 * 24 * time.Hour)
	case "all":
		since = time.Unix(0, 0)
	default:
		return report, fmt.Errorf("invalid agent usage range")
	}
	from, until := since.UnixMilli(), now.UnixMilli()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(success), 0),
		coalesce(sum(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0),
		count(DISTINCT nullif(agent_id, '')) FROM agent_invocation_usage
		WHERE created_at_ms >= ? AND created_at_ms <= ?`, from, until).Scan(
		&report.Summary.Calls, &report.Summary.Succeeded, &report.Summary.Failed, &report.Summary.ActiveAgents); err != nil {
		return report, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT u.agent_id, coalesce(a.name, ''), max(u.runtime), count(*),
		coalesce(sum(u.success), 0), coalesce(sum(CASE WHEN u.success = 0 THEN 1 ELSE 0 END), 0)
		FROM agent_invocation_usage u LEFT JOIN agents a ON a.id = u.agent_id
		WHERE u.created_at_ms >= ? AND u.created_at_ms <= ?
		GROUP BY u.agent_id, a.name ORDER BY count(*) DESC, u.agent_id`, from, until)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var item AgentUsageAgent
		if err := rows.Scan(&item.AgentID, &item.Name, &item.Runtime, &item.Calls, &item.Succeeded, &item.Failed); err != nil {
			rows.Close()
			return report, err
		}
		report.Agents = append(report.Agents, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT capability, count(*), coalesce(sum(success), 0),
		coalesce(sum(CASE WHEN success = 0 THEN 1 ELSE 0 END), 0) FROM agent_invocation_usage
		WHERE created_at_ms >= ? AND created_at_ms <= ? GROUP BY capability ORDER BY count(*) DESC, capability`, from, until)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var item AgentUsageCapability
		if err := rows.Scan(&item.Capability, &item.Calls, &item.Succeeded, &item.Failed); err != nil {
			rows.Close()
			return report, err
		}
		report.Capabilities = append(report.Capabilities, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT u.capability, u.agent_id, coalesce(a.name, ''), u.runtime,
		u.success, u.duration_ms, u.created_at_ms FROM agent_invocation_usage u LEFT JOIN agents a ON a.id = u.agent_id
		WHERE u.created_at_ms >= ? AND u.created_at_ms <= ? ORDER BY u.created_at_ms DESC, u.row_id DESC LIMIT 30`, from, until)
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var item AgentUsageRecent
		var millis int64
		if err := rows.Scan(&item.Capability, &item.AgentID, &item.Name, &item.Runtime, &item.Success, &item.DurationMS, &millis); err != nil {
			rows.Close()
			return report, err
		}
		item.OccurredAt = time.UnixMilli(millis).UTC()
		report.Recent = append(report.Recent, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	if err := rows.Close(); err != nil {
		return report, err
	}
	if err := tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}
