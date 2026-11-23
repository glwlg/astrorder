import { useEffect } from 'react'
import type { Agent, Message, Session, SessionSchedule, SessionSwarm } from '../../domain/types'
import { isAntigravityAgent } from '../../hooks/useSessionSwarm'
import { GlassPanel } from '../../components/GlassPanel'
import { WorkspaceChangesContent } from './WorkspaceChangesContent'

export function WorkspaceChangesPanel({
  session,
  agent,
  messages,
  swarm,
  swarmLoading,
  swarmFetching,
  swarmError,
  onRefreshSwarm,
  schedules,
  schedulesLoading,
  schedulesError,
  onOpenSession,
  onClose,
}: {
  session: Session
  agent?: Agent
  messages: Message[]
  swarm?: SessionSwarm
  swarmLoading?: boolean
  swarmFetching?: boolean
  swarmError?: unknown
  onRefreshSwarm?: () => void
  schedules?: SessionSchedule[]
  schedulesLoading?: boolean
  schedulesError?: unknown
  onOpenSession?: (agentId: string, sessionId: string) => void
  onClose: () => void
}) {
  const showSwarm = isAntigravityAgent(agent)
  useEffect(() => {
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented) return
      event.preventDefault()
      onClose()
      document.querySelector<HTMLButtonElement>('.chat-changes-toggle')?.focus()
    }
    document.addEventListener('keydown', closeOnEscape)
    return () => document.removeEventListener('keydown', closeOnEscape)
  }, [onClose])

  return (
    <aside
      id="workspace-changes-panel"
      className="workspace-changes-panel"
      aria-label={showSwarm ? 'Swarm、定时与产物' : '定时与产物'}
      data-liquid-ignore
    >
      <GlassPanel active snapshot=".chat-workbench">
        <WorkspaceChangesContent
          key={`${session.agent_id}:${session.id}`}
          session={session}
          agent={agent}
          messages={messages}
          swarm={swarm}
          swarmLoading={swarmLoading}
          swarmFetching={swarmFetching}
          swarmError={swarmError}
          onRefreshSwarm={onRefreshSwarm}
          schedules={schedules}
          schedulesLoading={schedulesLoading}
          schedulesError={schedulesError}
          onOpenSession={onOpenSession}
          onClose={onClose}
        />
      </GlassPanel>
    </aside>
  )
}
