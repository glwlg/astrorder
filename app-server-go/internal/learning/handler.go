package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"astrorder.dev/app-server/internal/bots"
)

// Service coordinates Learning Radar scheduling, evidence packaging, Mentor Bot dispatch,
// Agent CLI ingest/update, spaced-repetition review state, and HTTP endpoints.
type Service struct {
	options       Options
	repo          *Repository
	logger        *slog.Logger
	now           func() time.Time
	location      *time.Location
	schedulerTick time.Duration
	wakeCh        chan struct{}
	dispatching   atomic.Bool
	wg            sync.WaitGroup
}

func NewService(options Options) *Service {
	logger := options.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	nowFn := options.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	loc := options.Location
	if loc == nil {
		loc = time.Local
	}
	tick := options.SchedulerTick
	if tick <= 0 {
		tick = defaultSchedulerTick
	}
	return &Service{
		options:       options,
		repo:          NewRepository(options.Store, nowFn, loc),
		logger:        logger,
		now:           nowFn,
		location:      loc,
		schedulerTick: tick,
		wakeCh:        make(chan struct{}, 1),
	}
}

// Matches reports whether method + path belong to /api/v1/learning/*.
func Matches(method, reqPath string) bool {
	clean := strings.TrimRight(reqPath, "/")
	if !strings.HasPrefix(clean, BasePath+"/") {
		return false
	}
	rest := strings.TrimPrefix(clean, BasePath+"/")
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		switch parts[0] {
		case "overview":
			return method == http.MethodGet
		case "config":
			return method == http.MethodGet || method == http.MethodPut
		case "scans":
			return method == http.MethodGet || method == http.MethodPost
		case "topics":
			return method == http.MethodGet
		}
	case 2:
		switch parts[0] {
		case "mentor-bot":
			return parts[1] == "bootstrap" && method == http.MethodPost
		case "profile":
			return parts[1] == "export" && method == http.MethodGet
		case "topics":
			return method == http.MethodPatch || method == http.MethodDelete
		}
	case 3:
		if parts[0] == "topics" && (parts[2] == "study" || parts[2] == "review") {
			return method == http.MethodPost
		}
	}
	return false
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.options.Authorizer == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Authentication is not configured")
		return
	}
	if failure := s.options.Authorizer.AuthorizeBrowser(r); failure != nil {
		writeJSONError(w, failure.Status, failure.Detail)
		return
	}

	clean := strings.TrimRight(r.URL.Path, "/")
	rest := strings.TrimPrefix(clean, BasePath+"/")
	parts := strings.Split(rest, "/")

	switch len(parts) {
	case 1:
		switch parts[0] {
		case "overview":
			s.handleOverview(w, r)
			return
		case "config":
			if r.Method == http.MethodGet {
				s.handleGetConfig(w, r)
				return
			}
			if r.Method == http.MethodPut {
				s.handlePutConfig(w, r)
				return
			}
		case "scans":
			if r.Method == http.MethodGet {
				s.handleListScans(w, r)
				return
			}
			if r.Method == http.MethodPost {
				s.handleTriggerScan(w, r)
				return
			}
		case "topics":
			s.handleListTopics(w, r)
			return
		}
	case 2:
		if parts[0] == "mentor-bot" && parts[1] == "bootstrap" && r.Method == http.MethodPost {
			s.handleBootstrapMentorBot(w, r)
			return
		}
		if parts[0] == "profile" && parts[1] == "export" && r.Method == http.MethodGet {
			s.handleExportProfile(w, r)
			return
		}
		if parts[0] == "topics" {
			id, _ := url.PathUnescape(parts[1])
			if r.Method == http.MethodPatch {
				s.handlePatchTopic(w, r, id)
				return
			}
			if r.Method == http.MethodDelete {
				s.handleDeleteTopic(w, r, id)
				return
			}
		}
	case 3:
		if parts[0] == "topics" {
			id, _ := url.PathUnescape(parts[1])
			if parts[2] == "study" && r.Method == http.MethodPost {
				s.handleStartStudy(w, r, id)
				return
			}
			if parts[2] == "review" && r.Method == http.MethodPost {
				s.handleSubmitReview(w, r, id)
				return
			}
		}
	}
	writeJSONError(w, http.StatusNotFound, "Not Found")
}

