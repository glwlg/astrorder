import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { learningApi, type LearningOverviewResponse, type LearningTopic } from '../../api/learning'
import { LearningRadarPage } from './LearningRadarPage'

const mockNavigate = vi.fn()

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom')
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  }
})

function sampleTopic(overrides: Partial<LearningTopic> = {}): LearningTopic {
  return {
    id: 'topic-1',
    fingerprint: 'fp-1',
    title: '为什么移除 -s -w 能大幅降低 Windows Defender 误报？',
    category: 'blackbox_code',
    priority: 'high',
    status: 'open',
    estimated_minutes: 3,
    project_name: 'astrorder',
    workspace: '/home/luwei/workspace/glwlg/astrorder',
    why_now: '你在构建脚本中调整了 Go ldflags 参数以规避 Defender 误报。',
    key_points: [
      '-s -w 会剥离符号表与 DWARF 调试段，导致 PE 熵值升高。',
      '无 EV 签名二进制被剥离后更容易命中启发式检测。',
      '保留符号表仅增加少量体积即可明显降低误报率。',
    ],
    quiz_questions: [
      {
        question: '为什么可以保留 -trimpath 而不加 -s -w？',
        hint: '想想路径脱敏与符号表剥离的区别。',
        answer: '-trimpath 只清洗源码路径字符串，不破坏符号表结构与节区熵值特征。',
      },
    ],
    evidence: [
      {
        kind: 'file',
        label: 'scripts/desktop-wails.mjs',
        file_path: 'scripts/desktop-wails.mjs',
        project_name: 'astrorder',
      },
      {
        kind: 'session',
        label: '会话「怎么会报毒啊」',
        agent_id: 'antigravity',
        session_id: 'sess-123',
      },
    ],
    occurrence_count: 2,
    review: {
      stage: 0,
      interval_days: 0,
      pass_count: 0,
      fuzzy_count: 0,
    },
    created_at: '2026-11-26T10:00:00Z',
    updated_at: '2026-11-26T10:00:00Z',
    ...overrides,
  }
}

function sampleOverview(overrides: Partial<LearningOverviewResponse> = {}): LearningOverviewResponse {
  return {
    config: {
      mentor_bot_id: 'bot-mentor',
      enabled: true,
      weekday: 0,
      time: '20:00',
      timezone: 'Asia/Shanghai',
      lookback_days: 7,
      misfire_catchup_hours: 48,
      notify_dynamic_island: true,
      min_activity_sessions: 1,
      min_activity_commits: 1,
      excluded_workspaces: [],
      signal_sources: {
        sessions: true,
        git: true,
        failures: true,
        meetings: true,
      },
      next_run_at: '2026-11-29T12:00:00Z',
    },
    mentor_bot: {
      id: 'bot-mentor',
      name: '成长导师',
      slug: 'learning-mentor',
      description: '代码实战型成长导师',
      workspace: '/home/luwei/.astrorder/bots/learning-mentor',
      default_agent_id: 'antigravity',
      has_agents_md: true,
      skills: [],
      session_count: 1,
      running_count: 0,
      created_at: '2026-11-26T00:00:00Z',
      updated_at: '2026-11-26T00:00:00Z',
    },
    stats: {
      pending_count: 1,
      blackbox_count: 1,
      friction_count: 0,
      failure_count: 0,
      new_domain_count: 0,
      due_review_count: 1,
      learning_count: 0,
      mastered_count: 0,
      dismissed_count: 0,
      project_count: 1,
      weekly_new_count: 1,
    },
    topics: [
      sampleTopic(),
      sampleTopic({
        id: 'topic-review-1',
        title: 'React 19 useSyncExternalStore 跨标签页同步',
        status: 'review_due',
        review: {
          stage: 2,
          interval_days: 3,
          next_review_at: '2026-11-26T08:00:00Z',
          pass_count: 1,
          fuzzy_count: 0,
        },
      }),
    ],
    runs: [],
    ...overrides,
  }
}

function renderPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/learning']}>
        <MantineProvider>
          <LearningRadarPage />
        </MantineProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

