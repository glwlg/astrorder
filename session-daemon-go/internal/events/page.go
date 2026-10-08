package events

import (
	storage "astrorder.dev/session-daemon/internal/journal"
	"fmt"
)

type ReplayPage struct {
	Frames                []Frame
	Next, Through, MinSeq int64
	HasMore               bool
	Status                string
}

func (j *Journal) Page(sessionID string, after, through int64, limit int) (ReplayPage, error) {
	if after < 0 || through < 0 || limit < 1 || limit > 1000 {
		return ReplayPage{}, fmt.Errorf("invalid replay bounds")
	}
	if j.store != nil {
		p, err := j.store.Page(j.identity, sessionID, after, through, limit)
		return convertPage(p), err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	p := ReplayPage{Frames: []Frame{}, Next: after}
	max := j.next[sessionID]
	if through == 0 {
		through = max
	}
	if after > through || through > max {
		return p, fmt.Errorf("replay cursor exceeds committed boundary")
	}
	p.Through = through
	for _, f := range j.retained[sessionID] {
		if p.MinSeq == 0 {
			p.MinSeq = f.SeqID
		}
		if f.SeqID <= through {
			if f.Event != "runtime.owner" && f.Status != "" {
				p.Status = f.Status
			}
		}
		if f.SeqID > after && f.SeqID <= through {
			if len(p.Frames) == limit {
				p.HasMore = true
				continue
			}
			p.Frames = append(p.Frames, f)
			p.Next = f.SeqID
		}
	}
	if through == max {
		if status, exists := j.catalogStatus[sessionID]; exists {
			p.Status = status
		}
	}
	return p, nil
}
func convertPage(p storage.ReplayPage) ReplayPage {
	result := ReplayPage{Frames: []Frame{}, Next: p.Next, Through: p.Through, MinSeq: p.MinSeq, HasMore: p.HasMore, Status: p.Status}
	for _, f := range p.Frames {
		result.Frames = append(result.Frames, Frame{Timestamp: f.Timestamp, SessionID: f.SessionID, SeqID: f.SeqID, Event: f.Event, Payload: f.Payload, Status: f.Status})
	}
	return result
}
