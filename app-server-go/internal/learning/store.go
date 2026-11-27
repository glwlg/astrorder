package learning

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"astrorder.dev/app-server/internal/store"
)

var leitnerIntervals = []int{1, 3, 7, 21}

type topicsPayload struct {
	Version int             `json:"version"`
	Items   []LearningTopic `json:"items"`
}

type runsPayload struct {
	Version int       `json:"version"`
	Items   []ScanRun `json:"items"`
}

// Repository persists Learning Radar config, topics, and scan runs in workspace_preferences.
type Repository struct {
	store    *store.Store
	now      func() time.Time
	location *time.Location
	mu       sync.Mutex
}

func NewRepository(st *store.Store, nowFn func() time.Time, loc *time.Location) *Repository {
	if nowFn == nil {
		nowFn = time.Now
	}
	if loc == nil {
		loc = time.Local
	}
	return &Repository{
		store:    st,
		now:      nowFn,
		location: loc,
	}
}

// DefaultConfig returns sensible defaults (Weekly Sunday 20:00 local, 7-day lookback, 48h catchup).
func DefaultConfig(now time.Time, loc *time.Location) ScheduleConfig {
	cfg := ScheduleConfig{
		Enabled:             true,
		Weekday:             0, // Sunday
		Time:                "20:00",
		Timezone:            "Local",
		LookbackDays:        DefaultLookbackDays,
		MisfireCatchupHours: DefaultMisfireCatchupHrs,
		NotifyDynamicIsland: true,
		MinActivitySessions: 1,
		MinActivityCommits:  2,
		ExcludedWorkspaces:  []string{},
		SignalSources: SignalSourcesConfig{
			Sessions: true,
			Git:      true,
			Failures: true,
			Meetings: true,
		},
	}
	if next := NextWeeklyRunTime(cfg, now, loc); next != nil {
		cfg.NextRunAt = next.UTC().Format(time.RFC3339)
	}
	return cfg
}

func (r *Repository) resolveLocation(tz string) *time.Location {
	trimmed := strings.TrimSpace(tz)
	if trimmed == "" || strings.EqualFold(trimmed, "local") {
		if r != nil && r.location != nil {
			return r.location
		}
		return time.Local
	}
	if loaded, err := time.LoadLocation(trimmed); err == nil {
		return loaded
	}
	if r != nil && r.location != nil {
		return r.location
	}
	return time.Local
}

func parseHHMM(raw string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%w: 执行时间格式必须为 HH:MM（例如 20:00）", ErrInvalid)
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("%w: 执行时间必须在 00:00 到 23:59 之间", ErrInvalid)
	}
	return hour, minute, nil
}

// NextWeeklyRunTime computes the next scheduled UTC run time strictly after now in loc.
func NextWeeklyRunTime(cfg ScheduleConfig, now time.Time, loc *time.Location) *time.Time {
	if !cfg.Enabled {
		return nil
	}
	if loc == nil {
		loc = time.Local
	}
	hour, minute, err := parseHHMM(cfg.Time)
	if err != nil {
		hour, minute = 20, 0
	}
	wd := cfg.Weekday
	if wd < 0 || wd > 6 {
		wd = 0
	}
	localNow := now.In(loc)
	daysAhead := (wd - int(localNow.Weekday()) + 7) % 7
	candidate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, loc).AddDate(0, 0, daysAhead)
	if !candidate.After(localNow) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	nextUTC := candidate.UTC()
	return &nextUTC
}

