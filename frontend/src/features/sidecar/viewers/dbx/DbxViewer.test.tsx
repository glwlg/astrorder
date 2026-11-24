import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MarkdownContent } from '../../../../components/MarkdownContent'
import { MarkdownLinkMenu } from '../../../../components/MarkdownLinkMenu'
import { usePluginSettingsStore } from '../../../plugins/pluginSettingsStore'
import { useSidecarStore } from '../../sidecarStore'
import { tryInjectSqlSync } from './dbxBridge'
import { useDbxSidecarStore } from './dbxStore'
import { DbxViewer } from './DbxViewer'
import { extractSqlFileName, extractSqlLine, isSqlFilePath, stripSqlLocationSuffix } from './sqlFormats'

describe('sqlFormats', () => {
  it('recognizes sql migration paths with line/hash suffixes and rejects non-sql files', () => {
    expect(isSqlFilePath('backend/migrations/opscore/001_init.sql')).toBe(true)
    expect(isSqlFilePath('/home/luwei/workspace/OpsCore/002_seed.SQL:42')).toBe(true)
    expect(isSqlFilePath('schema/views.psql#L12')).toBe(true)
    expect(isSqlFilePath('backend/main.go')).toBe(false)
    expect(isSqlFilePath('README.md')).toBe(false)
    expect(stripSqlLocationSuffix('backend/001_init.sql:15-20')).toBe('backend/001_init.sql')
    expect(extractSqlLine('backend/001_init.sql:15-20')).toBe(15)
    expect(extractSqlLine('backend/001_init.sql#L28')).toBe(28)
    expect(extractSqlFileName('backend/migrations/001_init.sql:10')).toBe('001_init.sql')
  })
})

describe('dbxBridge tryInjectSqlSync', () => {
  it('creates an editable query tab inheriting active connection without executing', () => {
    const createdTabs: Array<Record<string, unknown>> = []
    const queryStore = {
      tabs: [
        {
          id: 'tab-existing',
          mode: 'query',
          title: 'active.sql',
          connectionId: 'conn-prod',
          database: 'opscore',
          schema: 'public',
          catalog: undefined,
          sql: 'SELECT 1;',
          isExecuting: false,
        },
      ],
      activeTabId: 'tab-existing',
      isOpenTabsLoaded: true,
      activateTab: vi.fn(),
      createTab: vi.fn((connectionId, database, title, mode, schema, sql, catalog, options) => {
        const next = {
          id: 'tab-new-1',
          connectionId,
          database,
          title,
          mode,
          schema,
          sql,
          catalog,
          options,
          isExecuting: false,
        }
        createdTabs.push(next)
        queryStore.tabs.push(next as (typeof queryStore.tabs)[number])
        return next.id
      }),
    }

    const rootEl = document.createElement('div') as HTMLDivElement & { __vue_app__?: unknown }
    rootEl.id = 'root'
    rootEl.__vue_app__ = {
      config: {
        globalProperties: {
          $pinia: {
            _s: new Map([['query', queryStore]]),
          },
        },
      },
    }
    const fakeDoc = { getElementById: (id: string) => (id === 'root' ? rootEl : null) }
    const fakeIframe = {
      contentWindow: {},
      contentDocument: fakeDoc,
    } as unknown as HTMLIFrameElement

    const first = tryInjectSqlSync(fakeIframe, {
      filename: '001_add_col.sql',
      path: 'backend/migrations/001_add_col.sql',
      content: 'ALTER TABLE users ADD COLUMN role TEXT;',
      line: 1,
    })

    expect(first).toEqual({
      ok: true,
      tabId: 'tab-new-1',
      reused: false,
      connectionBound: true,
    })
    expect(queryStore.createTab).toHaveBeenCalledWith(
      'conn-prod',
      'opscore',
      '001_add_col.sql',
      'query',
      'public',
      'ALTER TABLE users ADD COLUMN role TEXT;',
      undefined,
      { forceNew: true },
    )
    expect(createdTabs[0].isExecuting).toBe(false)

    // Sending the same filename again reuses and updates the existing tab instead of duplicating.
    const second = tryInjectSqlSync(fakeIframe, {
      filename: '001_add_col.sql',
      path: 'backend/migrations/001_add_col.sql',
      content: 'ALTER TABLE users ADD COLUMN role TEXT NOT NULL;',
    })
    expect(second?.ok).toBe(true)
    expect(second?.reused).toBe(true)
    expect(queryStore.activateTab).toHaveBeenCalledWith('tab-new-1')
    expect(queryStore.tabs.find((t) => t.id === 'tab-new-1')?.sql).toBe(
      'ALTER TABLE users ADD COLUMN role TEXT NOT NULL;',
    )
  })
})

