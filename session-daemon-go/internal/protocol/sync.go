package protocol

import (
	"fmt"
	"math"
)

func sequence(value any) (int64, error) {
	switch n := value.(type) {
	case float64:
		if n < 0 || n > 9007199254740991 || math.Trunc(n) != n {
			return 0, fmt.Errorf("checkpoint must be a non-negative safe integer")
		}
		return int64(n), nil
	case int:
		if n < 0 {
			return 0, fmt.Errorf("checkpoint must be a non-negative safe integer")
		}
		return int64(n), nil
	case int64:
		if n < 0 {
			return 0, fmt.Errorf("checkpoint must be a non-negative safe integer")
		}
		return n, nil
	default:
		return 0, fmt.Errorf("checkpoint must be a non-negative safe integer")
	}
}
func (d *Daemon) sync(request map[string]any) map[string]any {
	if flag, exists := request["defer_live"]; exists {
		if _, ok := flag.(bool); !ok {
			return errorResponse(request["request_id"], "defer_live must be a boolean")
		}
	}
	checkpoints, ok := request["sessions"].(map[string]any)
	if !ok {
		return errorResponse(request["request_id"], "sessions must be an object")
	}
	if len(checkpoints) > 128 {
		return errorResponse(request["request_id"], "too many sessions in sync request")
	}
	limit := int64(1000)
	if raw, exists := request["limit"]; exists {
		var err error
		limit, err = sequence(raw)
		if err != nil || limit < 1 || limit > 1000 {
			return errorResponse(request["request_id"], "invalid replay limit")
		}
	}
	boundaries := map[string]any{}
	if raw, exists := request["through"]; exists {
		var ok bool
		boundaries, ok = raw.(map[string]any)
		if !ok {
			return errorResponse(request["request_id"], "through must be an object")
		}
	}
	result := map[string]any{}
	states := d.journal.Statuses()
	for id, raw := range checkpoints {
		if id == "" {
			return errorResponse(request["request_id"], "session_id must be non-empty")
		}
		after, err := sequence(raw)
		if err != nil {
			return errorResponse(request["request_id"], err.Error())
		}
		through := int64(0)
		if raw, exists := boundaries[id]; exists {
			through, err = sequence(raw)
			if err != nil {
				return errorResponse(request["request_id"], err.Error())
			}
		}
		if _, exists := states[id]; !exists {
			continue
		}
		page, err := d.journal.Page(id, after, through, int(limit))
		if err != nil {
			return errorResponse(request["request_id"], err.Error())
		}
		frames := make([]map[string]any, 0, len(page.Frames))
		for _, f := range page.Frames {
			frames = append(frames, map[string]any{"timestamp": f.Timestamp, "session_id": f.SessionID, "seq_id": f.SeqID, "event": f.Event, "payload": f.Payload, "status": f.Status})
		}
		result[id] = map[string]any{"frames": frames, "overflow": page.MinSeq > 0 && after < page.MinSeq-1, "min_seq_id": page.MinSeq, "max_seq_id": page.Through, "status": page.Status, "has_more": page.HasMore, "next_seq_id": page.Next}
	}
	return map[string]any{"action": "session.sync.result", "request_id": request["request_id"], "daemon_id": d.daemonID, "sessions": result}
}
