import { useEffect } from 'react'
import type { Message, Session } from '../../domain/types'
import { GlassPanel } from '../../components/GlassPanel'
import { WorkspaceChangesContent } from './WorkspaceChangesContent'

export function WorkspaceChangesPanel({ session, messages, onClose }: {
  session: Session
  messages: Message[]
  onClose: () => void
}) {
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
    <aside id="workspace-changes-panel" className="workspace-changes-panel" aria-label="工作区变更" data-liquid-ignore>
      <GlassPanel active snapshot=".chat-workbench">
        <WorkspaceChangesContent key={`${session.agent_id}:${session.id}`} session={session} messages={messages} onClose={onClose} />
      </GlassPanel>
    </aside>
  )
}