// GetOverview returns KPI stats, schedule config, bound Mentor Bot, sorted topics, and scan runs.
func (s *Service) GetOverview(ctx context.Context) (*OverviewResponse, error) {
	_, _ = s.repo.RefreshDueReviews(ctx)
	_, _ = s.repo.ExpireStaleRuns(ctx, DefaultScanTimeout)

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	topics, err := s.repo.ListTopics(ctx)
	if err != nil {
		return nil, err
	}
	runs, err := s.repo.ListRuns(ctx)
	if err != nil {
		return nil, err
	}

	sortTopicsForFeed(topics)

	var mentorBot *bots.BotSummary
	if strings.TrimSpace(cfg.MentorBotID) != "" && s.options.Bots != nil {
		if found, bErr := s.options.Bots.GetSummary(ctx, cfg.MentorBotID); bErr == nil && found != nil {
			mentorBot = found
		}
	}

	var activeRun, latestRun *ScanRun
	for i := range runs {
		if latestRun == nil {
			cp := runs[i]
			latestRun = &cp
		}
		if runs[i].Status == "running" && activeRun == nil {
			cp := runs[i]
			activeRun = &cp
		}
	}

	return &OverviewResponse{
		Config:    cfg,
		MentorBot: mentorBot,
		Stats:     computeStats(topics, s.now().UTC()),
		ActiveRun: activeRun,
		LatestRun: latestRun,
		Topics:    topics,
		Runs:      runs,
	}, nil
}

// IngestFromAgent handles `astrorder-agent learning ingest` from the Mentor Bot.
func (s *Service) IngestFromAgent(ctx context.Context, req IngestRequest) (*IngestResponse, error) {
	if len(req.Topics) == 0 && strings.TrimSpace(req.Summary) == "" {
		return nil, fmt.Errorf("%w: topics 不能为空", ErrInvalid)
	}
	resp, err := s.repo.IngestTopics(ctx, strings.TrimSpace(req.RunID), req.Topics)
	if err != nil {
		return nil, err
	}
	completedRun, _ := s.repo.CompleteRun(ctx, strings.TrimSpace(req.RunID), resp.NewTopicsCount, resp.MergedTopicsCount, resp.SkippedCount, req.Summary)
	cfg, _ := s.repo.GetConfig(ctx)
	s.publishEvent(ctx, "learning.scan.completed", map[string]any{
		"run_id":                resp.RunID,
		"run":                   completedRun,
		"new_topics_count":      resp.NewTopicsCount,
		"merged_topics_count":   resp.MergedTopicsCount,
		"notify_dynamic_island": cfg.NotifyDynamicIsland,
	})
	return &resp, nil
}

// StatusForAgent handles `astrorder-agent learning status`.
func (s *Service) StatusForAgent(ctx context.Context, statusFilter string) (map[string]any, error) {
	overview, err := s.GetOverview(ctx)
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(strings.TrimSpace(statusFilter))
	filtered := make([]LearningTopic, 0, len(overview.Topics))
	for _, t := range overview.Topics {
		if want == "" {
			if t.Status == "dismissed" {
				continue
			}
		} else if t.Status != want {
			continue
		}
		filtered = append(filtered, t)
	}
	return map[string]any{
		"mentor_bot_id": overview.Config.MentorBotID,
		"schedule":      overview.Config,
		"stats":         overview.Stats,
		"latest_run":    overview.LatestRun,
		"topics":        filtered,
	}, nil
}

// UpdateFromAgent handles `astrorder-agent learning update`.
func (s *Service) UpdateFromAgent(ctx context.Context, topicID string, req UpdateTopicRequest) (*LearningTopic, error) {
	updated, err := s.repo.UpdateTopic(ctx, topicID, req)
	if err != nil {
		return nil, err
	}
	s.publishEvent(ctx, "learning.topic.updated", map[string]any{"topic": updated})
	return updated, nil
}

