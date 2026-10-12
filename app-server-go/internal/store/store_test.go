package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestOpenModesSchemaAndPragmas(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.sqlite3")

	if _, err := OpenWithOptions(path, Options{SchemaMode: SchemaValidateOnly}); err == nil {
		t.Fatal("validate-only must reject a missing database")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("validate-only created a database: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	row, err := st.QueryRow(ctx, "PRAGMA user_version")
	if err != nil || row["user_version"] != int64(CurrentSchemaVersion) {
		t.Fatalf("user_version=%v err=%v", row, err)
	}
	for pragma, want := range map[string]any{
		"foreign_keys": int64(1),
		"busy_timeout": int64(30000),
		"journal_mode": "wal",
	} {
		got, queryErr := st.QueryRow(ctx, "PRAGMA "+pragma)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		var actual any
		for _, value := range got {
			actual = value
			break
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("%s=%#v want %#v", pragma, actual, want)
		}
	}
	if err := assertCanonicalSchema(ctx, t, st); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	validated, err := OpenWithOptions(path, Options{SchemaMode: SchemaValidateOnly})
	if err != nil {
		t.Fatalf("validate current schema: %v", err)
	}
	if err := validated.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	if _, err := OpenWithOptions(path, Options{SchemaMode: SchemaValidateOnly}); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("validate-only accepted stale schema: %v", err)
	}
	migrated, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = migrated.Close()
}

func TestFixtureUpgradePreservesEveryTableAndValue(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join("..", "parity", "testdata", "store", "store-compat-fixture.sqlite3")
	path := filepath.Join(t.TempDir(), "fixture.sqlite3")
	copyFile(t, source, path)

	before := rawSnapshot(t, path)
	if len(before) != 16 {
		t.Fatalf("fixture table count=%d want 16", len(before))
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := assertCanonicalSchema(ctx, t, st); err != nil {
		t.Fatal(err)
	}
	fixtureMetadata := rawSchemaMetadata(t, path)
	freshPath := filepath.Join(t.TempDir(), "fresh.sqlite3")
	fresh, err := Open(freshPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = fresh.Close()
	if freshMetadata := rawSchemaMetadata(t, freshPath); !reflect.DeepEqual(freshMetadata, fixtureMetadata) {
		t.Fatalf("fresh schema metadata differs from Python fixture\nfixture=%#v\nfresh=%#v", fixtureMetadata, freshMetadata)
	}
	after := rawSnapshotFromDB(t, st.DB())
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("fixture rows changed during upgrade\nbefore=%#v\nafter=%#v", before, after)
	}

	session, err := st.GetSession(ctx, "fixture-agent-a", "same-native-session")
	if err != nil {
		t.Fatal(err)
	}
	if session["ephemeral"] != true || session["handoff_context_consumed"] != false {
		t.Fatalf("boolean wire values lost: %#v", session)
	}
	owner := session["runtime_owner"].(map[string]any)
	if owner["daemon_id"] != "fixture-daemon" || owner["epoch"] != "fixture-epoch" {
		t.Fatalf("runtime_owner=%#v", owner)
	}
	if session["updated_at"] != "2026-01-02T03:04:07.000Z" {
		t.Fatalf("wire timestamp=%#v", session["updated_at"])
	}

	messages, _, err := st.ListMessages(ctx, "fixture-agent-a", "same-native-session", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0]["tool"] != nil {
		t.Fatalf("textual JSON null was not decoded as null: %#v", messages)
	}
	if messages[1]["tool"].(map[string]any)["ok"] != true {
		t.Fatalf("tool JSON=%#v", messages[1]["tool"])
	}
	checkpoint, err := st.GetCheckpoint(ctx, "fixture-daemon", "same-native-session")
	if err != nil || checkpoint != int64(9223372036854770000) {
		t.Fatalf("checkpoint=%d err=%v", checkpoint, err)
	}
	project, err := st.QueryRow(ctx, "SELECT id FROM projects")
	if err != nil || project["id"] != "project:fixture-source-shared\x00fixture-project" {
		t.Fatalf("composite project id=%#v err=%v", project["id"], err)
	}
	pref, err := st.GetPreference(ctx, "appearance:tombstone")
	if err != nil || pref.Exists != true || pref.Value != nil {
		t.Fatalf("textual null preference=%#v err=%v", pref, err)
	}
}

func TestAdditiveBackfillAndExplicitNativeIdentityMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite3")
	raw, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.Exec(`
CREATE TABLE agents (id VARCHAR(256) PRIMARY KEY, kind VARCHAR(32) NOT NULL, name VARCHAR(256) NOT NULL, status VARCHAR(32) NOT NULL, capabilities JSON NOT NULL, limitation TEXT, updated_at DATETIME NOT NULL);
CREATE TABLE sessions (row_id INTEGER PRIMARY KEY, id VARCHAR(256) NOT NULL, agent_id VARCHAR(256) NOT NULL, title VARCHAR(1024) NOT NULL, workspace TEXT, status VARCHAR(32) NOT NULL, updated_at DATETIME NOT NULL, CONSTRAINT uq_sessions_agent_id UNIQUE(agent_id,id));
CREATE TABLE commands (row_id INTEGER PRIMARY KEY, id VARCHAR(256) NOT NULL, session_id VARCHAR(256) NOT NULL, agent_id VARCHAR(256) NOT NULL, action VARCHAR(32) NOT NULL, state VARCHAR(32) NOT NULL, text TEXT NOT NULL, attachments JSON NOT NULL, target_id VARCHAR(256), created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, error TEXT, CONSTRAINT uq_commands_scope UNIQUE(agent_id,session_id,id));
CREATE TABLE ssh_connections (id VARCHAR(64) PRIMARY KEY, host VARCHAR(253), port INTEGER NOT NULL, user VARCHAR(64), ssh_config_alias VARCHAR(128), identity_file TEXT, hermes_path TEXT, workspace TEXT, state VARCHAR(32) NOT NULL, detail TEXT, updated_at DATETIME NOT NULL);
CREATE TABLE attachments (id VARCHAR(256) PRIMARY KEY, name VARCHAR(256) NOT NULL, media_type VARCHAR(128) NOT NULL, size INTEGER NOT NULL, storage_name VARCHAR(512) NOT NULL UNIQUE, created_at DATETIME NOT NULL);
INSERT INTO agents VALUES ('agent','hermes','Legacy','ready','[]',NULL,'2026-01-01 00:00:00.000000');
INSERT INTO sessions(row_id,id,agent_id,title,workspace,status,updated_at) VALUES (1,'legacy-session','agent','` + strings.Repeat("x", 700) + `',NULL,'idle','2026-01-01 00:00:00.000000');
INSERT INTO sessions(row_id,id,agent_id,title,workspace,status,updated_at) VALUES (2,'side','agent','[侧边聊天]',NULL,'idle','2026-01-01 00:00:00.000000');
INSERT INTO commands(row_id,id,session_id,agent_id,action,state,text,attachments,target_id,created_at,updated_at,error) VALUES (1,'legacy-command','legacy-session','agent','send','queued','legacy','[]',NULL,'2026-01-01 00:00:00.000000','2026-01-01 00:00:00.000000',NULL);
INSERT INTO ssh_connections VALUES ('default','host',22,'user',NULL,NULL,NULL,NULL,'disconnected','legacy','2026-01-01 00:00:00.000000');`)
	if err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.GetAgent(ctx, "agent")
	if err != nil || agent["source_id"] != "agent" || agent["runtime_id"] != "agent" {
		t.Fatalf("agent backfill=%#v err=%v", agent, err)
	}
	legacy, _ := st.GetSession(ctx, "agent", "legacy-session")
	if len(legacy["title"].(string)) != 512 || legacy["source_session_id"] != "legacy-session" {
		t.Fatalf("session backfill=%#v", legacy)
	}
	side, _ := st.GetSession(ctx, "agent", "side")
	if side["ephemeral"] != true {
		t.Fatalf("side-chat marker not backfilled: %#v", side)
	}
	connections, err := st.ListSSHConnections(ctx)
	if err != nil || len(connections) != 1 || connections[0]["id"] == "default" || connections[0]["profile_name"] != "default" {
		t.Fatalf("ssh migration=%#v err=%v", connections, err)
	}
	commandHashRow, err := st.QueryRow(ctx, "SELECT row_id,payload_hash FROM commands WHERE agent_id=? AND session_id=? AND id=?", "agent", "legacy-session", "legacy-command")
	if err != nil || commandHashRow["row_id"] != int64(1) || commandHashRow["payload_hash"] != "58146bd8766d6bbcb44710ccd6f8cb8029eab80cb6f2d9333f59c2fba1241b92" {
		t.Fatalf("legacy command Python hash backfill=%#v err=%v", commandHashRow, err)
	}
	if _, _, err := st.CreateCommand(ctx, map[string]any{"id": "legacy-command", "agent_id": "agent", "session_id": "legacy-session", "action": "send", "text": "legacy"}, "queued"); err != nil {
		t.Fatalf("legacy command hash was not backfilled: %v", err)
	}
	_ = st.Close()

	identityPath := filepath.Join(t.TempDir(), "identity.sqlite3")
	identity, err := Open(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, payload := range []map[string]any{
		{"id": "history-old", "agent_id": "agent", "title": "Friendly", "status": "idle", "source_session_id": "native-id", "updated_at": "2026-01-01T00:00:00Z"},
		{"id": "native-id", "agent_id": "agent", "title": "native-id", "status": "idle", "updated_at": "2026-01-01T00:00:00Z"},
		{"id": "similar-text-only", "agent_id": "agent", "title": "Friendly", "status": "idle", "source_session_id": "other-native", "updated_at": "2026-01-01T00:00:00Z"},
	} {
		if _, err := identity.UpsertSession(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := identity.UpsertMessage(ctx, map[string]any{"id": "message", "agent_id": "agent", "session_id": "history-old", "role": "user", "kind": "message", "text": "keep", "attachments": []any{}, "created_at": "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := identity.CreateCommand(ctx, map[string]any{"id": "command", "agent_id": "agent", "session_id": "history-old", "action": "send", "text": "keep"}, "queued"); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.DB().Exec(fmt.Sprintf("PRAGMA user_version = %d", CurrentSchemaVersion-1)); err != nil {
		t.Fatal(err)
	}
	_ = identity.Close()
	reopened, err := Open(identityPath)
	if err != nil {
		t.Fatal(err)
	}
	if old, _ := reopened.GetSession(ctx, "agent", "history-old"); old != nil {
		t.Fatalf("explicit legacy identity remains: %#v", old)
	}
	native, _ := reopened.GetSession(ctx, "agent", "native-id")
	if native["title"] != "Friendly" {
		t.Fatalf("native metadata not merged: %#v", native)
	}
	if msg, _ := reopened.GetMessage(ctx, "agent", "native-id", "message"); msg == nil || msg["text"] != "keep" {
		t.Fatalf("native child not migrated: %#v", msg)
	}
	if _, _, err := reopened.CreateCommand(ctx, map[string]any{"id": "command", "agent_id": "agent", "session_id": "native-id", "action": "send", "text": "keep"}, "queued"); err != nil {
		t.Fatalf("migrated command is no longer idempotent: %v", err)
	}
	different := map[string]any{"id": "command", "agent_id": "agent", "session_id": "native-id", "action": "send", "text": "different"}
	if _, _, err := reopened.CreateCommand(ctx, different, "queued"); err == nil || !errors.As(err, new(DuplicateCommandError)) {
		t.Fatalf("different command payload did not return DuplicateCommandError: %v", err)
	}
	if similar, _ := reopened.GetSession(ctx, "agent", "similar-text-only"); similar == nil {
		t.Fatal("non-history identity was incorrectly inferred from text")
	}
	_ = reopened.Close()
}

func TestConcurrentFirstOpenSerializesMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-open.sqlite3")
	const workers = 8
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := Open(path)
			if err == nil {
				err = st.Close()
			}
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent schema migrator failed: %v", err)
		}
	}
	validated, err := OpenWithOptions(path, Options{SchemaMode: SchemaValidateOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer validated.Close()
}

func TestCompositeIdentityPaginationTransactionsAndConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "domain.sqlite3")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for _, agent := range []string{"agent-a", "agent-b"} {
		if _, err := st.UpsertAgent(ctx, map[string]any{"id": agent, "kind": "hermes", "name": agent, "status": "ready", "capabilities": []any{"chat"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertSession(ctx, map[string]any{"id": "same", "agent_id": agent, "title": agent, "status": "idle", "updated_at": "2026-01-01T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []map[string]any{
		{"id": "oldest", "created_at": "2026-01-01T00:00:01Z"},
		{"id": "tie-a", "created_at": "2026-01-01T00:00:02Z"},
		{"id": "tie-b", "created_at": "2026-01-01T00:00:02Z"},
		{"id": "newest", "created_at": "2026-01-01T00:00:03Z"},
	} {
		item["agent_id"], item["session_id"] = "agent-a", "same"
		item["role"], item["kind"], item["text"], item["attachments"] = "assistant", "message", item["id"], []any{}
		if _, err := st.UpsertMessage(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.UpsertMessage(ctx, map[string]any{"id": "oldest", "agent_id": "agent-b", "session_id": "same", "role": "assistant", "kind": "message", "text": "other", "attachments": []any{}, "created_at": "2026-01-01T00:00:01Z"}); err != nil {
		t.Fatal(err)
	}
	latest, cursor, err := st.ListMessages(ctx, "agent-a", "same", "", 2)
	if err != nil || ids(latest) != "tie-b,newest" || cursor == "" {
		t.Fatalf("latest=%s cursor=%q err=%v", ids(latest), cursor, err)
	}
	older, next, err := st.ListMessages(ctx, "agent-a", "same", cursor, 2)
	if err != nil || ids(older) != "oldest,tie-a" || next != "" {
		t.Fatalf("older=%s cursor=%q err=%v", ids(older), next, err)
	}
	if _, _, err := st.ListMessages(ctx, "agent-b", "same", cursor, 2); err == nil {
		t.Fatal("cursor from another agent scope was accepted")
	}

	for _, id := range []string{"live", "item-2"} {
		if _, err := st.UpsertMessage(ctx, map[string]any{"id": id, "agent_id": "agent-a", "session_id": "same", "role": "assistant", "kind": "message", "text": "identical text", "attachments": []any{}, "created_at": "2026-01-01T00:00:04Z"}); err != nil {
			t.Fatal(err)
		}
	}
	all, _, _ := st.ListMessages(ctx, "agent-a", "same", "", 20)
	if !containsIDs(all, "live", "item-2") {
		t.Fatalf("text-based aliases were silently merged: %s", ids(all))
	}

	rollbackErr := st.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO workspace_preferences(key,value) VALUES(?,?)", "rollback", `{"x":1}`); err != nil {
			return err
		}
		return errors.New("inject rollback")
	})
	if rollbackErr == nil {
		t.Fatal("expected transaction failure")
	}
	if pref, _ := st.GetPreference(ctx, "rollback"); pref.Exists {
		t.Fatalf("failed transaction committed: %#v", pref)
	}

	const workers = 24
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, writeErr := st.UpsertTask(ctx, map[string]any{
				"id": fmt.Sprintf("task-%02d", i), "agent_id": "agent-a", "session_id": "same",
				"kind": "background", "title": "task", "status": "running", "logs": []any{},
				"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
			})
			errCh <- writeErr
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	tasks, err := st.ListTasks(ctx, "agent-a", "same")
	if err != nil || len(tasks) != workers {
		t.Fatalf("concurrent tasks=%d err=%v", len(tasks), err)
	}
}

func TestEventRetentionReplayAndConnectionHistoryRedaction(t *testing.T) {
	ctx := context.Background()
	st, err := OpenWithOptions(filepath.Join(t.TempDir(), "retention.sqlite3"), Options{EventRetention: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 1; i <= 5; i++ {
		if _, err := st.AppendEvent(ctx, Event{ID: fmt.Sprintf("event-%d", i), Type: "fixture", Data: map[string]any{"n": i}}); err != nil {
			t.Fatal(err)
		}
	}
	resync, events, err := st.ReplayEvents(ctx, 0)
	if err != nil || !resync || len(events) != 0 {
		t.Fatalf("expired replay resync=%v events=%#v err=%v", resync, events, err)
	}
	resync, events, err = st.ReplayEvents(ctx, 2)
	if err != nil || resync || len(events) != 3 || events[0].ID != "event-3" || events[2].ID != "event-5" {
		t.Fatalf("retained replay resync=%v events=%#v err=%v", resync, events, err)
	}
	history, err := st.AppendConnectionHistory(ctx, "connection", "probe", "failed", "token=visible\nline", map[string]any{
		"nested": map[string]any{"api_key": "visible", "message": "password:visible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(history), "visible") {
		t.Fatalf("connection history leaked a secret: %#v", history)
	}
}

func TestApplyFrameIsRegisteredAtomicContiguousAndReopenSafe(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "frames.sqlite3")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	projection := ProjectionFunc(func(tx *sql.Tx) ([]Event, error) {
		calls.Add(1)
		if _, err := tx.ExecContext(ctx, "INSERT INTO workspace_preferences(key,value) VALUES(?,?)", "projected", `{"n":9223372036854770000}`); err != nil {
			return nil, err
		}
		return []Event{{ID: "event-1", Type: "fixture.applied", AgentID: "agent-a", SessionID: "stream", Data: map[string]any{"n": int64(9223372036854770000)}}}, nil
	})
	if _, err := st.ApplyFrame(ctx, "epoch", "stream", 1, nil); err == nil {
		t.Fatal("unregistered projection callback was accepted")
	}
	projector, err := st.RegisterProjector("fixture", projection)
	if err != nil {
		t.Fatal(err)
	}
	forged := &Projector{owner: st, name: "fixture", apply: projection}
	if _, err := st.ApplyFrame(ctx, "closure-epoch", "stream", 1, forged); err == nil {
		t.Fatal("unregistered handle with matching fields was accepted")
	}
	events, err := st.ApplyFrame(ctx, "epoch", "stream", 1, projector)
	if err != nil || len(events) != 1 || events[0].Cursor < 1 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if _, err := st.ApplyFrame(ctx, "epoch", "stream", 1, projector); err != nil {
		t.Fatalf("duplicate frame: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("duplicate reran projection: calls=%d", calls.Load())
	}
	if _, err := st.ApplyFrame(ctx, "epoch", "stream", 3, projector); err == nil {
		t.Fatal("non-contiguous frame accepted")
	}

	failing := ProjectionFunc(func(tx *sql.Tx) ([]Event, error) {
		if _, err := tx.ExecContext(ctx, "INSERT INTO workspace_preferences(key,value) VALUES(?,?)", "failed", `true`); err != nil {
			return nil, err
		}
		return []Event{{ID: "must-not-exist", Type: "fixture.failed", AgentID: "agent-a", SessionID: "stream", Data: map[string]any{}}}, errors.New("projection failed")
	})
	failingProjector, err := st.RegisterProjector("failing", failing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApplyFrame(ctx, "epoch", "stream", 2, failingProjector); err == nil {
		t.Fatal("projection failure was swallowed")
	}
	checkpoint, _ := st.GetCheckpoint(ctx, "epoch", "stream")
	if checkpoint != 1 {
		t.Fatalf("failed projection advanced checkpoint to %d", checkpoint)
	}
	if pref, _ := st.GetPreference(ctx, "failed"); pref.Exists {
		t.Fatalf("failed projection committed data: %#v", pref)
	}
	if row, err := st.QueryRow(ctx, "SELECT cursor FROM events WHERE event_id = ?", "must-not-exist"); err != nil || row != nil {
		t.Fatalf("failed event persisted: %#v err=%v", row, err)
	}
	_ = st.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	checkpoint, err = reopened.GetCheckpoint(ctx, "epoch", "stream")
	if err != nil || checkpoint != 1 {
		t.Fatalf("reopened checkpoint=%d err=%v", checkpoint, err)
	}
	rows, err := reopened.QueryRows(ctx, "SELECT data FROM events WHERE event_id = ?", "event-1")
	if err != nil || len(rows) != 1 || rows[0]["data"].(map[string]any)["n"].(int64) != int64(9223372036854770000) {
		t.Fatalf("durable event=%#v err=%v", rows, err)
	}
}

func assertCanonicalSchema(ctx context.Context, t *testing.T, st *Store) error {
	t.Helper()
	wantTables := []string{"agent_connection_choices", "agents", "attachments", "commands", "connection_history", "daemon_checkpoints", "events", "messages", "projects", "review_relay_bindings", "review_relay_runs", "sessions", "ssh_connections", "tasks", "token_metrics", "workspace_preferences"}
	rows, err := st.QueryRows(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return err
	}
	gotTables := make([]string, len(rows))
	for i := range rows {
		gotTables[i] = rows[i]["name"].(string)
	}
	if !reflect.DeepEqual(gotTables, wantTables) {
		return fmt.Errorf("tables=%v want %v", gotTables, wantTables)
	}
	wantIndices := []string{
		"ix_agents_connection_id", "ix_agents_source_id", "ix_commands_agent_id", "ix_commands_session_id",
		"ix_connection_history_connection_id", "ix_events_agent_id", "ix_events_session_id", "ix_messages_agent_id",
		"ix_messages_session_id", "ix_projects_agent_id", "ix_projects_connection_id", "ix_projects_source_id",
		"ix_review_relay_runs_source_agent_id", "ix_review_relay_runs_source_session_id", "ix_sessions_agent_id",
		"ix_sessions_connection_id", "ix_sessions_handoff_from_agent_id", "ix_sessions_handoff_from_session_id",
		"ix_sessions_project_id", "ix_sessions_source_id", "ix_sessions_source_session_id", "ix_tasks_agent_id",
		"ix_tasks_session_id", "ix_token_metrics_agent_id", "ix_token_metrics_date", "ix_token_metrics_model",
		"ix_token_metrics_session_id",
	}
	rows, err = st.QueryRows(ctx, "SELECT name FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_%' AND sql IS NOT NULL ORDER BY name")
	if err != nil {
		return err
	}
	gotIndices := make([]string, len(rows))
	for i := range rows {
		gotIndices[i] = rows[i]["name"].(string)
	}
	if !reflect.DeepEqual(gotIndices, wantIndices) {
		return fmt.Errorf("indices=%v want %v", gotIndices, wantIndices)
	}
	for table, defaults := range map[string]map[string]string{
		"sessions":              {"ephemeral": "'0'", "handoff_context_consumed": "'0'"},
		"review_relay_bindings": {"enabled": "'1'"},
	} {
		columns, err := st.QueryRows(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return err
		}
		for _, column := range columns {
			name := column["name"].(string)
			if want, ok := defaults[name]; ok && column["dflt_value"] != want {
				return fmt.Errorf("%s.%s default=%#v want %q", table, name, column["dflt_value"], want)
			}
		}
	}
	return nil
}

func rawSnapshot(t *testing.T, path string) map[string][][]any {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	return rawSnapshotFromDB(t, db)
}

func rawSnapshotFromDB(t *testing.T, db *sql.DB) map[string][][]any {
	t.Helper()
	tableRows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for tableRows.Next() {
		var table string
		if err := tableRows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	_ = tableRows.Close()
	result := make(map[string][][]any, len(tables))
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, _ := rows.Columns()
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(values))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for i, value := range values {
				if bytes, ok := value.([]byte); ok {
					values[i] = string(bytes)
				}
			}
			result[table] = append(result[table], values)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func copyFile(t *testing.T, source, target string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func rawSchemaMetadata(t *testing.T, path string) []map[string]any {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := queryRows(context.Background(), db, `SELECT type,name,tbl_name,sql FROM sqlite_master WHERE type IN ('table','index') AND name NOT LIKE 'sqlite_%' ORDER BY type,name`)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if sqlText, ok := row["sql"].(string); ok {
			row["sql"] = normalizeSchemaSQL(sqlText)
		}
	}
	return rows
}

func normalizeSchemaSQL(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func ids(rows []map[string]any) string {
	result := make([]string, len(rows))
	for i := range rows {
		result[i] = rows[i]["id"].(string)
	}
	return strings.Join(result, ",")
}

func containsIDs(rows []map[string]any, wanted ...string) bool {
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		seen[row["id"].(string)] = true
	}
	sort.Strings(wanted)
	for _, id := range wanted {
		if !seen[id] {
			return false
		}
	}
	return true
}
