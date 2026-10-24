package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type columnDefinition struct {
	Name       string
	Definition string
}

type tableDefinition struct {
	Name    string
	Create  string
	Columns []columnDefinition
}

var tables = []tableDefinition{
	{Name: "draft_shelves", Create: `CREATE TABLE draft_shelves (
scope TEXT NOT NULL,
revision INTEGER NOT NULL,
entries JSON NOT NULL,
PRIMARY KEY (scope)
)`},
	{Name: "workspace_preferences", Create: `CREATE TABLE workspace_preferences (
	"key" VARCHAR(1100) NOT NULL,
	value JSON,
	PRIMARY KEY ("key")
)`},
	{Name: "agents", Create: `CREATE TABLE agents (
	id VARCHAR(256) NOT NULL,
	kind VARCHAR(32) NOT NULL,
	name VARCHAR(256) NOT NULL,
	status VARCHAR(32) NOT NULL,
	capabilities JSON NOT NULL,
	limitation TEXT,
	source_id VARCHAR(256),
	connection_id VARCHAR(64),
	profile_name VARCHAR(128),
	runtime_id VARCHAR(256),
	control_state VARCHAR(32) NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (id)
)`, Columns: []columnDefinition{
		{Name: "source_id", Definition: "VARCHAR(256)"},
		{Name: "connection_id", Definition: "VARCHAR(64)"},
		{Name: "profile_name", Definition: "VARCHAR(128)"},
		{Name: "runtime_id", Definition: "VARCHAR(256)"},
		{Name: "control_state", Definition: "VARCHAR(32) NOT NULL DEFAULT 'unknown'"},
	}},
	{Name: "agent_connection_choices", Create: `CREATE TABLE agent_connection_choices (
	id VARCHAR(256) NOT NULL,
	enabled INTEGER NOT NULL,
	PRIMARY KEY (id)
)`},
	{Name: "sessions", Create: `CREATE TABLE sessions (
	row_id INTEGER NOT NULL,
	id VARCHAR(256) NOT NULL,
	agent_id VARCHAR(256) NOT NULL,
	title VARCHAR(512) NOT NULL,
	workspace TEXT,
	status VARCHAR(32) NOT NULL,
	source_id VARCHAR(256),
	connection_id VARCHAR(64),
	source_session_id VARCHAR(256),
	project_id VARCHAR(256),
	project_name VARCHAR(512),
	history_state VARCHAR(32) NOT NULL,
	native_kind VARCHAR(32),
	ephemeral BOOLEAN DEFAULT '0' NOT NULL,
	control_state VARCHAR(32) NOT NULL,
	runtime_owner JSON,
	selected_model_provider VARCHAR(160),
	selected_model VARCHAR(160),
	selected_reasoning_effort VARCHAR(32),
	selected_approval_mode VARCHAR(32),
	handoff_from_agent_id VARCHAR(256),
	handoff_from_session_id VARCHAR(256),
	handoff_context TEXT,
	handoff_context_consumed BOOLEAN DEFAULT '0' NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (row_id),
	CONSTRAINT uq_sessions_agent_id UNIQUE (agent_id, id)
)`, Columns: []columnDefinition{
		{Name: "source_id", Definition: "VARCHAR(256)"},
		{Name: "connection_id", Definition: "VARCHAR(64)"},
		{Name: "source_session_id", Definition: "VARCHAR(256)"},
		{Name: "project_id", Definition: "VARCHAR(256)"},
		{Name: "project_name", Definition: "VARCHAR(512)"},
		{Name: "history_state", Definition: "VARCHAR(32) NOT NULL DEFAULT 'local'"},
		{Name: "native_kind", Definition: "VARCHAR(32)"},
		{Name: "ephemeral", Definition: "BOOLEAN NOT NULL DEFAULT 0"},
		{Name: "control_state", Definition: "VARCHAR(32) NOT NULL DEFAULT 'unknown'"},
		{Name: "runtime_owner", Definition: "JSON"},
		{Name: "selected_model_provider", Definition: "VARCHAR(160)"},
		{Name: "selected_model", Definition: "VARCHAR(160)"},
		{Name: "selected_reasoning_effort", Definition: "VARCHAR(32)"},
		{Name: "selected_approval_mode", Definition: "VARCHAR(32)"},
		{Name: "handoff_from_agent_id", Definition: "VARCHAR(256)"},
		{Name: "handoff_from_session_id", Definition: "VARCHAR(256)"},
		{Name: "handoff_context", Definition: "TEXT"},
		{Name: "handoff_context_consumed", Definition: "BOOLEAN NOT NULL DEFAULT 0"},
	}},
	{Name: "projects", Create: `CREATE TABLE projects (
	id VARCHAR(512) NOT NULL,
	source_id VARCHAR(256) NOT NULL,
	connection_id VARCHAR(64),
	agent_id VARCHAR(256),
	profile_name VARCHAR(128),
	project_id VARCHAR(256) NOT NULL,
	project_name VARCHAR(512),
	workspace TEXT,
	session_count INTEGER NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (id)
)`},
	{Name: "attachments", Create: `CREATE TABLE attachments (
	id VARCHAR(256) NOT NULL,
	name VARCHAR(256) NOT NULL,
	media_type VARCHAR(128) NOT NULL,
	size INTEGER NOT NULL,
	storage_name VARCHAR(512) NOT NULL,
	source_path TEXT,
	created_at DATETIME NOT NULL,
	PRIMARY KEY (id),
	UNIQUE (storage_name)
)`, Columns: []columnDefinition{{Name: "source_path", Definition: "TEXT"}}},
	{Name: "messages", Create: `CREATE TABLE messages (
	row_id INTEGER NOT NULL,
	id VARCHAR(256) NOT NULL,
	session_id VARCHAR(256) NOT NULL,
	agent_id VARCHAR(256) NOT NULL,
	role VARCHAR(32) NOT NULL,
	kind VARCHAR(32) NOT NULL,
	text TEXT NOT NULL,
	attachments JSON NOT NULL,
	created_at DATETIME NOT NULL,
	command_id VARCHAR(256),
	tool JSON,
	PRIMARY KEY (row_id),
	CONSTRAINT uq_messages_scope UNIQUE (agent_id, session_id, id)
)`},
	{Name: "commands", Create: `CREATE TABLE commands (
	row_id INTEGER NOT NULL,
	id VARCHAR(256) NOT NULL,
	session_id VARCHAR(256) NOT NULL,
	agent_id VARCHAR(256) NOT NULL,
	action VARCHAR(32) NOT NULL,
	state VARCHAR(32) NOT NULL,
	text TEXT NOT NULL,
	attachments JSON NOT NULL,
	target_id VARCHAR(256),
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	error TEXT,
	payload_hash VARCHAR(128) NOT NULL,
	PRIMARY KEY (row_id),
	CONSTRAINT uq_commands_scope UNIQUE (agent_id, session_id, id)
)`, Columns: []columnDefinition{
		{Name: "payload_hash", Definition: "VARCHAR(128)"},
	}},
	{Name: "tasks", Create: `CREATE TABLE tasks (
	row_id INTEGER NOT NULL,
	id VARCHAR(256) NOT NULL,
	session_id VARCHAR(256) NOT NULL,
	agent_id VARCHAR(256) NOT NULL,
	kind VARCHAR(32) NOT NULL,
	title VARCHAR(512) NOT NULL,
	status VARCHAR(32) NOT NULL,
	progress JSON,
	command TEXT,
	logs JSON NOT NULL,
	target_id VARCHAR(256),
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (row_id),
	CONSTRAINT uq_tasks_scope UNIQUE (agent_id, session_id, id)
)`},
	{Name: "ssh_connections", Create: `CREATE TABLE ssh_connections (
	id VARCHAR(64) NOT NULL,
	display_name VARCHAR(256),
	profile_name VARCHAR(128),
	host VARCHAR(253),
	port INTEGER NOT NULL,
	user VARCHAR(64),
	ssh_config_alias VARCHAR(128),
	identity_file TEXT,
	hermes_path TEXT,
	workspace TEXT,
	state VARCHAR(32) NOT NULL,
	detail TEXT,
	remote_os VARCHAR(64),
	agent_id VARCHAR(256),
	runtime_id VARCHAR(256),
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (id)
)`, Columns: []columnDefinition{
		{Name: "display_name", Definition: "VARCHAR(256)"},
		{Name: "profile_name", Definition: "VARCHAR(128)"},
		{Name: "remote_os", Definition: "VARCHAR(64)"},
		{Name: "agent_id", Definition: "VARCHAR(256)"},
		{Name: "runtime_id", Definition: "VARCHAR(256)"},
	}},
	{Name: "connection_history", Create: `CREATE TABLE connection_history (
	row_id INTEGER NOT NULL,
	id VARCHAR(256) NOT NULL,
	connection_id VARCHAR(64) NOT NULL,
	stage VARCHAR(64) NOT NULL,
	state VARCHAR(32) NOT NULL,
	detail TEXT NOT NULL,
	details JSON NOT NULL,
	created_at DATETIME NOT NULL,
	PRIMARY KEY (row_id),
	UNIQUE (id)
)`},
	{Name: "review_relay_bindings", Create: `CREATE TABLE review_relay_bindings (
	source_agent_id VARCHAR(256) NOT NULL,
	source_session_id VARCHAR(256) NOT NULL,
	review_agent_id VARCHAR(256) NOT NULL,
	review_session_id VARCHAR(256) NOT NULL,
	workspace TEXT NOT NULL,
	enabled BOOLEAN DEFAULT '1' NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (source_agent_id, source_session_id)
)`},
	{Name: "review_relay_runs", Create: `CREATE TABLE review_relay_runs (
	id VARCHAR(64) NOT NULL,
	source_agent_id VARCHAR(256) NOT NULL,
	source_session_id VARCHAR(256) NOT NULL,
	review_agent_id VARCHAR(256) NOT NULL,
	review_session_id VARCHAR(256) NOT NULL,
	command_id VARCHAR(256) NOT NULL,
	baseline_ids JSON NOT NULL,
	status VARCHAR(32) NOT NULL,
	comment_text TEXT,
	error TEXT,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (id)
)`},
	{Name: "events", Create: `CREATE TABLE events (
	cursor INTEGER NOT NULL,
	event_id VARCHAR(256) NOT NULL,
	type VARCHAR(128) NOT NULL,
	agent_id VARCHAR(256),
	session_id VARCHAR(256),
	data JSON NOT NULL,
	created_at DATETIME NOT NULL,
	PRIMARY KEY (cursor),
	CONSTRAINT uq_events_source_id UNIQUE (agent_id, event_id)
)`},
	{Name: "daemon_checkpoints", Create: `CREATE TABLE daemon_checkpoints (
	daemon_id VARCHAR(128) NOT NULL,
	session_id VARCHAR(256) NOT NULL,
	seq_id INTEGER NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (daemon_id, session_id)
)`},
	{Name: "token_metrics", Create: `CREATE TABLE token_metrics (
	row_id INTEGER NOT NULL,
	agent_id VARCHAR(256) NOT NULL,
	session_id VARCHAR(256) NOT NULL,
	model VARCHAR(128) NOT NULL,
	provider VARCHAR(128),
	date VARCHAR(10) NOT NULL,
	input_tokens INTEGER NOT NULL,
	output_tokens INTEGER NOT NULL,
	cached_tokens INTEGER NOT NULL,
	reasoning_tokens INTEGER NOT NULL,
	total_tokens INTEGER NOT NULL,
	context_window INTEGER NOT NULL,
	last_input_tokens INTEGER NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (row_id),
	CONSTRAINT uq_token_metrics_daily UNIQUE (agent_id, session_id, model, date)
)`},
	{Name: "delegation_edges", Create: `CREATE TABLE delegation_edges (
	child_agent_id VARCHAR(256) NOT NULL,
	child_session_id VARCHAR(256) NOT NULL,
	parent_agent_id VARCHAR(256) NOT NULL,
	parent_session_id VARCHAR(256) NOT NULL,
	created_at DATETIME NOT NULL,
	PRIMARY KEY (child_agent_id, child_session_id)
)`},
	{Name: "delegation_operations", Create: `CREATE TABLE delegation_operations (
	operation_id VARCHAR(128) NOT NULL,
	operation_type VARCHAR(64) NOT NULL,
	caller_agent_id VARCHAR(256) NOT NULL,
	caller_session_id VARCHAR(256) NOT NULL,
	target_agent_id VARCHAR(256) NOT NULL,
	target_session_id VARCHAR(256),
	request_hash VARCHAR(128) NOT NULL,
	stage VARCHAR(32) NOT NULL,
	request_payload JSON NOT NULL,
	result_payload JSON,
	error_message TEXT,
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL,
	PRIMARY KEY (operation_id)
)`},
}

