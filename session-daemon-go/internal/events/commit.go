package events

import (
	storage "astrorder.dev/session-daemon/internal/journal"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

const maxDurableBatchFrames = 64

type commitBatchResult struct {
	frames []Frame
	err    error
}
type commitRequest struct {
	frames []Frame
	done   chan commitBatchResult
}

func (j *Journal) commitDurable(frame Frame) (Frame, error) {
	committed, err := j.commitDurableBatch([]Frame{frame})
	if err != nil {
		return Frame{}, err
	}
	return committed[0], nil
}

func (j *Journal) commitDurableBatch(frames []Frame) ([]Frame, error) {
	if len(frames) == 0 {
		return nil, nil
	}
	// Reject bad payloads before grouping so one caller cannot poison another's transaction.
	prepared := make([]Frame, len(frames))
	for i, frame := range frames {
		if math.IsNaN(frame.Timestamp) || math.IsInf(frame.Timestamp, 0) {
			return nil, fmt.Errorf("timestamp must be finite")
		}
		raw, err := json.Marshal(frame.Payload)
		if err != nil {
			return nil, err
		}
		if err = storage.DecodePayload(raw, &frame.Payload); err != nil {
			return nil, err
		}
		prepared[i] = frame
	}
	j.commitGate.RLock()
	defer j.commitGate.RUnlock()
	if j.commitClosed {
		return nil, fmt.Errorf("journal is closed")
	}
	j.mu.Lock()
	for _, frame := range prepared {
		if j.retired[frame.SessionID] {
			j.mu.Unlock()
			return nil, fmt.Errorf("session has been retired")
		}
	}
	j.mu.Unlock()
	committed := make([]Frame, 0, len(prepared))
	for start := 0; start < len(prepared); start += maxDurableBatchFrames {
		end := start + maxDurableBatchFrames
		if end > len(prepared) {
			end = len(prepared)
		}
		request := commitRequest{frames: prepared[start:end], done: make(chan commitBatchResult, 1)}
		select {
		case j.commitQueue <- request:
		default:
			return nil, fmt.Errorf("durable commit queue is full")
		}
		result := <-request.done
		if result.err != nil {
			return nil, result.err
		}
		committed = append(committed, result.frames...)
	}
	return committed, nil
}

func (j *Journal) commitLoop() {
	defer close(j.commitDone)
	for first := range j.commitQueue {
		batch := []commitRequest{first}
		totalFrames := len(first.frames)
		if totalFrames < maxDurableBatchFrames {
			timer := time.NewTimer(time.Millisecond)
		collect:
			for totalFrames < maxDurableBatchFrames {
				select {
				case next, ok := <-j.commitQueue:
					if !ok {
						break collect
					}
					batch = append(batch, next)
					totalFrames += len(next.frames)
				case <-timer.C:
					break collect
				}
			}
			timer.Stop()
		}
		frames := make([]storage.Frame, 0, totalFrames)
		for _, r := range batch {
			for _, f := range r.frames {
				frames = append(frames, storage.Frame{DaemonID: j.identity, SessionID: f.SessionID, Timestamp: f.Timestamp, Event: f.Event, Payload: f.Payload, Status: f.Status})
			}
		}
		committed, err := j.store.AppendBatch(frames)
		if err != nil {
			j.markStorageFailure(err)
			// Never resend after an uncertain commit result. The caller retains
			// the error and must reconcile durable state rather than duplicate output.
			for _, r := range batch {
				r.done <- commitBatchResult{err: err}
			}
			continue
		}
		j.mu.Lock()
		offset := 0
		for _, r := range batch {
			n := len(r.frames)
			out := make([]Frame, n)
			for i := 0; i < n; i++ {
				stored := committed[offset+i]
				result := Frame{Timestamp: stored.Timestamp, SessionID: stored.SessionID, SeqID: stored.SeqID, Event: stored.Event, Payload: stored.Payload, Status: stored.Status}
				j.publishCommitted(result)
				out[i] = result
			}
			offset += n
			r.done <- commitBatchResult{frames: out}
		}
		j.mu.Unlock()
	}
}
