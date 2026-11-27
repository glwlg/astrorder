import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type OfficePersonaProfile } from '../../api/client'
import type { Agent, Session } from '../../domain/types'
import { useSidecarStore } from '../sidecar/sidecarStore'
import {
  countDocumentStats,
  markdownToRichHtml,
  richHtmlToMarkdown,
} from '../sidecar/viewers/monaco/markdownHtmlCodec'
import { SheetViewer } from '../sidecar/viewers/sheet/SheetViewer'
import { OfficeHomeDashboard } from './OfficeHomeDashboard'
import { OPEN_ONBOARDING_WIZARD_EVENT } from './OnboardingWizard'
import { OfficePersonaCard } from './OfficePersonaCard'
import { OfficeSourcesBar, type PinnedOfficeSource } from './OfficeSourcesBar'
import { OfficeTemplateModal } from './OfficeTemplateModal'

const SAMPLE_PERSONA: OfficePersonaProfile = {
  display_name: '林思远',
  role: '战略运营总监',
  department: '数字化运营部',
  default_audience: '集团执委会',
  writing_style: 'concise_exec',
  tone_rules: '结论先行，关键数据附带同比环比',
  glossary: '星序、OKR、留存率',
  avoid_words: '内部测试机',
  auto_inject_chat: true,
}

const SAMPLE_SESSION: Session = {
  id: 'sess-office-1',
  agent_id: 'agent-1',
  title: 'Q4 经营复盘',
  workspace: '/workspace/office-q4',
  connection_id: 'local',
  status: 'idle',
  updated_at: '2026-11-25T10:00:00Z',
}

const SAMPLE_AGENTS: Record<string, Agent> = {
  'agent-1': {
    id: 'agent-1',
    name: '办公助手',
    kind: 'codex',
    status: 'ready',
    capabilities: ['chat', 'attachments'],
    limitation: null,
    connection_id: 'local',
  },
}

function wrapWithProviders(node: React.ReactNode) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return (
    <QueryClientProvider client={qc}>
      <MantineProvider>
        <MemoryRouter>{node}</MemoryRouter>
      </MantineProvider>
    </QueryClientProvider>
  )
}