var indices = []string{
	"CREATE INDEX IF NOT EXISTS ix_agents_connection_id ON agents (connection_id)",
	"CREATE INDEX IF NOT EXISTS ix_agents_source_id ON agents (source_id)",
	"CREATE INDEX IF NOT EXISTS ix_commands_agent_id ON commands (agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_commands_session_id ON commands (session_id)",
	"CREATE INDEX IF NOT EXISTS ix_connection_history_connection_id ON connection_history (connection_id)",
	"CREATE INDEX IF NOT EXISTS ix_delegation_edges_parent ON delegation_edges (parent_agent_id, parent_session_id)",
	"CREATE INDEX IF NOT EXISTS ix_delegation_operations_caller ON delegation_operations (caller_agent_id, caller_session_id)",
	"CREATE INDEX IF NOT EXISTS ix_events_agent_id ON events (agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_events_session_id ON events (session_id)",
	"CREATE INDEX IF NOT EXISTS ix_messages_agent_id ON messages (agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_messages_session_id ON messages (session_id)",
	"CREATE INDEX IF NOT EXISTS ix_projects_agent_id ON projects (agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_projects_connection_id ON projects (connection_id)",
	"CREATE INDEX IF NOT EXISTS ix_projects_source_id ON projects (source_id)",
	"CREATE INDEX IF NOT EXISTS ix_review_relay_runs_source_agent_id ON review_relay_runs (source_agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_review_relay_runs_source_session_id ON review_relay_runs (source_session_id)",
	"CREATE INDEX IF NOT EXISTS ix_sessions_agent_id ON sessions (agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_sessions_connection_id ON sessions (connection_id)",
	"CREATE INDEX IF NOT EXISTS ix_sessions_handoff_from_agent_id ON sessions (handoff_from_agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_sessions_handoff_from_session_id ON sessions (handoff_from_session_id)",
	"CREATE INDEX IF NOT EXISTS ix_sessions_project_id ON sessions (project_id)",
	"CREATE INDEX IF NOT EXISTS ix_sessions_source_id ON sessions (source_id)",
	"CREATE INDEX IF NOT EXISTS ix_sessions_source_session_id ON sessions (source_session_id)",
	"CREATE INDEX IF NOT EXISTS ix_tasks_agent_id ON tasks (agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_tasks_session_id ON tasks (session_id)",
	"CREATE INDEX IF NOT EXISTS ix_token_metrics_agent_id ON token_metrics (agent_id)",
	"CREATE INDEX IF NOT EXISTS ix_token_metrics_date ON token_metrics (date)",
	"CREATE INDEX IF NOT EXISTS ix_token_metrics_model ON token_metrics (model)",
	"CREATE INDEX IF NOT EXISTS ix_token_metrics_session_id ON token_metrics (session_id)",
}

func migrateSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	defer tx.Rollback()
	version, err := schemaVersion(ctx, tx)
	if err != nil {
		return err
	}
	if version > CurrentSchemaVersion {
		return fmt.Errorf("schema version %d is newer than supported version %d", version, CurrentSchemaVersion)
	}
	for _, table := range tables {
		if _, err := tx.ExecContext(ctx, table.Create); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "already exists") {
				return fmt.Errorf("create table %s: %w", table.Name, err)
			}
		}
		present, err := tableColumns(ctx, tx, table.Name)
		if err != nil {
			return err
		}
		for _, column := range table.Columns {
			if present[column.Name] {
				continue
			}
			statement := fmt.Sprintf(`ALTER TABLE "%s" ADD COLUMN "%s" %s`, table.Name, column.Name, column.Definition)
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("add %s.%s: %w", table.Name, column.Name, err)
			}
		}
	}
	if err := applyBackfills(ctx, tx); err != nil {
		return err
	}
	if version < 4 {
		if err := migrateNativeIdentity(ctx, tx); err != nil {
			return err
		}
		if err := rebuildCanonicalTables(ctx, tx); err != nil {
			return err
		}
	}
	for _, statement := range indices {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create index: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", CurrentSchemaVersion)); err != nil {
		return fmt.Errorf("set schema version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema migration: %w", err)
	}
	return validateSchema(ctx, db)
}

