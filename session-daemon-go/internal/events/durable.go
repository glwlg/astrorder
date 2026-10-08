package events

import storage "astrorder.dev/session-daemon/internal/journal"

func Open(path string, capacity int) (*Journal, error) {
	store, err := storage.OpenChecked(path)
	if err != nil {
		return nil, err
	}
	id, err := store.Identity()
	if err != nil {
		store.Close()
		return nil, err
	}
	latest, err := store.Latest(id)
	if err != nil {
		store.Close()
		return nil, err
	}
	j := New(capacity)
	j.store = store
	j.identity = id
	for _, f := range latest {
		j.catalogStatus[f.SessionID] = f.Status
		j.next[f.SessionID] = f.SeqID
		j.minimum[f.SessionID], _ = f.Payload["min_seq_id"].(int64)
		j.retained[f.SessionID] = []Frame{{SessionID: f.SessionID, SeqID: f.SeqID, Status: f.Status}}
	}
	j.commitQueue = make(chan commitRequest, 256)
	j.commitDone = make(chan struct{})
	go j.commitLoop()
	return j, nil
}
func (j *Journal) DaemonID() string { return j.identity }
func (j *Journal) Close() error {
	j.commitGate.Lock()
	defer j.commitGate.Unlock()
	if j.commitClosed {
		return nil
	}
	j.commitClosed = true
	j.mu.Lock()
	j.storageClosed = true
	j.mu.Unlock()
	if j.commitQueue != nil {
		close(j.commitQueue)
		<-j.commitDone
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.store != nil {
		return j.store.Close()
	}
	return nil
}
func (j *Journal) Replay(sessionID string, checkpoint int64) ([]Frame, error) {
	if j.store == nil {
		return j.After(sessionID, checkpoint), nil
	}
	stored, err := j.store.Replay(j.identity, sessionID, checkpoint)
	if err != nil {
		return nil, err
	}
	frames := make([]Frame, 0, len(stored))
	for _, f := range stored {
		frames = append(frames, Frame{Timestamp: f.Timestamp, SessionID: f.SessionID, SeqID: f.SeqID, Event: f.Event, Payload: f.Payload, Status: f.Status})
	}
	return frames, nil
}
