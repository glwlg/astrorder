package observe

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"
)

// Tail writes each new spool event to out as one JSON line until ctx ends or
// out fails. It starts at the current end of the spool and does not replay old
// events, so a restarted reader sees only activity from now on.
//
// Dropped lines are acceptable: the next manual or startup reconciliation
// repairs any session whose activity was missed.
func Tail(ctx context.Context, spool string, interval time.Duration, out io.Writer) error {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	_, cursor := After(spool, Cursor{})
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	encoder := json.NewEncoder(out)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		var paths []string
		paths, cursor = After(spool, cursor)
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var row Observation
			if json.Unmarshal(raw, &row) != nil || !row.valid(path) {
				continue
			}
			if err := encoder.Encode(row); err != nil {
				return err
			}
		}
	}
}
