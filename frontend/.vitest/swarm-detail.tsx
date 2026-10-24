import React from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { MemoryRouter } from 'react-router-dom'
import '@mantine/core/styles.css'
import '../src/index.css'
import '../src/workspaceVisuals.css'
import { SwarmDagView } from '../src/features/monitor/SwarmDagView'
import { useAstrorderStore } from '../src/state/store'
import { api } from '../src/api/client'
import type { Session } from '../src/domain/types'

// Isolated UI fixture: no production sessions or model calls.
const base = { agent_id: 'fixture-agent', workspace: '/fixture', status: 'idle' as const, updated_at: '2026-10-20T00:00:00Z' }
const a: Session = { ...base, id: 'a', title: '回归主星 A' }
const b: Session = { ...base, id: 'b', title: '回归伴星 B', delegation_parent_agent_id: a.agent_id, delegation_parent_session_id: a.id }
const c: Session = { ...base, id: 'c', title: '回归伴星 C', delegation_parent_agent_id: b.agent_id, delegation_parent_session_id: b.id }
useAstrorderStore.getState().resetRuntime()
useAstrorderStore.setState({ sessions: { 'fixture-agent::a': a, 'fixture-agent::b': b, 'fixture-agent::c': c } })
api.getTelemetry = async () => ({ items: {} })
api.listSos = async () => ({ items: [], count: 0 })
api.getBlackboard = async (namespace) => ({ namespace, items: {}, count: 0 })
api.syncSession = async (id) => ({ ...({ a, b, c }[id]!), status: 'running' })
api.getMessages = async () => ({ items: [], next_cursor: null })
api.getCommands = async () => ({ items: [] })
api.getTasks = async () => ({ items: [] })
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
function Fixture() {
  const sessions = useAstrorderStore(state => state.sessions)
  const root = new URLSearchParams(location.search).get('root') || 'fixture-agent::a'
  return <div style={{ height: '100vh' }}><SwarmDagView allSessions={Object.values(sessions)} agents={{ 'fixture-agent': { id: 'fixture-agent', name: '回归 Agent', kind: 'hermes', status: 'ready', capabilities: ['chat'], limitation: null } }} activeRootKey={root} onOpenSession={() => { throw new Error('Double click must stay on the star map') }} /></div>
}
createRoot(document.getElementById('root')!).render(<MantineProvider><MemoryRouter><QueryClientProvider client={queryClient}><Fixture /></QueryClientProvider></MemoryRouter></MantineProvider>)