func rebuildCanonicalTables(ctx context.Context, tx *sql.Tx) error {
	for _, table := range tables {
		temporary := "__astrorder_migrate_" + table.Name
		if _, err := tx.ExecContext(ctx, `ALTER TABLE "`+table.Name+`" RENAME TO "`+temporary+`"`); err != nil {
			return fmt.Errorf("rebuild table %s rename: %w", table.Name, err)
		}
		if _, err := tx.ExecContext(ctx, table.Create); err != nil {
			return fmt.Errorf("rebuild table %s create: %w", table.Name, err)
		}
		columns := requiredColumns(table)
		quoted := make([]string, len(columns))
		for i, column := range columns {
			quoted[i] = `"` + strings.ReplaceAll(column, `"`, `""`) + `"`
		}
		columnList := strings.Join(quoted, ",")
		if _, err := tx.ExecContext(ctx, `INSERT INTO "`+table.Name+`" (`+columnList+`) SELECT `+columnList+` FROM "`+temporary+`"`); err != nil {
			return fmt.Errorf("rebuild table %s copy: %w", table.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `DROP TABLE "`+temporary+`"`); err != nil {
			return fmt.Errorf("rebuild table %s drop old: %w", table.Name, err)
		}
	}
	return nil
}

func validateSchema(ctx context.Context, db *sql.DB) error {
	version, err := schemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if version != CurrentSchemaVersion {
		return fmt.Errorf("schema version %d does not match required version %d", version, CurrentSchemaVersion)
	}
	for _, table := range tables {
		actual, err := tableDefinitionShapeOf(ctx, db, table.Name)
		if err != nil {
			return err
		}
		if len(actual.Columns) == 0 {
			return fmt.Errorf("schema validation: missing table %s", table.Name)
		}
		expected, err := expectedTableDefinition(table)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("schema validation: table %s definition mismatch", table.Name)
		}
	}
	for _, statement := range indices {
		name := strings.Fields(statement)[5]
		actual, err := indexShape(ctx, db, name)
		if err != nil {
			return err
		}
		expected, err := expectedIndexShape(statement)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(actual, expected) {
			return fmt.Errorf("schema validation: index %s definition mismatch", name)
		}
	}
	return nil
}

