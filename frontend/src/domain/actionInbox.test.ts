import { describe, expect, it } from 'vitest'
import { buildActionInboxItems, countUrgentInboxItems } from './actionInbox'
import { scopeKey } from './semantics'
import type { Agent, Approval, EventNotification, Message, Session } from './types'

const readyAgent: Agent = {
  id: 'agent-1',
  kind: 'antigravity',
  name: 'Antigravity',
  status: 'ready',
  capabilities: ['chat', 'stop', 'approvals'],
  limitation: null,
}

function makeSession(overrides: Partial<Session> & { id: string }): Session {
  return {
    agent_id: 'agent-1',
    title: `会话 ${overrides.id}`,
    workspace: '/workspace/demo',
    status: 'idle',
    updated_at: '2026-11-23T10:00:00Z',
    ...overrides,
  }
}

describe('buildActionInboxItems', () => {
  it('orders approvals, questions, errors, and unread ready sessions by priority and recency', () => {
    const sApproval = makeSession({ id: 's-app', title: '需审批会话', status: 'waiting_approval', updated_at: '2026-11-23T10:01:00Z' })
    const sQuestion = makeSession({ id: 's-q', title: '提问会话', status: 'idle', updated_at: '2026-11-23T10:02:00Z' })
    const sError = makeSession({ id: 's-err', title: '异常会话', status: 'idle', updated_at: '2026-11-23T10:03:00Z' })
    const sStaleSleptError = makeSession({ id: 's-slept', title: '三天前休眠会话', status: 'error', updated_at: '2026-11-20T07:10:00Z' })
    const sReady = makeSession({ id: 's-ready', title: '已完成会话', status: 'idle', updated_at: '2026-11-23T10:04:00Z' })
    const sRunning = makeSession({ id: 's-run', title: '运行中会话', status: 'running', updated_at: '2026-11-23T10:05:00Z' })

    const approval: Approval = {
      id: 'ap-1',
      agent_id: 'agent-1',
      session_id: 's-app',
      title: '执行数据库迁移',
      detail: 'alembic upgrade head',
      state: 'pending',
      target_id: 'ap-1',
      data: { created_at: '2026-11-23T10:01:00Z' },
    }
    const qMsg: Message = {
      id: 'm-q',
      agent_id: 'agent-1',
      session_id: 's-q',
      role: 'assistant',
      kind: 'message',
      text: '请选择部署方式',
      attachments: [],
      created_at: '2026-11-23T10:02:00Z',
      command_id: null,
      tool: null,
      delivery: 'async',
      questions: [{ title: '采用哪种部署方式？', options: ['蓝绿发布', '滚动发布'] }],
    }
    const errorNotice: EventNotification = {
      key: 'n-err',
      agent_id: 'agent-1',
      session_id: 's-err',
      kind: 'task_failed',
      title: '会话未完成',
      message: '编译阶段依赖拉取超时',
      created_at: '2026-11-23T10:03:00Z',
      cursor: 11,
    }
    const readyNotice: EventNotification = {
      key: 'n-ready',
      agent_id: 'agent-1',
      session_id: 's-ready',
      kind: 'task_completed',
      title: '已完成会话',
      message: '6 屏设计稿已全部生成并校验完成',
      created_at: '2026-11-23T10:04:00Z',
      cursor: 12,
    }

    const unreadSet = new Set([scopeKey('agent-1', 's-err'), scopeKey('agent-1', 's-ready'), scopeKey('agent-1', 's-run')])
    const items = buildActionInboxItems({
      sessions: [sApproval, sQuestion, sError, sStaleSleptError, sReady, sRunning],
      agents: { 'agent-1': readyAgent },
      approvals: { 'ap-1': approval },
      messages: { [scopeKey('agent-1', 's-q')]: { 'm-q': qMsg } },
      notifications: { 'n-err': errorNotice, 'n-ready': readyNotice },
      isUnread: (k) => unreadSet.has(k),
    })

    expect(items.map((i) => i.kind)).toEqual(['approval', 'question', 'error', 'ready'])
    expect(items.some((i) => i.sessionId === 's-slept')).toBe(false)
    expect(items[0].canApproveInline).toBe(true)
    expect(items[1].questionMessage?.id).toBe('m-q')
    expect(items[2].summary).toContain('编译阶段依赖拉取超时')
    expect(items[3].summary).toContain('6 屏设计稿')
    expect(items[3].canQuickReply).toBe(true)
    expect(countUrgentInboxItems(items)).toBe(3)
  })

  it('skips async questions that already have a subsequent user reply', () => {
    const sQuestion = makeSession({ id: 's-q', title: '已回答会话' })
    const key = scopeKey('agent-1', 's-q')
    const qMsg: Message = {
      id: 'm-1',
      agent_id: 'agent-1',
      session_id: 's-q',
      role: 'assistant',
      kind: 'message',
      text: '问一个问题',
      attachments: [],
      created_at: '2026-11-23T10:00:00Z',
      command_id: null,
      tool: null,
      delivery: 'async',
      questions: [{ title: '确认继续？', options: ['是', '否'] }],
    }
    const replyMsg: Message = {
      id: 'm-2',
      agent_id: 'agent-1',
      session_id: 's-q',
      role: 'user',
      kind: 'message',
      text: '是',
      attachments: [],
      created_at: '2026-11-23T10:01:00Z',
      command_id: 'c-1',
      tool: null,
    }

    const items = buildActionInboxItems({
      sessions: [sQuestion],
      agents: { 'agent-1': readyAgent },
      approvals: {},
      messages: { [key]: { 'm-1': qMsg, 'm-2': replyMsg } },
      notifications: {},
      isUnread: () => false,
    })

    expect(items).toEqual([])
  })

  it('ignores stale :live: async questions once native history marks them answered or session resumes running', () => {
    const sIdle = makeSession({ id: 's-agy', title: '监控Agent', status: 'idle' })
    const key = scopeKey('agent-1', 's-agy')
    const questions = [
      { title: '将插件从 Alloy 切换为 Grafana Beyla 后，现有 alloy 插件项如何处理?', options: ['A', 'B'] },
      { title: '本次改造范围按哪种节奏推进?', options: ['节奏1', '节奏2'] },
    ]
    const nativeAnswered: Message = {
      id: 'step-42',
      agent_id: 'agent-1',
      session_id: 's-agy',
      role: 'assistant',
      kind: 'message',
      text: '将插件从 Alloy 切换为 Grafana Beyla 后，现有 alloy 插件项如何处理?',
      attachments: [],
      created_at: '2026-11-24T02:55:00Z',
      command_id: null,
      tool: null,
      delivery: 'async',
      answered: true,
      questions,
    }
    const staleLiveCopy: Message = {
      ...nativeAnswered,
      id: 'antigravity:s-agy:live:7',
      created_at: '2026-11-24T02:55:03Z',
      answered: false,
    }

    // Even when the stale :live: copy has a newer timestamp and answered:false, deduplication clears it.
    expect(
      buildActionInboxItems({
        sessions: [sIdle],
        agents: { 'agent-1': readyAgent },
        approvals: {},
        messages: { [key]: { [nativeAnswered.id]: nativeAnswered, [staleLiveCopy.id]: staleLiveCopy } },
        notifications: {},
        isUnread: () => false,
      }),
    ).toEqual([])

    // Even with only the :live: copy present, an accepted answer command or running session or later tool activity clears it.
    expect(
      buildActionInboxItems({
        sessions: [makeSession({ id: 's-agy', title: '监控Agent', status: 'running' })],
        agents: { 'agent-1': readyAgent },
        approvals: {},
        messages: { [key]: { [staleLiveCopy.id]: staleLiveCopy } },
        notifications: {},
        isUnread: () => false,
      }),
    ).toEqual([])

    expect(
      buildActionInboxItems({
        sessions: [sIdle],
        agents: { 'agent-1': readyAgent },
        approvals: {},
        messages: { [key]: { [staleLiveCopy.id]: staleLiveCopy } },
        commands: {
          [`${key}::async-answer:step-42`]: {
            id: 'async-answer:step-42',
            agent_id: 'agent-1',
            session_id: 's-agy',
            action: 'send',
            target_id: 'step-42',
            text: `${questions[0].title}\nA\n\n${questions[1].title}\n节奏1`,
            attachments: [],
            state: 'completed',
            created_at: '2026-11-24T02:55:10Z',
            error: null,
          },
        },
        notifications: {},
        isUnread: () => false,
      }),
    ).toEqual([])

    expect(
      buildActionInboxItems({
        sessions: [sIdle],
        agents: { 'agent-1': readyAgent },
        approvals: {},
        messages: {
          [key]: {
            [staleLiveCopy.id]: staleLiveCopy,
            'step-43': {
              id: 'step-43',
              agent_id: 'agent-1',
              session_id: 's-agy',
              role: 'tool',
              kind: 'tool',
              text: 'Search all alloy references',
              attachments: [],
              created_at: '2026-11-24T02:55:20Z',
              command_id: null,
              tool: { name: 'search', status: 'completed' },
            },
          },
        },
        notifications: {},
        isUnread: () => false,
      }),
    ).toEqual([])
  })
})