// ValidateAndNormalizeConfig cleans user-supplied config and computes the next run time.
func ValidateAndNormalizeConfig(in ScheduleConfig, now time.Time, loc *time.Location) (ScheduleConfig, error) {
	out := in
	if out.Weekday < 0 || out.Weekday > 6 {
		return ScheduleConfig{}, fmt.Errorf("%w: 星期设置必须在 0（周日）到 6（周六）之间", ErrInvalid)
	}
	if strings.TrimSpace(out.Time) == "" {
		out.Time = "20:00"
	}
	hour, minute, err := parseHHMM(out.Time)
	if err != nil {
		return ScheduleConfig{}, err
	}
	out.Time = fmt.Sprintf("%02d:%02d", hour, minute)

	if strings.TrimSpace(out.Timezone) == "" {
		out.Timezone = "Local"
	}
	if out.LookbackDays <= 0 {
		out.LookbackDays = DefaultLookbackDays
	}
	if out.LookbackDays != 7 && out.LookbackDays != 14 && out.LookbackDays != 30 {
		if out.LookbackDays < 1 || out.LookbackDays > 90 {
			return ScheduleConfig{}, fmt.Errorf("%w: 回溯天数需在 1 到 90 天之间", ErrInvalid)
		}
	}
	if out.MisfireCatchupHours < 0 {
		out.MisfireCatchupHours = 0
	}
	if out.MisfireCatchupHours > 168 {
		out.MisfireCatchupHours = 168
	}
	if out.MinActivitySessions < 0 {
		out.MinActivitySessions = 0
	}
	if out.MinActivityCommits < 0 {
		out.MinActivityCommits = 0
	}
	out.MentorBotID = strings.TrimSpace(out.MentorBotID)
	cleanedExcluded := make([]string, 0, len(out.ExcludedWorkspaces))
	seenEx := make(map[string]bool)
	for _, raw := range out.ExcludedWorkspaces {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if !seenEx[key] {
			seenEx[key] = true
			cleanedExcluded = append(cleanedExcluded, trimmed)
		}
	}
	out.ExcludedWorkspaces = cleanedExcluded

	if !out.SignalSources.Sessions && !out.SignalSources.Git && !out.SignalSources.Failures && !out.SignalSources.Meetings {
		out.SignalSources.Sessions = true
	}

	if out.Enabled {
		if next := NextWeeklyRunTime(out, now, loc); next != nil {
			out.NextRunAt = next.UTC().Format(time.RFC3339)
		}
	} else {
		out.NextRunAt = ""
	}
	out.UpdatedAt = now.UTC().Format(time.RFC3339)
	return out, nil
}

func (r *Repository) GetConfig(ctx context.Context) (ScheduleConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadConfigLocked(ctx)
}

func (r *Repository) SaveConfig(ctx context.Context, req ScheduleConfig) (ScheduleConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, _ := r.loadConfigLocked(ctx)
	if strings.TrimSpace(req.LastRunAt) == "" {
		req.LastRunAt = existing.LastRunAt
	}
	loc := r.resolveLocation(req.Timezone)
	normalized, err := ValidateAndNormalizeConfig(req, r.now(), loc)
	if err != nil {
		return ScheduleConfig{}, err
	}
	if err := r.saveConfigLocked(ctx, normalized); err != nil {
		return ScheduleConfig{}, err
	}
	return normalized, nil
}

// AdvanceScheduleAfterTrigger updates LastRunAt and computes the next weekly NextRunAt.
func (r *Repository) AdvanceScheduleAfterTrigger(ctx context.Context, ranAt time.Time) (ScheduleConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg, err := r.loadConfigLocked(ctx)
	if err != nil {
		return ScheduleConfig{}, err
	}
	cfg.LastRunAt = ranAt.UTC().Format(time.RFC3339)
	if cfg.Enabled {
		loc := r.resolveLocation(cfg.Timezone)
		if next := NextWeeklyRunTime(cfg, ranAt.Add(time.Second), loc); next != nil {
			cfg.NextRunAt = next.UTC().Format(time.RFC3339)
		}
	} else {
		cfg.NextRunAt = ""
	}
	cfg.UpdatedAt = ranAt.UTC().Format(time.RFC3339)
	if err := r.saveConfigLocked(ctx, cfg); err != nil {
		return ScheduleConfig{}, err
	}
	return cfg, nil
}

func (r *Repository) loadConfigLocked(ctx context.Context) (ScheduleConfig, error) {
	def := DefaultConfig(r.now(), r.location)
	if r.store == nil {
		return def, nil
	}
	pref, err := r.store.GetPreference(ctx, PrefConfigKey)
	if err != nil {
		return def, err
	}
	if !pref.Exists || pref.Value == nil {
		return def, nil
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return def, nil
	}
	cfg := def
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return def, nil
	}
	if cfg.ExcludedWorkspaces == nil {
		cfg.ExcludedWorkspaces = []string{}
	}
	if cfg.Enabled && strings.TrimSpace(cfg.NextRunAt) == "" {
		loc := r.resolveLocation(cfg.Timezone)
		if next := NextWeeklyRunTime(cfg, r.now(), loc); next != nil {
			cfg.NextRunAt = next.UTC().Format(time.RFC3339)
		}
	}
	return cfg, nil
}

