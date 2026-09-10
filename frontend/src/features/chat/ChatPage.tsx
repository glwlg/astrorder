import { IconInfoCircle, IconSparkles, IconX } from '@tabler/icons-react'
import { ActionIcon, Button, Drawer, Group, Paper, Text, Title } from '@mantine/core'
import { useDisclosure, useMediaQuery } from '@mantine/hooks'
import { useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
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
  const [taskDetailsOpened, { open: openTaskDetails, close: closeTaskDetails }] = useDisclosure(false)
  const [selectedTask, setSelectedTask] = useState<Task | null>(null)
  const [previewImage, setPreviewImage] = useState<string | null>(null)
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
  return (
    <div className="route-page chat-page">
      <div className={`chat-layout${detailsPinned ? ' has-details' : ''}`}>
        <section className="chat-column">
          <Paper className="chat-heading" withBorder radius="lg" p="md">
            <Group justify="space-between" align="flex-start" wrap="nowrap">
              <div className="chat-title-block">
                <Text size="xs" c="dimmed">{selected.project_name || '项目会话'}</Text>
                <Title order={2} size="h3" mt={2}>{selected.title || '未命名会话'}</Title>
                <Text className="workspace-path" size="xs" c="dimmed">{selected.workspace || '未提供工作区'}</Text>
              </div>
              <Group gap="xs" wrap="nowrap">
                <AgentKindBadge agent={agent} />
                <SessionStatusLabel status={sessionActivityStatus(selected)} />
                {!!approvals.length && <Button size="compact-sm" color="yellow" variant="light" onClick={openDetails}>等待授权 · {approvals.length}</Button>}
                <ActionIcon className="chat-details-button" variant="subtle" onClick={openDetails} aria-label="打开会话详情" title="会话详情">
                  <IconInfoCircle size={18} />
                </ActionIcon>
              </Group>
            </Group>
          </Paper>
          <SessionRuntimeBar messages={messages} commands={commands} tasks={tasks} nativeBranch={modelBinding.data?.branch ?? null} onTaskOpen={(task) => { setSelectedTask(task); openTaskDetails() }} />
          <Transcript
            key={`transcript:${scopeKey(selected.agent_id, selected.id)}`}
            messages={messages}
            outbox={outbox}
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
          <ChatComposer key={`composer:${scopeKey(selected.agent_id, selected.id)}`} session={selected} agent={agent} />
        </section>
        {detailsPinned && <aside className="desktop-details">
          <Button variant="subtle" size="compact-sm" onClick={() => setDetailsPinned(false)}>收起详情侧栏</Button>
          <SessionDetails session={selected} agent={agent} commands={commands} messages={messages} approvals={approvals} onApproval={(approval, action) => void handleApproval(approval, action)} />
        </aside>}
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