describe('LearningRadarPage', () => {
  beforeEach(() => {
    mockNavigate.mockReset()
    vi.spyOn(api, 'listBots').mockResolvedValue({ items: [] })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('shows 1-click Mentor Bot bootstrap banner when no bot is bound', async () => {
    vi.spyOn(learningApi, 'getOverview').mockResolvedValue(
      sampleOverview({
        mentor_bot: undefined,
        config: {
          ...sampleOverview().config,
          mentor_bot_id: '',
        },
        topics: [],
        stats: {
          ...sampleOverview().stats,
          pending_count: 0,
          due_review_count: 0,
        },
      }),
    )
    const bootstrapSpy = vi.spyOn(learningApi, 'bootstrapMentorBot').mockResolvedValue({
      ok: true,
      created: true,
      bot: sampleOverview().mentor_bot!,
      config: sampleOverview().config,
    })

    renderPage()

    const bootstrapBtn = await screen.findByRole('button', { name: /一键创建官方成长导师 Bot/ })
    fireEvent.click(bootstrapBtn)

    await waitFor(() => {
      expect(bootstrapSpy).toHaveBeenCalledTimes(1)
    })
  })

  it('renders radar topics, triggers Mentor Bot study session, and navigates to chat', async () => {
    vi.spyOn(learningApi, 'getOverview').mockResolvedValue(sampleOverview())
    const startStudySpy = vi.spyOn(learningApi, 'startStudy').mockResolvedValue({
      ok: true,
      topic: sampleTopic({ status: 'learning' }),
      agent_id: 'antigravity',
      session_id: 'study-sess-9',
      session_key: 'antigravity::study-sess-9',
    })

    renderPage()

    expect((await screen.findAllByText(/为什么移除 -s -w 能大幅降低 Windows Defender 误报/)).length).toBeGreaterThan(0)
    const studyBtn = screen.getAllByRole('button', { name: /用我的代码讲给我听/ })[0]
    fireEvent.click(studyBtn)

    await waitFor(() => {
      expect(startStudySpy).toHaveBeenCalledWith('topic-1', {
        mode: 'study',
        sync_session_schedule: true,
      })
      expect(mockNavigate).toHaveBeenCalledWith('/chat/study-sess-9?agent_id=antigravity')
    })
  })

  it('submits spaced-repetition review feedback in Review Queue tab', async () => {
    vi.spyOn(learningApi, 'getOverview').mockResolvedValue(sampleOverview())
    const reviewSpy = vi.spyOn(learningApi, 'submitReview').mockResolvedValue({
      ok: true,
      topic: sampleTopic({ id: 'topic-review-1', status: 'learning' }),
      message: '已记录复习成果',
    })

    renderPage()

    const reviewTab = await screen.findByRole('button', { name: /复习队列/ })
    fireEvent.click(reviewTab)

    expect((await screen.findAllByText(/React 19 useSyncExternalStore 跨标签页同步/)).length).toBeGreaterThan(0)
    const rememberedBtn = screen.getByRole('button', { name: /记住了/ })
    fireEvent.click(rememberedBtn)

    await waitFor(() => {
      expect(reviewSpy).toHaveBeenCalledWith('topic-review-1', {
        outcome: 'remembered',
        sync_session_schedule: true,
      })
    })
  })

  it('triggers manual Bot scan from topbar', async () => {
    vi.spyOn(learningApi, 'getOverview').mockResolvedValue(sampleOverview())
    const triggerSpy = vi.spyOn(learningApi, 'triggerScan').mockResolvedValue({
      ok: true,
      run: {
        id: 'scan-99',
        trigger: 'manual',
        status: 'running',
        lookback_days: 7,
        started_at: '2026-11-27T01:00:00Z',
        evidence_count: 12,
        active_sessions: 3,
        commit_count: 5,
        new_topics_count: 0,
        merged_topics_count: 0,
      },
      message: '已派发成长导师 Bot',
    })

    renderPage()

    await screen.findByText(/@learning-mentor/)
    const scanBtn = screen.getByRole('button', { name: /立即扫描/ })
    fireEvent.click(scanBtn)

    await waitFor(() => {
      expect(triggerSpy).toHaveBeenCalledWith({ lookback_days: 7 })
    })
  })

  it('edits schedule time and toggles options in Schedule tab without React event null crash', async () => {
    vi.spyOn(learningApi, 'getOverview').mockResolvedValue(sampleOverview())
    const saveSpy = vi.spyOn(learningApi, 'saveConfig').mockResolvedValue({
      config: { ...sampleOverview().config, time: '17:00' },
    })

    renderPage()

    const scheduleTab = await screen.findByRole('button', { name: '定时与历史' })
    fireEvent.click(scheduleTab)

    const timeInput = await screen.findByLabelText('执行时间（本地时间）')
    fireEvent.change(timeInput, { target: { value: '17:00' } })
    expect((timeInput as HTMLInputElement).value).toBe('17:00')

    const islandCheckbox = screen.getByLabelText(/灵动岛温和提醒/)
    fireEvent.click(islandCheckbox)

    const saveBtn = screen.getByRole('button', { name: /保存定时计划/ })
    fireEvent.click(saveBtn)

    await waitFor(() => {
      expect(saveSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          time: '17:00',
          notify_dynamic_island: false,
        }),
      )
    })
  })
})
