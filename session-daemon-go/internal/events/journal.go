package events

import (
	storage "astrorder.dev/session-daemon/internal/journal"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type Frame struct {
	Timestamp float64
	SessionID string
	SeqID     int64
	Event     string
	Payload   map[string]any
	Status    string
}

type Subscription struct {
	frames  chan Frame
	healthy atomic.Bool
}

func (subscription *Subscription) Frames() <-chan Frame {
	return subscription.frames
}

func (subscription *Subscription) Healthy() bool {
	return subscription.healthy.Load()
}

type Journal struct {
	storageFailed bool
	storageClosed bool
	commitGate    sync.RWMutex
	commitQueue   chan commitRequest
	commitDone    chan struct{}
	commitClosed  bool
	catalogStatus map[string]string
	retired       map[string]bool
	minimum       map[string]int64
	store         *storage.Store
	identity      string
	capacity      int
	mu            sync.Mutex
	next          map[string]int64
	retained      map[string][]Frame
	subscribers   map[*Subscription]struct{}
}

func New(capacity int) *Journal {
	return &Journal{
		catalogStatus: map[string]string{}, retired: map[string]bool{},
		capacity:    capacity,
		minimum:     map[string]int64{},
		next:        map[string]int64{},
		retained:    map[string][]Frame{},
		subscribers: map[*Subscription]struct{}{},
	}
}

func (journal *Journal) Publish(sessionID, event string, payload map[string]any, status string) Frame {
	frame, err := journal.Commit(sessionID, event, payload, status)
	if err != nil {
		panic(err)
	}
	return frame
}

func (journal *Journal) Commit(sessionID, event string, payload map[string]any, status string) (Frame, error) {
	return journal.CommitAt(sessionID, event, payload, status, float64(time.Now().UnixMicro())/1e6)
}

func (journal *Journal) CommitAt(sessionID, event string, payload map[string]any, status string, timestamp float64) (Frame, error) {
	if math.IsNaN(timestamp) || math.IsInf(timestamp, 0) {
		return Frame{}, fmt.Errorf("timestamp must be finite")
	}
	if journal.store != nil {
		return journal.commitDurable(Frame{Timestamp: timestamp, SessionID: sessionID, Event: event, Payload: payload, Status: status})
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	frame := Frame{Timestamp: timestamp, SessionID: sessionID, SeqID: journal.next[sessionID] + 1, Event: event, Payload: payload, Status: status}
	if journal.retired[sessionID] {
		return Frame{}, fmt.Errorf("session has been retired")
	}
	journal.publishCommitted(frame)
	return frame, nil
}

// publishCommitted runs under mu, after durable storage has acknowledged the frame.
func (journal *Journal) publishCommitted(frame Frame) {
	sessionID, event, status := frame.SessionID, frame.Event, frame.Status
	journal.next[sessionID] = frame.SeqID
	if event != "runtime.owner" && status != "" {
		journal.catalogStatus[sessionID] = status
	}
	if journal.minimum[sessionID] == 0 {
		journal.minimum[sessionID] = frame.SeqID
	}
	retained := append(journal.retained[sessionID], frame)
	if len(retained) > journal.capacity {
		retained = retained[len(retained)-journal.capacity:]
	}
	journal.retained[sessionID] = retained
	subscribers := make([]*Subscription, 0, len(journal.subscribers))
	for subscriber := range journal.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	for _, subscriber := range subscribers {
		select {
		case subscriber.frames <- frame:
		default:
			subscriber.healthy.Store(false)
			delete(journal.subscribers, subscriber)
		}
	}
}

func (journal *Journal) Subscribe() *Subscription {
	return journal.SubscribeWithCapacity(64)
}

func (journal *Journal) SubscribeWithCapacity(capacity int) *Subscription {
	subscription := &Subscription{frames: make(chan Frame, capacity)}
	subscription.healthy.Store(true)
	journal.mu.Lock()
	journal.subscribers[subscription] = struct{}{}
	journal.mu.Unlock()
	return subscription
}

func (journal *Journal) Unsubscribe(subscription *Subscription) {
	journal.mu.Lock()
	delete(journal.subscribers, subscription)
	journal.mu.Unlock()
}

func (journal *Journal) Statuses() map[string]map[string]any {
	if journal.store != nil {
		latest, err := journal.store.Latest(journal.identity)
		if err == nil {
			result := map[string]map[string]any{}
			for _, frame := range latest {
				result[frame.SessionID] = map[string]any{"status": frame.Status, "min_seq_id": frame.Payload["min_seq_id"], "max_seq_id": frame.SeqID}
			}
			return result
		}
		journal.markStorageFailure(err)
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	result := map[string]map[string]any{}
	for sessionID, frames := range journal.retained {
		if journal.retired[sessionID] {
			continue
		}
		status := "idle"
		maxSeq := journal.next[sessionID]
		minSeq := journal.next[sessionID]
		if len(frames) > 0 {
			status = frames[len(frames)-1].Status
			for i := len(frames) - 1; i >= 0; i-- {
				if frames[i].Event != "runtime.owner" && frames[i].Status != "" {
					status = frames[i].Status
					break
				}
			}
			minSeq = frames[0].SeqID
			if journal.store != nil {
				minSeq = journal.minimum[sessionID]
			}
		}
		if latest, exists := journal.catalogStatus[sessionID]; exists {
			status = latest
		}
		result[sessionID] = map[string]any{"status": status, "min_seq_id": minSeq, "max_seq_id": maxSeq}
	}
	return result
}

func (journal *Journal) After(sessionID string, checkpoint int64) []Frame {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	var result []Frame
	for _, frame := range journal.retained[sessionID] {
		if frame.SeqID > checkpoint {
			result = append(result, frame)
		}
	}
	return result
}
