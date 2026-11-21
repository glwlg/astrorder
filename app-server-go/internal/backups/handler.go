package backups

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"

	"astrorder.dev/app-server/internal/targetfs"
)

const (
	backupsPrefix  = "/api/v1/backups/"
	targetsPath    = "/api/v1/backups/targets"
	browsePath     = "/api/v1/backups/browse"
	jobsPath       = "/api/v1/backups/jobs"
	jobsPrefix     = "/api/v1/backups/jobs/"
	versionsPrefix = "/api/v1/backups/versions/"
	maxJSONBody    = 256 * 1024
)

// Matches reports whether the request should be routed to the backups service.
func Matches(_, p string) bool {
	return strings.HasPrefix(p, backupsPrefix)
}

type jobPayload struct {
	Name                    string          `json:"name"`
	ConnectionID            string          `json:"connection_id"`
	SourcePath              string          `json:"source_path"`
	DestinationDir          string          `json:"destination_dir"`
	ScheduleEnabled         bool            `json:"schedule_enabled"`
	ScheduleMode            string          `json:"schedule_mode"`
	IntervalMinutes         *int            `json:"interval_minutes"`
	DailyTime               string          `json:"daily_time"`
	Weekday                 *int            `json:"weekday"`
	RetentionCount          *int            `json:"retention_count"`
	PostRestoreCommand      *string         `json:"post_restore_command"`
	PostRestoreRestartAgent json.RawMessage `json:"post_restore_restart_agent"`
}

func (s *Service) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if s.authorizer != nil {
		if failure := s.authorizer.AuthorizeBrowser(request); failure != nil {
			writeJSONError(response, failure.Status, failure.Detail)
			return
		}
	}

	p := request.URL.Path
	switch {
	case p == targetsPath:
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		res, err := s.ListTargets(request.Context())
		if err != nil {
			writeServiceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, res)

	case p == browsePath:
		if request.Method != http.MethodGet {
			methodNotAllowed(response, http.MethodGet)
			return
		}
		connID := request.URL.Query().Get("connection_id")
		targetPath := request.URL.Query().Get("path")
		res, err := s.BrowsePath(request.Context(), connID, targetPath)
		if err != nil {
			writeServiceError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, res)

	case p == jobsPath:
		switch request.Method {
		case http.MethodGet:
			jobs, err := s.listEnrichedJobs(request.Context())
			if err != nil {
				writeServiceError(response, err)
				return
			}
			writeJSON(response, http.StatusOK, map[string]any{"items": jobs})
		case http.MethodPost:
			s.handleCreateJob(response, request)
		default:
			methodNotAllowed(response, http.MethodGet, http.MethodPost)
		}

	case strings.HasPrefix(p, jobsPrefix):
		rest := strings.TrimPrefix(p, jobsPrefix)
		if rest == "" {
			writeJSONError(response, http.StatusNotFound, "Not Found")
			return
		}
		if jobID, ok := strings.CutSuffix(rest, "/clone"); ok && jobID != "" && !strings.Contains(jobID, "/") {
			s.handleCloneJob(response, request, jobID)
			return
		}
		if strings.HasSuffix(rest, "/run") {
			jobID := strings.TrimSuffix(rest, "/run")
			if jobID == "" || strings.Contains(jobID, "/") {
				writeJSONError(response, http.StatusNotFound, "Not Found")
				return
			}
			if request.Method != http.MethodPost {
				methodNotAllowed(response, http.MethodPost)
				return
			}
			s.handleRunJob(response, request, jobID)
			return
		}
		if strings.HasSuffix(rest, "/versions") {
			jobID := strings.TrimSuffix(rest, "/versions")
			if jobID == "" || strings.Contains(jobID, "/") {
				writeJSONError(response, http.StatusNotFound, "Not Found")
				return
			}
			if request.Method != http.MethodGet {
				methodNotAllowed(response, http.MethodGet)
				return
			}
			s.handleListVersions(response, request, jobID)
			return
		}
		if strings.Contains(rest, "/") {
			writeJSONError(response, http.StatusNotFound, "Not Found")
			return
		}
		switch request.Method {
		case http.MethodPut:
			s.handleUpdateJob(response, request, rest)
		case http.MethodDelete:
			deleteFiles := parseBoolQuery(request.URL.Query().Get("delete_files"))
			if err := s.DeleteJob(request.Context(), rest, deleteFiles); err != nil {
				writeServiceError(response, err)
				return
			}
			writeJSON(response, http.StatusOK, map[string]any{"deleted": true})
		default:
			methodNotAllowed(response, http.MethodPut, http.MethodDelete)
		}

	case strings.HasPrefix(p, versionsPrefix):
		rest := strings.TrimPrefix(p, versionsPrefix)
		if rest == "" {
			writeJSONError(response, http.StatusNotFound, "Not Found")
			return
		}
		if versionID, ok := strings.CutSuffix(rest, "/restore"); ok && versionID != "" && !strings.Contains(versionID, "/") {
			s.handleRestoreVersion(response, request, versionID)
			return
		}
		if strings.HasSuffix(rest, "/open") {
			versionID := strings.TrimSuffix(rest, "/open")
			if versionID == "" || strings.Contains(versionID, "/") {
				writeJSONError(response, http.StatusNotFound, "Not Found")
				return
			}
			if request.Method != http.MethodPost {
				methodNotAllowed(response, http.MethodPost)
				return
			}
			s.handleOpenVersion(response, request, versionID)
			return
		}
		if strings.Contains(rest, "/") {
			writeJSONError(response, http.StatusNotFound, "Not Found")
			return
		}
		switch request.Method {
		case http.MethodPatch:
			s.handlePatchVersion(response, request, rest)
		case http.MethodDelete:
			deleteFiles := parseBoolQuery(request.URL.Query().Get("delete_files"))
			if err := s.DeleteVersion(request.Context(), rest, deleteFiles); err != nil {
				writeServiceError(response, err)
				return
			}
			writeJSON(response, http.StatusOK, map[string]any{"deleted": true})
		default:
			methodNotAllowed(response, http.MethodPatch, http.MethodDelete)
		}

	default:
		writeJSONError(response, http.StatusNotFound, "Not Found")
	}
}

