import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { learningApi, type LearningOverviewResponse, type LearningTopic } from '../../../api/learning'
import { MobileLearningFeed } from './MobileLearningFeed'

function makeTopic(overrides: Partial<LearningTopic> = {}): LearningTopic {
  return {
    id: 'm-topic-1',
    fingerprint: 'fp-m1',
    title: '为什么移除 `-s -w` 能大幅降低 Windows Defender 误报？',
    category: 'blackbox_code',
    priority: 'high',
    status: 'open',
    estimated_minutes: 2,
    project_name: 'astrorder',
    why_now: '昨天在发布脚本中修改了 Go 编译参数。',
    key_points: [
      '-s -w 会彻底剥离符号表与 DWARF 调试段。',
      '无 EV 签名二进制容易命中启发式加壳检测。',
    ],
    quiz_questions: [
      {
        question: '为什么仍可安全保留 `-trimpath`？',
        answer: '-trimpath 仅清洗路径信息，不影响符号表与熵值特征。',
      },
    ],
    evidence: [
      {
        kind: 'session',
        label: '会话「怎么会报毒啊」',
        agent_id: 'antigravity',
        session_id: 'sess-88',
        snippet: '讨论 Defender 误报',
      },
      {
        kind: 'file',
        label: 'scripts/desktop-wails.mjs',
        file_path: 'scripts/desktop-wails.mjs',
      },
    ],
    occurrence_count: 1,
    review: { stage: 0, interval_days: 0, pass_count: 0, fuzzy_count: 0 },
    created_at: '2026-11-26T10:00:00Z',
    updated_at: '2026-11-26T10:00:00Z',
    ...overrides,
  }
}

function makeOverview(topics: LearningTopic[] = [makeTopic()]): LearningOverviewResponse {
  return {
    config: {
      mentor_bot_id: 'bot-1',
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
      signal_sources: { sessions: true, git: true, failures: true, meetings: true },
    },
    mentor_bot: {
      id: 'bot-1',
      name: '成长导师',
      slug: 'learning-mentor',
      description: '实战成长导师',
      workspace: '/tmp/mentor',
      default_agent_id: 'antigravity',
      has_agents_md: true,
      skills: [],
      session_count: 0,
      running_count: 0,
      created_at: '2026-11-26T00:00:00Z',
      updated_at: '2026-11-26T00:00:00Z',
    },
    stats: {
      pending_count: topics.length,
      blackbox_count: topics.length,
      friction_count: 0,
      failure_count: 0,
      new_domain_count: 0,
      due_review_count: 0,
      learning_count: 0,
      mastered_count: 0,
      dismissed_count: 0,
      project_count: 1,
      weekly_new_count: topics.length,
    },
    topics,
    runs: [],
  }
}

function renderFeed(onClose = vi.fn(), onOpenSession = vi.fn()) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return {
    onClose,
    onOpenSession,
    ...render(
      <QueryClientProvider client={queryClient}>
        <MantineProvider>
          <MobileLearningFeed onClose={onClose} onOpenSession={onOpenSession} />
        </MantineProvider>
      </QueryClientProvider>,
    ),
  }
}

describe('MobileLearningFeed', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  afterEach(() => {
    cleanup()
  })

  it('renders vertical card, reveals 30s self-check answer, and marks mastered with undo', async () => {
    vi.spyOn(learningApi, 'getOverview').mockResolvedValue(makeOverview())
    const updateSpy = vi.spyOn(learningApi, 'updateTopic').mockResolvedValue({
      topic: makeTopic({ status: 'mastered' }),
    })

    renderFeed()

    expect(await screen.findByText(/能大幅降低 Windows Defender 误报/)).toBeTruthy()
    expect(screen.getByText('1 / 1')).toBeTruthy()

    // Reveal 30s self-check answer inline
    fireEvent.click(screen.getByRole('button', { name: /轻点展开参考要点/ }))
    expect(await screen.findByText(/仅清洗路径信息/)).toBeTruthy()

    // Tap right-rail "已掌握" and verify Undo toast
    fireEvent.click(screen.getByRole('button', { name: '标记已掌握' }))
    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith('m-topic-1', { status: 'mastered' })
    })

    const undoBtn = await screen.findByRole('button', { name: '撤销' })
    fireEvent.click(undoBtn)
    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith('m-topic-1', { status: 'open' })
    })
  })

  it('opens evidence bottom sheet, jumps to source session, and saves quick note', async () => {
    vi.spyOn(learningApi, 'getOverview').mockResolvedValue(makeOverview())
    const updateSpy = vi.spyOn(learningApi, 'updateTopic').mockResolvedValue({
      topic: makeTopic({ study_notes: '- 保留符号表很关键' }),
    })

    const { onOpenSession } = renderFeed()

    await screen.findByText(/能大幅降低 Windows Defender 误报/)
    fireEvent.click(screen.getByRole('button', { name: /查看溯源证据/ }))

    expect(await screen.findByText('原工作现场线索')).toBeTruthy()
    expect(screen.getByText('会话「怎么会报毒啊」')).toBeTruthy()

    // Save a quick note (and ensure placeholder uses general copy without geek jargon)
    const quickInput = screen.getByLabelText('快捷速记输入') as HTMLInputElement
    expect(quickInput.placeholder).not.toContain('剥离符号表')
    expect(quickInput.placeholder).not.toContain('PE 熵值')
    fireEvent.change(quickInput, { target: { value: '保留符号表很关键' } })
    fireEvent.click(screen.getByRole('button', { name: '保存快捷速记' }))

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith('m-topic-1', { study_notes: '- 保留符号表很关键' })
    })

    // Jump to source session from evidence row
    fireEvent.click(screen.getByRole('button', { name: '进会话 ›' }))
    expect(onOpenSession).toHaveBeenCalledWith('antigravity', 'sess-88')
  })

  it('dispatches Mentor Bot study session from right rail', async () => {
    vi.spyOn(learningApi, 'getOverview').mockResolvedValue(makeOverview())
    const startStudySpy = vi.spyOn(learningApi, 'startStudy').mockResolvedValue({
      ok: true,
      topic: makeTopic({ status: 'learning' }),
      agent_id: 'antigravity',
      session_id: 'mentor-chat-1',
    })

    const { onOpenSession } = renderFeed()

    await screen.findByText(/能大幅降低 Windows Defender 误报/)
    fireEvent.click(screen.getByRole('button', { name: '问成长导师 Bot' }))

    await waitFor(() => {
      expect(startStudySpy).toHaveBeenCalledWith('m-topic-1', {
        mode: 'study',
        sync_session_schedule: true,
      })
      expect(onOpenSession).toHaveBeenCalledWith('antigravity', 'mentor-chat-1')
    })
  })
})
