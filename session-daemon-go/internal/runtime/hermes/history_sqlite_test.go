package hermes

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func historyFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, query := range []string{
		`CREATE TABLE sessions (id TEXT PRIMARY KEY, parent_session_id TEXT, end_reason TEXT, source TEXT, model_config TEXT, started_at REAL, ended_at REAL)`,
		`CREATE TABLE messages (id INTEGER PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, timestamp REAL, active INTEGER DEFAULT 1, compacted INTEGER DEFAULT 0, display_kind TEXT, display_metadata TEXT, display_identity BLOB)`,
		`INSERT INTO sessions VALUES ('root',NULL,'compression','cli','{}',1,2), ('middle','root','compression','cli','{}',2,3), ('tip','middle',NULL,'cli','{}',3,NULL)`,
		`INSERT INTO messages(id,session_id,role,content,timestamp) VALUES (1,'root','user','first input',1),(2,'root','assistant','first answer',2),(3,'middle','user','middle input',3),(4,'tip','assistant','latest answer',4)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

func allNativeHistory(t *testing.T, path, session string, size int) []string {
	t.Helper()
	var contents []string
	before := ""
	seen := map[string]bool{}
	for range 20 {
		page, err := readNativePage(context.Background(), path, session, "fixture", before, size)
		if err != nil {
			t.Fatal(err)
		}
		items := page["items"].([]map[string]any)
		var batch []string
		for _, row := range items {
			batch = append(batch, row["content"].(string))
		}
		contents = append(batch, contents...)
		next, _ := page["next_cursor"].(string)
		if next == "" {
			return contents
		}
		if seen[next] {
			t.Fatal("history cursor repeated")
		}
		seen[next] = true
		before = next
	}
	t.Fatal("history pagination did not terminate")
	return nil
}

func TestNativeHistoryPagesAcrossCompressionLineage(t *testing.T) {
	_, path := historyFixture(t)
	want := []string{"first input", "first answer", "middle input", "latest answer"}
	for _, id := range []string{"root", "middle", "tip"} {
		t.Run(id, func(t *testing.T) {
			if got := allNativeHistory(t, path, id, 2); !reflect.DeepEqual(got, want) {
				t.Fatalf("history = %v, want %v", got, want)
			}
		})
	}
}

func TestNativeHistoryCompactedDisplayRowsAreDeduplicatedBeforePaging(t *testing.T) {
	db, path := historyFixture(t)
	for _, query := range []string{
		`UPDATE messages SET active=0, compacted=1 WHERE id IN (1,2,3)`,
		`INSERT INTO messages(id,session_id,role,content,timestamp) VALUES (5,'tip','user','middle input',3)`,
		`INSERT INTO messages(id,session_id,role,content,timestamp,active,compacted,display_kind,display_metadata) VALUES (6,'tip','user','rewound',6,0,0,NULL,NULL),(7,'tip','user','model summary',7,1,0,NULL,'{"model_only":true}'),(8,'tip','user','hidden',8,1,0,'hidden',NULL)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"first input", "first answer", "middle input", "latest answer"}
	if got := allNativeHistory(t, path, "tip", 1); !reflect.DeepEqual(got, want) {
		t.Fatalf("display history = %v, want %v", got, want)
	}
}

func TestNativeHistoryDoesNotCrossForkOrResetBoundaries(t *testing.T) {
	db, path := historyFixture(t)
	for _, marker := range []string{"_branched_from", "_delegate_from", "_reset_from"} {
		t.Run(marker, func(t *testing.T) {
			if _, err := db.Exec(`INSERT INTO sessions VALUES (?, 'middle', NULL, 'cli', ?, 10, NULL)`, marker, `{"`+marker+`":"middle"}`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO messages(session_id,role,content,timestamp) VALUES (?,'user','own branch',10)`, marker); err != nil {
				t.Fatal(err)
			}
			if got := allNativeHistory(t, path, marker, 1); !reflect.DeepEqual(got, []string{"own branch"}) {
				t.Fatalf("fork history = %v", got)
			}
		})
	}
}

func TestHermesHistoryUsesPlatformDataHomeWithoutGateway(t *testing.T) {
	db, path := historyFixture(t)
	_ = db
	home := filepath.Dir(path)
	t.Setenv("HERMES_HOME", home)
	adapter := New(Config{})
	defer adapter.Close()
	session := &SessionState{SessionID: "tip", Status: "idle"}
	page, err := adapter.handleHistoryPage(context.Background(), session, core.Request{Fields: map[string]any{"limit": 2}})
	if err != nil {
		t.Fatalf("native history did not use HERMES_HOME: %v", err)
	}
	if next, _ := page.Payload["next_cursor"].(string); next == "" {
		t.Fatal("compression ancestors missing from continuation")
	}
}

func TestNativeHistoryMatchesRealHermesDisplayFixture(t *testing.T) {
	root := os.Getenv("ASTRORDER_HERMES_HISTORY_FIXTURE")
	if root == "" {
		t.Skip("explicit isolated native fixture required")
	}
	raw, err := os.ReadFile(filepath.Join(root, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"native-root", "native-tip"} {
		if got := allNativeHistory(t, filepath.Join(root, "state.db"), id, 2); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s native projection mismatch: got %v want %v", id, got, want)
		}
	}
	t.Logf("Matched %d real Hermes display messages from root and tip in two-message pages", len(want))
}

func TestNativeHistoryRejectsForeignCursorAnchor(t *testing.T) {
	db, path := historyFixture(t)
	if _, err := db.Exec(`INSERT INTO sessions(id) VALUES ('foreign'); INSERT INTO messages(id,session_id,role,content) VALUES (99,'foreign','user','foreign')`); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(Cursor{Session: "tip", Source: "fixture", Before: 99})
	cursor := "native:" + base64.RawURLEncoding.EncodeToString(raw)
	if _, err := readNativePage(context.Background(), path, "tip", "fixture", cursor, 1); err == nil {
		t.Fatal("foreign anchor accepted")
	}
}
