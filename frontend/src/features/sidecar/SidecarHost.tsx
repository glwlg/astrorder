import { Suspense, useEffect } from 'react'
import { api } from '../../api/client'
import { notifications } from '@mantine/notifications'
import { ActionIcon, Center, Group, Loader, Tooltip } from '@mantine/core'
import { IconFolderSearch, IconQuote } from '@tabler/icons-react'
import { SidecarHeader } from './SidecarHeader'
import { displaySessionTitle } from '../../components/sessionRailModel'
import { SidecarWelcomeMenu } from './SidecarWelcomeMenu'
import { SessionDetails } from '../chat/SessionDetails'
import { artifactViewerRegistry, useViewerRegistryVersion } from './registry'
import { useSidecarStore } from './sidecarStore'
import { openDatabaseWorkbench, useHasDatabaseTool } from './databaseTool'
import {
  AGENT_GRAPH_VIEWER_PLUGIN_ID,
  BLACKBOARD_VIEWER_PLUGIN_ID,
  BROWSER_VIEWER_PLUGIN_ID,
  FILETREE_VIEWER_PLUGIN_ID,
  MEMORY_VIEWER_PLUGIN_ID,
  SIDECHAT_VIEWER_PLUGIN_ID,
  WORKFLOW_GIT_WORKTREE_PLUGIN_ID,
  WORKSPACE_DIGEST_PLUGIN_ID,
  WORKSPACE_MEETINGS_PLUGIN_ID,
  XTERM_VIEWER_PLUGIN_ID,
} from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { useIsOfficeExperience } from '../office/appProfile'
import { extensionRegistry } from '../extensions/extensionRegistry'
import { useExtensionStore } from '../extensions/extensionStore'
import { PluginErrorBoundary } from '../extensions/PluginErrorBoundary'
import { useWelcomeMenuExtensions } from '../extensions/SlotRenderer'
import {
  EXTERNAL_PLUGIN_PANEL_VIEWER_ID,
  parsePluginPanelArtifact,
  type OpenPluginPanelEventDetail,
} from '../extensions/types'
import type { Agent, Approval, Command, Message, Session } from '../../domain/types'

const OFFICE_HIDDEN_VIEWER_IDS = new Set<string>([
  'xterm-viewer',
  'dbx-viewer',
  'agent-graph-viewer',
  'git-diff-viewer',
  'git-worktrees-viewer',
  'diff-viewer',
  'blackboard-viewer',
  'memory-viewer',
])

const OFFICE_HIDDEN_MEDIA_TYPES = new Set<string>([
  'application/x-terminal',
  'application/x-dbx',
  'application/x-agent-graph',
  'application/x-git-diff',
  'application/x-git-worktrees',
  'application/x-diff',
  'application/x-blackboard',
  'application/x-memory',
])

function ViewerLoadingState() {
  return (
    <Center style={{ height: '100%', minHeight: 160 }} role="status" aria-label="正在加载预览器">
      <Loader size="sm" color="violet" />
    </Center>
  )
}

