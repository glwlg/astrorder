package events

import "fmt"

// Health reports admission safety without exposing storage paths or raw errors.
func (j *Journal) Health() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	return map[string]any{"degraded": j.storageFailed || j.storageClosed, "closed": j.storageClosed}
}

func (j *Journal) CheckAdmission() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.storageFailed || j.storageClosed {
		return fmt.Errorf("event storage unavailable; new work is blocked")
	}
	return nil
}

func (j *Journal) markStorageFailure(err error) {
	if err == nil {
		return
	}
	j.mu.Lock()
	j.storageFailed = true
	j.mu.Unlock()
}