describe('DbxViewer & MarkdownLinkMenu', () => {
  const originalFetch = globalThis.fetch

  beforeEach(() => {
    usePluginSettingsStore.getState().reset()
    useDbxSidecarStore.setState({ itemsBySession: {}, pendingRequest: null, nextRequestId: 1 })
  })

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('renders same-origin DBX iframe and SQL checklist when reachable', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          configured: true,
          upstream_url: 'http://127.0.0.1:14224',
          base_path: '/api/v1/plugins/dbx',
          reachable: true,
          authenticated: true,
          password_required: false,
          setup_required: false,
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ) as unknown as typeof fetch

    useDbxSidecarStore.getState().enqueueSql('sess-1', {
      path: '/home/luwei/workspace/OpsCore/backend/migrations/001_init.sql',
      filename: '001_init.sql',
      content: 'SELECT 1;',
      agentId: 'codex',
    })

    render(
      <MantineProvider>
        <DbxViewer
          artifact={{
            id: 'dbx:workbench',
            name: 'DBX 数据库',
            kind: 'workspace_file',
            mediaType: 'application/x-astrorder-dbx',
            readUrl: '/api/v1/plugins/dbx/',
            writable: false,
            sessionId: 'sess-1',
            agentId: 'codex',
          }}
        />
      </MantineProvider>,
    )

    const iframe = await screen.findByTitle('数据库工作台')
    expect(iframe.getAttribute('src')).toContain('/api/v1/plugins/dbx/')
    expect(screen.getByText('001_init.sql')).toBeTruthy()

    const toggleBtn = screen.getByRole('button', { name: '标记 001_init.sql 已执行' })
    expect(toggleBtn.getAttribute('aria-pressed')).toBe('false')
    fireEvent.click(toggleBtn)
    await waitFor(() => {
      expect(screen.getByRole('button', { name: '取消标记 001_init.sql 已执行' }).getAttribute('aria-pressed')).toBe('true')
    })
  })

  it('shows helpful wslc fallback card when DBX upstream is unreachable', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          configured: true,
          upstream_url: 'http://127.0.0.1:14224',
          base_path: '/api/v1/plugins/dbx',
          reachable: false,
          authenticated: false,
          password_required: false,
          setup_required: false,
          error: '无法连接 DBX 服务',
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    ) as unknown as typeof fetch

    render(
      <MantineProvider>
        <DbxViewer
          artifact={{
            id: 'dbx:workbench',
            name: 'DBX 数据库',
            kind: 'workspace_file',
            mediaType: 'application/x-astrorder-dbx',
            readUrl: '/api/v1/plugins/dbx/',
            writable: false,
            sessionId: 'sess-2',
            agentId: 'codex',
          }}
        />
      </MantineProvider>,
    )

    expect(await screen.findByText('数据库服务暂不可用')).toBeTruthy()
    expect(screen.getByText(/wslc run -d --name astrorder-dbx/)).toBeTruthy()
  })

  it('shows right-click open in DBX item only for sql files in MarkdownLinkMenu', async () => {
    const sampleSession = {
      id: 'sess-sql',
      agent_id: 'codex',
      title: 'OpsCore',
      workspace: '/home/luwei/workspace/OpsCore',
      status: 'idle' as const,
      updated_at: '2026-11-24T00:00:00Z',
    }
    const { rerender } = render(
      <MantineProvider>
        <MarkdownLinkMenu
          target={{ x: 100, y: 100, kind: 'file', value: '/home/luwei/workspace/OpsCore/001_test.sql' }}
          session={sampleSession}
          onClose={() => {}}
        />
      </MantineProvider>,
    )

    expect(await screen.findByText('在数据库工具中打开')).toBeTruthy()
    expect(screen.getByText('在代码编辑器中打开')).toBeTruthy()
    expect(screen.queryByText(/DBX/i)).toBeNull()

    rerender(
      <MantineProvider>
        <MarkdownLinkMenu
          target={{ x: 100, y: 100, kind: 'file', value: '/home/luwei/workspace/OpsCore/main.go' }}
          session={sampleSession}
          onClose={() => {}}
        />
      </MantineProvider>,
    )

    await waitFor(() => {
      expect(screen.queryByText('在数据库工具中打开')).toBeNull()
    })
  })

  it('opens sql links directly in database tool on left click when plugin is enabled, and falls back to Monaco when disabled', async () => {
    const sampleSession = {
      id: 'sess-click',
      agent_id: 'codex',
      title: 'OpsCore',
      workspace: '/home/luwei/workspace/OpsCore',
      status: 'idle' as const,
      updated_at: '2026-11-24T00:00:00Z',
    }
    useSidecarStore.setState({
      isOpen: false,
      activeTabId: '',
      tabs: [],
      dirtyTabs: {},
      activeSessionKey: 'codex:sess-click',
      sessionMemories: {},
    })
    globalThis.fetch = vi.fn().mockImplementation((input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes('/api/v1/files/raw')) {
        return Promise.resolve(
          new Response('ALTER TABLE ops ADD COLUMN enabled BOOLEAN;', {
            status: 200,
            headers: { 'Content-Type': 'text/plain' },
          }),
        )
      }
      return Promise.resolve(
        new Response(JSON.stringify({ existing: [], entries: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )
    }) as unknown as typeof fetch

    render(
      <MantineProvider>
        <MarkdownContent
          value="请执行 [001_add_enabled.sql](backend/migrations/001_add_enabled.sql)"
          session={sampleSession}
        />
      </MantineProvider>,
    )

    // 1. Plugin enabled (default): clicking .sql opens database workbench directly
    fireEvent.click(screen.getByRole('button', { name: '001_add_enabled.sql' }))
    await waitFor(() => {
      expect(useSidecarStore.getState().activeTabId).toBe('database:workbench')
    })
    const queued = useDbxSidecarStore.getState().itemsBySession['sess-click'] || []
    expect(queued).toHaveLength(1)
    expect(queued[0].filename).toBe('001_add_enabled.sql')
    expect(queued[0].content).toBe('ALTER TABLE ops ADD COLUMN enabled BOOLEAN;')

    // 2. Plugin disabled: clicking .sql opens Monaco editor instead
    usePluginSettingsStore.setState({ enabledPlugins: { 'dbx-viewer': false } })
    fireEvent.click(screen.getByRole('button', { name: '001_add_enabled.sql' }))
    await waitFor(() => {
      const state = useSidecarStore.getState()
      const active = state.tabs.find((t) => t.id === state.activeTabId)
      expect(active?.viewerId).toBe('monaco-viewer')
    })
  })

  it('adds a generic database tool button (without DBX branding) to fenced SQL code blocks when enabled', async () => {
    const sampleSession = {
      id: 'sess-sql-block',
      agent_id: 'codex',
      title: 'OpsCore',
      workspace: '/home/luwei/workspace/OpsCore',
      status: 'idle' as const,
      updated_at: '2026-11-24T00:00:00Z',
    }
    useSidecarStore.setState({
      isOpen: false,
      activeTabId: '',
      tabs: [],
      dirtyTabs: {},
      activeSessionKey: 'codex:sess-sql-block',
      sessionMemories: {},
    })

    const sqlSnippet = [
      'SELECT version, checksum',
      'FROM agent_maintenance_schema_migrations',
      "WHERE module = 'agent-maintenance'",
      'ORDER BY version;',
    ].join('\n')

    const { rerender } = render(
      <MantineProvider>
        <MarkdownContent
          value={`\`\`\`sql\n${sqlSnippet}\n\`\`\`\n\n\`\`\`ts\nconst x = 1\n\`\`\``}
          session={sampleSession}
        />
      </MantineProvider>,
    )

    const openButtons = screen.getAllByRole('button', { name: '在数据库工具中打开此 SQL' })
    expect(openButtons).toHaveLength(1)
    expect(openButtons[0].textContent).toContain('数据库打开')
    expect(openButtons[0].textContent).not.toMatch(/dbx/i)

    fireEvent.click(openButtons[0])
    await waitFor(() => {
      expect(useSidecarStore.getState().activeTabId).toBe('database:workbench')
    })
    const queued = useDbxSidecarStore.getState().itemsBySession['sess-sql-block'] || []
    expect(queued).toHaveLength(1)
    expect(queued[0].filename).toBe('select_agent_maintenance_schema_migrations.sql')
    expect(queued[0].content).toBe(sqlSnippet)
    expect(queued[0].isSnippet).toBe(true)

    usePluginSettingsStore.setState({ enabledPlugins: { 'dbx-viewer': false } })
    rerender(
      <MantineProvider>
        <MarkdownContent
          value={`\`\`\`sql\n${sqlSnippet}\n\`\`\``}
          session={sampleSession}
        />
      </MantineProvider>,
    )
    expect(screen.queryByRole('button', { name: '在数据库工具中打开此 SQL' })).toBeNull()
  })
})
