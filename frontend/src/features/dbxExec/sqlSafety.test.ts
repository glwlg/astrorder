import { describe, expect, it } from 'vitest'
import {
  classifySql,
  formatSqlResultAsJson,
  formatSqlResultAsMarkdown,
  requiresExecutionConfirmation,
} from './sqlSafety'
import type { DbxExecuteResult } from '../../api/client'

describe('sqlSafety', () => {
  it('classifies single read-only queries accurately and handles comments/quotes', () => {
    const simple = classifySql(
      "-- verify migration\nSELECT version, checksum FROM agent_maintenance_schema_migrations WHERE module = 'a;DROP TABLE x';",
    )
    expect(simple.risk_level).toBe('read_only')
    expect(simple.statement_count).toBe(1)
    expect(simple.read_only_allowed).toBe(true)

    const cte = classifySql('WITH latest AS (SELECT * FROM runs LIMIT 5) SELECT * FROM latest;')
    expect(cte.risk_level).toBe('read_only')
    expect(cte.read_only_allowed).toBe(true)

    const dollar = classifySql('SELECT $q$hello; DELETE FROM users;$q$ AS note;')
    expect(dollar.risk_level).toBe('read_only')
    expect(dollar.read_only_allowed).toBe(true)
  })

  it('allows multi-statement pure read-only queries and flags transaction/write/DDL statements', () => {
    expect(classifySql('SELECT 1; SELECT 2;').read_only_allowed).toBe(true)
    expect(classifySql('BEGIN; SELECT 1; COMMIT;').read_only_allowed).toBe(false)
    expect(classifySql('START TRANSACTION; SELECT 1;').read_only_allowed).toBe(false)
    expect(classifySql('EXPLAIN ANALYZE SELECT * FROM users;').read_only_allowed).toBe(false)
    expect(classifySql('SELECT * FROM jobs FOR UPDATE;').read_only_allowed).toBe(false)
    expect(classifySql('WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d;').read_only_allowed).toBe(false)

    const alter = classifySql('ALTER TABLE nodes ADD COLUMN updated_by text;')
    expect(alter.risk_level).toBe('destructive')
    expect(alter.read_only_allowed).toBe(false)

    const noWhereDelete = classifySql('DELETE FROM nodes;')
    expect(noWhereDelete.risk_level).toBe('destructive')
  })

  it('enforces confirmation policy: pure read-only direct (including PROD), transaction/write/DDL must confirm', () => {
    const readSql = 'SELECT version FROM migrations;'
    const txSql = 'BEGIN; SELECT version FROM migrations; COMMIT;'
    const writeSql = "INSERT INTO migrations VALUES ('007');"
    expect(requiresExecutionConfirmation(readSql, false)).toBe(false)
    expect(requiresExecutionConfirmation(readSql, true)).toBe(false)
    expect(requiresExecutionConfirmation(txSql, false)).toBe(true)
    expect(requiresExecutionConfirmation(txSql, true)).toBe(true)
    expect(requiresExecutionConfirmation(writeSql, false)).toBe(true)
    expect(requiresExecutionConfirmation(writeSql, true)).toBe(true)
  })

  it('formats query results cleanly as Markdown and JSON', () => {
    const result: DbxExecuteResult = {
      ok: true,
      connection_id: 'conn-prod',
      connection_name: 'ops@10.20.64.38',
      database: 'ops_metadata',
      is_prod: true,
      classification: classifySql('SELECT version, checksum FROM migrations;'),
      results: [
        {
          index: 0,
          columns: [
            { name: 'version', data_type: 'text' },
            { name: 'checksum', data_type: 'text' },
          ],
          rows: [['007', 'sha256:abc']],
          row_count: 1,
          elapsed_ms: 12,
        },
      ],
      total_elapsed_ms: 12,
    }

    const md = formatSqlResultAsMarkdown({ result, sourceLabel: 'check.sql' })
    expect(md).toContain('ops@10.20.64.38 / ops_metadata [PROD]')
    expect(md).toContain('来源：`check.sql`')
    expect(md).toContain('| version | checksum |')
    expect(md).toContain('| 007 | sha256:abc |')

    const parsedJson = JSON.parse(formatSqlResultAsJson(result))
    expect(parsedJson.rows).toEqual([{ version: '007', checksum: 'sha256:abc' }])
  })
})
