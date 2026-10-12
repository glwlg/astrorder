package hermes

import (
	"context"
	"errors"
	"fmt"
	"sort"

	core "astrorder.dev/session-daemon/internal/runtime"
)

// Shutdown releases idle native gateways while keeping the registered pool
// available for a later explicit connection. Close permanently retires it.
func (p *Isolated) Shutdown(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	if p.closed || p.disconnecting || len(p.starting) != 0 || len(p.attaching) != 0 {
		p.mu.Unlock()
		return nil, core.ErrRuntimeBusy
	}
	for _, count := range p.inflight {
		if count != 0 {
			p.mu.Unlock()
			return nil, core.ErrRuntimeBusy
		}
	}
	workers := make(map[string]*Adapter, len(p.workers))
	for id, worker := range p.workers {
		worker.mu.RLock()
		state := worker.sessions[id]
		idle := !worker.closed && !worker.failed && state != nil && state.Status == "idle" && len(worker.pending) == 0 && len(worker.pendingApprovals) == 0
		worker.mu.RUnlock()
		if !idle {
			p.mu.Unlock()
			return nil, core.ErrRuntimeBusy
		}
		workers[id] = worker
	}
	p.disconnecting = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.disconnecting = false
		p.mu.Unlock()
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var failures error
	for _, worker := range workers {
		failures = errors.Join(failures, worker.Close())
		select {
		case <-worker.eventDone:
		case <-ctx.Done():
			failures = errors.Join(failures, fmt.Errorf("Hermes projection exit was not confirmed: %w", ctx.Err()))
		}
	}
	if failures != nil {
		for _, worker := range workers {
			worker.mu.Lock()
			for _, session := range worker.sessions {
				session.Status = "error"
			}
			worker.mu.Unlock()
		}
		return nil, failures
	}
	p.mu.Lock()
	released := make([]string, 0, len(workers))
	for id, worker := range workers {
		if p.workers[id] == worker {
			delete(p.workers, id)
			released = append(released, id)
		}
	}
	p.mu.Unlock()
	sort.Strings(released)
	return released, nil
}

var _ core.RuntimeDisconnector = (*Isolated)(nil)