func (r *Repository) saveConfigLocked(ctx context.Context, cfg ScheduleConfig) error {
	if r.store == nil {
		return fmt.Errorf("store is not configured")
	}
	return r.store.SetPreference(ctx, PrefConfigKey, cfg, true)
}

func (r *Repository) ListTopics(ctx context.Context) ([]LearningTopic, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadTopicsLocked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LearningTopic, len(items))
	copy(out, items)
	return out, nil
}

func (r *Repository) GetTopic(ctx context.Context, id string) (*LearningTopic, error) {
	target := strings.TrimSpace(id)
	if target == "" {
		return nil, ErrNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadTopicsLocked(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].ID == target {
			cp := items[i]
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("%w: 未找到学习主题 %q", ErrNotFound, target)
}

func (r *Repository) IngestTopics(ctx context.Context, runID string, inputs []IngestTopicInput) (IngestResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadTopicsLocked(ctx)
	if err != nil {
		return IngestResponse{}, err
	}
	now := r.now().UTC()
	nowISO := now.Format(time.RFC3339)

	byFP := make(map[string]int, len(items))
	for i := range items {
		fp := items[i].Fingerprint
		if fp == "" {
			fp = NormalizeFingerprint(items[i].ProjectKey, items[i].Title)
			items[i].Fingerprint = fp
		}
		byFP[fp] = i
	}

	resp := IngestResponse{
		OK:     true,
		RunID:  runID,
		Topics: make([]LearningTopic, 0, len(inputs)),
	}

	for _, in := range inputs {
		title := strings.TrimSpace(in.Title)
		if title == "" {
			resp.SkippedCount++
			continue
		}
		cat := normalizeCategory(in.Category)
		prio := normalizePriority(in.Priority)
		est := in.EstimatedMinutes
		if est <= 0 {
			est = 10
		}
		if est > 90 {
			est = 90
		}
		projKey := strings.ToLower(strings.TrimSpace(in.ProjectKey))
		if projKey == "" && strings.TrimSpace(in.Workspace) != "" {
			projKey = strings.ToLower(strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(in.Workspace), "\\", "/"), "/"))
		}
		if projKey == "" && strings.TrimSpace(in.ProjectName) != "" {
			projKey = "name:" + strings.ToLower(strings.TrimSpace(in.ProjectName))
		}
		fp := NormalizeFingerprint(projKey, title)

		if idx, exists := byFP[fp]; exists {
			cur := items[idx]
			if cur.Status == "mastered" || cur.Status == "dismissed" {
				resp.SkippedCount++
				continue
			}
			if cur.Status == "snoozed" && cur.SnoozeUntil != "" {
				if until, parseErr := time.Parse(time.RFC3339, cur.SnoozeUntil); parseErr == nil && until.After(now) {
					resp.SkippedCount++
					continue
				}
				cur.Status = "open"
				cur.SnoozeUntil = ""
			}
			if strings.TrimSpace(in.WhyNow) != "" {
				cur.WhyNow = strings.TrimSpace(in.WhyNow)
			}
			if len(in.KeyPoints) > 0 {
				cur.KeyPoints = cleanStringList(in.KeyPoints, 8)
			}
			if len(in.QuizQuestions) > 0 {
				cur.QuizQuestions = cleanQuizList(in.QuizQuestions, 5)
			}
			cur.Evidence = mergeEvidence(cur.Evidence, in.Evidence, 12)
			if strings.TrimSpace(in.NotesFilePath) != "" {
				cur.NotesFilePath = strings.TrimSpace(in.NotesFilePath)
			}
			if strings.TrimSpace(in.Workspace) != "" && cur.Workspace == "" {
				cur.Workspace = strings.TrimSpace(in.Workspace)
			}
			if strings.TrimSpace(in.ProjectName) != "" && cur.ProjectName == "" {
				cur.ProjectName = strings.TrimSpace(in.ProjectName)
			}
			if cur.OccurrenceCount <= 0 {
				cur.OccurrenceCount = 1
			}
			cur.OccurrenceCount++
			cur.Priority = higherPriority(cur.Priority, prio)
			if runID != "" {
				cur.LastScanRunID = runID
			}
			cur.UpdatedAt = nowISO
			items[idx] = cur
			resp.MergedTopicsCount++
			resp.Topics = append(resp.Topics, cur)
			continue
		}

		topic := LearningTopic{
			ID:               "lt-" + randomHex(6),
			Fingerprint:      fp,
			Title:            title,
			Category:         cat,
			Priority:         prio,
			Status:           "open",
			EstimatedMinutes: est,
			ProjectKey:       projKey,
			ProjectName:      strings.TrimSpace(in.ProjectName),
			Workspace:        strings.TrimSpace(in.Workspace),
			ConnectionID:     strings.TrimSpace(in.ConnectionID),
			WhyNow:           strings.TrimSpace(in.WhyNow),
			KeyPoints:        cleanStringList(in.KeyPoints, 8),
			QuizQuestions:    cleanQuizList(in.QuizQuestions, 5),
			Evidence:         mergeEvidence(nil, in.Evidence, 12),
			NotesFilePath:    strings.TrimSpace(in.NotesFilePath),
			OccurrenceCount:  1,
			LastScanRunID:    runID,
			CreatedAt:        nowISO,
			UpdatedAt:        nowISO,
		}
		items = append([]LearningTopic{topic}, items...)
		byFP[fp] = 0
		for k := range byFP {
			if k != fp {
				byFP[k]++
			}
		}
		resp.NewTopicsCount++
		resp.Topics = append(resp.Topics, topic)
	}

	if len(items) > maxStoredTopics {
		items = items[:maxStoredTopics]
	}
	if err := r.saveTopicsLocked(ctx, items); err != nil {
		return IngestResponse{}, err
	}
	return resp, nil
}

