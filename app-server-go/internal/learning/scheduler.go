package learning

import (
	"context"
	"strings"
	"time"

	"astrorder.dev/app-server/internal/store"
)

const (
	// PluginID is the built-in workspace plugin toggle key controlling Learning Radar.
	PluginID             = "workspace-learning-radar"
	defaultSchedulerTick = 30 * time.Second
)

func (s *Service) isPluginEnabled(ctx context.Context) bool {
	if s == nil || s.options.Store == nil {
		return true
	}
	pref, err := s.options.Store.GetPreference(ctx, store.PrefPluginsEnabled)
	if err != nil || !pref.Exists {
		return true
	}
	if saved, ok := pref.Value.(map[string]any); ok {
		if flag, ok := saved[PluginID].(bool); ok {
			return flag
		}
	}
	return true
}

// WakeScheduler nudges the background loop to re-evaluate weekly due scans immediately.
func (s *Service) WakeScheduler() {
	if s == nil || s.wakeCh == nil {
		return
	}
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// RunScheduler drives weekly automated Bot scans, stale-run timeout expiry, and due review updates.
func (s *Service) RunScheduler(ctx context.Context) {
	s.tick(ctx)
	ticker := time.NewTicker(s.schedulerTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wakeCh:
			s.tick(ctx)
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if !s.isPluginEnabled(ctx) {
		return
	}
	if changed, err := s.repo.RefreshDueReviews(ctx); err == nil && changed > 0 {
		s.publishEvent(ctx, "learning.reviews.due", map[string]any{"count": changed})
	}
	if expired, err := s.repo.ExpireStaleRuns(ctx, DefaultScanTimeout); err == nil && expired > 0 {
		s.publishEvent(ctx, "learning.scan.timeout", map[string]any{"expired": expired})
	}

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil || !cfg.Enabled || strings.TrimSpace(cfg.MentorBotID) == "" {
		return
	}
	due, missed := s.isScheduleDue(cfg, s.now().UTC())
	if !due {
		if missed {
			// Beyond catchup grace window -> quietly roll NextRunAt forward to next week.
			_, _ = s.repo.AdvanceScheduleAfterTrigger(ctx, s.now().UTC())
		}
		return
	}
	if running, _ := s.repo.HasRunningRun(ctx); running != nil {
		return
	}

	// Prevent concurrent overlapping scheduler dispatches.
	if !s.dispatching.CompareAndSwap(false, true) {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.dispatching.Store(false)
		_, dispatchErr := s.DispatchScan(ctx, TriggerScanRequest{
			LookbackDays: cfg.LookbackDays,
			BotID:        cfg.MentorBotID,
			Trigger:      "scheduled",
		})
		if dispatchErr != nil {
			s.logger.Warn("scheduled learning radar scan failed", "error", dispatchErr)
		}
	}()
}

// isScheduleDue evaluates whether NextRunAt has elapsed within the misfire catch-up grace window.
// Returns (shouldRun, expiredBeyondCatchup).
func (s *Service) isScheduleDue(cfg ScheduleConfig, nowUTC time.Time) (bool, bool) {
	if !cfg.Enabled || strings.TrimSpace(cfg.NextRunAt) == "" {
		return false, false
	}
	nextRun, err := time.Parse(time.RFC3339, cfg.NextRunAt)
	if err != nil {
		return false, false
	}
	if nowUTC.Before(nextRun) {
		return false, false
	}
	lateness := nowUTC.Sub(nextRun)
	// Within 5 minutes counts as on-time; otherwise enforce MisfireCatchupHours.
	if lateness <= 5*time.Minute {
		return true, false
	}
	if cfg.MisfireCatchupHours > 0 && lateness <= time.Duration(cfg.MisfireCatchupHours)*time.Hour {
		return true, false
	}
	return false, true
}

// Wait blocks until any in-flight scheduled scan dispatch finishes (used in tests).
func (s *Service) Wait() {
	s.wg.Wait()
}
