import { describe, expect, it } from 'vitest'
import { buildProjectGroups, displaySessionTitle, sessionActivityStatus } from './sessionRailModel'
import type { Command, Message, Session, Task } from '../domain/types'
import { useAstrorderStore } from '../state/store'

function message(partial: Partial<Message> & Pick<Message, 'id' | 'role'>): Message {
  return {
    session_id: 's-1',
    agent_id: 'local-codex',
    kind: 'message',
    text: '',
    attachments: [],
    created_at: '2026-09-10T12:00:00Z',
    command_id: null,
    tool: null,
    ...partial,
  }
}

describe('sessionActivityStatus', () => {
  const baseSession: Session = {
    id: 's-1',
    agent_id: 'local-codex',
    title: 'Test Session',
    status: 'idle',
    updated_at: '2026-09-10T12:00:00Z',
    workspace: '/test',
  }

  it('returns idle when session is idle even if live is true (presence does not mean running)', () => {
    const sessionWithLive: Session = {
      ...baseSession,
      status: 'idle',
      live: true,
    }
    expect(sessionActivityStatus(sessionWithLive)).toBe('idle')
  })

  it('returns running when session.status is running', () => {
    const runningSession: Session = {
      ...baseSession,
      status: 'running',
    }
    expect(sessionActivityStatus(runningSession)).toBe('running')
  })

  it('returns waiting_approval when session.status is waiting_approval', () => {
    const approvalSession: Session = {
      ...baseSession,
      status: 'waiting_approval',
    }
    expect(sessionActivityStatus(approvalSession)).toBe('waiting_approval')
  })

  it('returns error when session.status is error', () => {
    const errorSession: Session = {
      ...baseSession,
      status: 'error',
    }
    expect(sessionActivityStatus(errorSession)).toBe('error')
  })

  it('returns running when session is idle but has an active in-flight command in commandsMap', () => {
    const activeCommand: Command = {
      id: 'cmd-1',
      session_id: 's-1',
      agent_id: 'local-codex',
      action: 'send',
      state: 'running',
      text: 'hello',
      attachments: [],
      created_at: '2026-09-10T12:00:00Z',
      error: null,
    }
    expect(sessionActivityStatus(baseSession, [activeCommand])).toBe('running')
  })

  it('returns idle when command is completed', () => {
    const completedCommand: Command = {
      id: 'cmd-1',
      session_id: 's-1',
      agent_id: 'local-codex',
      action: 'send',
      state: 'completed',
      text: 'hello',
      attachments: [],
      created_at: '2026-09-10T12:00:00Z',
      error: null,
    }
    expect(sessionActivityStatus(baseSession, [completedCommand])).toBe('idle')
  })

  it('falls back to useAstrorderStore.getState().commands when commandsMap is not provided', () => {
    useAstrorderStore.getState().resetRuntime()
    expect(sessionActivityStatus(baseSession)).toBe('idle')

    const activeCommand: Command = {
      id: 'cmd-store',
      session_id: 's-1',
      agent_id: 'local-codex',
      action: 'send',
      state: 'running',
      text: 'in store',
      attachments: [],
      created_at: '2026-09-10T12:00:00Z',
      error: null,
    }
    useAstrorderStore.getState().applyEvent({
      cursor: 1,
      id: 'evt-1',
      type: 'command.upsert',
      agent_id: 'local-codex',
      session_id: 's-1',
      data: { ...activeCommand } as unknown as Record<string, unknown>,
    })
    expect(sessionActivityStatus(baseSession)).toBe('running')

    useAstrorderStore.getState().applyEvent({
      cursor: 2,
      id: 'evt-2',
      type: 'command.upsert',
      agent_id: 'local-codex',
      session_id: 's-1',
      data: { ...activeCommand, state: 'completed' } as unknown as Record<string, unknown>,
    })
    expect(sessionActivityStatus(baseSession)).toBe('idle')
    useAstrorderStore.getState().resetRuntime()
  })

  it('treats a running native task as running even when session.status stays idle', () => {
    useAstrorderStore.getState().resetRuntime()
    const task: Task = {
      id: 'task-1',
      session_id: 's-1',
      agent_id: 'local-codex',
      kind: 'background',
      title: '后台命令',
      status: 'running',
      progress: null,
      command: null,
      logs: [],
      target_id: 'call-1',
      created_at: '2026-09-10T12:00:00Z',
      updated_at: '2026-09-10T12:00:00Z',
    }
    useAstrorderStore.getState().mergeTasks([task])
    expect(sessionActivityStatus(baseSession)).toBe('running')
    useAstrorderStore.getState().resetRuntime()
  })

  it('keeps non-blocking skill evolution visible without marking the session running', () => {
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.getState().mergeMessages('local-codex', 's-1', [message({
      id: 'skill-tool', role: 'tool', kind: 'tool',
      tool: { name: 'skill_manage', status: 'running', background: true },
      created_at: new Date().toISOString(),
    })])
    useAstrorderStore.getState().mergeTasks([{
      id: 'skill-task', session_id: 's-1', agent_id: 'local-codex', kind: 'background',
      title: '工具：skill_manage', status: 'running', progress: { blocking: false }, command: null,
      logs: [], target_id: 'call-1', created_at: new Date().toISOString(), updated_at: new Date().toISOString(),
    }])
    expect(sessionActivityStatus(baseSession)).toBe('idle')
    useAstrorderStore.getState().resetRuntime()
  })
})

