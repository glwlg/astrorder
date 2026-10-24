package store

import (
	"context"
	"encoding/json"
	"errors"

	"strings"
	"testing"
	"unicode/utf8"
)

func TestPayloadHashMatchesPythonCanonicalJSON(t *testing.T) {
	fixtures := []struct {
		name    string
		payload map[string]any
		want    string
	}{
		{
			name: "sorted unicode html and line separators",
			payload: map[string]any{
				"text":           "你好 <tag>& \u2028 \u2029",
				"target_id":      "target<&>",
				"session_id":     "fixture-session",
				"id":             "python-command",
				"attachment_ids": []any{"attachment-1", "附件<&>"},
				"agent_id":       "fixture-agent",
				"action":         "enqueue",
			},
			want: "a97d9db6a153e46238230fbb74ea06f00224ae8397009e68ae4bd01f37a3b0f5",
		},
		{
			name: "unknown integer and distinct float literal",
			payload: map[string]any{
				"文字":      "雪",
				"unknown": json.Number("922337203685477580812345"),
				"line":    "\u2028\u2029",
				"integer": int64(1),
				"html":    "<&>",
				"float":   json.Number("1.0"),
			},
			want: "6132587c59a30dfa0845dfb74e888b845c202d26470dba4dddbb85de3ad06746",
		},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			got, err := payloadHash(fixture.payload)
			if err != nil {
				t.Fatal(err)
			}
			if got != fixture.want {
				t.Fatalf("payload hash=%s want Python canonical hash %s", got, fixture.want)
			}
		})
	}
}

func TestJSONCodecPreservesUnknownIntegersAndFloatSemantics(t *testing.T) {
	const source = `{"above":922337203685477580812345,"float":1.0,"integer":1}`
	decoded, err := decodeJSON(source)
	if err != nil {
		t.Fatal(err)
	}
	object := decoded.(map[string]any)
	if object["above"] != json.Number("922337203685477580812345") {
		t.Fatalf("above=%#v (%T)", object["above"], object["above"])
	}
	if object["float"] != json.Number("1.0") {
		t.Fatalf("float=%#v (%T)", object["float"], object["float"])
	}
	if object["integer"] != int64(1) {
		t.Fatalf("integer=%#v (%T)", object["integer"], object["integer"])
	}
	encoded, err := encodeJSON(object)
	if err != nil {
		t.Fatal(err)
	}
	if encoded != source {
		t.Fatalf("encoded=%s want exact native numeric JSON %s", encoded, source)
	}

	ctx := context.Background()
	st, err := Open(t.TempDir() + "/numbers.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetPreference(ctx, "numeric-fixture", object, true); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := st.DB().QueryRowContext(ctx, `SELECT value FROM workspace_preferences WHERE "key"=?`, "numeric-fixture").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != source {
		t.Fatalf("stored JSON=%s want %s", stored, source)
	}
	preference, err := st.GetPreference(ctx, "numeric-fixture")
	if err != nil {
		t.Fatal(err)
	}
	storedObject := preference.Value.(map[string]any)
	if storedObject["above"] != json.Number("922337203685477580812345") || storedObject["float"] != json.Number("1.0") || storedObject["integer"] != int64(1) {
		t.Fatalf("numeric types changed after store round trip: %#v", storedObject)
	}
}

