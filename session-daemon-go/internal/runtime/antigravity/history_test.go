package antigravity

import (
	core "astrorder.dev/session-daemon/internal/runtime"
	"context"
	"database/sql"
	"google.golang.org/protobuf/encoding/protowire"
	"os"
	"path/filepath"
	"testing"
)

func nativeText(kind int, text string) []byte {
	var nested []byte
	nested = protowire.AppendTag(nested, 1, protowire.BytesType)
	nested = protowire.AppendString(nested, text)
	var b []byte
	b = protowire.AppendTag(b, 1, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(kind))
	field := protowire.Number(19)
	if kind == 15 {
		field = 20
	}
	b = protowire.AppendTag(b, field, protowire.BytesType)
	return protowire.AppendBytes(b, nested)
}
func TestNativeHistoryPagination(t *testing.T) {
	dir := t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	db, err := sql.Open("sqlite", filepath.Join(dir, id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE steps(idx INTEGER PRIMARY KEY,step_type INTEGER,status INTEGER,step_format INTEGER,step_payload BLOB,metadata BLOB)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		kind := 14
		if i%2 == 1 {
			kind = 15
		}
		if _, err = db.Exec(`INSERT INTO steps VALUES(?,?,3,0,?,NULL)`, i, kind, nativeText(kind, "native text")); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	before := ""
	seen := map[string]bool{}
	pages := 0
	for {
		items, next, err := readHistory(context.Background(), dir, id, before, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, item := range items {
			key := str(item, "id")
			if seen[key] {
				t.Fatalf("duplicate %s", key)
			}
			seen[key] = true
			if str(item, "text") != "native text" {
				t.Fatalf("decode failed: %#v", item)
			}
		}
		if next == nil {
			break
		}
		before = next.(string)
		if pages > 4 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 7 || pages != 4 {
		t.Fatalf("history coverage %d items, %d pages", len(seen), pages)
	}
	for _, cursor := range []string{"-1", "../bad", "overflow999999999999999999999999999"} {
		if _, _, err = readHistory(context.Background(), dir, id, cursor, 2); err == nil {
			t.Fatalf("accepted cursor %s", cursor)
		}
	}
}
func TestNativeReadOnlyAcceptance(t *testing.T) {
	if os.Getenv("ASTRORDER_AGY_NATIVE_READ_TEST") != "1" {
		t.Skip("explicit native read-only acceptance")
	}
	home, _ := os.UserHomeDir()
	a := New(Config{Executable: filepath.Join(os.Getenv("LOCALAPPDATA"), "agy", "bin", "agy.exe"), Workspace: home, Allowed: []string{home}, Home: home})
	models, err := a.models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := a.Query(context.Background(), core.Request{Fields: map[string]any{"method": "session.list"}})
	if err != nil {
		t.Fatal(err)
	}
	sessions := catalog["items"].([]map[string]any)
	total := 0
	for _, s := range sessions {
		id := str(s, "id")
		if _, err = os.Stat(filepath.Join(a.historyDir(), id+".db")); os.IsNotExist(err) {
			continue
		}
		before := ""
		seen := map[string]bool{}
		for {
			items, next, err := readHistory(context.Background(), a.historyDir(), id, before, 50)
			if err != nil {
				t.Fatalf("native %s: %v", id, err)
			}
			for _, item := range items {
				key := str(item, "id")
				if seen[key] {
					t.Fatal("duplicate native item")
				}
				seen[key] = true
			}
			if next == nil {
				break
			}
			before = next.(string)
		}
		total += len(seen)
	}
	t.Logf("read-only native acceptance: models=%d sessions=%d history_items=%d; no prompts sent", len(models), len(sessions), total)
}
