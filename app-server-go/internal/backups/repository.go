package backups

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const storageTSLayout = "2006-01-02T15:04:05.000Z"

func formatTS(t time.Time) string {
	return t.UTC().Format(storageTSLayout)
}

func formatTSPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTS(*t)
	return &s
}

func parseTS(raw any) string {
	if raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case time.Time:
		return formatTS(v)
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func nullableTSPtr(raw any) *string {
	if raw == nil {
		return nil
	}
	s := parseTS(raw)
	if s == "" {
		return nil
	}
	return &s
}

func sqlNullableString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func nullableStringPtr(raw any) *string {
	if raw == nil {
		return nil
	}
	s := fmt.Sprint(raw)
	return &s
}

func asInt(raw any) int {
	switch v := raw.(type) {
	case int64:
		return int(v)
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

func asInt64(raw any) int64 {
	switch v := raw.(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	default:
		return 0
	}
}

func asBool(raw any) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case int64:
		return v != 0
	case int:
		return v != 0
	case string:
		return v == "1" || strings.EqualFold(v, "true")
	default:
		return false
	}
}

func jobFromRow(row map[string]any) BackupJob {
	var agent *RestoreAgentTarget
	environment, _ := row["post_restore_agent_environment"].(string)
	kind, _ := row["post_restore_agent_kind"].(string)
	if environment != "" || kind != "" {
		agent = &RestoreAgentTarget{EnvironmentID: environment, Kind: kind}
	}
	return BackupJob{
		ID:                      fmt.Sprint(row["id"]),
		Name:                    fmt.Sprint(row["name"]),
		ConnectionID:            fmt.Sprint(row["connection_id"]),
		SourcePath:              fmt.Sprint(row["source_path"]),
		DestinationDir:          fmt.Sprint(row["destination_dir"]),
		PostRestoreCommand:      fmt.Sprint(row["post_restore_command"]),
		PostRestoreRestartAgent: agent,
		ScheduleEnabled:         asBool(row["schedule_enabled"]),
		ScheduleMode:            fmt.Sprint(row["schedule_mode"]),
		IntervalMinutes:         asInt(row["interval_minutes"]),
		DailyTime:               fmt.Sprint(row["daily_time"]),
		Weekday:                 asInt(row["weekday"]),
		RetentionCount:          asInt(row["retention_count"]),
		LastRunAt:               nullableTSPtr(row["last_run_at"]),
		LastStatus:              nullableStringPtr(row["last_status"]),
		LastError:               nullableStringPtr(row["last_error"]),
		NextRunAt:               nullableTSPtr(row["next_run_at"]),
		CreatedAt:               parseTS(row["created_at"]),
		UpdatedAt:               parseTS(row["updated_at"]),
	}
}

func versionFromRow(row map[string]any) BackupVersion {
	localPath := fmt.Sprint(row["local_path"])
	_, statErr := os.Stat(localPath)
	return BackupVersion{
		ID:             fmt.Sprint(row["id"]),
		JobID:          fmt.Sprint(row["job_id"]),
		VersionNumber:  asInt(row["version_number"]),
		VersionLabel:   fmt.Sprint(row["version_label"]),
		TriggerKind:    fmt.Sprint(row["trigger_kind"]),
		Note:           fmt.Sprint(row["note"]),
		Status:         fmt.Sprint(row["status"]),
		ErrorDetail:    nullableStringPtr(row["error_detail"]),
		LocalPath:      localPath,
		SourceKind:     fmt.Sprint(row["source_kind"]),
		FileCount:      asInt(row["file_count"]),
		TotalBytes:     asInt64(row["total_bytes"]),
		ManifestSHA256: nullableStringPtr(row["manifest_sha256"]),
		CreatedAt:      parseTS(row["created_at"]),
		FinishedAt:     nullableTSPtr(row["finished_at"]),
		ExistsOnDisk:   statErr == nil,
		ConnectionID:   fmt.Sprint(row["connection_id"]),
		SourcePath:     fmt.Sprint(row["source_path"]),
	}
}

func (s *Service) listJobRows(ctx context.Context) ([]BackupJob, error) {
	rows, err := s.store.QueryRows(ctx, `SELECT * FROM backup_jobs ORDER BY created_at DESC, id ASC`)
	if err != nil {
		return nil, err
	}
	jobs := make([]BackupJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, jobFromRow(row))
	}
	return jobs, nil
}

func (s *Service) getJobRow(ctx context.Context, jobID string) (BackupJob, bool, error) {
	row, err := s.store.QueryRow(ctx, `SELECT * FROM backup_jobs WHERE id = ?`, jobID)
	if err != nil {
		return BackupJob{}, false, err
	}
	if row == nil {
		return BackupJob{}, false, nil
	}
	return jobFromRow(row), true, nil
}

func (s *Service) insertJobRow(ctx context.Context, job BackupJob) error {
	return insertJob(ctx, s.store.DB(), job)
}

type rowExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertJob(ctx context.Context, db rowExecutor, job BackupJob) error {
	enabled := 0
	if job.ScheduleEnabled {
		enabled = 1
	}
	agent := restoreAgentValues(job.PostRestoreRestartAgent)
	_, err := db.ExecContext(ctx, `INSERT INTO backup_jobs (
		id, name, connection_id, source_path, destination_dir,
		schedule_enabled, schedule_mode, interval_minutes, daily_time, weekday, retention_count,
		last_run_at, last_status, last_error, next_run_at, created_at, updated_at, post_restore_command,
		post_restore_agent_environment, post_restore_agent_kind
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Name, job.ConnectionID, job.SourcePath, job.DestinationDir,
		enabled, job.ScheduleMode, job.IntervalMinutes, job.DailyTime, job.Weekday, job.RetentionCount,
		sqlNullableString(job.LastRunAt), sqlNullableString(job.LastStatus), sqlNullableString(job.LastError), sqlNullableString(job.NextRunAt), job.CreatedAt, job.UpdatedAt, job.PostRestoreCommand,
		agent.EnvironmentID, agent.Kind,
	)
	return err
}

func (s *Service) updateJobRow(ctx context.Context, job BackupJob) error {
	enabled := 0
	if job.ScheduleEnabled {
		enabled = 1
	}
	agent := restoreAgentValues(job.PostRestoreRestartAgent)
	_, err := s.store.DB().ExecContext(ctx, `UPDATE backup_jobs SET
		name = ?, connection_id = ?, source_path = ?, destination_dir = ?,
		schedule_enabled = ?, schedule_mode = ?, interval_minutes = ?, daily_time = ?, weekday = ?, retention_count = ?,
		next_run_at = ?, updated_at = ?, post_restore_command = ?,
		post_restore_agent_environment = ?, post_restore_agent_kind = ?
	WHERE id = ?`,
		job.Name, job.ConnectionID, job.SourcePath, job.DestinationDir,
		enabled, job.ScheduleMode, job.IntervalMinutes, job.DailyTime, job.Weekday, job.RetentionCount,
		sqlNullableString(job.NextRunAt), job.UpdatedAt, job.PostRestoreCommand, agent.EnvironmentID, agent.Kind, job.ID,
	)
	return err
}

func restoreAgentValues(target *RestoreAgentTarget) RestoreAgentTarget {
	if target == nil {
		return RestoreAgentTarget{}
	}
	return *target
}

func (s *Service) updateJobRunStatus(ctx context.Context, jobID, lastRunAt, lastStatus string, lastError *string, nextRunAt *string, updatedAt string) error {
	_, err := s.store.DB().ExecContext(ctx, `UPDATE backup_jobs SET
		last_run_at = ?, last_status = ?, last_error = ?, next_run_at = ?, updated_at = ?
	WHERE id = ?`,
		lastRunAt, lastStatus, sqlNullableString(lastError), sqlNullableString(nextRunAt), updatedAt, jobID,
	)
	return err
}

func (s *Service) updateJobNextRun(ctx context.Context, jobID string, nextRunAt *string, updatedAt string) error {
	_, err := s.store.DB().ExecContext(ctx, `UPDATE backup_jobs SET next_run_at = ?, updated_at = ? WHERE id = ?`, sqlNullableString(nextRunAt), updatedAt, jobID)
	return err
}

func (s *Service) listDueJobRows(ctx context.Context, now time.Time) ([]BackupJob, error) {
	rows, err := s.store.QueryRows(ctx, `SELECT * FROM backup_jobs WHERE schedule_enabled = 1 AND schedule_mode != 'manual' AND next_run_at IS NOT NULL ORDER BY next_run_at ASC`)
	if err != nil {
		return nil, err
	}
	nowUTC := now.UTC()
	jobs := make([]BackupJob, 0, len(rows))
	for _, row := range rows {
		job := jobFromRow(row)
		if job.NextRunAt == nil {
			continue
		}
		due, parseErr := time.Parse(time.RFC3339Nano, *job.NextRunAt)
		if parseErr != nil {
			continue
		}
		if !due.After(nowUTC) {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (s *Service) nextVersionNumber(ctx context.Context, jobID string) (int, error) {
	var maxNum sql.NullInt64
	err := s.store.DB().QueryRowContext(ctx, `SELECT COALESCE(MAX(version_number), 0) FROM backup_versions WHERE job_id = ?`, jobID).Scan(&maxNum)
	if err != nil {
		return 0, err
	}
	return int(maxNum.Int64) + 1, nil
}

func (s *Service) insertVersionRow(ctx context.Context, v BackupVersion) error {
	return insertVersion(ctx, s.store.DB(), v)
}

func insertVersion(ctx context.Context, db rowExecutor, v BackupVersion) error {
	_, err := db.ExecContext(ctx, `INSERT INTO backup_versions (
		id, job_id, version_number, version_label, trigger_kind, note, status,
		error_detail, local_path, source_kind, file_count, total_bytes, manifest_sha256,
		created_at, finished_at, connection_id, source_path
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.JobID, v.VersionNumber, v.VersionLabel, v.TriggerKind, v.Note, v.Status,
		sqlNullableString(v.ErrorDetail), v.LocalPath, v.SourceKind, v.FileCount, v.TotalBytes, sqlNullableString(v.ManifestSHA256),
		v.CreatedAt, sqlNullableString(v.FinishedAt), v.ConnectionID, v.SourcePath,
	)
	return err
}

func (s *Service) insertClonedJob(ctx context.Context, job BackupJob, versions []BackupVersion) error {
	tx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertJob(ctx, tx, job); err != nil {
		return err
	}
	for _, version := range versions {
		if err := insertVersion(ctx, tx, version); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) updateVersionResult(ctx context.Context, v BackupVersion) error {
	_, err := s.store.DB().ExecContext(ctx, `UPDATE backup_versions SET
		status = ?, error_detail = ?, source_kind = ?, file_count = ?, total_bytes = ?, manifest_sha256 = ?, finished_at = ?
	WHERE id = ?`,
		v.Status, sqlNullableString(v.ErrorDetail), v.SourceKind, v.FileCount, v.TotalBytes, sqlNullableString(v.ManifestSHA256), sqlNullableString(v.FinishedAt), v.ID,
	)
	return err
}

func (s *Service) getVersionRow(ctx context.Context, versionID string) (BackupVersion, bool, error) {
	row, err := s.store.QueryRow(ctx, `SELECT * FROM backup_versions WHERE id = ?`, versionID)
	if err != nil {
		return BackupVersion{}, false, err
	}
	if row == nil {
		return BackupVersion{}, false, nil
	}
	return versionFromRow(row), true, nil
}

func (s *Service) listVersionRows(ctx context.Context, jobID string) ([]BackupVersion, error) {
	rows, err := s.store.QueryRows(ctx, `SELECT * FROM backup_versions WHERE job_id = ? ORDER BY version_number DESC`, jobID)
	if err != nil {
		return nil, err
	}
	versions := make([]BackupVersion, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, versionFromRow(row))
	}
	return versions, nil
}

func (s *Service) versionSummaryByJob(ctx context.Context) (counts map[string]int, latest map[string]*BackupVersion, err error) {
	counts = make(map[string]int)
	latest = make(map[string]*BackupVersion)
	rows, err := s.store.QueryRows(ctx, `SELECT * FROM backup_versions ORDER BY job_id ASC, version_number DESC`)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		v := versionFromRow(row)
		counts[v.JobID]++
		if _, exists := latest[v.JobID]; !exists {
			copyV := v
			latest[v.JobID] = &copyV
		}
	}
	return counts, latest, nil
}

