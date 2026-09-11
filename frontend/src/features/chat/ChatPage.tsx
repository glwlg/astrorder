import {
  IconFolder,
  IconInfoCircle,
  IconLayoutSidebarRightCollapse,
  IconLayoutSidebarRightExpand,
  IconSparkles,
  IconX,
} from '@tabler/icons-react'
import { ActionIcon, Button, Drawer, Group, Paper, Title, Tooltip } from '@mantine/core'
import { useDisclosure, useMediaQuery } from '@mantine/hooks'
import { useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useShallow } from 'zustand/react/shallow'
import { useParams, useSearchParams } from 'react-router-dom'
import { ApiError, api } from '../../api/client'
import { newCommandId, scopeKey } from '../../domain/semantics'
import type { Approval, Command, Session, Task } from '../../domain/types'
import { EmptyState } from '../../components/EmptyState'
import { SessionStatusLabel } from '../../components/Status'
import { sessionActivityStatus } from '../../components/sessionRailModel'
import { AgentKindBadge } from '../../components/SessionRuntimeFacts'
import { selectApprovals, selectCommands, selectOutbox, selectSessions, selectTasks, useAstrorderStore } from '../../state/store'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { useSessionModel } from '../../hooks/useSessionModel'
import { ChatComposer } from './ChatComposer'

import { SessionDetails } from './SessionDetails'
import { SessionRuntimeBar } from './SessionRuntimeBar'
import { TaskDetails } from './TaskDetails'
import { Transcript } from './Transcript'
import { SidecarHost } from '../sidecar/SidecarHost'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { ErrorBoundary } from '../../components/ErrorBoundary'


const NO_COMMANDS: Command[] = []
const NO_OUTBOX: import('../../domain/types').OutboxEntry[] = []
const NO_APPROVALS: Approval[] = []
const NO_TASKS: Task[] = []

function errorText(error: unknown): string {
  if (error instanceof ApiError) return error.detail
  if (error instanceof Error) return error.message
  return '会话数据读取失败。'
}

function resolveSession(sessions: Session[], sessionId: string | undefined, agentId: string | null): Session | null {
  if (!sessionId) return sessions.length === 1 ? sessions[0] : null
  const decodedId = decodeURIComponent(sessionId)
  const candidates = sessions.filter((session) => session.id === decodedId || session.id === sessionId)
  if (agentId) return candidates.find((session) => session.agent_id === agentId) || null
  return candidates.length === 1 ? candidates[0] : null
}

