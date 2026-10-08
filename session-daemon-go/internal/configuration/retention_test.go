package configuration

import (
	"astrorder.dev/session-daemon/internal/protocol"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetentionConfiguration(t *testing.T) {
	for _, raw := range []string{
		`{"journal":{"total_bytes":4096,"session_bytes":2048,"max_age_seconds":3600}}`,
		`{"journal":{"total_bytes":4096,"session_bytes":8192,"max_age_seconds":3600}}`,
		`{"journal":{"total_bytes":0,"session_bytes":2048,"max_age_seconds":3600}}`,
	} {
		c, err := Parse(strings.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		d, err := protocol.Open("key", filepath.Join(t.TempDir(), "events.db"))
		if err != nil {
			t.Fatal(err)
		}
		err = c.Register(d)
		d.Close()
		if strings.Contains(raw, `"session_bytes":2048`) && !strings.Contains(raw, `"total_bytes":0`) {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("invalid limits accepted")
		}
	}
}
