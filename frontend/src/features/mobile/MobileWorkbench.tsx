import { useEffect, useState } from 'react'
import { Popover } from '@mantine/core'
import { IconGitBranch, IconMaximize, IconMinimize, IconSearch, IconX } from '@tabler/icons-react'
import type { Agent, Approval, Command, Message, Session } from '../../domain/types'
import { GitStatusBar } from '../chat/GitStatusBar'
import { QuickOpen } from '../chat/QuickOpen'
import { useIsOfficeExperience } from '../office/appProfile'
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
  const [gitOpen, setGitOpen] = useState(false)
  const [immersive, setImmersive] = useState(false)
  const isOfficeMode = useIsOfficeExperience(session)
  const tabs = useSidecarStore((state) => state.tabs)
  const activeTabId = useSidecarStore((state) => state.activeTabId)
  const activeTab = tabs.find((t) => t.id === activeTabId)
  const hasActiveContent = Boolean(activeTab)

  useEffect(() => { useSidecarStore.getState().switchSession(`${session.agent_id}:${session.id}`) }, [session.agent_id, session.id])
  useEffect(() => {
    if (!opened) {
      setSearching(false)
      setGitOpen(false)
      setImmersive(false)
    }
  }, [opened])
  useEffect(() => {
    if (!hasActiveContent && immersive) setImmersive(false)
  }, [hasActiveContent, immersive])
  useEffect(() => {
    if (!opened) return
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || searching || document.querySelector('.mantine-Modal-content, [data-menu-dropdown="true"]:not([style*="display: none"])')) return
      if (immersive) {
        setImmersive(false)
        return
      }
      onClose()
    }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [opened, searching, immersive, onClose])

  return (
    <div className="m-workbench-overlay" hidden={!opened}>
      <section
        className={`m-workbench${immersive ? ' is-immersive' : ''}`}
        role={opened ? 'dialog' : undefined}
        aria-label="会话工作区"
        aria-modal={opened || undefined}
      >
        {!immersive && (
          <header className="m-workbench-heading">
            <strong className="m-workbench-title" title={activeTab?.title || '工作区'}>
              {activeTab?.title || '工作区'}
            </strong>
            <div className="m-workbench-heading-actions">
              <button
                type="button"
                className="m-workbench-icon-btn"
                disabled={!session.workspace}
                aria-label="快速打开文件"
                title="快速打开文件"
                onClick={() => setSearching(true)}
              >
                <IconSearch size={18} aria-hidden="true" />
              </button>
              {!isOfficeMode && session.workspace && (
                <Popover
                  opened={gitOpen}
                  onChange={setGitOpen}
                  position="bottom-end"
                  withArrow
                  shadow="md"
                  withinPortal
                >
                  <Popover.Target>
                    <button
                      type="button"
                      className={`m-workbench-icon-btn${gitOpen ? ' is-active' : ''}`}
                      aria-label="Git 状态与分支"
                      title="Git 状态与分支"
                      onClick={() => setGitOpen((v) => !v)}
                    >
                      <IconGitBranch size={18} aria-hidden="true" />
                    </button>
                  </Popover.Target>
                  <Popover.Dropdown p="xs" style={{ maxWidth: 'min(92vw, 340px)' }}>
                    <GitStatusBar key={`${session.agent_id}:${session.id}`} session={session} />
                  </Popover.Dropdown>
                </Popover>
              )}
              {hasActiveContent && (
                <button
                  type="button"
                  className="m-workbench-icon-btn"
                  aria-label="沉浸全屏阅读"
                  title="沉浸全屏阅读"
                  onClick={() => setImmersive(true)}
                >
                  <IconMaximize size={17} aria-hidden="true" />
                </button>
              )}
              <button
                type="button"
                className="m-workbench-icon-btn"
                aria-label="关闭工作区"
                title="关闭工作区"
                onClick={onClose}
              >
                <IconX size={19} aria-hidden="true" />
              </button>
            </div>
          </header>
        )}

        <div className="m-workbench-content">
          <SidecarHost
            session={session}
            agent={agent}
            commands={commands}
            messages={messages}
            approvals={approvals}
            onApproval={onApproval}
            onCloseSidecar={onClose}
            surface="mobile"
            isVisible={opened}
            immersive={immersive}
          />
          {immersive && (
            <button
              type="button"
              className="m-workbench-immersive-exit"
              aria-label="退出全屏"
              onClick={() => setImmersive(false)}
            >
              <IconMinimize size={15} aria-hidden="true" />
              <span>退出全屏</span>
            </button>
          )}
        </div>

        <QuickOpen key={`${session.agent_id}:${session.id}`} session={session} opened={searching} onClose={() => setSearching(false)} />
      </section>
    </div>
  )
}
