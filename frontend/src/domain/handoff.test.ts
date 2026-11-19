import { describe, expect, it } from 'vitest'

import { connectionName, matchesAgent } from '../components/AgentSessionFilter'
import { buildProjectGroups } from '../components/sessionRailModel'
import { cleanServerName } from '../features/mobile/MobileWorkspace'
import { sessionHandoffTargets } from './handoff'
import type { Agent, Session } from './types'

describe('remote Antigravity parity helpers', () => {
  const remoteAgents: Record<string, Agent> = {
    'ssh-antigravity-wsl': {
      id: 'ssh-antigravity-wsl',
      kind: 'antigravity',
      name: 'Antigravity · WSL',
      status: 'ready',
      connection_id: 'wsl',
      capabilities: ['chat', 'history', 'queue', 'attachments', 'stop'],
      limitation: null,
    },
    'ssh-codex-wsl': {
      id: 'ssh-codex-wsl',
      kind: 'codex',
      name: 'WSL · Codex',
      status: 'ready',
      connection_id: 'wsl',
      capabilities: ['chat', 'history', 'queue', 'attachments', 'stop'],
      limitation: null,
    },
    'local-antigravity': {
      id: 'local-antigravity',
      kind: 'antigravity',
      name: 'Antigravity',
      status: 'ready',
      capabilities: ['chat', 'history', 'queue', 'attachments', 'stop'],
      limitation: null,
    },
  }

  const remoteAgySession: Session = {
    id: '52794208-5f2e-4120-a6b1-99e5ee29c7f1',
    agent_id: 'ssh-antigravity-wsl',
    connection_id: 'wsl',
    title: '远程 Antigravity 会话',
    workspace: '/home/luwei/workspace/astrorder',
    status: 'idle',
    updated_at: '2026-11-18T00:00:00Z',
  }

  const remoteCodexSession: Session = {
    id: 'codex-thread-1',
    agent_id: 'ssh-codex-wsl',
    connection_id: 'wsl',
    title: '远程 Codex 会话',
    workspace: '/home/luwei/workspace/astrorder',
    status: 'idle',
    updated_at: '2026-11-18T00:01:00Z',
  }

  it('supports bidirectional handoff between remote Antigravity and remote Codex on the same SSH host', () => {
    const fromAgy = sessionHandoffTargets(remoteAgySession, remoteAgents)
    expect(fromAgy.map((a) => a.id)).toEqual(['ssh-codex-wsl'])

    const fromCodex = sessionHandoffTargets(remoteCodexSession, remoteAgents)
    expect(fromCodex.map((a) => a.id)).toEqual(['ssh-antigravity-wsl'])
  })

  it('strips Antigravity prefix and suffix cleanly for server labels and filters', () => {
    expect(connectionName(remoteAgents['ssh-antigravity-wsl'])).toBe('WSL')
    expect(cleanServerName(remoteAgents['ssh-antigravity-wsl'])).toBe('WSL')
    expect(
      cleanServerName({
        ...remoteAgents['ssh-antigravity-wsl'],
        name: 'Debian · Antigravity',
      }),
    ).toBe('Debian')
    expect(matchesAgent(remoteAgySession, remoteAgents, 'antigravity')).toBe(true)
    expect(matchesAgent(remoteAgySession, remoteAgents, 'connection:wsl|kind:antigravity')).toBe(true)
  })

  it('groups remote Antigravity and remote Codex sessions on the same host and workspace together', () => {
    const groups = buildProjectGroups([remoteAgySession, remoteCodexSession], remoteAgents, [])
    expect(groups).toHaveLength(1)
    expect(groups[0].remoteLabel).toBe('WSL')
    expect(groups[0].sessions).toHaveLength(2)
  })
})