func TestExistingPythonQueuedCommandRetryUsesExactLedgerHash(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir() + "/queued.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const pythonHash = "a97d9db6a153e46238230fbb74ea06f00224ae8397009e68ae4bd01f37a3b0f5"
	const createdAt = "2026-01-02 03:04:05.000000"
	attachments := `[{"id":"attachment-1","name":"first"},{"id":"附件<&>","name":"second"}]`
	_, err = st.DB().ExecContext(ctx, `INSERT INTO commands
(row_id,id,session_id,agent_id,action,state,text,attachments,target_id,created_at,updated_at,error,payload_hash)
VALUES(41,?,?,?,?,?,?,?,?,?,?,NULL,?)`,
		"python-command", "fixture-session", "fixture-agent", "enqueue", "queued", "你好 <tag>& \u2028 \u2029",
		attachments, "target<&>", createdAt, createdAt, pythonHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetCheckpoint(ctx, "fixture-epoch", "fixture-session", 7); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"id": "python-command", "session_id": "fixture-session", "agent_id": "fixture-agent",
		"action": "enqueue", "text": "你好 <tag>& \u2028 \u2029", "target_id": "target<&>",
		"attachments": []any{
			map[string]any{"id": "attachment-1", "name": "first"},
			map[string]any{"id": "附件<&>", "name": "second"},
		},
	}
	command, created, err := st.CreateCommand(ctx, payload, "received")
	if err != nil || created {
		t.Fatalf("legacy queued retry created=%v command=%#v err=%v", created, command, err)
	}
	assertCommandLedgerRow(t, st, 41, "python-command", "fixture-session", "queued", createdAt, pythonHash)

	different := copyMap(payload)
	different["text"] = "different"
	if _, _, err := st.CreateCommand(ctx, different, "queued"); err == nil || !errors.As(err, new(DuplicateCommandError)) {
		t.Fatalf("different retry did not return DuplicateCommandError: %v", err)
	}
	assertCommandLedgerRow(t, st, 41, "python-command", "fixture-session", "queued", createdAt, pythonHash)
	checkpoint, err := st.GetCheckpoint(ctx, "fixture-epoch", "fixture-session")
	if err != nil || checkpoint != 7 {
		t.Fatalf("command retry changed checkpoint: checkpoint=%d err=%v", checkpoint, err)
	}
}

