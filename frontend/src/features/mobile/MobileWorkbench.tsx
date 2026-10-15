import { useEffect, useState } from 'react'
import { IconSearch, IconX } from '@tabler/icons-react'
import type { Agent, Approval, Command, Message, Session } from '../../domain/types'
import { GitStatusBar } from '../chat/GitStatusBar'
import { QuickOpen } from '../chat/QuickOpen'
import { SidecarHost } from '../sidecar/SidecarHost'
import { useSidecarStore } from '../sidecar/sidecarStore'

export function MobileWorkbench({ session, agent, commands, messages, approvals, onApproval, opened, onClose }: {
  session: Session
  agent?: Agent
  commands: Command[]
  messages: Message[]
  approvals: Approval[]
  onApproval: (approval: Approval, action: 'approve' | 'cancel') => void
  opened: boolean
  onClose: () => void
}) {
  const [searching, setSearching] = useState(false)
  useEffect(() => { useSidecarStore.getState().switchSession(`${session.agent_id}:${session.id}`) }, [session.agent_id, session.id])
  useEffect(() => { if (!opened) setSearching(false) }, [opened])
  useEffect(() => {
    if (!opened) return
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || searching || document.querySelector('.mantine-Modal-content, [data-menu-dropdown="true"]:not([style*="display: none"])')) return
      onClose()
    }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [opened, searching, onClose])
  return <div className="m-workbench-overlay" hidden={!opened}>
    <section className="m-workbench" role={opened ? 'dialog' : undefined} aria-label="会话工作区" aria-modal={opened || undefined}>
      <header className="m-workbench-heading"><strong>工作区</strong><button aria-label="关闭工作区" onClick={onClose}><IconX size={20} /></button></header>
      <div className="m-workbench-tools">
        <button disabled={!session.workspace} onClick={() => setSearching(true)}><IconSearch size={16} />快速打开文件</button>
        <GitStatusBar key={`${session.agent_id}:${session.id}`} session={session} />
      </div>
      <div className="m-workbench-content"><SidecarHost session={session} agent={agent} commands={commands} messages={messages} approvals={approvals} onApproval={onApproval} onCloseSidecar={onClose} surface="mobile" isVisible={opened} /></div>
      <QuickOpen key={`${session.agent_id}:${session.id}`} session={session} opened={searching} onClose={() => setSearching(false)} />
    </section>
  </div>
}