func (s *Service) listEnrichedJobs(ctx context.Context) ([]BackupJob, error) {
	jobs, err := s.listJobRows(ctx)
	if err != nil {
		return nil, err
	}
	targetsResp, err := s.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	targetMap := make(map[string]Target, len(targetsResp.Items))
	for _, t := range targetsResp.Items {
		targetMap[t.ID] = t
	}
	counts, latestMap, err := s.versionSummaryByJob(ctx)
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		s.enrichJob(&jobs[i], targetMap, counts, latestMap)
	}
	return jobs, nil
}

func (s *Service) enrichJob(job *BackupJob, targetMap map[string]Target, counts map[string]int, latestMap map[string]*BackupVersion) {
	if t, ok := targetMap[job.ConnectionID]; ok {
		job.TargetName = t.Name
		job.TargetKind = t.Kind
		job.TargetState = t.State
	} else {
		job.TargetName = job.ConnectionID
		if strings.HasPrefix(job.ConnectionID, "wsl:") {
			job.TargetKind = "wsl"
		} else if job.ConnectionID == targetfs.LocalConnectionID {
			job.TargetKind = "local"
			job.TargetName = "本机"
		} else {
			job.TargetKind = "ssh"
		}
		job.TargetState = "disconnected"
	}
	job.VersionCount = counts[job.ID]
	job.LatestVersion = latestMap[job.ID]
	job.IsRunning = s.isJobRunning(job.ID) || (job.LastStatus != nil && *job.LastStatus == StatusRunning)
}

