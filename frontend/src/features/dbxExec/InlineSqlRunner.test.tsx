import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Session } from '../../domain/types'
import { MarkdownContent } from '../../components/MarkdownContent'
import { useAstrorderStore } from '../../state/store'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import '../sidecar/viewers/dbx/index'
import * as dbxStore from '../sidecar/viewers/dbx/dbxStore'
import { DBX_PLUGIN_ID } from '../sidecar/viewers/dbx/DbxViewer'
import { useProjectDbStore } from './projectDbStore'

const sampleSession: Session = {
  id: 'sess-opscore-1',
  agent_id: 'local-agy',
  title: 'OpsCore Session',
  status: 'idle',
  workspace: '/home/luwei/workspace/OpsCore',
  project_id: 'opscore',
  project_name: 'OpsCore',
  source_id: 'local',
  updated_at: '2026-11-25T05:00:00Z',
}

describe('Inline SQL execution in MarkdownContent', () => {
  beforeEach(() => {
    usePluginSettingsStore.setState({
      enabledPlugins: { [DBX_PLUGIN_ID]: true },
      pluginConfigs: { [DBX_PLUGIN_ID]: { provider: 'dbx' } },
      hydrated: true,
    })
    useAstrorderStore.setState({
      projects: {
        'project:local\u0000opscore': {
          id: 'project:local\u0000opscore',
          source_id: 'local',
          project_id: 'opscore',
          project_name: 'OpsCore',
          workspace: '/home/luwei/workspace/OpsCore',
          session_count: 1,
          updated_at: '2026-11-25T05:00:00Z',
        },
      },
      agents: {},
    })
    useProjectDbStore.setState({
      hydrated: true,
      connections: [],
      loadingBindings: false,
      loadingConnections: false,
      connectionsError: '',
      sessionTargetOverride: {},
      bindings: [
        {
          project_key: 'project:local\u0000opscore',
          workspace_key: 'connection:local\u0000/home/luwei/workspace/OpsCore',
          project_label: 'OpsCore',
          targets: [
            {
              id: 'conn-local::opscore_dev::',
              connection_id: 'conn-local',
              connection_name: 'pg-local',
              db_type: 'postgresql',
              database: 'opscore_dev',
              is_prod: false,
              is_default: true,
            },
            {
              id: 'conn-prod::ops_metadata::',
              connection_id: 'conn-prod',
              connection_name: 'ops@10.20.64.38',
              db_type: 'postgresql',
              database: 'ops_metadata',
              is_prod: true,
              is_default: false,
            },
          ],
        },
      ],
    })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('executes non-PROD read-only SQL block immediately and sends result back to chat', async () => {
    const execSpy = vi.spyOn(api, 'executeDbxSql').mockResolvedValue({
      ok: true,
      connection_id: 'conn-local',
      connection_name: 'pg-local',
      database: 'opscore_dev',
      is_prod: false,
      classification: {
        risk_level: 'read_only',
        statement_count: 1,
        primary_verb: 'SELECT',
        read_only_allowed: true,
      },
      results: [
        {
          index: 0,
          columns: [
            { name: 'version', data_type: 'text' },
            { name: 'checksum', data_type: 'text' },
          ],
          rows: [['007', 'sha256:40432e']],
          row_count: 1,
          elapsed_ms: 9,
        },
      ],
      total_elapsed_ms: 9,
    })

    const sentEvents: Array<CustomEvent<{ text: string }>> = []
    const quotedEvents: Array<CustomEvent<{ text: string }>> = []
    const onSend = (e: Event) => sentEvents.push(e as CustomEvent<{ text: string }>)
    const onQuote = (e: Event) => quotedEvents.push(e as CustomEvent<{ text: string }>)
    window.addEventListener('astrorder:send-to-composer', onSend)
    window.addEventListener('astrorder:quote-to-composer', onQuote)

    try {
      const md = '```sql\nSELECT version, checksum FROM agent_maintenance_schema_migrations;\n```'
      render(
        <MantineProvider>
          <MarkdownContent value={md} session={sampleSession} />
        </MantineProvider>,
      )

      fireEvent.click(screen.getByRole('button', { name: '原地执行此 SQL' }))

      await waitFor(() => {
        expect(execSpy).toHaveBeenCalledTimes(1)
      })
      expect(execSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          connection_id: 'conn-local',
          database: 'opscore_dev',
          confirmed: false,
        }),
        expect.any(AbortSignal),
      )

      expect(await screen.findByText('sha256:40432e')).toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: '填入输入框' }))
      expect(quotedEvents).toHaveLength(1)
      expect(quotedEvents[0].detail.text).toContain('| 007 | sha256:40432e |')

      fireEvent.click(screen.getByRole('button', { name: '发送给当前会话' }))
      expect(sentEvents).toHaveLength(1)
      expect(sentEvents[0].detail.text).toContain('SQL 执行成功')
    } finally {
      window.removeEventListener('astrorder:send-to-composer', onSend)
      window.removeEventListener('astrorder:quote-to-composer', onQuote)
    }
  })

  it('executes PROD pure read-only SQL directly without modal, but requires PROD confirmation for transactions or writes', async () => {
    useProjectDbStore.getState().setSessionTargetOverride('local-agy::sess-opscore-1', 'conn-prod::ops_metadata::')
    const execSpy = vi.spyOn(api, 'executeDbxSql').mockResolvedValue({
      ok: true,
      connection_id: 'conn-prod',
      connection_name: 'ops@10.20.64.38',
      database: 'ops_metadata',
      is_prod: true,
      classification: {
        risk_level: 'read_only',
        statement_count: 1,
        primary_verb: 'SELECT',
        read_only_allowed: true,
      },
      results: [{ index: 0, columns: [{ name: 'ok' }], rows: [[1]], row_count: 1, elapsed_ms: 5 }],
      total_elapsed_ms: 5,
    })

    const { rerender } = render(
      <MantineProvider>
        <MarkdownContent value={'```sql\nSELECT 1 AS ok;\n```'} session={sampleSession} />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '原地执行此 SQL' }))
    await waitFor(() => {
      expect(execSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          connection_id: 'conn-prod',
          database: 'ops_metadata',
          confirmed: false,
        }),
        expect.any(AbortSignal),
      )
    })
    expect(screen.queryByText(/生产环境保护/)).not.toBeInTheDocument()

    execSpy.mockClear()
    rerender(
      <MantineProvider>
        <MarkdownContent value={'```sql\nBEGIN;\nUPDATE t SET a = 1 WHERE id = 1;\nCOMMIT;\n```'} session={sampleSession} />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '原地执行此 SQL' }))
    expect(execSpy).not.toHaveBeenCalled()
    expect(await screen.findByText(/生产环境保护/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '确认在 PROD 执行' }))
    await waitFor(() => {
      expect(execSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          connection_id: 'conn-prod',
          database: 'ops_metadata',
          confirmed: true,
        }),
        expect.any(AbortSignal),
      )
    })
  })

  it('executes .sql file references inline via confirmation modal for DDL migration scripts', async () => {
    vi.spyOn(api, 'getWorkspaceRootFiles').mockResolvedValue({
      root: '/home/luwei/workspace/OpsCore',
      items: [],
    })
    vi.spyOn(api, 'checkWorkspaceFiles').mockResolvedValue({
      existing: ['backend/migrations/007_audit_updated_by.sql'],
    })
    vi.spyOn(dbxStore, 'readSqlText').mockResolvedValue(
      'ALTER TABLE agent_maintenance_nodes ADD COLUMN IF NOT EXISTS updated_by text;',
    )
    const execSpy = vi.spyOn(api, 'executeDbxSql').mockResolvedValue({
      ok: true,
      connection_id: 'conn-local',
      connection_name: 'pg-local',
      database: 'opscore_dev',
      is_prod: false,
      classification: {
        risk_level: 'destructive',
        statement_count: 1,
        primary_verb: 'ALTER',
        read_only_allowed: false,
      },
      results: [{ index: 0, row_count: 0, affected_rows: 0, elapsed_ms: 18 }],
      total_elapsed_ms: 18,
    })

    const md = '1. [007_audit_updated_by.sql](backend/migrations/007_audit_updated_by.sql)'
    render(
      <MantineProvider>
        <MarkdownContent value={md} session={sampleSession} />
      </MantineProvider>,
    )

    const runBtn = screen.getByRole('button', { name: '原地执行 SQL 文件 007_audit_updated_by.sql' })
    fireEvent.click(runBtn)

    expect(await screen.findByText(/高危 DDL/)).toBeInTheDocument()
    const preview = screen.getByRole('region', { name: 'SQL 内容预览' })
    expect(preview).toHaveTextContent(/ALTER TABLE agent_maintenance_nodes/)
    expect(preview.querySelectorAll('.tok-keyword').length).toBeGreaterThanOrEqual(4)
    expect(preview.querySelector('.tok-type')?.textContent).toBe('text')
    expect(preview.querySelectorAll('.sql-confirm-line-no')).toHaveLength(1)
    expect(execSpy).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: '确认执行' }))

    await waitFor(() => {
      expect(execSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          source_kind: 'file',
          source_name: '007_audit_updated_by.sql',
          confirmed: true,
        }),
        expect.any(AbortSignal),
      )
    })
    expect(await screen.findByText('执行成功，影响 0 行。')).toBeInTheDocument()
  })

  it('offers inline SQL execution from the .sql file right-click context menu', async () => {
    vi.spyOn(api, 'getWorkspaceRootFiles').mockResolvedValue({
      root: '/home/luwei/workspace/OpsCore',
      items: [],
    })
    vi.spyOn(api, 'checkWorkspaceFiles').mockResolvedValue({
      existing: ['backend/migrations/check.sql'],
    })
    vi.spyOn(dbxStore, 'readSqlText').mockResolvedValue('SELECT 1 AS ready;')
    const execSpy = vi.spyOn(api, 'executeDbxSql').mockResolvedValue({
      ok: true,
      connection_id: 'conn-local',
      connection_name: 'pg-local',
      database: 'opscore_dev',
      is_prod: false,
      classification: {
        risk_level: 'read_only',
        statement_count: 1,
        primary_verb: 'SELECT',
        read_only_allowed: true,
      },
      results: [{ index: 0, columns: [{ name: 'ready' }], rows: [[1]], row_count: 1, elapsed_ms: 4 }],
      total_elapsed_ms: 4,
    })

    const md = '[check.sql](backend/migrations/check.sql)'
    render(
      <MantineProvider>
        <MarkdownContent value={md} session={sampleSession} />
      </MantineProvider>,
    )

    fireEvent.contextMenu(screen.getByRole('button', { name: 'check.sql' }))
    const execItem = await screen.findByRole('menuitem', { name: '原地执行 SQL' })
    fireEvent.click(execItem)

    await waitFor(() => {
      expect(execSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          source_kind: 'file',
          source_name: 'check.sql',
          confirmed: false,
        }),
        expect.any(AbortSignal),
      )
    })
  })
})
