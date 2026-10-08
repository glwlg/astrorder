package events

import (
	storage "astrorder.dev/session-daemon/internal/journal"
	"errors"
	"fmt"
)

func (j *Journal) ReserveOperation(scope, id, digest string) (bool, []byte, error) {
	j.commitGate.RLock()
	defer j.commitGate.RUnlock()
	if j.commitClosed || j.store == nil {
		return false, nil, fmt.Errorf("durable operation storage unavailable")
	}
	fresh, response, err := j.store.ReserveOperation(scope, id, digest)
	if err != nil && !errors.Is(err, storage.ErrOperationConflict) {
		j.markStorageFailure(err)
	}
	return fresh, response, err
}
func (j *Journal) OperationCapability() map[string]any {
	j.commitGate.RLock()
	defer j.commitGate.RUnlock()
	if j.store == nil {
		return map[string]any{}
	}
	return map[string]any{"version": 1, "lookup": true}
}

func (j *Journal) ReadOperation(scope, id string) (bool, []byte, error) {
	j.commitGate.RLock()
	defer j.commitGate.RUnlock()
	if j.commitClosed || j.store == nil {
		return false, nil, fmt.Errorf("durable operation storage unavailable")
	}
	return j.store.ReadOperation(scope, id)
}

func (j *Journal) PendingOperations() (int, error) {
	j.commitGate.RLock()
	defer j.commitGate.RUnlock()
	if j.commitClosed {
		return 0, fmt.Errorf("journal is closed")
	}
	if j.store == nil {
		return 0, nil
	}
	return j.store.PendingOperations()
}

func (j *Journal) CompleteOperation(scope, id string, response []byte) error {
	j.commitGate.RLock()
	defer j.commitGate.RUnlock()
	if j.commitClosed || j.store == nil {
		return fmt.Errorf("durable operation storage unavailable")
	}
	err := j.store.CompleteOperation(scope, id, response)
	j.markStorageFailure(err)
	return err
}
