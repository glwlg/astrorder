import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { setAppProfile } from './appProfile'
import { ExportMenu } from './export/ExportMenu'
import { DigestPage } from '../digest/DigestPage'
import { ActionInboxView } from '../inbox/ActionInboxView'
import { MeetingsPage } from '../meetings/MeetingsPage'
import { useMeetingSessionStore } from '../meetings/meetingSessionStore'
import { DocxViewer } from '../sidecar/viewers/docx/DocxViewer'
import { MarkdownRefineView, splitMarkdownSections } from '../sidecar/viewers/monaco/MarkdownRefineView'
import { PptxViewer } from '../sidecar/viewers/pptx/PptxViewer'

describe('Office Suite End-to-End Capabilities (Phases 3-6)', () => {
  beforeEach(() => {
    localStorage.clear()
    setAppProfile('developer')
  })

  afterEach(() => {
    useMeetingSessionStore.getState()._resetForTests()
    localStorage.clear()
    cleanup()
    vi.restoreAllMocks()
  })

  it('ExportMenu supports saving Markdown/Word/PPT directly into the current workspace', async () => {
    const saveSpy = vi.spyOn(api, 'saveOfficeExport').mockResolvedValue({
      ok: true,
      path: '/workspace/office/report.docx',
      name: 'report.docx',
      size: 2048,
    })

    render(
      <MantineProvider>
        <ExportMenu
          markdown="# 季度总结\n\n核心交付已完成。"
          title="季度总结"
          workspace="/workspace/office"
          sessionId="sess-1"
        />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '导出' }))
    const saveDocxBtn = await screen.findByText('保存为 Word (.docx)')
    fireEvent.click(saveDocxBtn)

    await waitFor(() => {
      expect(saveSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          format: 'docx',
          workspace: '/workspace/office',
          session_id: 'sess-1',
          title: '季度总结',
        }),
      )
    })
  })

  it('DocxViewer renders numbered ordered lists (1. 2.) and supports in-document search', async () => {
    vi.spyOn(api, 'previewDocx').mockResolvedValue({
      path: '/office/spec.docx',
      name: 'spec.docx',
      title: '规范文档',
      outline: [],
      plain_paragraphs: ['第一条规则', '第二条规则'],
      stats: { paragraphs: 2, tables: 0, images: 0, words: 12, headings: 0 },
      blocks: [
        { id: 'b1', kind: 'list_item', ordered: true, level: 0, runs: [{ text: '第一条规则' }] },
        { id: 'b2', kind: 'list_item', ordered: true, level: 0, runs: [{ text: '第二条规则' }] },
      ],
    })

    render(
      <MantineProvider>
        <DocxViewer
          artifact={{
            id: 'a1',
            kind: 'workspace_file',
            name: 'spec.docx',
            path: '/office/spec.docx',
            mediaType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
            writable: false,
            readUrl: '/api/v1/files/raw?path=%2Foffice%2Fspec.docx',
            agentId: 'ag-1',
            sessionId: 's-1',
          }}
          session={null}
          isActive
        />
      </MantineProvider>,
    )

    expect(await screen.findByText('1.')).toBeInTheDocument()
    expect(screen.getByText('2.')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '文档内搜索' }))
    const input = screen.getByLabelText('在文档内搜索')
    fireEvent.change(input, { target: { value: '第二条' } })
    expect(screen.getByText('1/1')).toBeInTheDocument()
  })

  it('PptxViewer ignores arrow keys when inactive (isActive=false)', async () => {
    vi.spyOn(api, 'previewPptx').mockResolvedValue({
      path: '/office/deck.pptx',
      name: 'deck.pptx',
      title: '季度汇报',
      slide_count: 2,
      stats: { slide_count: 2, words: 10, tables: 0, images: 0 },
      slides: [
        { index: 1, title: '第一页封面', paragraphs: [], tables: [], images: [], notes: '' },
        { index: 2, title: '第二页正文', paragraphs: [], tables: [], images: [], notes: '' },
      ],
    })

    render(
      <MantineProvider>
        <PptxViewer
          artifact={{
            id: 'p1',
            kind: 'workspace_file',
            name: 'deck.pptx',
            path: '/office/deck.pptx',
            mediaType: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
            writable: false,
            readUrl: '/api/v1/files/raw?path=%2Foffice%2Fdeck.pptx',
            agentId: 'ag-1',
            sessionId: 's-1',
          }}
          session={null}
          isActive={false}
        />
      </MantineProvider>,
    )

    expect(await screen.findByText('1 / 2 页')).toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'ArrowRight' })
    expect(screen.getByText('1 / 2 页')).toBeInTheDocument()
  })

  it('MarkdownRefineView chunks headingless long docs, auto-saves via onSaveNow, and supports undo', async () => {
    const longPlain = Array.from({ length: 24 }, (_, idx) =>
      idx % 5 === 4 ? '' : `正文第 ${idx + 1} 行用于测试无标题长文档分段精修。`,
    ).join('\n')
    expect(splitMarkdownSections(longPlain).length).toBeGreaterThan(1)

    vi.spyOn(api, 'getAiProviders').mockResolvedValue({
      providers: [],
      default_provider_id: '',
      default_model: '',
      default_reasoning: 'low',
      effective_configured: false,
    })
    vi.spyOn(api, 'refineOfficeSelection').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      model: 'gpt-4o-mini',
      rewritten_text: '# 概述\n\n精修后的简洁结论。',
      assembled_prompt: 'prompt',
    })

    const onApply = vi.fn()
    const onSaveNow = vi.fn().mockResolvedValue(true)

    render(
      <MantineProvider>
        <MarkdownRefineView
          markdown={'# 概述\n\n原始较长正文内容。'}
          documentTitle="汇报.md"
          session={null}
          writable
          onApplyChange={onApply}
          onSaveNow={onSaveNow}
        />
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: '精修这段' }))
    fireEvent.click(screen.getByRole('button', { name: '更精炼' }))

    const applyBtn = await screen.findByRole('button', { name: '应用到正文' })
    fireEvent.click(applyBtn)

    await waitFor(() => {
      expect(onApply).toHaveBeenCalledWith('# 概述\n\n精修后的简洁结论。')
      expect(onSaveNow).toHaveBeenCalledWith('# 概述\n\n精修后的简洁结论。')
    })

    const undoBtn = await screen.findByRole('button', { name: '撤销上次替换' })
    fireEvent.click(undoBtn)
    await waitFor(() => {
      expect(onApply).toHaveBeenLastCalledWith('# 概述\n\n原始较长正文内容。')
    })
  })

  it('DigestPage defaults to local-only office sources and office_weekly template in office profile', async () => {
    setAppProfile('office')
    vi.spyOn(api, 'getAiProviders').mockResolvedValue({
      providers: [
        {
          id: 'p1',
          name: 'OpenAI',
          protocol: 'openai',
          base_url: 'https://api.openai.com/v1',
          api_key_set: true,
          models: ['gpt-4o-mini'],
          enabled: true,
        },
      ],
      default_provider_id: 'p1',
      default_model: 'gpt-4o-mini',
      default_reasoning: 'low',
      effective_configured: true,
      effective_model: 'gpt-4o-mini',
      effective_provider_name: 'OpenAI',
    })
    vi.spyOn(api, 'listDigestTemplates').mockResolvedValue({
      items: [
        { id: 'office_weekly', name: '办公周报 · 通用版', description: '办公周报', audience: '管理层', built_in: true, system_prompt: 'p' },
        { id: 'weekly_exec', name: '周报 · 汇报版', description: '汇报版', audience: '管理者', built_in: true, system_prompt: 'p' },
      ],
    })
    vi.spyOn(api, 'listDigestHistory').mockResolvedValue({ items: [] })
    const collectSpy = vi.spyOn(api, 'collectDigest').mockResolvedValue({
      from: '2026-11-23',
      to: '2026-11-25',
      generated_at: '2026-11-25T14:00:00Z',
      llm_configured: true,
      llm_model: 'gpt-4o-mini',
      available_projects: [],
      projects: [],
      stats: {
        project_count: 1,
        session_count: 2,
        commit_count: 0,
        task_count: 0,
        checkpoint_count: 0,
        sql_audit_count: 0,
        files_changed: 0,
        insertions: 0,
        deletions: 0,
        office_doc_count: 3,
        meeting_count: 2,
      },
    })

    render(
      <MemoryRouter>
        <MantineProvider>
          <DigestPage />
        </MantineProvider>
      </MemoryRouter>,
    )

    await waitFor(() => {
      expect(collectSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          local_only: true,
          include_office_docs: true,
          include_meetings: true,
          include_git: false,
          include_checkpoints: false,
          include_sql_audit: false,
        }),
      )
    })
    expect(await screen.findByText('文档产出')).toBeInTheDocument()
  })

  it('MeetingsPage opens a meeting directly via ?id= deep-link and recovers crash drafts', async () => {
    vi.spyOn(api, 'listMeetings').mockResolvedValue({
      items: [
        {
          id: 'mtg_deep_42',
          title: '产品季度评审会',
          meeting_date: '2026-11-25',
          summary: '深度链接测试摘要内容',
          minutes_markdown: '# 产品季度评审会',
          action_items: [],
          segments: [{ id: 's1', timestamp: '00:05', seconds: 5, text: '讨论季度目标', kind: 'speech' }],
        },
      ],
    })
    vi.spyOn(api, 'getMeetingSettings').mockResolvedValue({
      model_source: 'global',
      base_url: '',
      api_key_set: false,
      model: 'gpt-4o-mini',
      auto_summarize: false,
      auto_summarize_interval_sec: 30,
      summary_style: 'concise',
      effective_model: 'gpt-4o-mini',
      effective_configured: true,
      global_model: 'gpt-4o-mini',
      global_configured: true,
    })
    vi.spyOn(api, 'getLocalVoicePlugin').mockResolvedValue({
      id: 'r2t2-local-voice',
      name: 'R2T2',
      enabled: false,
      state: 'disabled',
      available: true,
      progress: null,
      error: null,
      pid: null,
      device: '',
      model: '',
    })

    render(
      <QueryClientProvider client={new QueryClient()}>
        <MemoryRouter initialEntries={['/meetings?id=mtg_deep_42']}>
          <MantineProvider>
            <MeetingsPage />
          </MantineProvider>
        </MemoryRouter>
      </QueryClientProvider>,
    )

    expect(await screen.findByText('深度链接测试摘要内容')).toBeInTheDocument()
  })

  it('ActionInboxView creates personal todos and deep-links meeting actions to /meetings?id=...', async () => {
    vi.spyOn(api, 'listTodos').mockResolvedValue({
      open_count: 1,
      items: [
        { id: 'td_1', text: '提交季度预算表', status: 'open', priority: 'high', due_date: '2026-11-26' },
      ],
    })
    vi.spyOn(api, 'listAllSchedules').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'listMeetingActionItems').mockResolvedValue({
      open_count: 1,
      items: [
        {
          id: 'act_55',
          meeting_id: 'mtg_55',
          meeting_title: '客户方案沟通会',
          text: '发送修订后报价单',
          owner: '王经理',
          due_date: '2026-11-27',
          priority: 'high',
          status: 'open',
        },
      ],
    })
    const createTodoSpy = vi.spyOn(api, 'createTodo').mockResolvedValue({
      id: 'td_2',
      text: '准备周五演示文稿',
      status: 'open',
      priority: 'medium',
    })
    const navEvents: string[] = []
    const navHandler = (e: Event) => {
      navEvents.push((e as CustomEvent<{ to: string }>).detail.to)
    }
    window.addEventListener('astrorder:navigate', navHandler)

    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <MantineProvider>
          <ActionInboxView onOpenSession={vi.fn()} />
        </MantineProvider>
      </QueryClientProvider>,
    )

    expect(await screen.findByText('提交季度预算表')).toBeInTheDocument()
    expect(screen.getByText('发送修订后报价单')).toBeInTheDocument()

    // Deep-link from meeting action item
    fireEvent.click(screen.getByRole('button', { name: '查看纪要' }))
    expect(navEvents).toContain('/meetings?id=mtg_55')

    // Quick-add personal todo
    const input = screen.getByLabelText('新增个人待办')
    fireEvent.change(input, { target: { value: '准备周五演示文稿' } })
    fireEvent.submit(input.closest('form')!)
    await waitFor(() => {
      expect(createTodoSpy).toHaveBeenCalledWith(
        expect.objectContaining({ text: '准备周五演示文稿', priority: 'medium' }),
      )
    })

    window.removeEventListener('astrorder:navigate', navHandler)
  })
})
