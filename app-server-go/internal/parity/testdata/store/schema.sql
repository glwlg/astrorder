-- Generated from a fresh isolated Python Store; sqlite_master SQL.
-- No production lifespan or database was opened.

CREATE TABLE agent_connection_choices (
	id VARCHAR(256) NOT NULL, 
	enabled INTEGER NOT NULL, 
	PRIMARY KEY (id)
);

CREATE TABLE agents (
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
);

CREATE TABLE attachments (
	id VARCHAR(256) NOT NULL, 
	name VARCHAR(256) NOT NULL, 
	media_type VARCHAR(128) NOT NULL, 
	size INTEGER NOT NULL, 
	storage_name VARCHAR(512) NOT NULL, 
	source_path TEXT, 
	created_at DATETIME NOT NULL, 
	PRIMARY KEY (id), 
	UNIQUE (storage_name)
);

CREATE TABLE commands (
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
);

CREATE TABLE connection_history (
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
);

CREATE TABLE daemon_checkpoints (
	daemon_id VARCHAR(128) NOT NULL, 
	session_id VARCHAR(256) NOT NULL, 
	seq_id INTEGER NOT NULL, 
	updated_at DATETIME NOT NULL, 
	PRIMARY KEY (daemon_id, session_id)
);

CREATE TABLE events (
	cursor INTEGER NOT NULL, 
	event_id VARCHAR(256) NOT NULL, 
	type VARCHAR(128) NOT NULL, 
	agent_id VARCHAR(256), 
	session_id VARCHAR(256), 
	data JSON NOT NULL, 
	created_at DATETIME NOT NULL, 
	PRIMARY KEY (cursor), 
	CONSTRAINT uq_events_source_id UNIQUE (agent_id, event_id)
);

CREATE TABLE messages (
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
);

CREATE TABLE projects (
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
);

CREATE TABLE review_relay_bindings (
	source_agent_id VARCHAR(256) NOT NULL, 
	source_session_id VARCHAR(256) NOT NULL, 
	review_agent_id VARCHAR(256) NOT NULL, 
	review_session_id VARCHAR(256) NOT NULL, 
	workspace TEXT NOT NULL, 
	enabled BOOLEAN DEFAULT '1' NOT NULL, 
	updated_at DATETIME NOT NULL, 
	PRIMARY KEY (source_agent_id, source_session_id)
);

CREATE TABLE review_relay_runs (
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
);

CREATE TABLE sessions (
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
);

CREATE TABLE ssh_connections (
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
);

CREATE TABLE tasks (
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
);

CREATE TABLE token_metrics (
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
);

CREATE TABLE workspace_preferences (
	"key" VARCHAR(1100) NOT NULL, 
	value JSON, 
	PRIMARY KEY ("key")
);

CREATE INDEX ix_agents_connection_id ON agents (connection_id);

CREATE INDEX ix_agents_source_id ON agents (source_id);

CREATE INDEX ix_commands_agent_id ON commands (agent_id);

CREATE INDEX ix_commands_session_id ON commands (session_id);

CREATE INDEX ix_connection_history_connection_id ON connection_history (connection_id);

CREATE INDEX ix_events_agent_id ON events (agent_id);

CREATE INDEX ix_events_session_id ON events (session_id);

CREATE INDEX ix_messages_agent_id ON messages (agent_id);

CREATE INDEX ix_messages_session_id ON messages (session_id);

CREATE INDEX ix_projects_agent_id ON projects (agent_id);

CREATE INDEX ix_projects_connection_id ON projects (connection_id);

CREATE INDEX ix_projects_source_id ON projects (source_id);

CREATE INDEX ix_review_relay_runs_source_agent_id ON review_relay_runs (source_agent_id);

CREATE INDEX ix_review_relay_runs_source_session_id ON review_relay_runs (source_session_id);

CREATE INDEX ix_sessions_agent_id ON sessions (agent_id);

CREATE INDEX ix_sessions_connection_id ON sessions (connection_id);

CREATE INDEX ix_sessions_handoff_from_agent_id ON sessions (handoff_from_agent_id);

CREATE INDEX ix_sessions_handoff_from_session_id ON sessions (handoff_from_session_id);

CREATE INDEX ix_sessions_project_id ON sessions (project_id);

CREATE INDEX ix_sessions_source_id ON sessions (source_id);

CREATE INDEX ix_sessions_source_session_id ON sessions (source_session_id);

CREATE INDEX ix_tasks_agent_id ON tasks (agent_id);

CREATE INDEX ix_tasks_session_id ON tasks (session_id);

CREATE INDEX ix_token_metrics_agent_id ON token_metrics (agent_id);

CREATE INDEX ix_token_metrics_date ON token_metrics (date);

CREATE INDEX ix_token_metrics_model ON token_metrics (model);

CREATE INDEX ix_token_metrics_session_id ON token_metrics (session_id);
