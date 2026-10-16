package sessions

import (
	"encoding/json"
	"math"
	"time"
)

// Native item envelopes use milliseconds, independently of item completion order.
func codexMilliseconds(value any) string {
	var milliseconds int64
	switch v := value.(type) {
	case json.Number:
		var err error
		milliseconds, err = v.Int64()
		if err != nil {
			return ""
		}
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v > 253402300799999 {
			return ""
		}
		milliseconds = int64(v)
	case int64:
		milliseconds = v
	case int:
		milliseconds = int64(v)
	default:
		return ""
	}
	if milliseconds <= 0 || milliseconds > 253402300799999 {
		return ""
	}
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339Nano)
}
