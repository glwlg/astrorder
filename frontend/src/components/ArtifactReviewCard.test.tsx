import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Command, Message, Session } from '../domain/types'
import { Transcript } from '../features/chat/Transcript'
import { MobileTranscript } from '../features/mobile/MobileTranscript'
import { useSidecarStore } from '../features/sidecar/sidecarStore'
import { useAstrorderStore } from '../state/store'

const session: Session = {
  id: 'sess-plan',
  agent_id: 'agy-1',
  title: '代码审查改造',
  workspace: '/home/luwei/workspace/OpsCore',
  status: 'idle',
  updated_at: '2026-11-24T08:21:00Z',
}

const planMessages: Message[] = [
  {
    id: 'u-1',
    agent_id: 'agy-1',
    session_id: 'sess-plan',
    role: 'user',
    kind: 'message',
    text: '补充：全量智能代码审查增加多轮次配置',
    attachments: [],
    created_at: '2026-11-24T08:17:00Z',
    command_id: null,
    tool: null,
  },
  {
    id: 'antigravity:sess-plan:20',
    agent_id: 'agy-1',
    session_id: 'sess-plan',
    role: 'tool',
    kind: 'tool',
    text: 'Created file.',
    attachments: [],
    created_at: '2026-11-24T08:19:00Z',
    command_id: null,
    tool: {
      name: 'write_file',
      status: 'completed',
      arguments: {
        Path: '"/home/luwei/.gemini/antigravity-cli/brain/sess-plan/code_review_implementation_plan.md"',
        ArtifactMetadata: JSON.stringify({
          UserFacing: true,
          RequestFeedback: true,
          Summary: '全量智能代码审查多轮次配置与合并结果的分阶段实施计划。',
        }),
      },
    },
  },
  {
    id: 'antigravity:sess-plan:21',
    agent_id: 'agy-1',
    session_id: 'sess-plan',
    role: 'assistant',
    kind: 'message',
    text: '确认后直接点 Proceed 或回复即可开始编码实施。',
    attachments: [],
    created_at: '2026-11-24T08:21:00Z',
    command_id: null,
    tool: null,
  },
]

const proceedReceipt: Command = {
  id: 'artifact-proceed:antigravity:sess-plan:20',
  agent_id: 'agy-1',
  session_id: 'sess-plan',
  action: 'send',
  state: 'accepted',
  text: 'Proceed',
  attachments: [],
  created_at: '2026-11-24T08:22:00Z',
  error: null,
  target_id: 'antigravity:sess-plan:20',
}

afterEach(() => {
  cleanup()
  useAstrorderStore.getState().resetRuntime()
  vi.restoreAllMocks()
})

describe('ArtifactReviewCard in desktop and mobile transcripts', () => {
  it.each(['desktop', 'mobile'] as const)(
    'renders Antigravity artifact review card with Proceed button in %s transcript and dispatches Proceed',
    async (mode) => {
      const send = vi.spyOn(api, 'createCommand').mockResolvedValue(proceedReceipt)
      vi.spyOn(api, 'getMessages').mockResolvedValue({ items: planMessages, next_cursor: null })
      const onFile = vi.fn()

      render(
        mode === 'desktop' ? (
          <MantineProvider>
            <Transcript session={session} messages={planMessages} outbox={[]} />
          </MantineProvider>
        ) : (
          <MantineProvider>
            <MobileTranscript
              session={session}
              messages={planMessages}
              busy={false}
              hasOlder={false}
              loadingOlder={false}
              loadOlder={vi.fn()}
              onMessageAction={vi.fn()}
              onImage={vi.fn()}
              onFile={onFile}
              onSwipe={vi.fn()}
            />
          </MantineProvider>
        ),
      )

      expect(screen.getByText('计划待确认')).toBeInTheDocument()
      expect(screen.getByText('code_review_implementation_plan.md')).toBeInTheDocument()
      expect(screen.getByText('全量智能代码审查多轮次配置与合并结果的分阶段实施计划。')).toBeInTheDocument()

      if (mode === 'mobile') {
        fireEvent.click(screen.getByRole('button', { name: '查看文档' }))
        expect(onFile).toHaveBeenCalledWith(
          '/home/luwei/.gemini/antigravity-cli/brain/sess-plan/code_review_implementation_plan.md',
        )
      } else {
        const openArtifactSpy = vi.spyOn(useSidecarStore.getState(), 'openArtifact')
        fireEvent.click(screen.getByRole('button', { name: '查看文档' }))
        expect(openArtifactSpy).toHaveBeenCalledTimes(1)
        expect(openArtifactSpy.mock.calls[0][0].path).toBe(
          '/home/luwei/.gemini/antigravity-cli/brain/sess-plan/code_review_implementation_plan.md',
        )
      }

      fireEvent.click(screen.getByRole('button', { name: 'Proceed' }))
      await screen.findByText('已发送 Proceed，等待 Antigravity 开始执行。')
      expect(send).toHaveBeenCalledExactlyOnceWith({
        id: 'artifact-proceed:antigravity:sess-plan:20',
        agent_id: 'agy-1',
        session_id: 'sess-plan',
        action: 'send',
        text: 'Proceed',
        attachment_ids: [],
        target_id: 'antigravity:sess-plan:20',
      })
    },
  )

  it('hides Proceed button and shows settled status when user already replied manually', () => {
    const withReply: Message[] = [
      ...planMessages,
      {
        id: 'antigravity:sess-plan:22',
        agent_id: 'agy-1',
        session_id: 'sess-plan',
        role: 'user',
        kind: 'message',
        text: '确认，先做 Phase 1',
        attachments: [],
        created_at: '2026-11-24T08:23:00Z',
        command_id: null,
        tool: null,
      },
    ]

    render(
      <MantineProvider>
        <Transcript session={session} messages={withReply} outbox={[]} />
      </MantineProvider>,
    )

    expect(screen.getByText('已确认继续')).toBeInTheDocument()
    expect(screen.getByText('已有后续回复，此计划确认已结束。')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Proceed' })).not.toBeInTheDocument()
  })

  it('renders user-facing report artifacts without a Proceed button when RequestFeedback is false', () => {
    const docOnlyMessages: Message[] = [
      {
        id: 'antigravity:sess-plan:30',
        agent_id: 'agy-1',
        session_id: 'sess-plan',
        role: 'tool',
        kind: 'tool',
        text: 'Created file.',
        attachments: [],
        created_at: '2026-11-24T08:25:00Z',
        command_id: null,
        tool: {
          name: 'write_file',
          status: 'completed',
          arguments: {
            Path: '/brain/astrorder-ui-ux-audit.md',
            ArtifactMetadata: {
              UserFacing: true,
              RequestFeedback: false,
              Summary: '全界面 UI/UX 审查报告',
            },
          },
        },
      },
    ]

    render(
      <MantineProvider>
        <Transcript session={session} messages={docOnlyMessages} outbox={[]} />
      </MantineProvider>,
    )

    expect(screen.getByText('文档产物')).toBeInTheDocument()
    expect(screen.getByText('astrorder-ui-ux-audit.md')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '查看文档' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Proceed' })).not.toBeInTheDocument()
  })
})
