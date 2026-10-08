package events

import (
	storage "astrorder.dev/session-daemon/internal/journal"
	"fmt"
	"time"
)

func (j *Journal) ConfigureRetention(total, perSession int64, seconds int64) error {
	if seconds <= 0 || seconds > int64((1<<63-1)/int64(time.Second)) {
		return fmt.Errorf("invalid retention age")
	}
	j.commitGate.Lock()
	defer j.commitGate.Unlock()
	if j.commitClosed || j.store == nil {
		return fmt.Errorf("durable journal unavailable")
	}
	return j.store.SetRetention(storage.Retention{TotalBytes: total, SessionBytes: perSession, MaxAge: time.Duration(seconds) * time.Second})
}
