package preferences

import (
	"context"
	"path/filepath"
	"testing"

	"astrorder.dev/app-server/internal/store"
)

func TestMalformedStoredJSONRollsBackWholePreferenceWrite(t *testing.T) {
	for _, malformed := range []string{`{} {}`, `{} trailing`, `null false`} {
		t.Run(malformed, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "corrupt.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if _, err := st.DB().Exec(`INSERT INTO workspace_preferences("key",value) VALUES('another-domain',?)`, malformed); err != nil {
				t.Fatal(err)
			}
			for _, importing := range []bool{false, true} {
				patch := Patch{SessionPins: map[string]any{"one": true, "two": false}, PinnedProjects: list("new-project")}
				var err error
				if importing {
					_, err = Import(context.Background(), st, patch)
				} else {
					_, err = Apply(context.Background(), st, patch)
				}
				if err == nil {
					t.Errorf("import=%t accepted malformed stored document", importing)
				}
				var count int
				if err := st.DB().QueryRow(`SELECT count(*) FROM workspace_preferences WHERE "key" IN ('session_pins:one','session_pins:two','pinned_projects')`).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Errorf("import=%t failed readback committed %d preference changes", importing, count)
				}
			}
		})
	}
}
