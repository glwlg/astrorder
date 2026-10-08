package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"errors"
	"fmt"
	"sync"
)

// Isolated keeps one native gateway per durable session. No subprocess I/O is
// performed while holding the pool metadata lock.
type Isolated struct {
	mu        sync.Mutex
	config    Config
	workers   map[string]*Adapter
	starting  map[*Adapter]bool
	attaching map[string]bool
	closed    bool
	inflight  map[string]int
	deleting  map[string]bool
}

func NewIsolated(config Config) *Isolated {
	return &Isolated{config: config, workers: map[string]*Adapter{}, starting: map[*Adapter]bool{}, attaching: map[string]bool{}, inflight: map[string]int{}, deleting: map[string]bool{}}
}
func (p *Isolated) Create(ctx context.Context, r core.Request) (core.Result, error) {
	return p.open(ctx, r, false)
}
func (p *Isolated) Spawn(ctx context.Context, r core.Request) (core.Result, error) {
	return p.open(ctx, r, true)
}
func (p *Isolated) open(ctx context.Context, r core.Request, resume bool) (core.Result, error) {
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return core.Result{}, fmt.Errorf("Hermes pool is closed")
	}
	previous := p.workers[r.SessionID]
	if resume && (r.SessionID == "" || p.attaching[r.SessionID] || p.inflight[r.SessionID] != 0 || p.deleting[r.SessionID] || (previous != nil && !previous.exitedForReattach())) {
		p.mu.Unlock()
		return core.Result{}, fmt.Errorf("Hermes session is invalid or already attached")
	}
	worker := New(p.config)
	p.starting[worker] = true
	if resume {
		p.attaching[r.SessionID] = true
	}
	p.mu.Unlock()
	if previous != nil && resume {
		if err := previous.Close(); err != nil {
			p.mu.Lock()
			delete(p.starting, worker)
			delete(p.attaching, r.SessionID)
			p.mu.Unlock()
			return core.Result{}, err
		}
	}
	defer func() {
		p.mu.Lock()
		delete(p.starting, worker)
		if resume {
			delete(p.attaching, r.SessionID)
		}
		p.mu.Unlock()
	}()
	var result core.Result
	var err error
	if resume {
		result, err = worker.Spawn(ctx, r)
	} else {
		result, err = worker.Create(ctx, r)
	}
	if err != nil {
		return core.Result{}, errors.Join(err, worker.Close())
	}
	p.mu.Lock()
	if p.closed || result.SessionID == "" || (p.workers[result.SessionID] != nil && p.workers[result.SessionID] != previous) || (resume && result.SessionID != r.SessionID) {
		p.mu.Unlock()
		return core.Result{}, errors.Join(fmt.Errorf("Hermes ownership changed during startup"), worker.Close())
	}
	p.workers[result.SessionID] = worker
	p.mu.Unlock()
	return result, nil
}
func (p *Isolated) Command(ctx context.Context, r core.Request) (core.Result, error) {
	p.mu.Lock()
	worker := p.workers[r.SessionID]
	closed := p.closed
	deleting := r.Action == "session.delete"
	if p.deleting[r.SessionID] || (deleting && p.inflight[r.SessionID] > 0) {
		p.mu.Unlock()
		return core.Result{}, fmt.Errorf("Hermes session has an in-flight operation")
	}
	if !closed && worker != nil {
		p.inflight[r.SessionID]++
		if deleting {
			p.deleting[r.SessionID] = true
		}
	}
	p.mu.Unlock()
	if closed || worker == nil {
		return core.Result{}, fmt.Errorf("Hermes session is not owned by this pool")
	}
	defer func() {
		p.mu.Lock()
		p.inflight[r.SessionID]--
		if deleting {
			delete(p.deleting, r.SessionID)
		}
		p.mu.Unlock()
	}()
	result, err := worker.Command(ctx, r)
	if err != nil {
		return core.Result{}, err
	}
	if result.Payload["deleted"] == r.SessionID {
		if err := worker.Close(); err != nil {
			return core.Result{}, err
		}
		p.mu.Lock()
		if p.workers[r.SessionID] == worker {
			delete(p.workers, r.SessionID)
		}
		p.mu.Unlock()
	}
	return result, nil
}
func (p *Isolated) Snapshot() map[string]core.Result {
	p.mu.Lock()
	workers := map[string]*Adapter{}
	for id, w := range p.workers {
		workers[id] = w
	}
	p.mu.Unlock()
	out := map[string]core.Result{}
	for id, w := range workers {
		if result, ok := w.Snapshot()[id]; ok {
			out[id] = result
		} else {
			out[id] = core.Result{SessionID: id, Status: "error", Payload: map[string]any{"reconciliation_required": true}}
		}
	}
	return out
}
func (p *Isolated) Close() error {
	p.mu.Lock()
	p.closed = true
	workers := map[*Adapter]bool{}
	for _, w := range p.workers {
		workers[w] = true
	}
	for w := range p.starting {
		workers[w] = true
	}
	p.mu.Unlock()
	var failures error
	for w := range workers {
		failures = errors.Join(failures, w.Close())
	}
	return failures
}