function isDevOnlySidecarTab(tab: {
  id?: string
  type?: string
  viewerId?: string
  artifact?: { id: string; mediaType?: string; metadata?: Record<string, unknown>; readUrl?: string }
}): boolean {
  if (tab.type !== 'artifact') return false
  if (tab.viewerId && OFFICE_HIDDEN_VIEWER_IDS.has(tab.viewerId)) return true
  if (tab.artifact?.mediaType && OFFICE_HIDDEN_MEDIA_TYPES.has(tab.artifact.mediaType)) return true
  if (tab.artifact) {
    const parsed = parsePluginPanelArtifact({
      id: tab.artifact.id,
      mediaType: tab.artifact.mediaType || '',
      metadata: tab.artifact.metadata,
      readUrl: tab.artifact.readUrl || '',
      name: '',
      kind: 'workspace_file',
      sessionId: '',
      agentId: '',
      writable: false,
    })
    const builtinMatch = extensionRegistry.findBuiltinPanel(
      parsed.pluginId || tab.viewerId,
      parsed.panelId,
      tab.artifact.mediaType,
    )
    if (builtinMatch) {
      const reg = extensionRegistry.getBuiltin(builtinMatch.pluginId)
      if (reg?.welcomeMenuItems?.some((item) => item.hideInOfficeMode)) return true
    }
    const pluginId = parsed.pluginId || (tab.viewerId && tab.viewerId !== EXTERNAL_PLUGIN_PANEL_VIEWER_ID ? tab.viewerId : null)
    if (pluginId) {
      const extPlugin = useExtensionStore.getState().getById(pluginId)
      if (extPlugin?.source === 'external' && extPlugin.category !== 'office') return true
    }
  }
  return false
}

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
  immersive?: boolean
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
  immersive = false,
}: SidecarHostProps) {
  const viewerRegistryVersion = useViewerRegistryVersion()
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  void enabledPlugins

  const tabs = useSidecarStore((state) => state.tabs)
  const mountedTabs = useSidecarStore((state) => state.mountedTabs)
  const activeSessionKey = useSidecarStore((state) => state.activeSessionKey)
  const activeTabId = useSidecarStore((state) => state.activeTabId)
  const dirtyTabs = useSidecarStore((state) => state.dirtyTabs)
  const setActiveTabId = useSidecarStore((state) => state.setActiveTabId)
  const closeTab = useSidecarStore((state) => state.closeTab)
  const setTabDirty = useSidecarStore((state) => state.setTabDirty)
  const hasDatabaseTool = useHasDatabaseTool()
  const fileTreeEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(FILETREE_VIEWER_PLUGIN_ID))
  const terminalEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(XTERM_VIEWER_PLUGIN_ID))
  const browserEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(BROWSER_VIEWER_PLUGIN_ID))
  const sideChatEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(SIDECHAT_VIEWER_PLUGIN_ID))
  const agentGraphEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(AGENT_GRAPH_VIEWER_PLUGIN_ID))
  const blackboardEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(BLACKBOARD_VIEWER_PLUGIN_ID))
  const memoryEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(MEMORY_VIEWER_PLUGIN_ID))
  const worktreeEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKFLOW_GIT_WORKTREE_PLUGIN_ID))
  const digestEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_DIGEST_PLUGIN_ID))
  const meetingsEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKSPACE_MEETINGS_PLUGIN_ID))
  const isOfficeMode = useIsOfficeExperience(session)
  const welcomeExtensionItems = useWelcomeMenuExtensions(session, isOfficeMode)

  const visibleTabs = isOfficeMode ? tabs.filter((t) => !isDevOnlySidecarTab(t)) : tabs
  const activeTab = visibleTabs.find((t) => t.id === activeTabId)
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
    ...visibleTabs.map((tab) => [tabsOwnerKey, tab] as const),
    ...Object.entries(mountedTabs).flatMap(([ownerSessionKey, ownerTabs]) =>
      ownerSessionKey === tabsOwnerKey
        ? []
        : ownerTabs
            .filter((tab) => dirtyTabs[tab.id] && (!isOfficeMode || !isDevOnlySidecarTab(tab)))
            .map((tab) => [ownerSessionKey, tab] as const),
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

  const handleOpenDatabase = () => {
    openDatabaseWorkbench(session.id, session.agent_id, session.connection_id || undefined)
  }

  const handleOpenSideChat = () => {
    useSidecarStore
      .getState()
      .openSideChat(session.id, session.agent_id, displaySessionTitle(session), session.connection_id || undefined)
  }

  const handleOpenBlackboard = () => {
    useSidecarStore.getState().openBlackboard(session.id, session.agent_id, session.connection_id || undefined)
  }

  const handleOpenMemory = () => {
    useSidecarStore.getState().openMemory(session.id)
  }

  const handleOpenWorktrees = () => {
    if (!session.workspace) {
      notifications.show({ color: 'yellow', message: '该会话没有可确认的工作区目录。' })
      return
    }
    useSidecarStore.getState().openWorktrees(
      session.id,
      session.agent_id,
      session.worktree_root_workspace || session.workspace || undefined,
      session.connection_id || undefined,
    )
  }

  useEffect(() => {
    const onOpenPluginPanel = (event: Event) => {
      const detail = (event as CustomEvent<OpenPluginPanelEventDetail>).detail
      if (!detail?.pluginId || !detail?.panelId) return
      if (detail.session && (detail.session.id !== session.id || detail.session.agent_id !== session.agent_id)) {
        return
      }
      const targetSession = detail.session || session
      const builtinPanel = extensionRegistry.findBuiltinPanel(detail.pluginId, detail.panelId, detail.mediaType)
      const extPlugin = useExtensionStore.getState().getById(detail.pluginId)
      const extPanel = extPlugin?.contributes?.sidecar_panels?.find(
        (panel) => panel.id === detail.panelId || (detail.mediaType && panel.media_type === detail.mediaType),
      )
      useSidecarStore.getState().openPluginPanel(detail.pluginId, detail.panelId, {
        title: detail.title || builtinPanel?.panel.title || extPanel?.title || extPlugin?.name || detail.panelId,
        mediaType: detail.mediaType || builtinPanel?.panel.mediaType || extPanel?.media_type,
        viewerId: builtinPanel ? detail.pluginId : EXTERNAL_PLUGIN_PANEL_VIEWER_ID,
        sessionId: targetSession.id,
        agentId: targetSession.agent_id,
        workspacePath: targetSession.workspace || undefined,
        connectionId: targetSession.connection_id || undefined,
        params: detail.params,
      })
    }
    const onInsertComposer = (event: Event) => {
      if (!isVisible) return
      const detail = (event as CustomEvent<{ text?: string }>).detail
      if (!detail?.text) return
      window.dispatchEvent(
        new CustomEvent('astrorder:quote-to-composer', {
          detail: { agentId: session.agent_id, sessionId: session.id, text: detail.text },
        }),
      )
    }
    window.addEventListener('astrorder:open-plugin-panel', onOpenPluginPanel)
    window.addEventListener('astrorder:insert-composer', onInsertComposer)
    return () => {
      window.removeEventListener('astrorder:open-plugin-panel', onOpenPluginPanel)
      window.removeEventListener('astrorder:insert-composer', onInsertComposer)
    }
  }, [isVisible, session])

  const handleOpenAgentGraph = () => {
    useSidecarStore
      .getState()
      .openAgentGraph(session.id, session.agent_id, displaySessionTitle(session), session.connection_id || undefined)
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
      className={`${surface === 'mobile' ? 'mobile-sidecar' : 'desktop-sidecar'}${immersive ? ' is-immersive' : ''}`}
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100%',
        minWidth: 0,
        background: 'var(--astr-surface)',
      }}
    >
      {!immersive && (
        <SidecarHeader
          tabs={visibleTabs}
          activeId={activeTab ? activeTab.id : ''}
          dirtyTabs={dirtyTabs}
          onSelect={setActiveTabId}
          onCloseTab={closeTab}
          onNewPanel={() => setActiveTabId('')}
          onClose={surface === 'desktop' ? onCloseSidecar : undefined}
        />
      )}

      <div style={{ flex: 1, minHeight: 0, overflow: 'auto', position: 'relative' }}>
        {/* 没有活动 Tab 时显示操作菜单；已打开的插件保持挂载，仅切换可见性。 */}
        {!activeTab && (
          <SidecarWelcomeMenu
            session={session}
            isOfficeMode={isOfficeMode}
            onOpenFileTree={fileTreeEnabled ? handleOpenFileTree : undefined}
            onOpenTerminal={!isOfficeMode && terminalEnabled ? handleOpenTerminal : undefined}
            onOpenBrowser={browserEnabled ? handleOpenBrowser : undefined}
            onOpenDatabase={!isOfficeMode && hasDatabaseTool ? handleOpenDatabase : undefined}
            onOpenSideChat={sideChatEnabled ? handleOpenSideChat : undefined}
            onOpenAgentGraph={!isOfficeMode && agentGraphEnabled ? handleOpenAgentGraph : undefined}
            onOpenBlackboard={!isOfficeMode && blackboardEnabled ? handleOpenBlackboard : undefined}
            onOpenMemory={!isOfficeMode && memoryEnabled ? handleOpenMemory : undefined}
            onOpenWorktrees={!isOfficeMode && worktreeEnabled && session.workspace ? handleOpenWorktrees : undefined}
            extraItems={welcomeExtensionItems}
            onOpenDigest={isOfficeMode && digestEnabled ? () => window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: '/digest' } })) : undefined}
            onOpenMeetings={isOfficeMode && meetingsEnabled ? () => window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: '/meetings' } })) : undefined}
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
                  const errorPluginId = viewer.pluginId || viewer.id || tab.viewerId || 'unknown-viewer'
                  const quoteArtifactToComposer = () => {
                    const selected = typeof window !== 'undefined' ? window.getSelection()?.toString().trim() || '' : ''
                    const refLabel = artifact.path || artifact.name
                    const quotedSelection = selected
                      ? isOfficeMode
                        ? selected.slice(0, 1200).split('\n').map((line) => `> ${line}`).join('\n')
                        : `\`\`\`\n${selected.slice(0, 1200)}\n\`\`\``
                      : ''
                    const text = selected
                      ? `参考 ${artifact.name || refLabel}：\n${quotedSelection}\n`
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
                  const isPluginPanelArtifact =
                    tab.viewerId === EXTERNAL_PLUGIN_PANEL_VIEWER_ID ||
                    artifact.id.startsWith('ext:') ||
                    Boolean(artifact.mediaType?.startsWith('application/x-astrorder-ext.'))
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
                      {!isPluginPanelArtifact && artifact.path && artifact.mediaType !== 'application/x-directory' && (
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
                    <PluginErrorBoundary
                      pluginId={errorPluginId}
                      pluginName={viewer.title}
                      resetKey={`${ownerSessionKey}:${tab.id}:${viewer.id}:${viewerRegistryVersion}`}
                    >
                      <Suspense fallback={<ViewerLoadingState />}>
                        <ViewerComponent
                          artifact={artifact}
                          session={ownerSessionKey === currentSessionKey ? session : undefined}
                          isActive={isActive}
                          surface={surface}
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
                      </Suspense>
                    </PluginErrorBoundary>
                  )
                })() : <div style={{ padding: 16 }}>暂无支持的查看器</div>}
              </div>
            )
          })}
      </div>
    </aside>
  )
}
