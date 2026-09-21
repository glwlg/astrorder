import { notifications } from '@mantine/notifications'
import { ActionIcon, Tooltip } from '@mantine/core'
import { IconFolderSearch, IconLayoutSidebarRightCollapse, IconPlus } from '@tabler/icons-react'
import { SidecarTabs } from './SidecarTabs'
import { SidecarWelcomeMenu } from './SidecarWelcomeMenu'
import { SessionDetails } from '../chat/SessionDetails'
import { artifactViewerRegistry } from './registry'
import { useSidecarStore } from './sidecarStore'
import type { Agent, Approval, Command, Message, Session } from '../../domain/types'

interface SidecarHostProps {
  session: Session
  agent?: Agent
  commands: Command[]
  messages: Message[]
  approvals: Approval[]
  onApproval: (approval: Approval, action: 'approve' | 'cancel') => void
  onCloseSidecar: () => void
}

export function SidecarHost({
  session,
  agent,
  commands,
  messages,
  approvals,
  onApproval,
  onCloseSidecar,
}: SidecarHostProps) {
  const tabs = useSidecarStore((state) => state.tabs)
  const mountedTabs = useSidecarStore((state) => state.mountedTabs)
  const activeSessionKey = useSidecarStore((state) => state.activeSessionKey)
  const activeTabId = useSidecarStore((state) => state.activeTabId)
  const dirtyTabs = useSidecarStore((state) => state.dirtyTabs)
  const setActiveTabId = useSidecarStore((state) => state.setActiveTabId)
  const closeTab = useSidecarStore((state) => state.closeTab)
  const setTabDirty = useSidecarStore((state) => state.setTabDirty)

  const activeTab = tabs.find((t) => t.id === activeTabId)
  const currentSessionKey = `${session.agent_id}:${session.id}`
  const liveSessionTabs = activeSessionKey
    ? { ...mountedTabs, [activeSessionKey]: tabs }
    : mountedTabs

  const handleOpenFileTree = () => {
    useSidecarStore
      .getState()
      .openFileTree(
        session.id,
        session.agent_id,
        session.workspace || undefined,
        session.project_name || session.title,
        session.connection_id || undefined,
      )
  }

  const handleOpenTerminal = () => {
    useSidecarStore
      .getState()
      .openTerminal(
        session.id,
        session.agent_id,
        session.project_name || session.title,
        session.connection_id || undefined,
      )
  }

  const handleOpenBrowser = () => {
    useSidecarStore
      .getState()
      .openBrowser(session.id, session.agent_id, undefined, session.connection_id || undefined)
  }

  const handleOpenSideChat = () => {
    useSidecarStore
      .getState()
      .openSideChat(session.id, session.agent_id, session.title || undefined, session.connection_id || undefined)
  }

  const handleOpenBlackboard = () => {
    useSidecarStore.getState().openBlackboard(session.id, session.agent_id, session.connection_id || undefined)
  }

  const handleOpenAgentGraph = () => {
    useSidecarStore
      .getState()
      .openAgentGraph(session.id, session.agent_id, session.title || undefined, session.connection_id || undefined)
  }

  const handleSaveContent = async (
    path: string | undefined,
    content: string,
    sessionId?: string,
    connectionId?: string,
  ): Promise<boolean> => {
    if (!path) return false
    try {
      const resp = await fetch('/api/v1/system/write-file', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          path,
          content,
          session_id: sessionId || session.id,
          connection_id: connectionId || session.connection_id,
        }),
      })
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}))
        const detail = data?.detail || `保存失败: HTTP ${resp.status}`
        notifications.show({
          color: 'red',
          message: detail,
        })
        return false
      }
      return true
    } catch (err: unknown) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存网络异常',
      })
      return false
    }
  }

  return (
    <aside
      className="desktop-sidecar"
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minWidth: 0,
        borderLeft: '1px solid var(--astr-border)',
        background: 'var(--astr-surface)',
      }}
    >
      <div
        className="sidecar-header-bar"
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          borderBottom: '1px solid var(--astr-border)',
          height: 'var(--astr-header-height, 48px)',
          minHeight: 'var(--astr-header-height, 48px)',
          maxHeight: 'var(--astr-header-height, 48px)',
          padding: '0 10px',
          boxSizing: 'border-box',
          background: 'var(--astr-surface)',
          flexShrink: 0,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: '6px', minWidth: 0, flex: 1, overflow: 'hidden' }}>
          <SidecarTabs
            tabs={tabs}
            activeId={activeTabId}
            dirtyTabs={dirtyTabs}
            onSelect={setActiveTabId}
            onClose={closeTab}
          />
          <Tooltip label="新建面板（返回操作菜单）">
            <ActionIcon
              variant="subtle"
              size="sm"
              color="gray"
              onClick={() => setActiveTabId('')}
              title="新建面板"
            >
              <IconPlus size={14} />
            </ActionIcon>
          </Tooltip>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
          <Tooltip label="收起工作台">
            <ActionIcon variant="subtle" size="sm" color="gray" onClick={onCloseSidecar} aria-label="收起工作台">
              <IconLayoutSidebarRightCollapse size={16} />
            </ActionIcon>
          </Tooltip>
        </div>
      </div>

      <div style={{ flex: 1, minHeight: 0, overflow: 'auto', position: 'relative' }}>
        {/* 没有活动 Tab 时显示操作菜单；已打开的插件保持挂载，仅切换可见性。 */}
        {!activeTab && (
          <SidecarWelcomeMenu
            session={session}
            onOpenFileTree={handleOpenFileTree}
            onOpenTerminal={handleOpenTerminal}
            onOpenBrowser={handleOpenBrowser}
            onOpenSideChat={handleOpenSideChat}
            onOpenAgentGraph={handleOpenAgentGraph}
            onOpenBlackboard={handleOpenBlackboard}
          />
        )}
        {activeTab?.type === 'details' && (
          <div style={{ padding: '8px 16px' }}>
            <SessionDetails
              session={session}
              agent={agent}
              commands={commands}
              messages={messages}
              approvals={approvals}
              onApproval={onApproval}
            />
          </div>
        )}
        {Object.entries(liveSessionTabs).flatMap(([ownerSessionKey, ownerTabs]) =>
          ownerTabs.filter((tab) => tab.type === 'artifact' && tab.artifact).map((tab) => {
            const artifact = tab.artifact!
            const viewer = artifactViewerRegistry.findViewer(artifact)
            const isActive = ownerSessionKey === currentSessionKey && tab.id === activeTabId
            return (
              <div
                key={`${ownerSessionKey}:${tab.id}`}
                aria-hidden={!isActive}
                style={{ display: isActive ? 'block' : 'none', height: '100%', minHeight: 0 }}
              >
                {viewer ? (() => {
                  const ViewerComponent = viewer.component
                  const toolbarAction = artifact.path && artifact.mediaType !== 'application/x-directory' ? (
                    <Tooltip label="在文件树中定位">
                      <ActionIcon
                        variant="subtle"
                        size="sm"
                        color="gray"
                        aria-label="在文件树中定位"
                        onClick={() => useSidecarStore.getState().revealFileInTree(
                          artifact.sessionId,
                          artifact.agentId,
                          artifact.path!,
                          session.workspace || undefined,
                          session.project_name || session.title,
                          artifact.connectionId,
                        )}
                      >
                        <IconFolderSearch size={16} />
                      </ActionIcon>
                    </Tooltip>
                  ) : undefined
                  return (
                    <ViewerComponent
                      artifact={artifact}
                      isActive={isActive}
                      toolbarAction={toolbarAction}
                      onDirtyChange={(dirty) => setTabDirty(tab.id, dirty)}
                      onSave={(content) =>
                        handleSaveContent(
                          artifact.path,
                          content,
                          artifact.sessionId,
                          artifact.connectionId,
                        )
                      }
                      onClose={() => closeTab(tab.id)}
                    />
                  )
                })() : <div style={{ padding: 16 }}>暂无支持的查看器</div>}
              </div>
            )
          }),
        )}
      </div>
    </aside>
  )
}