describe('Native Office Workstation — 5 Pillars', () => {
  beforeEach(() => {
    localStorage.clear()
    useSidecarStore.setState({
      isOpen: false,
      activeTabId: '',
      tabs: [],
    })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('round-trips Markdown <-> Rich HTML accurately with tables, formatting, stats, and XSS link guard', () => {
    const md = [
      '# 2026 年度经营汇报',
      '',
      '本季度核心指标 **同比增长 24%**，*利润率* 提升 ~~2%~~ <u>3.5%</u>。',
      '',
      '> 重点关注华东与华南区交付节奏。',
      '',
      '- 事项一：完成政企签约',
      '- 事项二：[查看官网](https://example.com) 与 [危险脚本](javascript:alert(1))',
      '',
      '| 区域 | 销售额 |',
      '| --- | --- |',
      '| 华东 | 120万 |',
      '| 华南 | 95万 |',
    ].join('\n')

    const html = markdownToRichHtml(md)
    expect(html).toContain('<h1>2026 年度经营汇报</h1>')
    expect(html).toContain('<strong>同比增长 24%</strong>')
    expect(html).toContain('<em>利润率</em>')
    expect(html).toContain('<u>3.5%</u>')
    expect(html).toContain('<table>')
    expect(html).toContain('https://example.com')
    expect(html).not.toContain('javascript:alert')

    const back = richHtmlToMarkdown(html)
    expect(back).toContain('# 2026 年度经营汇报')
    expect(back).toContain('**同比增长 24%**')
    expect(back).toContain('| 区域 | 销售额 |')
    expect(back).toContain('| 华东 | 120万 |')

    const stats = countDocumentStats(md)
    expect(stats.words).toBeGreaterThan(20)
    expect(stats.minutes).toBeGreaterThanOrEqual(1)
  })

  it('loads and saves personal OfficePersonaCard preferences', async () => {
    vi.spyOn(api, 'getOfficeProfile').mockResolvedValue(SAMPLE_PERSONA)
    const saveSpy = vi.spyOn(api, 'saveOfficeProfile').mockImplementation(async (p) => p)

    render(wrapWithProviders(<OfficePersonaCard />))

    await waitFor(() => {
      expect((screen.getByLabelText('你的称呼 / 姓名') as HTMLInputElement).value).toBe('林思远')
    })

    fireEvent.change(screen.getByLabelText('岗位与职责'), { target: { value: '业务运营 VP' } })
    fireEvent.click(screen.getByRole('button', { name: /保存画像/ }))

    await waitFor(() => {
      expect(saveSpy).toHaveBeenCalledTimes(1)
    })
    expect(saveSpy.mock.calls[0][0].role).toBe('业务运营 VP')
    expect(saveSpy.mock.calls[0][0].display_name).toBe('林思远')
  })

  it('prefills persona in OfficeTemplateModal and supports both send-to-chat and direct studio generation', async () => {
    vi.spyOn(api, 'getOfficeProfile').mockResolvedValue(SAMPLE_PERSONA)
    const synthSpy = vi.spyOn(api, 'synthesizeOfficeSources').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      model: 'gpt-4o',
      title: 'Q4 渠道拓展方案',
      markdown: '# Q4 渠道拓展方案\n\n## 一、核心结论\n预计带来 30% 新增线索。',
      assembled_prompt: '',
      sources: [],
    })
    const onSendToChat = vi.fn()
    const onGeneratedDocument = vi.fn()

    render(
      wrapWithProviders(
        <OfficeTemplateModal
          opened
          initialTemplateId="proposal"
          workspace="/workspace/office-q4"
          onClose={() => {}}
          onSendToChat={onSendToChat}
          onGeneratedDocument={onGeneratedDocument}
        />,
      ),
    )

    await waitFor(() => {
      expect((screen.getByLabelText(/汇报对象/) as HTMLInputElement).value).toBe('集团执委会')
    })

    fireEvent.change(screen.getByLabelText(/方案主题/), { target: { value: 'Q4 渠道拓展方案' } })
    fireEvent.change(screen.getByLabelText(/核心目标与预期收益/), {
      target: { value: '拓展华东区行业伙伴并提升续费率' },
    })

    // 1. Send to chat
    fireEvent.click(screen.getByRole('button', { name: /在对话中起草/ }))
    await waitFor(() => {
      expect(onSendToChat).toHaveBeenCalledTimes(1)
    })
    expect(onSendToChat.mock.calls[0][0]).toContain('Q4 渠道拓展方案')
    expect(onSendToChat.mock.calls[0][0]).toContain('集团执委会')

    // 2. Direct synthesize into Rich Document Studio
    fireEvent.click(screen.getByRole('button', { name: /直接生成到写作台/ }))
    await waitFor(() => {
      expect(synthSpy).toHaveBeenCalledTimes(1)
      expect(onGeneratedDocument).toHaveBeenCalledTimes(1)
    })
    expect(onGeneratedDocument.mock.calls[0][0].markdown).toContain('预计带来 30% 新增线索')
  })

  it('pins multi-document sources in OfficeSourcesBar and synthesizes cross-comparison into Sidecar Studio', async () => {
    const pinned: PinnedOfficeSource[] = [
      { path: '/workspace/office-q4/方案A.docx', name: '方案A.docx', ext: 'docx' },
      { path: '/workspace/office-q4/预算表.xlsx', name: '预算表.xlsx', ext: 'xlsx' },
    ]
    const synthSpy = vi.spyOn(api, 'synthesizeOfficeSources').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      title: '多文档交叉对比',
      markdown: '# 交叉对比结论\n\n- [资料 1] 目标明确\n- [资料 2] 预算充足',
      assembled_prompt: '',
      sources: [],
    })

    render(
      wrapWithProviders(
        <OfficeSourcesBar
          session={SAMPLE_SESSION}
          pinnedSources={pinned}
          onChangeSources={() => {}}
          onSendPromptToChat={() => {}}
          onOpenTemplateModal={() => {}}
        />,
      ),
    )

    expect(screen.getByText('方案A.docx')).toBeTruthy()
    expect(screen.getByText('预算表.xlsx')).toBeTruthy()

    fireEvent.click(screen.getByRole('button', { name: /交叉对比/ }))
    await waitFor(() => {
      expect(synthSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          paths: ['/workspace/office-q4/方案A.docx', '/workspace/office-q4/预算表.xlsx'],
          action: 'compare',
        }),
      )
    })

    await waitFor(() => {
      const sidecar = useSidecarStore.getState()
      expect(sidecar.isOpen).toBe(true)
      const docTab = sidecar.tabs.find((t) => t.artifact?.metadata?.preferRich)
      expect(docTab?.artifact?.metadata?.inlineContent).toContain('交叉对比结论')
    })
  })

  it('renders OfficeHomeDashboard with persona greeting, live recent docs, todos, and quick templates', async () => {
    vi.spyOn(api, 'getOfficeProfile').mockResolvedValue(SAMPLE_PERSONA)
    vi.spyOn(api, 'listRecentOfficeDocs').mockResolvedValue({
      items: [
        {
          path: '/workspace/office-q4/年度规划.docx',
          name: '年度规划.docx',
          ext: 'docx',
          size: 48200,
          mod_time: '2026-11-25T09:30:00Z',
          workspace: '/workspace/office-q4',
          project_name: 'Q4 经营',
        },
      ],
    })
    vi.spyOn(api, 'listTodos').mockResolvedValue({
      items: [
        {
          id: 'todo-1',
          text: '确认下周客户评审 PPT',
          status: 'open',
          priority: 'high',
          due_date: '2026-11-26',
          created_at: '2026-11-25T08:00:00Z',
        },
      ],
      open_count: 1,
    })
    vi.spyOn(api, 'listMeetingActionItems').mockResolvedValue({ items: [], open_count: 0 })
    vi.spyOn(api, 'listMeetings').mockResolvedValue({ items: [] })

    render(
      wrapWithProviders(
        <OfficeHomeDashboard sessions={[SAMPLE_SESSION]} agents={SAMPLE_AGENTS} />,
      ),
    )

    await waitFor(() => {
      expect(screen.getByText(/林思远/)).toBeTruthy()
      expect(screen.getByText('确认下周客户评审 PPT')).toBeTruthy()
      expect(screen.getByText('年度规划.docx')).toBeTruthy()
    })

    // 8 office templates rendered on home dashboard
    expect(screen.getByText('方案立项')).toBeTruthy()
    expect(screen.getByText('PPT 大纲')).toBeTruthy()
  })

  it('opens generated template document in inline writing/preview workbench instead of swallowing it when no session or agent exists', async () => {
    vi.spyOn(api, 'getOfficeProfile').mockResolvedValue(SAMPLE_PERSONA)
    vi.spyOn(api, 'listRecentOfficeDocs').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'listTodos').mockResolvedValue({ items: [], open_count: 0 })
    vi.spyOn(api, 'listMeetingActionItems').mockResolvedValue({ items: [], open_count: 0 })
    vi.spyOn(api, 'listMeetings').mockResolvedValue({ items: [] })
    vi.spyOn(api, 'synthesizeOfficeSources').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      model: 'gpt-4o',
      title: '年度渠道扩展计划',
      markdown: '# 年度渠道扩展计划\n\n核心目标：华东区新增 50 家核心伙伴。',
      assembled_prompt: '',
      sources: [],
    })

    const wizardSpy = vi.fn()
    window.addEventListener(OPEN_ONBOARDING_WIZARD_EVENT, wizardSpy)

    render(wrapWithProviders(<OfficeHomeDashboard sessions={[]} agents={{}} />))

    fireEvent.click(screen.getByText('方案立项'))
    fireEvent.change(await screen.findByLabelText(/方案主题/), { target: { value: '年度渠道扩展计划' } })
    fireEvent.change(screen.getByLabelText(/核心目标与预期收益/), { target: { value: '华东区新增 50 家核心伙伴' } })
    fireEvent.click(screen.getByRole('button', { name: /直接生成到写作台/ }))

    // Generated markdown MUST appear in the inline writing/preview modal rather than being silently dropped
    expect(await screen.findByText('写作预览台')).toBeInTheDocument()
    expect(screen.getByText(/核心目标：华东区新增 50 家核心伙伴/)).toBeInTheDocument()
    const onboardingBtn = screen.getByRole('button', { name: /前往初始化向导/ })
    fireEvent.click(onboardingBtn)
    expect(wizardSpy).toHaveBeenCalledTimes(1)
    window.removeEventListener(OPEN_ONBOARDING_WIZARD_EVENT, wizardSpy)
  })

  it('renders Excel visual charts (Bar + Donut), pivot summary table, and AI analysis prompts in SheetViewer', async () => {
    vi.spyOn(api, 'previewSheet').mockResolvedValue({
      path: '/workspace/office-q4/sales.xlsx',
      name: 'sales.xlsx',
      format: 'xlsx',
      encoding: 'UTF-8',
      sheets: [
        {
          name: '区域销售',
          columns: ['区域', '销售额'],
          rows: [
            ['华东', 300],
            ['华东', 200],
            ['华南', 350],
            ['华北', 150],
          ],
          total_rows: 4,
          total_cols: 2,
          truncated: false,
        },
      ],
    })

    const quotedEvents: CustomEvent[] = []
    const listener = (e: Event) => quotedEvents.push(e as CustomEvent)
    window.addEventListener('astrorder:quote-to-composer', listener)

    render(
      wrapWithProviders(
        <SheetViewer
          artifact={{
            id: 'sheet-studio-1',
            sessionId: 'sess-office-1',
            agentId: 'agent-1',
            name: 'sales.xlsx',
            path: '/workspace/office-q4/sales.xlsx',
            kind: 'workspace_file',
            readUrl: '/api/v1/files/raw?path=%2Fworkspace%2Foffice-q4%2Fsales.xlsx',
            mediaType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
            writable: false,
          }}
        />,
      ),
    )

    await waitFor(() => {
      expect(screen.getByText('350')).toBeTruthy()
    })

    // 1. Switch to Visual Chart view -> renders Bar SVG + switch to Donut SVG
    fireEvent.click(screen.getByRole('button', { name: '可视化图表' }))
    await waitFor(() => {
      expect(screen.getByRole('img', { name: '柱状图' })).toBeTruthy()
      expect(screen.getByText('1,000')).toBeTruthy()
    })

    fireEvent.click(screen.getByRole('button', { name: '环形图' }))
    await waitFor(() => {
      expect(screen.getByRole('img', { name: '环形图' })).toBeTruthy()
    })

    // Send chart insight to chat
    fireEvent.click(screen.getByRole('button', { name: /带结论发给对话/ }))
    expect(quotedEvents).toHaveLength(1)
    expect(quotedEvents[0].detail.text).toContain('华东')
    expect(quotedEvents[0].detail.text).toContain('50.0%')

    // 2. Switch to Pivot Summary table
    fireEvent.click(screen.getByRole('button', { name: '透视汇总' }))
    await waitFor(() => {
      expect(screen.getByRole('table', { name: '透视汇总：区域' })).toBeTruthy()
      expect(screen.getByText('总计（3 组）')).toBeTruthy()
    })

    window.removeEventListener('astrorder:quote-to-composer', listener)
  })
})
