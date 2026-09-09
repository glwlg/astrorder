import { expect, it } from 'vitest'
import type { Agent, Session } from '../domain/types'
import { matchesAgent } from './AgentSessionFilter'
import { agentEnvironment } from './NewSessionDialog'
const agents: Record<string, Agent> = {
 h: { id: 'h', name: 'Hermes', kind: 'hermes', source_id: 'hermes-local-digest', status: 'ready', capabilities: ['chat'], limitation: null },
 c: { id: 'c', name: 'Codex', kind: 'codex', source_id: 'local-codex', status: 'ready', capabilities: ['chat'], limitation: null },
 r: { id: 'r', name: 'WSL Codex', kind: 'codex', connection_id: 'ssh-wsl', status: 'ready', capabilities: ['chat'], limitation: null },
}
it('filters type or exact Agent without sorting sessions', () => {
 const rows = ['r', 'h', 'c'].map(agent_id => ({ agent_id }) as Session)
 expect(rows.filter(s => matchesAgent(s, agents, 'codex')).map(s => s.agent_id)).toEqual(['r', 'c'])
 expect(rows.filter(s => matchesAgent(s, agents, 'r')).map(s => s.agent_id)).toEqual(['r'])
 expect(rows.filter(s => matchesAgent(s, agents, 'all'))).toEqual(rows)
})
it('allows local peer Agents but isolates remote connection candidates', () => {
 expect(agentEnvironment(agents.h)).toBe(agentEnvironment(agents.c))
 expect(agentEnvironment(agents.r)).not.toBe(agentEnvironment(agents.h))
})
