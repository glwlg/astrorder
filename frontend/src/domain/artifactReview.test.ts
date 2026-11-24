import { describe, expect, it } from 'vitest'
import { activityPacks } from './activityPacks'
import { buildActionInboxItems } from './actionInbox'
import {
  collectTurnArtifactReviews,
  extractArtifactFromMessage,
  isArtifactFeedbackPending,
} from './artifactReview'
import type { Command, Message, Session } from './types'

function makeMsg(overrides: Partial<Message>): Message {
  return {
    id: overrides.id || 'm-1',
    session_id: overrides.session_id || 'sess-1',
    agent_id: overrides.agent_id || 'agy-1',
    role: overrides.role || 'assistant',
    kind: overrides.kind || 'message',
    text: overrides.text ?? '',
    attachments: overrides.attachments ?? [],
    created_at: overrides.created_at || '2026-11-24T08:18:00Z',
    command_id: overrides.command_id ?? null,
    tool: overrides.tool ?? null,
    ...overrides,
  }
}

describe('artifactReview extraction and turn anchoring', () => {
  it('parses JSON-encoded ArtifactMetadata and JSON-quoted Path from Antigravity native history', () => {
    const toolMsg = makeMsg({
      id: 'antigravity:sess-1:216',
      role: 'tool',
      kind: 'tool',
      text: 'Created file.',
      tool: {
        name: 'write_file',
        status: 'completed',
        arguments: {
          Path: '"/home/luwei/.gemini/antigravity-cli/brain/sess-1/code_review_implementation_plan.md"',
          ArtifactMetadata: JSON.stringify({
            UserFacing: true,
            RequestFeedback: true,
            Summary: '全量智能代码审查分阶段实施计划',
          }),
        },
      },
    })

    const item = extractArtifactFromMessage(toolMsg, 0)
    expect(item).not.toBeNull()
    expect(item?.path).toBe('/home/luwei/.gemini/antigravity-cli/brain/sess-1/code_review_implementation_plan.md')
    expect(item?.fileName).toBe('code_review_implementation_plan.md')
    expect(item?.summary).toBe('全量智能代码审查分阶段实施计划')
    expect(item?.userFacing).toBe(true)
    expect(item?.requestFeedback).toBe(true)
  })

  it('parses object ArtifactMetadata from live ACP rawInput and ignores internal non-user-facing artifacts', () => {
    const userFacing = makeMsg({
      id: 'antigravity:sess-1:live:4',
      role: 'tool',
      kind: 'tool',
      tool: {
        name: 'write_file',
        status: 'completed',
        arguments: {
          Path: '/brain/report.md',
          ArtifactMetadata: {
            UserFacing: true,
            RequestFeedback: false,
            Summary: '审查报告摘要',
          },
        },
      },
    })
    const internalOnly = makeMsg({
      id: 'antigravity:sess-1:live:5',
      role: 'tool',
      kind: 'tool',
      tool: {
        name: 'write_file',
        status: 'completed',
        arguments: {
          Path: '/brain/task.md',
          ArtifactMetadata: {
            UserFacing: false,
            RequestFeedback: false,
            Summary: 'internal checklist',
          },
        },
      },
    })
    const failedWrite = makeMsg({
      id: 'antigravity:sess-1:live:6',
      role: 'tool',
      kind: 'tool',
      tool: {
        name: 'write_file',
        status: 'error',
        arguments: {
          Path: '/brain/broken.md',
          ArtifactMetadata: { UserFacing: true, RequestFeedback: true, Summary: 'failed' },
        },
      },
    })

    expect(extractArtifactFromMessage(userFacing, 0)?.requestFeedback).toBe(false)
    expect(extractArtifactFromMessage(internalOnly, 1)).toBeNull()
    expect(extractArtifactFromMessage(failedWrite, 2)).toBeNull()
  })

  it('deduplicates multiple edits to the same artifact in one turn and anchors after the turn assistant reply', () => {
    const messages: Message[] = [
      makeMsg({ id: 'u1', role: 'user', kind: 'message', text: '写个实施计划', created_at: '2026-11-24T08:17:00Z' }),
      makeMsg({
        id: 't1',
        role: 'tool',
        kind: 'tool',
        created_at: '2026-11-24T08:18:00Z',
        tool: {
          name: 'write_file',
          status: 'completed',
          arguments: {
            Path: '/brain/plan.md',
            ArtifactMetadata: { UserFacing: true, RequestFeedback: true, Summary: '初版计划' },
          },
        },
      }),
      makeMsg({
        id: 't2',
        role: 'tool',
        kind: 'tool',
        created_at: '2026-11-24T08:19:00Z',
        tool: {
          name: 'edit_file',
          status: 'completed',
          arguments: {
            Path: '/brain/plan.md',
            ArtifactMetadata: { UserFacing: true, RequestFeedback: true, Summary: '已补充多轮次配置的最终计划' },
          },
        },
      }),
      makeMsg({
        id: 'a1',
        role: 'assistant',
        kind: 'message',
        text: '确认后直接点 Proceed 或回复即可开始编码实施。',
        created_at: '2026-11-24T08:21:00Z',
      }),
    ]

    const packs = activityPacks(messages)
    const collected = collectTurnArtifactReviews(messages, packs)

    expect(collected.all).toHaveLength(1)
    expect(collected.byPackStart.size).toBe(0)
    expect(collected.byMessageIndex.get(3)).toHaveLength(1)
    expect(collected.byMessageIndex.get(3)?.[0].artifact.messageId).toBe('t2')
    expect(collected.byMessageIndex.get(3)?.[0].artifact.summary).toBe('已补充多轮次配置的最终计划')
    expect(collected.byMessageIndex.get(3)?.[0].continued).toBe(false)
    expect(isArtifactFeedbackPending(collected.all[0], [], false)).toBe(true)
  })

  it('anchors after the activity pack when no assistant text reply follows', () => {
    const messages: Message[] = [
      makeMsg({ id: 'u1', role: 'user', kind: 'message', text: '生成计划' }),
      makeMsg({
        id: 't1',
        role: 'tool',
        kind: 'tool',
        tool: {
          name: 'write_file',
          status: 'completed',
          arguments: {
            Path: '/brain/plan.md',
            ArtifactMetadata: { UserFacing: true, RequestFeedback: true, Summary: '实施计划' },
          },
        },
      }),
    ]

    const packs = activityPacks(messages)
    const collected = collectTurnArtifactReviews(messages, packs)

    expect(collected.byMessageIndex.size).toBe(0)
    expect(collected.byPackStart.get(1)).toHaveLength(1)
  })

  it('marks artifact review settled when a subsequent user message or Proceed command exists and updates ActionInbox', () => {
    const planTool = makeMsg({
      id: 'antigravity:sess-1:live:10',
      role: 'tool',
      kind: 'tool',
      created_at: '2026-11-24T08:19:00Z',
      tool: {
        name: 'write_file',
        status: 'completed',
        arguments: {
          Path: '/brain/code_review_implementation_plan.md',
          ArtifactMetadata: { UserFacing: true, RequestFeedback: true, Summary: '完整分阶段实施计划' },
        },
      },
    })
    const reply = makeMsg({
      id: 'antigravity:sess-1:live:11',
      role: 'assistant',
      kind: 'message',
      text: '确认后直接点 Proceed 或回复即可开始编码实施。',
      created_at: '2026-11-24T08:21:00Z',
    })
    const session: Session = {
      id: 'sess-1',
      agent_id: 'agy-1',
      title: '代码审查重构',
      workspace: '/home/luwei/workspace/OpsCore',
      status: 'idle',
      updated_at: '2026-11-24T08:21:00Z',
    }

    // 1. Unresolved -> appears in ActionInbox
    const pendingInbox = buildActionInboxItems({
      sessions: [session],
      agents: { 'agy-1': { id: 'agy-1', kind: 'antigravity', name: 'Antigravity', status: 'ready', capabilities: ['chat'], limitation: null } },
      approvals: {},
      messages: { 'agy-1::sess-1': { [planTool.id]: planTool, [reply.id]: reply } },
      commands: {},
      notifications: {},
      isUnread: () => false,
    })
    expect(pendingInbox).toHaveLength(1)
    expect(pendingInbox[0].artifactReview?.fileName).toBe('code_review_implementation_plan.md')

    // 2. Settled by artifact-proceed command (even after live -> native message ID swap)
    const proceedCmd: Command = {
      id: 'artifact-proceed:antigravity:sess-1:live:10',
      agent_id: 'agy-1',
      session_id: 'sess-1',
      action: 'send',
      state: 'accepted',
      text: 'Proceed',
      attachments: [],
      created_at: '2026-11-24T08:22:00Z',
      error: null,
      target_id: 'antigravity:sess-1:live:10',
    }
    const nativePlanTool = { ...planTool, id: 'antigravity:sess-1:42' }
    const settledByCmdInbox = buildActionInboxItems({
      sessions: [session],
      agents: { 'agy-1': { id: 'agy-1', kind: 'antigravity', name: 'Antigravity', status: 'ready', capabilities: ['chat'], limitation: null } },
      approvals: {},
      messages: { 'agy-1::sess-1': { [nativePlanTool.id]: nativePlanTool, [reply.id]: reply } },
      commands: { [proceedCmd.id]: proceedCmd },
      notifications: {},
      isUnread: () => false,
    })
    expect(settledByCmdInbox).toHaveLength(0)

    // 3. Settled by subsequent user message
    const userFollowup = makeMsg({
      id: 'antigravity:sess-1:45',
      role: 'user',
      kind: 'message',
      text: '先交付 Phase 1',
      created_at: '2026-11-24T08:23:00Z',
    })
    const settledByUserInbox = buildActionInboxItems({
      sessions: [session],
      agents: { 'agy-1': { id: 'agy-1', kind: 'antigravity', name: 'Antigravity', status: 'ready', capabilities: ['chat'], limitation: null } },
      approvals: {},
      messages: { 'agy-1::sess-1': { [nativePlanTool.id]: nativePlanTool, [reply.id]: reply, [userFollowup.id]: userFollowup } },
      commands: {},
      notifications: {},
      isUnread: () => false,
    })
    expect(settledByUserInbox).toHaveLength(0)
  })
})
