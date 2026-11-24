import { useEffect } from 'react'
import { api } from '../../api/client'
import { notifications } from '@mantine/notifications'
import { ActionIcon, Group, Tooltip } from '@mantine/core'
import { IconFolderSearch, IconQuote } from '@tabler/icons-react'
import { SidecarHeader } from './SidecarHeader'
import { SidecarWelcomeMenu } from './SidecarWelcomeMenu'
import { SessionDetails } from '../chat/SessionDetails'
import { artifactViewerRegistry } from './registry'
import { useSidecarStore } from './sidecarStore'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { DBX_PLUGIN_ID } from './viewers/dbx'
import type { Agent, Approval, Command, Message, Session } from '../../domain/types'

interface SidecarHostProps {
  session: Session
  agent?: Agent
  commands: Command[]
  messages: Message[]
  approvals: Approval[]
  onApproval: (approval: Approval, action: 'approve' | 'cancel') => void
  onCloseSidecar: () => void
  surface?: 'desktop' | 'mobile'
  isVisible?: boolean
}

export function SidecarHost({
  session,
  agent,
  commands,
  messages,
  approvals,
  onApproval,
  onCloseSidecar,
  surface = 'desktop',
  isVisible = true,
}: SidecarHostProps) {
  const tabs = useSidecarStore((state) => state.tabs)
  const mountedTabs = useSidecarStore((state) => state.mountedTabs)
  const activeSessionKey = useSidecarStore((state) => state.activeSessionKey)
  const activeTabId = useSidecarStore((state) => state.activeTabId)
  const dirtyTabs = useSidecarStore((state) => state.dirtyTabs)
  const setActiveTabId = useSidecarStore((state) => state.setActiveTabId)
  const closeTab = useSidecarStore((state) => state.closeTab)
  const setTabDirty = useSidecarStore((state) => state.setTabDirty)
  const dbxPluginEnabled = usePluginSettingsStore((state) => state.isPluginEnabled(DBX_PLUGIN_ID))

  const activeTab = tabs.find((t) => t.id === activeTabId)
  const currentSessionKey = `${session.agent_id}:${session.id}`
  useEffect(() => {
    if (activeSessionKey === currentSessionKey && session.workspace) {
      useSidecarStore.getState().syncFileTreeWorkspace(
        session.id, session.workspace, session.project_name || session.title,
      )
    }
  }, [activeSessionKey, currentSessionKey, session.id, session.workspace, session.project_name, session.title])
  const tabsOwnerKey = activeSessionKey || currentSessionKey
  const retainedTabs = [
    ...tabs.map((tab) => [tabsOwnerKey, tab] as const),
    ...Object.entries(mountedTabs).flatMap(([ownerSessionKey, ownerTabs]) =>
      ownerSessionKey === tabsOwnerKey
        ? []
        : ownerTabs.filter((tab) => dirtyTabs[tab.id]).map((tab) => [ownerSessionKey, tab] as const),
    ),
  ]

  const handleOpenFileTree = () => {
    if (!session.workspace) {
      notifications.show({ color: 'yellow', message: '该会话没有可确认的工作区目录。' })
      return
    }
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

  const handleOpenDbx = () => {
    useSidecarStore
      .getState()
      .openDbx(session.id, session.agent_id, session.connection_id || undefined)
  }

  const handleOpenSideChat = () => {
    useSidecarStore
      .getState()
      .openSideChat(session.id, session.agent_id, session.title || undefined, session.connection_id || undefined)
  }

  const handleOpenBlackboard = () => {
    useSidecarStore.getState().openBlackboard(session.id, session.agent_id, session.connection_id || undefined)
  }

  const handleOpenMemory = () => {
    useSidecarStore.getState().openMemory(session.id)
  }

  const handleOpenAgentGraph = () => {
    useSidecarStore
      .getState()
      .openAgentGraph(session.id, session.agent_id, session.title || undefined, session.connection_id || undefined)
  }

  const handleSaveContent = async (
    path: string | undefined,
    content: string,
    sessionId: string,
    connectionId?: string,
  ): Promise<boolean> => {
    if (!path) return false
    try {
      await api.writeFile({
        path,
        content,
        session_id: sessionId,
        connection_id: connectionId,
      })
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
      className={surface === 'mobile' ? 'mobile-sidecar' : 'desktop-sidecar'}
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minWidth: 0,
        background: 'var(--astr-surface)',
      }}
    >
      <SidecarHeader
        tabs={tabs}
        activeId={activeTabId}
        dirtyTabs={dirtyTabs}
        onSelect={setActiveTabId}
        onCloseTab={closeTab}
        onNewPanel={() => setActiveTabId('')}
        onClose={surface === 'desktop' ? onCloseSidecar : undefined}
      />

      <div style={{ flex: 1, minHeight: 0, overflow: 'auto', position: 'relative' }}>
        {/* 没有活动 Tab 时显示操作菜单；已打开的插件保持挂载，仅切换可见性。 */}
        {!activeTab && (
          <SidecarWelcomeMenu
            session={session}
            onOpenFileTree={handleOpenFileTree}
            onOpenTerminal={handleOpenTerminal}
            onOpenBrowser={handleOpenBrowser}
            onOpenDbx={dbxPluginEnabled ? handleOpenDbx : undefined}
            onOpenSideChat={handleOpenSideChat}
            onOpenAgentGraph={handleOpenAgentGraph}
            onOpenBlackboard={handleOpenBlackboard}
            onOpenMemory={handleOpenMemory}
          />
        )}
        {activeTab?.type === 'details' && (
          <div className="sidecar-session-details">
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
        {retainedTabs.filter(([, tab]) => tab.type === 'artifact' && tab.artifact).map(([ownerSessionKey, tab]) => {
            const artifact = tab.artifact!
            const viewer = artifactViewerRegistry.findViewer(artifact)
            const isActive = isVisible && ownerSessionKey === currentSessionKey && tab.id === activeTabId
            return (
              <div
                key={`${ownerSessionKey}:${tab.id}`}
                aria-hidden={!isActive}
                style={{ display: isActive ? 'block' : 'none', height: '100%', minHeight: 0 }}
              >
                {viewer ? (() => {
                  const ViewerComponent = viewer.component
                  const quoteArtifactToComposer = () => {
                    const selected = typeof window !== 'undefined' ? window.getSelection()?.toString().trim() || '' : ''
                    const refLabel = artifact.path || artifact.name
                    const text = selected
                      ? `参考 ${refLabel}：\n\`\`\`\n${selected.slice(0, 1200)}\n\`\`\`\n`
                      : `参考 \`${refLabel}\` `
                    window.dispatchEvent(
                      new CustomEvent('astrorder:quote-to-composer', {
                        detail: { agentId: session.agent_id, sessionId: session.id, text },
                      }),
                    )
                    notifications.show({
                      color: 'violet',
                      message: selected ? `已引用所选片段（${refLabel}）到输入框` : `已引用 ${refLabel} 到输入框`,
                    })
                  }
                  const toolbarAction = (
                    <Group gap={4} wrap="nowrap">
                      <Tooltip label="引用到输入框追问（选中片段可带代码引用）">
                        <ActionIcon
                          variant="subtle"
                          size="sm"
                          color="violet"
                          aria-label="引用到输入框"
                          onClick={quoteArtifactToComposer}
                        >
                          <IconQuote size={15} />
                        </ActionIcon>
                      </Tooltip>
                      {artifact.path && artifact.mediaType !== 'application/x-directory' && (
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
                      )}
                    </Group>
                  )
                  return (
                    <ViewerComponent
                      artifact={artifact}
                      session={ownerSessionKey === currentSessionKey ? session : undefined}
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
          })}
      </div>
    </aside>
  )
}
