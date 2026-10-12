package sessions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func decodeNative(t *testing.T, raw string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCollectLastActiveFindsSessionsAnywhereInTheTree(t *testing.T) {
	tree := decodeNative(t, `{"projects":[{"id":"project-1","name":"p","lanes":[{"sessions":[
		{"id":"a","last_active":1790000000.5},
		{"id":"b","last_active":0},
		{"id":"c"}]}]}],
		"loose":[{"id":"a","last_active":1780000000},{"id":"d","last_active":"not-a-number"}]}`)
	got := map[string]time.Time{}
	collectLastActive(tree, got)

	if len(got) != 1 {
		t.Fatalf("collected %v, want only session a", got)
	}
	// The newest of the duplicate entries wins.
	want := time.Unix(1790000000, 500000000).UTC()
	if !got["a"].Equal(want) {
		t.Fatalf("a = %v, want %v", got["a"], want)
	}
}

func TestUnixInstantRejectsInvalidValues(t *testing.T) {
	for _, bad := range []string{"0", "-5", "NaN", "Infinity", "1e999"} {
		if at, ok := unixInstant(json.Number(bad)); ok {
			t.Fatalf("unixInstant(%q) = %v, want rejected", bad, at)
		}
	}
	if at, ok := unixInstant(json.Number("1790000000")); !ok || at.Unix() != 1790000000 {
		t.Fatalf("valid value = %v %v", at, ok)
	}
}