type tableColumnShape struct {
	Name       string
	Type       string
	NotNull    bool
	Default    string
	HasDefault bool
	PrimaryKey int
}

type tableDefinitionShape struct {
	Columns    []tableColumnShape
	UniqueKeys [][]string
}

type indexDefinitionShape struct {
	Unique  bool
	Partial bool
	Columns []string
}

func tableDefinitionShapeOf(ctx context.Context, db queryer, table string) (tableDefinitionShape, error) {
	columns, err := tableShape(ctx, db, table)
	if err != nil {
		return tableDefinitionShape{}, err
	}
	result := tableDefinitionShape{Columns: columns, UniqueKeys: make([][]string, 0)}
	rows, err := db.QueryContext(ctx, `PRAGMA index_list("`+strings.ReplaceAll(table, `"`, `""`)+`")`)
	if err != nil {
		return tableDefinitionShape{}, err
	}
	type uniqueIndex struct {
		name string
	}
	uniqueIndices := make([]uniqueIndex, 0)
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			return tableDefinitionShape{}, err
		}
		if unique != 0 && origin == "u" {
			uniqueIndices = append(uniqueIndices, uniqueIndex{name: name})
		}
	}
	if err := rows.Close(); err != nil {
		return tableDefinitionShape{}, err
	}
	for _, index := range uniqueIndices {
		shape, err := indexShape(ctx, db, index.name)
		if err != nil {
			return tableDefinitionShape{}, err
		}
		result.UniqueKeys = append(result.UniqueKeys, shape.Columns)
	}
	sort.Slice(result.UniqueKeys, func(i, j int) bool {
		return strings.Join(result.UniqueKeys[i], "\x00") < strings.Join(result.UniqueKeys[j], "\x00")
	})
	return result, nil
}