export function ChatPage() {
  const { sessionId } = useParams()
  const [searchParams] = useSearchParams()
  const queryClient = useQueryClient()
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const agents = useAstrorderStore((state) => state.agents)
  const selected = useMemo(
    () => resolveSession(sessions, sessionId ? decodeURIComponent(sessionId) : undefined, searchParams.get('agent_id')),
    [searchParams, sessionId, sessions],
  )
  const [detailsOpened, { open: openDetails, close: closeDetails }] = useDisclosure(false)
  const [detailsPinned, setDetailsPinned] = useState(false)
  const layoutRef = useRef<HTMLDivElement>(null)
  const sidecarOpen = useSidecarStore((state) => state.isOpen)
  const setSidecarOpen = useSidecarStore((state) => state.setIsOpen)
  const switchSession = useSidecarStore((state) => state.switchSession)
  const sidecarWidth = useSidecarStore((state) => state.sidecarWidth)
  const setSidecarWidth = useSidecarStore((state) => state.setSidecarWidth)
  const [isResizing, setIsResizing] = useState(false)

  // 当会话/项目发生切换时，自动保存并切换侧边栏记忆状态，保持每个会话 Tab 隔离且不丢失
  useEffect(() => {
    if (selected) {
      const sessionKey = `${selected.agent_id}:${selected.id}`
      switchSession(sessionKey)
    }
  }, [selected?.id, selected?.agent_id, switchSession])

  // 拖动调宽逻辑：基于 chat-layout 容器的实际几何位置与宽度进行精确计算
  useEffect(() => {
    if (!isResizing) return
    const handleMouseMove = (e: MouseEvent) => {
      if (!layoutRef.current) return
      const rect = layoutRef.current.getBoundingClientRect()
      const layoutWidth = rect.width
      if (layoutWidth <= 0) return
      // 鼠标相对于 chat-layout 右边界的距离
      const rightPx = rect.right - e.clientX
      const pct = (rightPx / layoutWidth) * 100
      setSidecarWidth(pct)
    }
    const handleMouseUp = () => {
      setIsResizing(false)
    }
    window.addEventListener('mousemove', handleMouseMove)
    window.addEventListener('mouseup', handleMouseUp)
    return () => {
      window.removeEventListener('mousemove', handleMouseMove)
      window.removeEventListener('mouseup', handleMouseUp)
    }
  }, [isResizing, setSidecarWidth])

  // 全局快捷键监听（对齐 Codex：Ctrl+P 打开文件树, Ctrl+Alt+S 侧边聊天, Ctrl+T 浏览器, Ctrl+` 终端）
  // 必须在 capture 阶段或尽早拦截并 preventDefault，屏蔽浏览器默认行为（如 Ctrl+P 打印, Ctrl+T 新标签页）
  useEffect(() => {
    const handleGlobalKeyDown = (e: KeyboardEvent) => {
      if (!selected) return
      const isCtrlOrMeta = e.ctrlKey || e.metaKey

      // 1. 侧边聊天快捷键：Ctrl + Alt + S
      if (isCtrlOrMeta && e.altKey && (e.key === 's' || e.key === 'S')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openSideChat(
            selected.id,
            selected.agent_id,
            selected.title || undefined,
            selected.connection_id || undefined,
          )
        return
      }

      // 决策状态机快捷键：Ctrl + Alt + G
      if (isCtrlOrMeta && e.altKey && (e.key === 'g' || e.key === 'G')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openAgentGraph(
            selected.id,
            selected.agent_id,
            selected.title || undefined,
            selected.connection_id || undefined,
          )
        return
      }

      // 2. 文件树快捷键：Ctrl + P (屏蔽浏览器原生打印)
      if (isCtrlOrMeta && !e.altKey && !e.shiftKey && (e.key === 'p' || e.key === 'P')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openFileTree(
            selected.id,
            selected.agent_id,
            selected.workspace || undefined,
            selected.project_name || selected.title,
            selected.connection_id || undefined,
          )
        return
      }

      // 3. 内置浏览器快捷键：Ctrl + T (屏蔽浏览器打开新标签页)
      if (isCtrlOrMeta && !e.altKey && !e.shiftKey && (e.key === 't' || e.key === 'T')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openBrowser(
            selected.id,
            selected.agent_id,
            undefined,
            selected.connection_id || undefined,
          )
        return
      }

      // 4. 内置终端快捷键：Ctrl + `
      if (isCtrlOrMeta && !e.altKey && !e.shiftKey && (e.key === '`' || e.key === '~')) {
        e.preventDefault()
        e.stopPropagation()
        useSidecarStore
          .getState()
          .openTerminal(
            selected.id,
            selected.agent_id,
            selected.project_name || selected.title,
            selected.connection_id || undefined,
          )
        return
      }
    }

    window.addEventListener('keydown', handleGlobalKeyDown, { capture: true })
    return () => {
      window.removeEventListener('keydown', handleGlobalKeyDown, { capture: true })
    }
  }, [selected])
  const [taskDetailsOpened, { open: openTaskDetails, close: closeTaskDetails }] = useDisclosure(false)
  const [selectedTask, setSelectedTask] = useState<Task | null>(null)
  const [previewImage, setPreviewImage] = useState<string | null>(null)
  const [composerHeight, setComposerHeight] = useState<number>(124)
  const mobileTaskSheet = useMediaQuery('(max-width: 767px)')
  const resources = useSessionResources(selected, true)
  const modelBinding = useSessionModel(selected)
  const messages = resources.visibleMessages
  const commands = useAstrorderStore(useShallow((state) => selected ? selectCommands(state, selected.agent_id, selected.id) : NO_COMMANDS))
  const outbox = useAstrorderStore(useShallow((state) => selected ? selectOutbox(state, selected.agent_id, selected.id) : NO_OUTBOX))
  const approvals = useAstrorderStore(useShallow((state) => selected ? selectApprovals(state, selected.agent_id, selected.id) : NO_APPROVALS))
  const tasks = useAstrorderStore(useShallow((state) => selected ? selectTasks(state, selected.agent_id, selected.id) : NO_TASKS))
  const agent = useMemo(() => {
    if (!selected) return undefined
    // 优先精确匹配
    if (agents[selected.agent_id]) return agents[selected.agent_id]
    // 跨别名匹配：local-hermes-default 与 local-hermes-hermes 互相兼容
    if (['local-hermes-default', 'local-hermes-hermes'].includes(selected.agent_id)) {
      const localAgent = Object.values(agents).find((a) => ['local-hermes-default', 'local-hermes-hermes'].includes(a.id))
      if (localAgent) return localAgent
    }
    return undefined
  }, [agents, selected])

  const handleApproval = async (approval: Approval, action: 'approve' | 'cancel') => {
    if (!selected || !agent?.capabilities.includes('approvals')) return
    const id = newCommandId()
    const command: Command = {
      id,
      session_id: selected.id,
      agent_id: selected.agent_id,
      action,
      state: 'received',
      text: '',
      attachments: [],
      created_at: new Date().toISOString(),
      error: null,
      target_id: approval.target_id || approval.id,
    }
    const store = useAstrorderStore.getState()
    store.addOutbox(command, 'submitting')
    try {
      const result = await api.createCommand({
        id,
        agent_id: selected.agent_id,
        session_id: selected.id,
        action,
        text: '',
        attachment_ids: [],
        target_id: approval.target_id || approval.id,
      })
      store.mergeCommands([result])
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', selected.agent_id, selected.id] })
    } catch (error) {
      store.markOutboxError(
        selected.agent_id,
        selected.id,
        id,
        errorText(error),
        error instanceof ApiError && error.status < 500 ? 'failed' : 'unknown',
      )
    }
  }

  const handleStopTask = async (task: Task) => {
    if (!selected || !agent?.capabilities.includes('stop') || !task.target_id) return
    const id = newCommandId()
    const command: Command = { id, session_id: selected.id, agent_id: selected.agent_id, action: 'stop', state: 'received', text: '', attachments: [], created_at: new Date().toISOString(), error: null, target_id: task.target_id }
    const store = useAstrorderStore.getState()
    store.addOutbox(command, 'submitting')
    try {
      const result = await api.createCommand({ id, agent_id: selected.agent_id, session_id: selected.id, action: 'stop', text: '', attachment_ids: [], target_id: task.target_id })
      store.mergeCommands([result])
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'commands', selected.agent_id, selected.id] })
    } catch (error) {
      store.markOutboxError(selected.agent_id, selected.id, id, errorText(error), error instanceof ApiError && error.status < 500 ? 'failed' : 'unknown')
    }
  }

  if (sessions.length === 0) {
    return <EmptyState icon={<IconSparkles />} title="还没有可用会话" description="认证成功，但服务端没有返回 Agent 会话。连接真实 Agent 后，会话会出现在这里。" />
  }

  if (!selected) {
    return (
      <div className="route-page chat-page">
        <EmptyState icon={<IconInfoCircle />} title="请选择一个会话" description="左侧会话列表为空或存在同名 ID 的不同 Agent，请从明确的 Agent 路由进入。" />
      </div>
    )
  }

  const resourceError = resources.messages.error || resources.commands.error || resources.tasks.error
  const showRightPanel = detailsPinned || sidecarOpen

  return (
    <div className="route-page chat-page">
      <div
        ref={layoutRef}
        className={`chat-layout${showRightPanel ? ' has-details' : ''}`}
        style={{
          gridTemplateColumns: showRightPanel ? `minmax(0, 1fr) ${sidecarWidth}%` : undefined,
        }}
      >
        <section className="chat-column">
          <Paper className="chat-heading" withBorder radius="lg" p="md">
            <Group justify="space-between" align="center" wrap="nowrap">
              <div className="chat-title-block">
                <Group gap={8} wrap="nowrap" align="center">
                  <IconFolder size={17} className="chat-title-icon" />
                  <Title order={2} size="h4" className="chat-title-text">{selected.title || '未命名会话'}</Title>
                </Group>
              </div>
              <Group gap="xs" wrap="nowrap">
                <AgentKindBadge agent={agent} />
                <SessionStatusLabel status={sessionActivityStatus(selected, commands)} />
                {!!approvals.length && <Button size="compact-sm" color="yellow" variant="light" onClick={openDetails}>等待授权 · {approvals.length}</Button>}
                <ActionIcon className="chat-details-button" variant="subtle" onClick={openDetails} aria-label="打开会话详情" title="会话详情">
                  <IconInfoCircle size={18} />
                </ActionIcon>
                <Tooltip label={showRightPanel ? '收起工作台侧边栏' : '展开工作台侧边栏'}>
                  <ActionIcon
                    variant="subtle"
                    color={showRightPanel ? 'blue' : 'gray'}
                    onClick={() => {
                      if (showRightPanel) {
                        setSidecarOpen(false)
                        setDetailsPinned(false)
                      } else {
                        setSidecarOpen(true)
                      }
                    }}
                    aria-label={showRightPanel ? '收起工作台侧边栏' : '展开工作台侧边栏'}
                  >
                    {showRightPanel ? (
                      <IconLayoutSidebarRightCollapse size={18} />
                    ) : (
                      <IconLayoutSidebarRightExpand size={18} />
                    )}
                  </ActionIcon>
                </Tooltip>
              </Group>
            </Group>
          </Paper>
          <SessionRuntimeBar messages={messages} commands={commands} tasks={tasks} nativeBranch={modelBinding.data?.branch ?? null} onTaskOpen={(task) => { setSelectedTask(task); openTaskDetails() }} />
          <Transcript
            key={`transcript:${scopeKey(selected.agent_id, selected.id)}`}
            session={selected}
            messages={messages}
            outbox={outbox}
            approvals={approvals}
            canApprove={agent?.capabilities.includes('approvals') === true}
            onApproval={(approval, action) => void handleApproval(approval, action)}
            composerHeight={composerHeight}
            loading={resources.messages.isLoading || resources.commands.isLoading}
            hasMoreHistory={Boolean(resources.messages.hasNextPage)}
            loadingOlder={resources.messages.isFetchingNextPage}
            onLoadOlder={() => resources.messages.fetchNextPage()}
            onImageClick={(url) => setPreviewImage(url)}
            error={resourceError ? errorText(resourceError) : undefined}
            onRetry={() => {
              void resources.messages.refetch()
              void resources.commands.refetch()
            }}
          />
          <ChatComposer
            key={`composer:${scopeKey(selected.agent_id, selected.id)}`}
            session={selected}
            agent={agent}
            onHeightChange={setComposerHeight}
          />
        </section>
        {showRightPanel && (
          <aside className="desktop-details" style={{ position: 'relative', width: '100%', padding: 0 }}>
            {/* 左右分界自由拖拽条 */}
            <div
              className="sidecar-resizer"
              onMouseDown={(e) => {
                e.preventDefault()
                setIsResizing(true)
              }}
              style={{
                position: 'absolute',
                top: 0,
                bottom: 0,
                left: -6,
                width: 12,
                cursor: 'col-resize',
                zIndex: 50,
                background: isResizing ? 'var(--astr-indigo)' : 'transparent',
                transition: 'background 0.15s ease',
              }}
              title="拖动调整对话与侧边栏宽度比例"
            />
            {/* 拖拽过程中覆盖遮罩，防止 iframe（如 drawio/html）吃掉鼠标 mousemove 事件 */}
            {isResizing && (
              <div
                style={{
                  position: 'fixed',
                  top: 0,
                  left: 0,
                  right: 0,
                  bottom: 0,
                  zIndex: 9999,
                  cursor: 'col-resize',
                }}
              />
            )}
            {sidecarOpen ? (
              <ErrorBoundary fallbackTitle="侧边栏加载异常">
                <SidecarHost
                  session={selected}
                  agent={agent}
                  commands={commands}
                  messages={messages}
                  approvals={approvals}
                  onApproval={(approval, action) => void handleApproval(approval, action)}
                  onCloseSidecar={() => setSidecarOpen(false)}
                />
              </ErrorBoundary>
            ) : (
              <div style={{ padding: '16px 0 0 18px' }}>
                <Button variant="subtle" size="compact-sm" onClick={() => setDetailsPinned(false)}>收起详情侧栏</Button>
                <SessionDetails session={selected} agent={agent} commands={commands} messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} />
              </div>
            )}
          </aside>
        )}
      </div>
      <DrawerDetails opened={detailsOpened} onClose={closeDetails} onPin={() => { setDetailsPinned(true); closeDetails() }} session={selected} agent={agent} commands={commands} messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} />
      <Drawer opened={taskDetailsOpened} onClose={() => { closeTaskDetails(); setSelectedTask(null) }} title="任务详情" position={mobileTaskSheet ? 'bottom' : 'right'} size={mobileTaskSheet ? 'min(88vh, 620px)' : 'min(92vw, 520px)'}>
        {selectedTask && <TaskDetails task={selectedTask} canStop={Boolean(agent?.capabilities.includes('stop') && selectedTask.target_id && ['pending', 'running', 'waiting_approval'].includes(selectedTask.status))} onStop={(task) => void handleStopTask(task)} onJumpToLatest={() => window.scrollTo({ top: document.body.scrollHeight, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' })} onClose={() => { closeTaskDetails(); setSelectedTask(null) }} />}
      </Drawer>
      {previewImage &&
        typeof document !== 'undefined' &&
        createPortal(
          <div
            className="desktop-lightbox"
            role="dialog"
            aria-label="图片预览"
            onClick={() => setPreviewImage(null)}
          >
            <button
              type="button"
              className="desktop-lightbox-close"
              aria-label="关闭图片"
              onClick={() => setPreviewImage(null)}
            >
              <IconX size={24} />
            </button>
            <img
              className="desktop-lightbox-image"
              src={previewImage}
              alt="放大预览"
              onClick={(e) => e.stopPropagation()}
            />
          </div>,
          document.body,
        )}
    </div>
  )
}

function DrawerDetails({
  opened,
  onClose,
  onPin,
  ...props
}: {
  opened: boolean
  onClose: () => void
  onPin: () => void
  session: Session
  agent?: import('../../domain/types').Agent
  commands: Command[]
  messages: import('../../domain/types').Message[]
  approvals: Approval[]
  onApproval: (approval: Approval, action: 'approve' | 'cancel') => void
}) {
  return (
    <Drawer opened={opened} onClose={onClose} title="会话详情" position="right" size="min(92vw, 380px)">
      <Button visibleFrom="md" variant="subtle" size="compact-sm" onClick={onPin}>固定详情侧栏</Button>
      <SessionDetails {...props} />
    </Drawer>
  )
}
