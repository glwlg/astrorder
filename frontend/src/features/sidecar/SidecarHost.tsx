import { notifications } from '@mantine/notifications'
import { ActionIcon, Tooltip } from '@mantine/core'
import { IconLayoutSidebarRightCollapse, IconPlus } from '@tabler/icons-react'
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
  const activeTabId = useSidecarStore((state) => state.activeTabId)
  const dirtyTabs = useSidecarStore((state) => state.dirtyTabs)
  const setActiveTabId = useSidecarStore((state) => state.setActiveTabId)
  const closeTab = useSidecarStore((state) => state.closeTab)
  const setTabDirty = useSidecarStore((state) => state.setTabDirty)

  const activeTab = tabs.find((t) => t.id === activeTabId)

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
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          borderBottom: '1px solid var(--astr-border)',
          padding: '4px 8px',
          minHeight: '38px',
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

        <Tooltip label="收起侧边栏">
          <ActionIcon variant="subtle" size="sm" color="gray" onClick={onCloseSidecar}>
            <IconLayoutSidebarRightCollapse size={16} />
          </ActionIcon>
        </Tooltip>
      </div>

      <div style={{ flex: 1, minHeight: 0, overflow: 'auto' }}>
        {/* 当没有 Tab 或未激活任何 Tab 时，展现 Codex 风格的欢迎菜单 */}
        {!activeTab ? (
          <SidecarWelcomeMenu
            session={session}
            onOpenFileTree={handleOpenFileTree}
            onOpenTerminal={handleOpenTerminal}
            onOpenBrowser={handleOpenBrowser}
            onOpenSideChat={handleOpenSideChat}
            onOpenAgentGraph={handleOpenAgentGraph}
          />
        ) : activeTab.type === 'details' ? (
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
        ) : activeTab.artifact ? (
          (() => {
            const viewer = artifactViewerRegistry.findViewer(activeTab.artifact)
            if (!viewer) {
              return <div style={{ padding: 16 }}>暂无支持的查看器</div>
            }
            const ViewerComponent = viewer.component
            return (
              <ViewerComponent
                key={activeTab.id}
                artifact={activeTab.artifact}
                onDirtyChange={(dirty) => setTabDirty(activeTab.id, dirty)}
                onSave={(content) =>
                  handleSaveContent(
                    activeTab.artifact?.path,
                    content,
                    activeTab.artifact?.sessionId,
                    activeTab.artifact?.connectionId,
                  )
                }
                onClose={() => closeTab(activeTab.id)}
              />
            )
          })()
        ) : null}
      </div>
    </aside>
  )
}