func tableShape(ctx context.Context, db queryer, table string) ([]tableColumnShape, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_xinfo("`+strings.ReplaceAll(table, `"`, `""`)+`")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]tableColumnShape, 0)
	for rows.Next() {
		var cid, notNull, primaryKey, hidden int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey, &hidden); err != nil {
			return nil, err
		}
		if hidden != 0 {
			continue
		}
		shape := tableColumnShape{Name: name, Type: strings.ToUpper(strings.TrimSpace(columnType)), NotNull: notNull != 0, PrimaryKey: primaryKey}
		if defaultValue != nil {
			shape.HasDefault = true
			shape.Default = fmt.Sprint(defaultValue)
		}
		result = append(result, shape)
	}
	return result, rows.Err()
}

func indexShape(ctx context.Context, db queryer, name string) (indexDefinitionShape, error) {
	var unique, partial int
	var tableName string
	list, err := db.QueryContext(ctx, "SELECT tbl_name FROM sqlite_master WHERE type='index' AND name=?", name)
	if err != nil {
		return indexDefinitionShape{}, err
	}
	if list.Next() {
		err = list.Scan(&tableName)
	} else if err = list.Err(); err == nil {
		err = fmt.Errorf("schema validation: missing index %s", name)
	}
	_ = list.Close()
	if err != nil {
		return indexDefinitionShape{}, err
	}
	indexRows, err := db.QueryContext(ctx, `PRAGMA index_list("`+strings.ReplaceAll(tableName, `"`, `""`)+`")`)
	if err != nil {
		return indexDefinitionShape{}, err
	}
	found := false
	for indexRows.Next() {
		var sequence int
		var candidate, origin string
		if err := indexRows.Scan(&sequence, &candidate, &unique, &origin, &partial); err != nil {
			_ = indexRows.Close()
			return indexDefinitionShape{}, err
		}
		if candidate == name {
			found = true
			break
		}
	}
	_ = indexRows.Close()
	if !found {
		return indexDefinitionShape{}, fmt.Errorf("schema validation: missing index %s", name)
	}
	columns, err := db.QueryContext(ctx, `PRAGMA index_xinfo("`+strings.ReplaceAll(name, `"`, `""`)+`")`)
	if err != nil {
		return indexDefinitionShape{}, err
	}
	defer columns.Close()
	type indexColumn struct {
		sequence int
		name     string
	}
	ordered := make([]indexColumn, 0)
	for columns.Next() {
		var sequence, cid, descending, key int
		var columnName, collating any
		if err := columns.Scan(&sequence, &cid, &columnName, &descending, &collating, &key); err != nil {
			return indexDefinitionShape{}, err
		}
		if key == 0 {
			continue
		}
		ordered = append(ordered, indexColumn{sequence: sequence, name: fmt.Sprint(columnName)})
	}
	if err := columns.Err(); err != nil {
		return indexDefinitionShape{}, err
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].sequence < ordered[j].sequence })
	result := indexDefinitionShape{Unique: unique != 0, Partial: partial != 0, Columns: make([]string, len(ordered))}
	for i := range ordered {
		result.Columns[i] = ordered[i].name
	}
	return result, nil
}