func (s *Service) buildValidatedJob(ctx context.Context, existing *BackupJob, payload jobPayload) (BackupJob, error) {
	connID := strings.TrimSpace(payload.ConnectionID)
	if connID == "" {
		connID = targetfs.LocalConnectionID
	}
	if connID != targetfs.LocalConnectionID {
		targetsResp, err := s.ListTargets(ctx)
		if err != nil {
			return BackupJob{}, err
		}
		foundTarget := false
		for _, t := range targetsResp.Items {
			if t.ID == connID {
				foundTarget = true
				break
			}
		}
		if !foundTarget {
			return BackupJob{}, policyError(404, "指定的源服务器连接不存在或未配置。")
		}
	}

	sourcePath, err := ValidateSourcePath(connID, payload.SourcePath)
	if err != nil {
		return BackupJob{}, err
	}
	destDir, err := ValidateDestinationDir(payload.DestinationDir)
	if err != nil {
		return BackupJob{}, err
	}
	if connID == targetfs.LocalConnectionID {
		if err := ValidateLocalPair(sourcePath, destDir); err != nil {
			return BackupJob{}, err
		}
	}

	name := strings.TrimSpace(payload.Name)
	if name == "" {
		slashSrc := strings.ReplaceAll(sourcePath, `\`, "/")
		base := path.Base(slashSrc)
		if base == "" || base == "/" || base == "." {
			base = "备份任务"
		}
		name = base
	}
	if len([]rune(name)) > 120 {
		return BackupJob{}, policyError(422, "任务名称长度不能超过 120 个字符。")
	}

	intervalMins := DefaultIntervalMins
	if payload.IntervalMinutes != nil {
		intervalMins = *payload.IntervalMinutes
	} else if existing != nil && existing.IntervalMinutes > 0 {
		intervalMins = existing.IntervalMinutes
	}

	weekday := 1
	if payload.Weekday != nil {
		weekday = *payload.Weekday
	} else if existing != nil {
		weekday = existing.Weekday
	}

	retention := DefaultRetentionCount
	if payload.RetentionCount != nil {
		retention = *payload.RetentionCount
	} else if existing != nil {
		retention = existing.RetentionCount
	}

	mode, normInterval, normTime, normWeekday, normRetention, err := ValidateSchedule(
		payload.ScheduleEnabled,
		payload.ScheduleMode,
		intervalMins,
		payload.DailyTime,
		weekday,
		retention,
	)
	if err != nil {
		return BackupJob{}, err
	}

	now := s.now().UTC()
	nowStr := formatTS(now)
	var job BackupJob
	if existing != nil {
		job = *existing
	} else {
		job = BackupJob{
			ID:        newID("bkj-"),
			CreatedAt: nowStr,
		}
	}
	job.Name = name
	job.ConnectionID = connID
	job.SourcePath = sourcePath
	job.DestinationDir = destDir
	job.ScheduleEnabled = payload.ScheduleEnabled
	job.ScheduleMode = mode
	job.IntervalMinutes = normInterval
	job.DailyTime = normTime
	job.Weekday = normWeekday
	job.RetentionCount = normRetention
	if payload.PostRestoreCommand != nil {
		command := *payload.PostRestoreCommand
		if len(command) > 8192 || strings.ContainsRune(command, 0) {
			return BackupJob{}, policyError(422, "还原后命令不能超过 8192 字节或包含空字符。")
		}
		if strings.TrimSpace(command) == "" {
			command = ""
		}
		job.PostRestoreCommand = command
		job.PostRestoreRestartAgent = nil
	}
	if payload.PostRestoreRestartAgent != nil {
		var target *RestoreAgentTarget
		if err := json.Unmarshal(payload.PostRestoreRestartAgent, &target); err != nil {
			return BackupJob{}, policyError(422, "请选择需要重启的 Agent。")
		}
		if target != nil {
			if payload.PostRestoreCommand != nil && job.PostRestoreCommand != "" {
				return BackupJob{}, policyError(422, "请选择一种还原后操作。")
			}
			if err := s.validateRestoreAgent(ctx, *target); err != nil {
				return BackupJob{}, err
			}
			job.PostRestoreCommand = ""
		}
		job.PostRestoreRestartAgent = target
	}
	job.NextRunAt = formatTSPtr(NextRunTime(job, now, s.location))
	job.UpdatedAt = nowStr
	return job, nil
}

func (s *Service) handleCreateJob(response http.ResponseWriter, request *http.Request) {
	var payload jobPayload
	if err := decodeJSONBody(response, request, &payload, false); err != nil {
		writeServiceError(response, err)
		return
	}
	job, err := s.buildValidatedJob(request.Context(), nil, payload)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if err := s.insertJobRow(request.Context(), job); err != nil {
		writeServiceError(response, err)
		return
	}
	enriched, err := s.loadEnrichedJob(request.Context(), job.ID)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, enriched)
}

func (s *Service) handleUpdateJob(response http.ResponseWriter, request *http.Request, jobID string) {
	var payload jobPayload
	if err := decodeJSONBody(response, request, &payload, false); err != nil {
		writeServiceError(response, err)
		return
	}
	if !s.acquireJobRun(jobID, "") {
		writeJSONError(response, http.StatusConflict, "该任务正在执行，请稍后再修改。")
		return
	}
	defer s.releaseJobRun(jobID)
	existing, found, err := s.getJobRow(request.Context(), jobID)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if !found {
		writeJSONError(response, http.StatusNotFound, "备份任务不存在。")
		return
	}
	updated, err := s.buildValidatedJob(request.Context(), &existing, payload)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if err := s.updateJobRow(request.Context(), updated); err != nil {
		writeServiceError(response, err)
		return
	}
	enriched, err := s.loadEnrichedJob(request.Context(), updated.ID)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	enriched.IsRunning = false
	writeJSON(response, http.StatusOK, enriched)
}

func (s *Service) loadEnrichedJob(ctx context.Context, jobID string) (BackupJob, error) {
	job, found, err := s.getJobRow(ctx, jobID)
	if err != nil {
		return BackupJob{}, err
	}
	if !found {
		return BackupJob{}, policyError(404, "备份任务不存在。")
	}
	targetsResp, err := s.ListTargets(ctx)
	if err != nil {
		return BackupJob{}, err
	}
	targetMap := make(map[string]Target, len(targetsResp.Items))
	for _, t := range targetsResp.Items {
		targetMap[t.ID] = t
	}
	counts, latestMap, err := s.versionSummaryByJob(ctx)
	if err != nil {
		return BackupJob{}, err
	}
	s.enrichJob(&job, targetMap, counts, latestMap)
	return job, nil
}

func (s *Service) handleRunJob(response http.ResponseWriter, request *http.Request, jobID string) {
	var payload struct {
		Note string `json:"note"`
	}
	if err := decodeJSONBody(response, request, &payload, true); err != nil {
		writeServiceError(response, err)
		return
	}
	version, err := s.triggerBackupAsync(request.Context(), jobID, TriggerManual, payload.Note)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"version": version})
}

func (s *Service) handleListVersions(response http.ResponseWriter, request *http.Request, jobID string) {
	_, found, err := s.getJobRow(request.Context(), jobID)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if !found {
		writeJSONError(response, http.StatusNotFound, "备份任务不存在。")
		return
	}
	versions, err := s.listVersionRows(request.Context(), jobID)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": versions})
}

func (s *Service) handlePatchVersion(response http.ResponseWriter, request *http.Request, versionID string) {
	var payload struct {
		Note string `json:"note"`
	}
	if err := decodeJSONBody(response, request, &payload, false); err != nil {
		writeServiceError(response, err)
		return
	}
	version, err := s.UpdateVersionNote(request.Context(), versionID, payload.Note)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, version)
}

func (s *Service) handleOpenVersion(response http.ResponseWriter, request *http.Request, versionID string) {
	version, found, err := s.getVersionRow(request.Context(), versionID)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if !found {
		writeJSONError(response, http.StatusNotFound, "备份版本不存在。")
		return
	}
	info, statErr := os.Stat(version.LocalPath)
	if statErr != nil || !info.IsDir() {
		writeJSONError(response, http.StatusNotFound, "该备份版本的本机目录已不存在。")
		return
	}
	if s.opener == nil {
		writeJSONError(response, http.StatusServiceUnavailable, "当前环境不支持打开本机文件夹。")
		return
	}
	if err := s.opener.Open(request.Context(), version.LocalPath, "open"); err != nil {
		writeJSONError(response, http.StatusInternalServerError, "打开本机备份目录失败: "+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"opened": true, "path": version.LocalPath})
}

func decodeJSONBody(response http.ResponseWriter, request *http.Request, target any, allowEmpty bool) error {
	if request.Body == nil {
		if allowEmpty {
			return nil
		}
		return policyError(422, "请求体不能为空。")
	}
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maxJSONBody))
	if err != nil {
		return policyError(400, "读取请求体失败。")
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		if allowEmpty {
			return nil
		}
		return policyError(422, "请求体不能为空。")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return policyError(422, "请求 JSON 格式不正确。")
	}
	return nil
}

func parseBoolQuery(raw string) bool {
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	return err == nil && parsed
}

func writeServiceError(response http.ResponseWriter, err error) {
	var valErr *ValidationError
	if errors.As(err, &valErr) {
		writeJSONError(response, valErr.Status, valErr.Detail)
		return
	}
	var fsErr *targetfs.Error
	if errors.As(err, &fsErr) {
		writeJSONError(response, fsErr.Status, fsErr.Detail)
		return
	}
	writeJSONError(response, http.StatusInternalServerError, err.Error())
}

func writeJSONError(response http.ResponseWriter, status int, detail string) {
	writeJSON(response, status, map[string]string{"detail": detail})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func methodNotAllowed(response http.ResponseWriter, methods ...string) {
	response.Header().Set("Allow", strings.Join(methods, ", "))
	writeJSONError(response, http.StatusMethodNotAllowed, "Method Not Allowed")
}
