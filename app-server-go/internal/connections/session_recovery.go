package connections

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type sessionRecoveryJob struct {
	cancel context.CancelFunc
}

type sessionRecovery struct {
	mu    sync.Mutex
	ctx   context.Context
	run   func(context.Context, string, string) error
	seen  map[string]bool
	jobs  map[string]*sessionRecoveryJob
	slots chan struct{}
}

// SetSessionRecovery binds recovery to the App lifetime, independent of the
// HTTP request that confirmed the connection. At most two scans run at once.
func (h *Handler) SetSessionRecovery(ctx context.Context, run func(context.Context, string, string) error) {
	h.recovery.mu.Lock()
	defer h.recovery.mu.Unlock()
	h.recovery.ctx, h.recovery.run = ctx, run
	h.recovery.seen = make(map[string]bool)
	h.recovery.jobs = make(map[string]*sessionRecoveryJob)
	h.recovery.slots = make(chan struct{}, 2)
}

func (h *Handler) forgetSessionRecovery(connID, kind string) {
	h.recovery.mu.Lock()
	defer h.recovery.mu.Unlock()
	key := connID + ":" + kind
	delete(h.recovery.seen, key)
	if job := h.recovery.jobs[key]; job != nil {
		job.cancel()
		delete(h.recovery.jobs, key)
	}
}

func (h *Handler) scheduleSessionRecovery(connID, kind string, force bool) {
	h.recovery.mu.Lock()
	defer h.recovery.mu.Unlock()
	r := &h.recovery
	key := connID + ":" + kind
	if r.run == nil || r.ctx == nil || r.ctx.Err() != nil || r.jobs[key] != nil || r.seen[key] && !force {
		return
	}
	r.seen[key] = true
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Minute)
	job := &sessionRecoveryJob{cancel: cancel}
	r.jobs[key] = job
	run, slots := r.run, r.slots
	go func() {
		defer cancel()
		defer func() {
			r.mu.Lock()
			if r.jobs[key] == job {
				delete(r.jobs, key)
			}
			r.mu.Unlock()
		}()
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		case <-ctx.Done():
			return
		}
		enabled, present, err := h.store.GetAgentConnectionChoice(ctx, key)
		if err != nil || present && !enabled || ctx.Err() != nil {
			return
		}
		if err := run(ctx, connID, kind); err != nil && ctx.Err() == nil {
			slog.Debug("connection session recovery incomplete", "connection_id", connID, "kind", kind, "error", err)
		}
	}()
}