func (s *Service) handleOverview(w http.ResponseWriter, r *http.Request) {
	resp, err := s.GetOverview(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.repo.GetConfig(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": cfg})
}

func (s *Service) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	var req ScheduleConfig
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的配置 JSON")
		return
	}
	saved, err := s.repo.SaveConfig(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	s.WakeScheduler()
	writeJSON(w, http.StatusOK, map[string]any{"config": saved})
}

func (s *Service) handleBootstrapMentorBot(w http.ResponseWriter, r *http.Request) {
	var req BootstrapMentorBotRequest
	if r.Body != nil {
		if err := decodeJSONBody(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "无效的初始化参数 JSON")
			return
		}
	}
	resp, err := s.BootstrapMentorBot(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	s.WakeScheduler()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleListScans(w http.ResponseWriter, r *http.Request) {
	_, _ = s.repo.ExpireStaleRuns(r.Context(), DefaultScanTimeout)
	runs, err := s.repo.ListRuns(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": runs})
}

func (s *Service) handleTriggerScan(w http.ResponseWriter, r *http.Request) {
	var req TriggerScanRequest
	if r.Body != nil {
		if err := decodeJSONBody(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "无效的扫描参数 JSON")
			return
		}
	}
	if req.Trigger == "" {
		req.Trigger = "manual"
	}
	resp, err := s.DispatchScan(r.Context(), req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleListTopics(w http.ResponseWriter, r *http.Request) {
	_, _ = s.repo.RefreshDueReviews(r.Context())
	topics, err := s.repo.ListTopics(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	category := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))
	filtered := make([]LearningTopic, 0, len(topics))
	for _, t := range topics {
		if status != "" && status != "all" {
			if status == "active" {
				if t.Status != "open" && t.Status != "learning" && t.Status != "review_due" {
					continue
				}
			} else if t.Status != status {
				continue
			}
		}
		if category != "" && category != "all" && t.Category != category {
			continue
		}
		filtered = append(filtered, t)
	}
	sortTopicsForFeed(filtered)
	writeJSON(w, http.StatusOK, map[string]any{"items": filtered})
}

func (s *Service) handlePatchTopic(w http.ResponseWriter, r *http.Request, id string) {
	var req UpdateTopicRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的更新 JSON")
		return
	}
	updated, err := s.repo.UpdateTopic(r.Context(), id, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	s.publishEvent(r.Context(), "learning.topic.updated", map[string]any{"topic": updated})
	writeJSON(w, http.StatusOK, map[string]any{"topic": updated})
}

func (s *Service) handleDeleteTopic(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.repo.DeleteTopic(r.Context(), id); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": id})
}

func (s *Service) handleStartStudy(w http.ResponseWriter, r *http.Request, id string) {
	var req StartStudyRequest
	if r.Body != nil {
		if err := decodeJSONBody(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, "无效的伴学参数 JSON")
			return
		}
	}
	resp, err := s.DispatchStudy(r.Context(), id, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleSubmitReview(w http.ResponseWriter, r *http.Request, id string) {
	var req ReviewFeedbackRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "无效的复习反馈 JSON")
		return
	}
	updated, msg, err := s.repo.SubmitReviewFeedback(r.Context(), id, req)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	// Optionally sync next due review onto the linked tutor session schedule.
	if updated.Review.SyncSessionSchedule && updated.Review.NextReviewAt != "" && updated.StudySessionKey != "" {
		if aid, sid := splitSessionKey(updated.StudySessionKey); aid != "" && sid != "" {
			if nextAt, parseErr := time.Parse(time.RFC3339, updated.Review.NextReviewAt); parseErr == nil {
				s.syncTopicSessionSchedule(r.Context(), *updated, aid, sid, nextAt)
			}
		}
	}
	s.publishEvent(r.Context(), "learning.topic.updated", map[string]any{"topic": updated})
	writeJSON(w, http.StatusOK, ReviewFeedbackResponse{
		OK:      true,
		Topic:   *updated,
		Message: msg,
	})
}

func (s *Service) handleExportProfile(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.repo.GetConfig(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	topics, err := s.repo.ListTopics(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exported_at": s.now().UTC().Format(time.RFC3339),
		"config":      cfg,
		"topics":      topics,
	})
}

func (s *Service) publishEvent(ctx context.Context, eventType string, data map[string]any) {
	if s.options.Publisher == nil {
		return
	}
	if err := s.options.Publisher.Publish(ctx, eventType, data); err != nil {
		s.logger.Debug("publish learning event failed", "type", eventType, "error", err)
	}
}

func computeStats(topics []LearningTopic, now time.Time) OverviewStats {
	var st OverviewStats
	projects := make(map[string]bool)
	weekAgo := now.AddDate(0, 0, -7)
	for _, t := range topics {
		switch t.Status {
		case "open", "learning", "review_due":
			st.PendingCount++
			if t.Status == "learning" {
				st.LearningCount++
			}
			if t.Status == "review_due" {
				st.DueReviewCount++
			}
			switch t.Category {
			case "blackbox_code":
				st.BlackboxCount++
			case "recurring_friction":
				st.FrictionCount++
			case "failure_pattern":
				st.FailureCount++
			case "new_domain":
				st.NewDomainCount++
			}
			if t.ProjectName != "" {
				projects[t.ProjectName] = true
			}
			if created, err := time.Parse(time.RFC3339, t.CreatedAt); err == nil && !created.Before(weekAgo) {
				st.WeeklyNewCount++
			}
		case "mastered":
			st.MasteredCount++
		case "dismissed":
			st.DismissedCount++
		}
	}
	st.ProjectCount = len(projects)
	return st
}

func sortTopicsForFeed(topics []LearningTopic) {
	statusRank := func(s string) int {
		switch s {
		case "review_due":
			return 0
		case "open":
			return 1
		case "learning":
			return 2
		case "snoozed":
			return 3
		case "mastered":
			return 4
		default:
			return 5
		}
	}
	prioRank := func(p string) int {
		switch p {
		case "high":
			return 0
		case "medium":
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(topics, func(i, j int) bool {
		if sr1, sr2 := statusRank(topics[i].Status), statusRank(topics[j].Status); sr1 != sr2 {
			return sr1 < sr2
		}
		if pr1, pr2 := prioRank(topics[i].Priority), prioRank(topics[j].Priority); pr1 != pr2 {
			return pr1 < pr2
		}
		return topics[i].UpdatedAt > topics[j].UpdatedAt
	})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes))
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]any{"detail": detail})
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrConflict):
		writeJSONError(w, http.StatusConflict, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}
