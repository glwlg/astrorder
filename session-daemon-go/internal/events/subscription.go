package events

import "fmt"

const (
	defaultReplaySubscriptionCapacity = 256
	maxReplaySubscriptionCapacity     = 65536
)

type SnapshotSession struct {
	Status   string
	MinSeqID int64
	MaxSeqID int64
}

type ReplaySubscription struct {
	Subscription *Subscription
	Sessions     map[string]SnapshotSession
}

// BeginSubscription atomically separates a fixed replay snapshot from future
// live delivery. Durable commits hold commitGate for both their store commit and
// in-memory publication, so taking the gate exclusively drains the interval in
// which SQLite has committed a frame that publishCommitted has not yet exposed.
func (j *Journal) BeginSubscription(capacity int) (ReplaySubscription, error) {
	if capacity == 0 {
		capacity = defaultReplaySubscriptionCapacity
	}
	if capacity < 1 || capacity > maxReplaySubscriptionCapacity {
		return ReplaySubscription{}, fmt.Errorf("invalid subscription capacity")
	}

	j.commitGate.Lock()
	defer j.commitGate.Unlock()

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.commitClosed || j.storageClosed {
		return ReplaySubscription{}, fmt.Errorf("journal is closed")
	}
	if j.storageFailed {
		return ReplaySubscription{}, fmt.Errorf("event storage unavailable")
	}

	subscription := &Subscription{frames: make(chan Frame, capacity)}
	subscription.healthy.Store(true)
	j.subscribers[subscription] = struct{}{}

	sessions, err := j.snapshotSessionsLocked()
	if err != nil {
		delete(j.subscribers, subscription)
		subscription.healthy.Store(false)
		j.storageFailed = true
		return ReplaySubscription{}, err
	}
	return ReplaySubscription{Subscription: subscription, Sessions: sessions}, nil
}

// snapshotSessionsLocked runs with commitGate and mu held. Registering the
// subscriber before this call ensures every frame beyond the returned durable
// high watermarks is queued by publishCommitted.
func (j *Journal) snapshotSessionsLocked() (map[string]SnapshotSession, error) {
	if j.store != nil {
		latest, err := j.store.Latest(j.identity)
		if err != nil {
			return nil, err
		}
		result := make(map[string]SnapshotSession, len(latest))
		for _, frame := range latest {
			minimum, ok := frame.Payload["min_seq_id"].(int64)
			if !ok {
				return nil, fmt.Errorf("invalid durable minimum sequence for session %q", frame.SessionID)
			}
			result[frame.SessionID] = SnapshotSession{
				Status:   frame.Status,
				MinSeqID: minimum,
				MaxSeqID: frame.SeqID,
			}
		}
		return result, nil
	}

	result := make(map[string]SnapshotSession, len(j.retained))
	for sessionID, frames := range j.retained {
		if j.retired[sessionID] {
			continue
		}
		snapshot := SnapshotSession{
			Status:   "idle",
			MinSeqID: j.next[sessionID],
			MaxSeqID: j.next[sessionID],
		}
		if len(frames) > 0 {
			snapshot.MinSeqID = frames[0].SeqID
			snapshot.Status = frames[len(frames)-1].Status
			for i := len(frames) - 1; i >= 0; i-- {
				if frames[i].Event != "runtime.owner" && frames[i].Status != "" {
					snapshot.Status = frames[i].Status
					break
				}
			}
		}
		if status, exists := j.catalogStatus[sessionID]; exists {
			snapshot.Status = status
		}
		result[sessionID] = snapshot
	}
	return result, nil
}
