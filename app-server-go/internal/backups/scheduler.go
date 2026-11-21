package backups

import (
	"context"
	"io"
	"log/slog"
	"time"
)

const (
	defaultSchedulerTick   = 20 * time.Second
	maxConcurrentScheduled = 2
)

// NewService constructs a backup service with safe defaults.
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
		store:                 options.Store,
		authorizer:            options.Authorizer,
		resolver:              options.Resolver,
		opener:                options.Opener,
		agentRestarter:        options.AgentRestarter,
		defaultWorkspace:      options.DefaultWorkspace,
		defaultDestinationDir: options.DefaultDestinationDir,
		wslBinary:             options.WSLBinary,
		logger:                logger,
		now:                   nowFn,
		location:              loc,
		schedulerTick:         tick,
		runningJobs:           make(map[string]string),
	}
}

// RunScheduler runs the background scheduled-backup loop until ctx is cancelled.
func (s *Service) RunScheduler(ctx context.Context) {
	if err := s.markInterruptedRunsFailed(ctx); err != nil {
		s.logger.Warn("recover interrupted backup runs failed", "error", err)
	}
	s.runDueJobs(ctx)

	ticker := time.NewTicker(s.schedulerTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDueJobs(ctx)
		}
	}
}

func (s *Service) runDueJobs(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	now := s.now().UTC()
	dueJobs, err := s.listDueJobRows(ctx, now)
	if err != nil {
		s.logger.Warn("query due backup jobs failed", "error", err)
		return
	}
	sem := make(chan struct{}, maxConcurrentScheduled)
	for _, job := range dueJobs {
		if s.isJobRunning(job.ID) {
			continue
		}
		next := formatTSPtr(NextRunTime(job, now, s.location))
		if err := s.updateJobNextRun(ctx, job.ID, next, formatTS(now)); err != nil {
			s.logger.Warn("advance next backup schedule failed", "job_id", job.ID, "error", err)
		}
		jobCopy := job
		s.runWG.Add(1)
		go func() {
			defer s.runWG.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			_, triggerErr := s.startBackup(ctx, jobCopy.ID, TriggerScheduled, "", false)
			if triggerErr != nil {
				s.logger.Warn("scheduled backup skipped or failed to start", "job_id", jobCopy.ID, "error", triggerErr)
			}
		}()
	}
}

// Wait blocks until all in-flight asynchronous backup executions finish (useful in tests).
func (s *Service) Wait() {
	s.runWG.Wait()
}
