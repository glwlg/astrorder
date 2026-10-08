package events

import (
	storage "astrorder.dev/session-daemon/internal/journal"
	"encoding/json"
	"fmt"
	"time"
)

type commitResult struct {
	frame Frame
	err   error
}
type commitRequest struct {
	frame Frame
	done  chan commitResult
}

func (j *Journal) commitDurable(frame Frame) (Frame, error) {
	// Reject bad payloads before grouping so one caller cannot poison another's transaction.
	raw, err := json.Marshal(frame.Payload)
	if err != nil {
		return Frame{}, err
	}
	if err = storage.DecodePayload(raw, &frame.Payload); err != nil {
		return Frame{}, err
	}
	j.commitGate.RLock()
	defer j.commitGate.RUnlock()
	if j.commitClosed {
		return Frame{}, fmt.Errorf("journal is closed")
	}
	j.mu.Lock()
	retired := j.retired[frame.SessionID]
	j.mu.Unlock()
	if retired {
		return Frame{}, fmt.Errorf("session has been retired")
	}
	request := commitRequest{frame: frame, done: make(chan commitResult, 1)}
	select {
	case j.commitQueue <- request:
	default:
		return Frame{}, fmt.Errorf("durable commit queue is full")
	}
	result := <-request.done
	return result.frame, result.err
}

func (j *Journal) commitLoop() {
	defer close(j.commitDone)
	for first := range j.commitQueue {
		batch := []commitRequest{first}
		timer := time.NewTimer(time.Millisecond)
	collect:
		for len(batch) < 64 {
			select {
			case next, ok := <-j.commitQueue:
				if !ok {
					break collect
				}
				batch = append(batch, next)
			case <-timer.C:
				break collect
			}
		}
		timer.Stop()
		frames := make([]storage.Frame, len(batch))
		for i, r := range batch {
			f := r.frame
			frames[i] = storage.Frame{DaemonID: j.identity, SessionID: f.SessionID, Timestamp: f.Timestamp, Event: f.Event, Payload: f.Payload, Status: f.Status}
		}
		committed, err := j.store.AppendBatch(frames)
		if err != nil {
			j.markStorageFailure(err)
			// Never resend after an uncertain commit result. The caller retains
			// the error and must reconcile durable state rather than duplicate output.
			for _, r := range batch {
				r.done <- commitResult{err: err}
			}
			continue
		}
		j.mu.Lock()
		for i, stored := range committed {
			result := Frame{Timestamp: stored.Timestamp, SessionID: stored.SessionID, SeqID: stored.SeqID, Event: stored.Event, Payload: stored.Payload, Status: stored.Status}
			j.publishCommitted(result)
			batch[i].done <- commitResult{frame: result}
		}
		j.mu.Unlock()
	}
}
