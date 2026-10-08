package events

import "fmt"

func validState(s string) bool {
	return s == "idle" || s == "running" || s == "waiting_approval" || s == "error"
}
func (j *Journal) TrackSession(id, status string) error {
	if id == "" || len(id) > 256 || !validState(status) {
		return fmt.Errorf("invalid tracked session")
	}
	j.commitGate.Lock()
	defer j.commitGate.Unlock()
	if j.commitClosed {
		return fmt.Errorf("journal is closed")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.store != nil {
		if err := j.store.TrackSession(j.identity, id, status); err != nil {
			j.storageFailed = true
			return err
		}
	}
	j.catalogStatus[id] = status
	delete(j.retired, id)
	if _, exists := j.retained[id]; !exists {
		j.retained[id] = []Frame{}
	}
	return nil
}
func (j *Journal) ForgetSession(id string) error {
	j.commitGate.Lock()
	defer j.commitGate.Unlock()
	if j.commitClosed {
		return fmt.Errorf("journal is closed")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.store != nil {
		if err := j.store.ForgetSession(j.identity, id); err != nil {
			j.storageFailed = true
			return err
		}
	}
	j.retired[id] = true
	return nil
}
