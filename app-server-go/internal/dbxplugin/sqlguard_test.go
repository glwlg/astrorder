package dbxplugin

import "testing"

func TestClassifySQL(t *testing.T) {
	tests := []struct {
		name            string
		sql             string
		wantRisk        string
		wantStmts       int
		wantReadAllowed bool
	}{
		{
			name:            "simple select",
			sql:             "SELECT version, checksum FROM agent_maintenance_schema_migrations WHERE module = 'agent-maintenance';",
			wantRisk:        RiskReadOnly,
			wantStmts:       1,
			wantReadAllowed: true,
		},
		{
			name:            "select with comments and semicolon inside string",
			sql:             "-- check row\n/* multi\nline */ SELECT 'a;b;DROP TABLE x' AS val FROM t;",
			wantRisk:        RiskReadOnly,
			wantStmts:       1,
			wantReadAllowed: true,
		},
		{
			name:            "select with dollar-quoted semicolon",
			sql:             "SELECT $tag$hello; DROP TABLE users;$tag$ AS txt;",
			wantRisk:        RiskReadOnly,
			wantStmts:       1,
			wantReadAllowed: true,
		},
		{
			name:            "read-only CTE",
			sql:             "WITH recent AS (SELECT id FROM logs LIMIT 10) SELECT * FROM recent;",
			wantRisk:        RiskReadOnly,
			wantStmts:       1,
			wantReadAllowed: true,
		},
		{
			name:            "plain explain select",
			sql:             "EXPLAIN SELECT * FROM users WHERE id = 1;",
			wantRisk:        RiskReadOnly,
			wantStmts:       1,
			wantReadAllowed: true,
		},
		{
			name:            "explain analyze requires confirmation",
			sql:             "EXPLAIN ANALYZE SELECT * FROM users;",
			wantRisk:        RiskWrite,
			wantStmts:       1,
			wantReadAllowed: false,
		},
		{
			name:            "select for update requires confirmation",
			sql:             "SELECT * FROM jobs WHERE id = 1 FOR UPDATE;",
			wantRisk:        RiskWrite,
			wantStmts:       1,
			wantReadAllowed: false,
		},
		{
			name:            "select into requires confirmation",
			sql:             "SELECT * INTO backup_users FROM users;",
			wantRisk:        RiskWrite,
			wantStmts:       1,
			wantReadAllowed: false,
		},
		{
			name:            "data modifying CTE",
			sql:             "WITH del AS (DELETE FROM logs WHERE id = 1 RETURNING *) SELECT * FROM del;",
			wantRisk:        RiskWrite,
			wantStmts:       1,
			wantReadAllowed: false,
		},
		{
			name:            "multi-statement pure select is read only",
			sql:             "SELECT 1; SELECT 2;",
			wantRisk:        RiskReadOnly,
			wantStmts:       2,
			wantReadAllowed: true,
		},
		{
			name:            "transaction block with select requires confirmation",
			sql:             "BEGIN; SELECT 1; COMMIT;",
			wantRisk:        RiskWrite,
			wantStmts:       3,
			wantReadAllowed: false,
		},
		{
			name:            "start transaction requires confirmation",
			sql:             "START TRANSACTION; SELECT version FROM migrations;",
			wantRisk:        RiskWrite,
			wantStmts:       2,
			wantReadAllowed: false,
		},
		{
			name:            "update with where is write",
			sql:             "UPDATE users SET active = true WHERE id = 42;",
			wantRisk:        RiskWrite,
			wantStmts:       1,
			wantReadAllowed: false,
		},
		{
			name:            "update without where is destructive",
			sql:             "UPDATE users SET active = false;",
			wantRisk:        RiskDestructive,
			wantStmts:       1,
			wantReadAllowed: false,
		},
		{
			name:            "delete without where is destructive",
			sql:             "DELETE FROM users;",
			wantRisk:        RiskDestructive,
			wantStmts:       1,
			wantReadAllowed: false,
		},
		{
			name:            "alter table is destructive",
			sql:             "ALTER TABLE agent_maintenance_nodes ADD COLUMN updated_by text;",
			wantRisk:        RiskDestructive,
			wantStmts:       1,
			wantReadAllowed: false,
		},
		{
			name:            "multi-statement migration with alter and insert",
			sql:             "ALTER TABLE t ADD COLUMN c int;\nINSERT INTO schema_migrations VALUES ('007');",
			wantRisk:        RiskDestructive,
			wantStmts:       2,
			wantReadAllowed: false,
		},
		{
			name:            "empty sql is not read only",
			sql:             "   -- only comment\n",
			wantRisk:        RiskWrite,
			wantStmts:       0,
			wantReadAllowed: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifySQL(tc.sql)
			if got.RiskLevel != tc.wantRisk {
				t.Fatalf("RiskLevel = %q, want %q (full=%+v)", got.RiskLevel, tc.wantRisk, got)
			}
			if got.StatementCount != tc.wantStmts {
				t.Fatalf("StatementCount = %d, want %d", got.StatementCount, tc.wantStmts)
			}
			if got.ReadOnlyAllowed != tc.wantReadAllowed {
				t.Fatalf("ReadOnlyAllowed = %v, want %v", got.ReadOnlyAllowed, tc.wantReadAllowed)
			}
		})
	}
}
