package modelgateway

import (
	"context"
	"fmt"
	"sync"
	"time"

	"astrorder.dev/app-server/internal/store"
)

const modelSyncJobsKey = "model_sync:jobs"

type syncJob struct {
	ID                 string           `json:"id"`
	Status             string           `json:"status"`
	CreatedAt          string           `json:"created_at"`
	UpdatedAt          string           `json:"updated_at"`
	Targets            []map[string]any `json:"targets"`
	CatalogFingerprint string           `json:"catalog_fingerprint,omitempty"`
	Error              string           `json:"error,omitempty"`
}

type jobManager struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	cancels map[string]context.CancelFunc
}

func newJobManager() *jobManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &jobManager{
		ctx:     ctx,
		cancel:  cancel,
		cancels: make(map[string]context.CancelFunc),
	}
}

func (m *jobManager) register(id string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancels[id] = cancel
}

func (m *jobManager) cancelJob(id string) bool {
	m.mu.Lock()
	cancel, ok := m.cancels[id]
	if ok {
		delete(m.cancels, id)
	}
	m.mu.Unlock()
	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

func (m *jobManager) finish(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cancels, id)
}

func (m *jobManager) isActive(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.cancels[id]
	return ok
}

func (m *jobManager) Close(ctx context.Context) error {
	m.mu.Lock()
	for _, c := range m.cancels {
		if c != nil {
			c()
		}
	}
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func listJobs(ctx context.Context, st *store.Store) ([]syncJob, error) {
	pref, err := st.GetPreference(ctx, modelSyncJobsKey)
	if err != nil {
		return nil, err
	}
	if !pref.Exists || pref.Value == nil {
		return []syncJob{}, nil
	}
	raw, ok := pref.Value.([]any)
	if !ok {
		return []syncJob{}, nil
	}
	jobs := make([]syncJob, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			targets := []map[string]any{}
			if tList, ok := m["targets"].([]any); ok {
				for _, t := range tList {
					if tm, ok := t.(map[string]any); ok {
						targets = append(targets, tm)
					}
				}
			}
			job := syncJob{
				ID:                 jobString(m, "id"),
				Status:             jobString(m, "status"),
				CreatedAt:          jobString(m, "created_at"),
				UpdatedAt:          jobString(m, "updated_at"),
				Targets:            targets,
				CatalogFingerprint: jobString(m, "catalog_fingerprint"),
				Error:              jobString(m, "error"),
			}
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func saveJob(ctx context.Context, st *store.Store, job syncJob) error {
	existing, _ := listJobs(ctx, st)
	updated := []syncJob{job}
	for _, it := range existing {
		if it.ID != job.ID {
			updated = append(updated, it)
		}
	}
	if len(updated) > 20 {
		updated = updated[:20]
	}
	return st.SetPreference(ctx, modelSyncJobsKey, updated, true)
}

func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// jobString reads a persisted job field as a plain string. Missing or null
// values map to "" — never the fmt.Sprint("<nil>") literal the old code leaked
// into the UI ("最近同步 · 1 个目标 · <nil>").
func jobString(m map[string]any, key string) string {
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