func (r *Repository) UpdateTopic(ctx context.Context, id string, req UpdateTopicRequest) (*LearningTopic, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadTopicsLocked(ctx)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range items {
		if items[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%w: 未找到学习主题 %q", ErrNotFound, id)
	}
	now := r.now().UTC()
	nowISO := now.Format(time.RFC3339)
	cur := items[idx]

	if req.Status != nil {
		nextStatus := strings.ToLower(strings.TrimSpace(*req.Status))
		switch nextStatus {
		case "open", "learning", "review_due", "mastered", "snoozed", "dismissed":
			cur.Status = nextStatus
		default:
			return nil, fmt.Errorf("%w: 不支持的主题状态 %q", ErrInvalid, nextStatus)
		}
		if nextStatus == "mastered" {
			cur.MasteredAt = nowISO
			cur.Review.NextReviewAt = ""
			cur.SnoozeUntil = ""
		} else if nextStatus == "snoozed" {
			days := 14
			if req.SnoozeDays != nil && *req.SnoozeDays > 0 && *req.SnoozeDays <= 180 {
				days = *req.SnoozeDays
			}
			cur.SnoozeUntil = now.AddDate(0, 0, days).Format(time.RFC3339)
		} else {
			cur.SnoozeUntil = ""
			if nextStatus == "open" || nextStatus == "learning" || nextStatus == "review_due" {
				cur.MasteredAt = ""
			}
		}
	}
	if req.StudyNotes != nil {
		cur.StudyNotes = strings.TrimSpace(*req.StudyNotes)
	}
	if req.NotesFilePath != nil {
		cur.NotesFilePath = strings.TrimSpace(*req.NotesFilePath)
	}
	cur.UpdatedAt = nowISO
	items[idx] = cur
	if err := r.saveTopicsLocked(ctx, items); err != nil {
		return nil, err
	}
	return &cur, nil
}

func (r *Repository) AttachStudySession(ctx context.Context, id, botID, sessionKey string, enrollReview bool, scheduleID string) (*LearningTopic, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadTopicsLocked(ctx)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i := range items {
		if items[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("%w: 未找到学习主题 %q", ErrNotFound, id)
	}
	now := r.now().UTC()
	cur := items[idx]
	cur.StudyBotID = strings.TrimSpace(botID)
	cur.StudySessionKey = strings.TrimSpace(sessionKey)
	if cur.Status == "open" || cur.Status == "snoozed" {
		cur.Status = "learning"
	}
	if enrollReview && cur.Review.Stage == 0 {
		cur.Review.Stage = 1
		cur.Review.IntervalDays = leitnerIntervals[0]
		cur.Review.NextReviewAt = now.AddDate(0, 0, leitnerIntervals[0]).Format(time.RFC3339)
	}
	if scheduleID != "" {
		cur.Review.SyncSessionSchedule = true
		cur.Review.ScheduleID = scheduleID
	}
	cur.UpdatedAt = now.Format(time.RFC3339)
	items[idx] = cur
	if err := r.saveTopicsLocked(ctx, items); err != nil {
		return nil, err
	}
	return &cur, nil
}

func (r *Repository) SubmitReviewFeedback(ctx context.Context, id string, req ReviewFeedbackRequest) (*LearningTopic, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadTopicsLocked(ctx)
	if err != nil {
		return nil, "", err
	}
	idx := -1
	for i := range items {
		if items[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, "", fmt.Errorf("%w: 未找到学习主题 %q", ErrNotFound, id)
	}
	now := r.now().UTC()
	nowISO := now.Format(time.RFC3339)
	cur := items[idx]
	if strings.TrimSpace(req.Notes) != "" {
		cur.StudyNotes = strings.TrimSpace(req.Notes)
	}
	if req.SyncSessionSchedule != nil {
		cur.Review.SyncSessionSchedule = *req.SyncSessionSchedule
	}

	outcome := strings.ToLower(strings.TrimSpace(req.Outcome))
	var message string
	switch outcome {
	case "enroll":
		cur.Status = "learning"
		cur.MasteredAt = ""
		cur.SnoozeUntil = ""
		cur.Review.Stage = 1
		cur.Review.IntervalDays = leitnerIntervals[0]
		cur.Review.NextReviewAt = now.AddDate(0, 0, leitnerIntervals[0]).Format(time.RFC3339)
		message = "已加入间隔复习队列（第 1 轮，1 天后抽查）"
	case "fuzzy":
		cur.Status = "learning"
		cur.MasteredAt = ""
		cur.Review.Stage = 1
		cur.Review.IntervalDays = leitnerIntervals[0]
		cur.Review.FuzzyCount++
		cur.Review.LastReviewedAt = nowISO
		cur.Review.NextReviewAt = now.AddDate(0, 0, leitnerIntervals[0]).Format(time.RFC3339)
		message = "已重置为第 1 轮复习，明天再次抽查"
	case "remembered":
		cur.Review.PassCount++
		cur.Review.LastReviewedAt = nowISO
		nextStage := cur.Review.Stage + 1
		if nextStage <= 0 {
			nextStage = 1
		}
		if nextStage > len(leitnerIntervals) {
			cur.Status = "mastered"
			cur.MasteredAt = nowISO
			cur.Review.Stage = len(leitnerIntervals)
			cur.Review.IntervalDays = 0
			cur.Review.NextReviewAt = ""
			message = "已完成 4 轮间隔复习，自动归档至「已掌握档案」"
		} else {
			days := leitnerIntervals[nextStage-1]
			cur.Status = "learning"
			cur.Review.Stage = nextStage
			cur.Review.IntervalDays = days
			cur.Review.NextReviewAt = now.AddDate(0, 0, days).Format(time.RFC3339)
			message = fmt.Sprintf("记住了！进入第 %d 轮间隔复习（%d 天后抽查）", nextStage, days)
		}
	case "mastered":
		cur.Status = "mastered"
		cur.MasteredAt = nowISO
		cur.Review.LastReviewedAt = nowISO
		cur.Review.PassCount++
		cur.Review.NextReviewAt = ""
		cur.SnoozeUntil = ""
		message = "已标记彻底掌握并移入档案，后续扫描将自动排除"
	default:
		return nil, "", fmt.Errorf("%w: outcome 必须为 enroll、fuzzy、remembered 或 mastered", ErrInvalid)
	}

	cur.UpdatedAt = nowISO
	items[idx] = cur
	if err := r.saveTopicsLocked(ctx, items); err != nil {
		return nil, "", err
	}
	return &cur, message, nil
}

func (r *Repository) DeleteTopic(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadTopicsLocked(ctx)
	if err != nil {
		return err
	}
	next := make([]LearningTopic, 0, len(items))
	found := false
	for _, it := range items {
		if it.ID == id {
			found = true
			continue
		}
		next = append(next, it)
	}
	if !found {
		return fmt.Errorf("%w: 未找到学习主题 %q", ErrNotFound, id)
	}
	return r.saveTopicsLocked(ctx, next)
}

// RefreshDueReviews promotes due learning topics to review_due and wakes expired snoozes.
func (r *Repository) RefreshDueReviews(ctx context.Context) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items, err := r.loadTopicsLocked(ctx)
	if err != nil {
		return 0, err
	}
	now := r.now().UTC()
	changed := 0
	for i := range items {
		cur := items[i]
		if cur.Status == "learning" && cur.Review.NextReviewAt != "" {
			if due, parseErr := time.Parse(time.RFC3339, cur.Review.NextReviewAt); parseErr == nil && !due.After(now) {
				items[i].Status = "review_due"
				items[i].UpdatedAt = now.Format(time.RFC3339)
				changed++
			}
		} else if cur.Status == "snoozed" && cur.SnoozeUntil != "" {
			if until, parseErr := time.Parse(time.RFC3339, cur.SnoozeUntil); parseErr == nil && !until.After(now) {
				items[i].Status = "open"
				items[i].SnoozeUntil = ""
				items[i].UpdatedAt = now.Format(time.RFC3339)
				changed++
			}
		}
	}
	if changed > 0 {
		if err := r.saveTopicsLocked(ctx, items); err != nil {
			return 0, err
		}
	}
	return changed, nil
}

func (r *Repository) ListRuns(ctx context.Context) ([]ScanRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	runs, err := r.loadRunsLocked(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ScanRun, len(runs))
	copy(out, runs)
	return out, nil
}

func (r *Repository) SaveRun(ctx context.Context, run ScanRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	runs, err := r.loadRunsLocked(ctx)
	if err != nil {
		return err
	}
	updated := false
	for i := range runs {
		if runs[i].ID == run.ID {
			runs[i] = run
			updated = true
			break
		}
	}
	if !updated {
		runs = append([]ScanRun{run}, runs...)
	}
	sort.SliceStable(runs, func(i, j int) bool {
		return runs[i].StartedAt > runs[j].StartedAt
	})
	if len(runs) > maxStoredRuns {
		runs = runs[:maxStoredRuns]
	}
	return r.saveRunsLocked(ctx, runs)
}

func (r *Repository) CompleteRun(ctx context.Context, runID string, newCount, mergedCount, skippedCount int, summary string) (*ScanRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	runs, err := r.loadRunsLocked(ctx)
	if err != nil {
		return nil, err
	}
	now := r.now().UTC()
	nowISO := now.Format(time.RFC3339)

	idx := -1
	if strings.TrimSpace(runID) != "" {
		for i := range runs {
			if runs[i].ID == runID {
				idx = i
				break
			}
		}
	} else {
		for i := range runs {
			if runs[i].Status == "running" {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return nil, nil
	}
	run := runs[idx]
	run.Status = "completed"
	run.FinishedAt = nowISO
	if started, parseErr := time.Parse(time.RFC3339, run.StartedAt); parseErr == nil {
		dur := now.Sub(started).Milliseconds()
		if dur >= 0 {
			run.DurationMs = dur
		}
	}
	run.NewTopicsCount = newCount
	run.MergedTopicsCount = mergedCount
	run.SkippedCount = skippedCount
	if strings.TrimSpace(summary) != "" {
		run.Summary = strings.TrimSpace(summary)
	}
	runs[idx] = run
	if err := r.saveRunsLocked(ctx, runs); err != nil {
		return nil, err
	}
	return &run, nil
}

// ExpireStaleRuns marks running scan sessions that never ingested within timeout as timed out.
func (r *Repository) ExpireStaleRuns(ctx context.Context, timeout time.Duration) (int, error) {
	if timeout <= 0 {
		timeout = DefaultScanTimeout
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	runs, err := r.loadRunsLocked(ctx)
	if err != nil {
		return 0, err
	}
	now := r.now().UTC()
	expired := 0
	for i := range runs {
		if runs[i].Status != "running" {
			continue
		}
		started, parseErr := time.Parse(time.RFC3339, runs[i].StartedAt)
		if parseErr != nil {
			continue
		}
		if now.Sub(started) > timeout {
			runs[i].Status = "timeout"
			runs[i].FinishedAt = now.Format(time.RFC3339)
			runs[i].DurationMs = now.Sub(started).Milliseconds()
			runs[i].Error = "导师 Bot 会话超时未回写扫描结果，可点击「查看 Bot 会话」排查"
			expired++
		}
	}
	if expired > 0 {
		if err := r.saveRunsLocked(ctx, runs); err != nil {
			return 0, err
		}
	}
	return expired, nil
}

func (r *Repository) HasRunningRun(ctx context.Context) (*ScanRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	runs, err := r.loadRunsLocked(ctx)
	if err != nil {
		return nil, err
	}
	for i := range runs {
		if runs[i].Status == "running" {
			cp := runs[i]
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *Repository) loadTopicsLocked(ctx context.Context) ([]LearningTopic, error) {
	if r.store == nil {
		return []LearningTopic{}, nil
	}
	pref, err := r.store.GetPreference(ctx, PrefTopicsKey)
	if err != nil {
		return nil, err
	}
	if !pref.Exists || pref.Value == nil {
		return []LearningTopic{}, nil
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return []LearningTopic{}, nil
	}
	var payload topicsPayload
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Items != nil {
		return payload.Items, nil
	}
	var direct []LearningTopic
	if err := json.Unmarshal(raw, &direct); err == nil && direct != nil {
		return direct, nil
	}
	return []LearningTopic{}, nil
}

func (r *Repository) saveTopicsLocked(ctx context.Context, items []LearningTopic) error {
	if r.store == nil {
		return fmt.Errorf("store is not configured")
	}
	return r.store.SetPreference(ctx, PrefTopicsKey, topicsPayload{Version: 1, Items: items}, true)
}

func (r *Repository) loadRunsLocked(ctx context.Context) ([]ScanRun, error) {
	if r.store == nil {
		return []ScanRun{}, nil
	}
	pref, err := r.store.GetPreference(ctx, PrefRunsKey)
	if err != nil {
		return nil, err
	}
	if !pref.Exists || pref.Value == nil {
		return []ScanRun{}, nil
	}
	raw, err := json.Marshal(pref.Value)
	if err != nil {
		return []ScanRun{}, nil
	}
	var payload runsPayload
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Items != nil {
		return payload.Items, nil
	}
	var direct []ScanRun
	if err := json.Unmarshal(raw, &direct); err == nil && direct != nil {
		return direct, nil
	}
	return []ScanRun{}, nil
}

func (r *Repository) saveRunsLocked(ctx context.Context, items []ScanRun) error {
	if r.store == nil {
		return fmt.Errorf("store is not configured")
	}
	return r.store.SetPreference(ctx, PrefRunsKey, runsPayload{Version: 1, Items: items}, true)
}

// NormalizeFingerprint builds a stable dedupe key from project + normalized topic title.
func NormalizeFingerprint(projectKey, title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.In(r, unicode.Han) {
			b.WriteRune(r)
		}
	}
	norm := strings.ToLower(strings.TrimSpace(projectKey)) + "::" + b.String()
	sum := sha1.Sum([]byte(norm))
	return hex.EncodeToString(sum[:8])
}

func normalizeCategory(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "blackbox_code", "blackbox", "ai_blackbox":
		return "blackbox_code"
	case "recurring_friction", "friction", "question":
		return "recurring_friction"
	case "failure_pattern", "failure", "error", "ci_failure":
		return "failure_pattern"
	case "new_domain", "domain", "meeting", "vocabulary":
		return "new_domain"
	default:
		return "blackbox_code"
	}
}

func normalizePriority(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "high", "urgent", "p0", "p1":
		return "high"
	case "low", "p3":
		return "low"
	default:
		return "medium"
	}
}

func higherPriority(a, b string) string {
	rank := func(p string) int {
		switch p {
		case "high":
			return 3
		case "medium":
			return 2
		default:
			return 1
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func cleanStringList(items []string, limit int) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]bool)
	for _, raw := range items {
		t := strings.TrimSpace(raw)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func cleanQuizList(items []QuizQuestion, limit int) []QuizQuestion {
	out := make([]QuizQuestion, 0, len(items))
	for _, q := range items {
		qt := strings.TrimSpace(q.Question)
		if qt == "" {
			continue
		}
		out = append(out, QuizQuestion{
			Question: qt,
			Hint:     strings.TrimSpace(q.Hint),
			Answer:   strings.TrimSpace(q.Answer),
		})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func mergeEvidence(existing, incoming []EvidenceItem, limit int) []EvidenceItem {
	out := make([]EvidenceItem, 0, len(existing)+len(incoming))
	seen := make(map[string]bool)
	add := func(ev EvidenceItem) {
		ev.Kind = strings.TrimSpace(ev.Kind)
		ev.Label = strings.TrimSpace(ev.Label)
		if ev.Label == "" {
			return
		}
		if ev.Kind == "" {
			ev.Kind = "file"
		}
		key := strings.ToLower(ev.Kind + "|" + ev.Label + "|" + ev.FilePath + "|" + ev.SessionID + "|" + ev.CommitSHA)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, ev)
	}
	for _, ev := range incoming {
		add(ev)
	}
	for _, ev := range existing {
		add(ev)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