func expectedTableDefinition(table tableDefinition) (tableDefinitionShape, error) {
	db, err := sql.Open("sqlite", "file::memory:?mode=memory")
	if err != nil {
		return tableDefinitionShape{}, err
	}
	defer db.Close()
	if _, err := db.Exec(table.Create); err != nil {
		return tableDefinitionShape{}, fmt.Errorf("build expected table %s: %w", table.Name, err)
	}
	return tableDefinitionShapeOf(context.Background(), db, table.Name)
}

func expectedIndexShape(statement string) (indexDefinitionShape, error) {
	fields := strings.Fields(statement)
	name := fields[5]
	open := strings.LastIndex(statement, "(")
	close := strings.LastIndex(statement, ")")
	if open < 0 || close <= open {
		return indexDefinitionShape{}, fmt.Errorf("invalid canonical index %s", name)
	}
	columns := strings.Split(statement[open+1:close], ",")
	for i := range columns {
		columns[i] = strings.Trim(strings.TrimSpace(columns[i]), `"`)
	}
	return indexDefinitionShape{Columns: columns}, nil
}

func schemaVersion(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func tableColumns(ctx context.Context, db queryer, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info("`+strings.ReplaceAll(table, `"`, `""`)+`")`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

func requiredColumns(table tableDefinition) []string {
	open := strings.IndexByte(table.Create, '(')
	close := strings.LastIndexByte(table.Create, ')')
	if open < 0 || close <= open {
		return nil
	}
	result := make([]string, 0)
	for _, line := range strings.Split(table.Create[open+1:close], "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, ","))
		upper := strings.ToUpper(line)
		if line == "" || strings.HasPrefix(upper, "PRIMARY KEY") || strings.HasPrefix(upper, "UNIQUE") || strings.HasPrefix(upper, "CONSTRAINT") || strings.HasPrefix(upper, "FOREIGN KEY") {
			continue
		}
		name := strings.Trim(strings.Fields(line)[0], `"`)
		result = append(result, name)
	}
	return result
}

func applyBackfills(ctx context.Context, tx *sql.Tx) error {
	statements := []string{
		"UPDATE agents SET source_id = id WHERE source_id IS NULL OR source_id = ''",
		"UPDATE agents SET runtime_id = id WHERE runtime_id IS NULL OR runtime_id = ''",
		"UPDATE sessions SET source_id = (SELECT source_id FROM agents WHERE agents.id = sessions.agent_id) WHERE source_id IS NULL OR source_id = ''",
		"UPDATE sessions SET source_session_id = id WHERE source_session_id IS NULL OR source_session_id = ''",
		"UPDATE sessions SET title = substr(title, 1, 512) WHERE length(title) > 512",
		"UPDATE sessions SET ephemeral = 1 WHERE title = '[侧边聊天]' AND ephemeral = 0",
		"UPDATE ssh_connections SET profile_name = 'default' WHERE profile_name IS NULL OR profile_name = ''",
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("schema backfill: %w", err)
		}
	}
	if err := backfillCommandHashes(ctx, tx); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM ssh_connections WHERE id='default'").Scan(&count); err != nil {
		return err
	}
	if count == 1 {
		id, err := randomID("ssh-", 24)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE ssh_connections SET id=? WHERE id='default'", id); err != nil {
			return err
		}
	}
	return nil
}

func commandHash(row map[string]any, sessionID string) (string, error) {
	attachmentIDs := make([]any, 0)
	if attachments, ok := row["attachments"].([]any); ok {
		for _, item := range attachments {
			if object, ok := item.(map[string]any); ok {
				attachmentIDs = append(attachmentIDs, object["id"])
			}
		}
	}
	return payloadHash(map[string]any{
		"id": row["id"], "agent_id": row["agent_id"], "session_id": sessionID,
		"action": row["action"], "text": row["text"], "attachment_ids": attachmentIDs,
		"target_id": row["target_id"],
	})
}