func (s *Service) updateVersionNoteRow(ctx context.Context, versionID, note string) error {
	res, err := s.store.DB().ExecContext(ctx, `UPDATE backup_versions SET note = ? WHERE id = ?`, note, versionID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return errors.New("version not found")
	}
	return nil
}

func (s *Service) deleteVersionRow(ctx context.Context, versionID string) error {
	_, err := s.store.DB().ExecContext(ctx, `DELETE FROM backup_versions WHERE id = ?`, versionID)
	return err
}

func (s *Service) deleteJobAndVersionsRows(ctx context.Context, jobID string) error {
	tx, err := s.store.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM backup_versions WHERE job_id = ?`, jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM backup_jobs WHERE id = ?`, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) listPrunableVersions(ctx context.Context, jobID string, keepCount int) ([]BackupVersion, error) {
	if keepCount <= 0 {
		return nil, nil
	}
	rows, err := s.store.QueryRows(ctx, `SELECT * FROM backup_versions WHERE job_id = ? AND status = ? ORDER BY version_number DESC`, jobID, StatusSuccess)
	if err != nil {
		return nil, err
	}
	if len(rows) <= keepCount {
		return nil, nil
	}
	prunable := make([]BackupVersion, 0, len(rows)-keepCount)
	for _, row := range rows[keepCount:] {
		prunable = append(prunable, versionFromRow(row))
	}
	return prunable, nil
}

func (s *Service) markInterruptedRunsFailed(ctx context.Context) error {
	nowStr := formatTS(s.now())
	errText := "备份进程在完成前被中断，请重新执行。"
	_, err := s.store.DB().ExecContext(ctx, `UPDATE backup_versions SET status = ?, error_detail = COALESCE(error_detail, ?), finished_at = ? WHERE status = ?`,
		StatusFailed, errText, nowStr, StatusRunning,
	)
	if err != nil {
		return err
	}
	_, err = s.store.DB().ExecContext(ctx, `UPDATE backup_jobs SET last_status = ?, last_error = ?, updated_at = ? WHERE last_status = ?`,
		StatusFailed, errText, nowStr, StatusRunning,
	)
	return err
}
