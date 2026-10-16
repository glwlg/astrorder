package sessions

import (
	"encoding/json"
	"testing"
)

func TestCodexHistoryEnvelopeTimeFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end any
		want       string
		native     bool
	}{
		{"float milliseconds", float64(1792116564173), nil, "2026-10-16T02:09:24.173Z", true},
		{"null start uses completion", nil, json.Number("1792116571278"), "2026-10-16T02:09:31.278Z", true},
		{"invalid number", json.Number("1.2"), nil, "2026-01-01T00:00:00Z", false},
		{"zero is unknown", json.Number("0"), nil, "2026-01-01T00:00:00Z", false},
		{"overflow is unknown", json.Number("9223372036854775807"), nil, "2026-01-01T00:00:00Z", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := projectHistoryItem(map[string]any{"startedAtMs": tc.start, "completedAtMs": tc.end, "item": map[string]any{"id": "r", "type": "agentMessage", "timestamp": "2026-01-01T00:00:00Z"}}, "a", "s", "s", "a", "codex")
			if rows[0]["created_at"] != tc.want || (rows[0]["timestamp_source"] == "native") != tc.native {
				t.Fatalf("unexpected time: %#v", rows)
			}
		})
	}
}

func TestCodexHistoryUsesNativeEnvelopeTime(t *testing.T) {
	for _, kind := range []string{"codex", "codex-ssh"} {
		rows := projectHistoryItem(map[string]any{
			"turnId": "old-turn", "startedAtMs": json.Number("1792116564173"), "completedAtMs": json.Number("1792116571278"),
			"item": map[string]any{"id": "old-reply", "type": "agentMessage", "text": "previous reply", "timestamp": "2026-10-16T02:58:00Z"},
		}, "a", "s", "s", "a", kind)
		if len(rows) != 1 || rows[0]["created_at"] != "2026-10-16T02:09:24.173Z" || rows[0]["completed_at"] != "2026-10-16T02:09:31.278Z" || rows[0]["timestamp_source"] != "native" {
			t.Fatalf("%s native envelope time lost: %#v", kind, rows)
		}
	}
}