func backfillCommandHashes(ctx context.Context, tx *sql.Tx) error {
	rows, err := queryRows(ctx, tx, "SELECT * FROM commands WHERE payload_hash IS NULL OR payload_hash='' ORDER BY row_id")
	if err != nil {
		return err
	}
	for _, row := range rows {
		hash, err := commandHash(row, row["session_id"].(string))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE commands SET payload_hash=? WHERE row_id=?", hash, row["row_id"]); err != nil {
			return err
		}
	}
	return nil
}

func migrateNativeIdentity(ctx context.Context, tx *sql.Tx) error {
	rows, err := queryRows(ctx, tx, `SELECT * FROM sessions WHERE id LIKE 'history-%' AND source_session_id IS NOT NULL AND source_session_id != '' AND source_session_id != id ORDER BY row_id`)
	if err != nil {
		return err
	}
	for _, old := range rows {
		agentID := old["agent_id"].(string)
		oldID := old["id"].(string)
		nativeID := old["source_session_id"].(string)
		var targetCount int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sessions WHERE agent_id=? AND id=?", agentID, nativeID).Scan(&targetCount); err != nil {
			return err
		}
		for _, table := range []string{"messages", "commands", "tasks"} {
			children, err := queryRows(ctx, tx, fmt.Sprintf("SELECT * FROM %s WHERE agent_id=? AND session_id=? ORDER BY row_id", table), agentID, oldID)
			if err != nil {
				return err
			}
			for _, child := range children {
				id := child["id"].(string)
				var duplicateCount int
				if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE agent_id=? AND session_id=? AND id=?", table), agentID, nativeID, id).Scan(&duplicateCount); err != nil {
					return err
				}
				if duplicateCount > 0 {
					return fmt.Errorf("native identity migration conflict in %s", table)
				}
				if table == "commands" {
					hash, err := commandHash(child, nativeID)
					if err != nil {
						return err
					}
					if _, err := tx.ExecContext(ctx, "UPDATE commands SET session_id=?,payload_hash=? WHERE agent_id=? AND session_id=? AND id=?", nativeID, hash, agentID, oldID, id); err != nil {
						return err
					}
				} else if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET session_id=? WHERE agent_id=? AND session_id=? AND id=?", table), nativeID, agentID, oldID, id); err != nil {
					return err
				}
			}
		}
		events, err := queryRows(ctx, tx, "SELECT cursor,type,data FROM events WHERE agent_id=? AND session_id=? ORDER BY cursor", agentID, oldID)
		if err != nil {
			return err
		}
		for _, event := range events {
			data, _ := event["data"].(map[string]any)
			if data["session_id"] == oldID {
				data["session_id"] = nativeID
			}
			if strings.HasPrefix(event["type"].(string), "session.") && data["id"] == oldID {
				data["id"] = nativeID
			}
			raw, err := encodeJSON(data)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE events SET session_id=?,data=? WHERE cursor=?", nativeID, raw, event["cursor"]); err != nil {
				return err
			}
		}
		if targetCount == 0 {
			if _, err := tx.ExecContext(ctx, "UPDATE sessions SET id=?,control_state='native' WHERE agent_id=? AND id=?", nativeID, agentID, oldID); err != nil {
				return err
			}
		} else {
			_, err := tx.ExecContext(ctx, `UPDATE sessions SET
 title=CASE WHEN title IS NULL OR title='' OR title=id THEN ? ELSE title END,
 project_id=coalesce(project_id,?),project_name=coalesce(project_name,?),workspace=coalesce(workspace,?),control_state='native'
 WHERE agent_id=? AND id=?`, old["title"], old["project_id"], old["project_name"], old["workspace"], agentID, nativeID)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE agent_id=? AND id=?", agentID, oldID); err != nil {
				return err
			}
		}
	}
	return nil
}

var errNotFound = errors.New("not found")