describe('sessionActivityStatus live stream', () => {
  const idleSession: Session = {
    id: 's-1',
    agent_id: 'local-codex',
    title: 'Test Session',
    status: 'idle',
    updated_at: '2026-09-10T12:00:00Z',
    workspace: '/test',
  }

  it('marks running as soon as a live assistant text upsert arrives, without waiting for session.status', () => {
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.getState().applyEvent({
      cursor: 11,
      id: 'live-stream-1',
      type: 'message.upsert',
      agent_id: idleSession.agent_id,
      session_id: idleSession.id,
      data: {
        ...message({
          id: 'a-stream',
          role: 'assistant',
          kind: 'message',
          text: '正在写',
          created_at: new Date().toISOString(),
        }),
      } as unknown as Record<string, unknown>,
    })
    expect(sessionActivityStatus(idleSession)).toBe('running')
    useAstrorderStore.getState().resetRuntime()
  })

  it('does not mark an idle session running when opening history with unfinished activity rows', () => {
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.getState().mergeMessages(idleSession.agent_id, idleSession.id, [
      message({ id: 'thinking-old', role: 'assistant', kind: 'thinking', text: '历史思考', created_at: new Date().toISOString() }),
      message({ id: 'tool-old', role: 'tool', kind: 'tool', tool: { name: 'execute_code', status: 'running' }, created_at: new Date().toISOString() }),
    ])
    const now = new Date().toISOString()
    useAstrorderStore.getState().mergeTasks([
      { id: 'tool-task-old', session_id: idleSession.id, agent_id: idleSession.agent_id, kind: 'tool', title: '历史工具', status: 'running', progress: null, command: null, logs: [], target_id: 'tool-old', created_at: now, updated_at: now },
      { id: 'todo-old', session_id: idleSession.id, agent_id: idleSession.agent_id, kind: 'todo', title: '未执行待办', status: 'pending', progress: null, command: null, logs: [], target_id: null, created_at: now, updated_at: now },
    ])
    expect(sessionActivityStatus(idleSession)).toBe('idle')
    useAstrorderStore.getState().resetRuntime()
  })

  it('does not treat a historical transcript merge as a live running turn', () => {
    useAstrorderStore.getState().resetRuntime()
    useAstrorderStore.getState().mergeMessages(idleSession.agent_id, idleSession.id, [
      message({
        id: 'a-old',
        role: 'assistant',
        kind: 'message',
        text: '昨天的回复',
        created_at: new Date().toISOString(),
      }),
    ])
    expect(sessionActivityStatus(idleSession)).toBe('idle')
    useAstrorderStore.getState().resetRuntime()
  })
})

function railSession(partial: Partial<Session> & Pick<Session, 'id' | 'title'>): Session {
  return {
    agent_id: 'local-codex',
    workspace: '/home/luwei/workspace/OpsCore',
    status: 'idle',
    updated_at: '2026-09-11T12:00:00Z',
    ...partial,
  }
}

function railIds(sessions: Session[]): string[] {
  return buildProjectGroups(sessions, {}).flatMap((group) => group.sessions).map((session) => session.id)
}

describe('session rail hygiene', () => {
  it('hides the exact legacy side-chat marker without hiding ordinary discussion titles', () => {
    expect(railIds([
      railSession({ id: 'legacy', title: '[侧边聊天]' }),
      railSession({ id: 'normal', title: '修复[侧边聊天]功能' }),
      railSession({ id: 'plain', title: '侧边聊天' }),
    ])).toEqual(['normal', 'plain'])
  })
  it('hides smoke, ephemeral, and native-kind smoke sessions from the rail', () => {
    expect(railIds([
      railSession({ id: 'keep', title: '排查连接' }),
      railSession({ id: 'kind', title: '看起来正常', native_kind: 'smoke' }),
      railSession({ id: 'flag', title: '临时 fork', ephemeral: true }),
      railSession({ id: 'title-en', title: 'native connector smoke · hermes' }),
      railSession({ id: 'title-zh', title: '冒烟测试 local-codex' }),
    ])).toEqual(['keep'])
  })

  it('hides approval-assessment and review-subagent titles, but keeps ordinary history titles', () => {
    expect(railIds([
      railSession({ id: 'ordinary', title: 'The following is the Codex agent history' }),
      railSession({ id: 'assess', title: 'The following is the Codex agent history whose request action you are assessing.' }),
      railSession({ id: 'review', title: '你是独立复审 subagent' }),
      railSession({ id: 'kind', title: '用户会话', native_kind: 'subagent' }),
    ])).toEqual(['ordinary'])
  })

  it('renders placeholder titles as 未命名 or the workspace name', () => {
    expect(displaySessionTitle(railSession({ id: 'empty', title: '' }))).toBe('OpsCore')
    expect(displaySessionTitle(railSession({ id: 'untitled', title: 'Untitled session' }))).toBe('OpsCore')
    expect(displaySessionTitle(railSession({ id: 'remote', title: 'Astrorder 远程会话' }))).toBe('OpsCore')
    expect(displaySessionTitle(railSession({ id: 'uuid', title: '01a08f9c2b7d4e11a5c6d7e8f90ab123' }))).toBe('OpsCore')
    expect(displaySessionTitle(railSession({ id: 'dashed', title: '550e8400-e29b-41d4-a716-446655440000' }))).toBe('OpsCore')
    expect(displaySessionTitle(railSession({ id: 'hermes', title: '20260911_143914_a75faf' }))).toBe('OpsCore')
    expect(displaySessionTitle(railSession({ id: 'no-ws', title: 'Untitled session', workspace: null }))).toBe('未命名')
    expect(displaySessionTitle(railSession({ id: 'real', title: '排查 Codex 思考强度' }))).toBe('排查 Codex 思考强度')
  })
})