func TestNativeIdentityMigrationRehashesWithPythonContractAndKeepsLedgerIdentity(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/identity.sqlite3"
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []map[string]any{
		{"id": "history-old", "agent_id": "agent", "title": "Friendly", "status": "idle", "source_session_id": "native-id", "updated_at": "2026-01-01T00:00:00Z"},
		{"id": "native-id", "agent_id": "agent", "title": "native-id", "status": "idle", "updated_at": "2026-01-01T00:00:00Z"},
	} {
		if _, err := st.UpsertSession(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	const oldPythonHash = "ee3fc382a0bde5f6f5cccd2e8608a83d6f2d022fb9b7411f30216199eb9ec4aa"
	const migratedPythonHash = "45f238f4a11bc8e222352fcc2920d736062acaf628d15cbefffec3ebc0bac45f"
	const createdAt = "2026-01-02 03:04:05.000000"
	_, err = st.DB().ExecContext(ctx, `INSERT INTO commands
(row_id,id,session_id,agent_id,action,state,text,attachments,target_id,created_at,updated_at,error,payload_hash)
VALUES(73,'migrated-command','history-old','agent','send','queued','keep <&> 世界','[]',NULL,?,?,NULL,?)`, createdAt, createdAt, oldPythonHash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetCheckpoint(ctx, "fixture-epoch", "history-old", 11); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec("PRAGMA user_version = 3"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertCommandLedgerRow(t, reopened, 73, "migrated-command", "native-id", "queued", createdAt, migratedPythonHash)
	payload := map[string]any{
		"id": "migrated-command", "session_id": "native-id", "agent_id": "agent",
		"action": "send", "text": "keep <&> 世界",
	}
	if _, created, err := reopened.CreateCommand(ctx, payload, "received"); err != nil || created {
		t.Fatalf("migrated command retry created=%v err=%v", created, err)
	}
	different := copyMap(payload)
	different["text"] = "different"
	if _, _, err := reopened.CreateCommand(ctx, different, "queued"); err == nil || !errors.As(err, new(DuplicateCommandError)) {
		t.Fatalf("different migrated retry did not return DuplicateCommandError: %v", err)
	}
	assertCommandLedgerRow(t, reopened, 73, "migrated-command", "native-id", "queued", createdAt, migratedPythonHash)
	checkpoint, err := reopened.GetCheckpoint(ctx, "fixture-epoch", "history-old")
	if err != nil || checkpoint != 11 {
		t.Fatalf("identity migration changed checkpoint: checkpoint=%d err=%v", checkpoint, err)
	}
}

func TestUnicodeLimitsPreserveCodepointsAndValidUTF8OnEveryTitlePath(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir() + "/unicode.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	created, err := st.UpsertSession(ctx, map[string]any{
		"id": "session", "agent_id": "agent", "title": strings.Repeat("界", 512) + "尾",
		"status": "idle", "updated_at": "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertUnicodeLimit(t, "upserted session title", created["title"].(string), 512)
	updated, err := st.UpdateSession(ctx, "agent", "session", map[string]any{"title": strings.Repeat("🙂", 512) + "尾"})
	if err != nil {
		t.Fatal(err)
	}
	assertUnicodeLimit(t, "updated session title", updated["title"].(string), 512)

	taskPayload := map[string]any{
		"id": "task", "agent_id": "agent", "session_id": "session", "kind": "background",
		"title": strings.Repeat("任", 512) + "尾", "status": "running", "logs": []any{},
		"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
	}
	task, err := st.UpsertTask(ctx, taskPayload)
	if err != nil {
		t.Fatal(err)
	}
	assertUnicodeLimit(t, "inserted task title", task["title"].(string), 512)
	taskPayload["title"] = strings.Repeat("工", 512) + "尾"
	task, err = st.UpsertTask(ctx, taskPayload)
	if err != nil {
		t.Fatal(err)
	}
	assertUnicodeLimit(t, "updated task title", task["title"].(string), 512)

	history, err := st.AppendConnectionHistory(ctx, "connection", strings.Repeat("阶", 64)+"尾", strings.Repeat("态", 32)+"尾", strings.Repeat("详", 2000)+"尾", map[string]any{
		"message": strings.Repeat("内", 2000) + "尾",
	})
	if err != nil {
		t.Fatal(err)
	}
	assertUnicodeLimit(t, "history stage", history["stage"].(string), 64)
	assertUnicodeLimit(t, "history state", history["state"].(string), 32)
	assertUnicodeLimit(t, "history detail", history["detail"].(string), 2000)
	assertUnicodeLimit(t, "structured history text", history["details"].(map[string]any)["message"].(string), 2000)
}

func assertCommandLedgerRow(t *testing.T, st *Store, rowID int64, id, sessionID, state, createdAt, hash string) {
	t.Helper()
	row, err := st.QueryRow(context.Background(), "SELECT row_id,id,session_id,state,created_at,payload_hash FROM commands WHERE agent_id=? AND id=?", "agent", id)
	if id == "python-command" {
		row, err = st.QueryRow(context.Background(), "SELECT row_id,id,session_id,state,created_at,payload_hash FROM commands WHERE agent_id=? AND id=?", "fixture-agent", id)
	}
	if err != nil {
		t.Fatal(err)
	}
	storedTime, timeErr := databaseTime(row["created_at"])
	if timeErr != nil {
		t.Fatalf("read ledger timestamp: %v", timeErr)
	}
	if row == nil || row["row_id"] != rowID || row["id"] != id || row["session_id"] != sessionID || row["state"] != state || storedTime != createdAt || row["payload_hash"] != hash {
		t.Fatalf("ledger row changed: %#v", row)
	}
}

func assertUnicodeLimit(t *testing.T, name, value string, codepoints int) {
	t.Helper()
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) != codepoints || strings.HasSuffix(value, "尾") {
		t.Fatalf("%s is not exactly %d valid Unicode codepoints: valid=%v runes=%d suffix=%q", name, codepoints, utf8.ValidString(value), utf8.RuneCountInString(value), value[max(0, len(value)-min(12, len(value))):])
	}
}
