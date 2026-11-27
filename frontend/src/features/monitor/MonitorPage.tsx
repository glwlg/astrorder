import { IconArrowDown, IconArrowsMaximize, IconArrowsMinimize, IconArrowUpRight, IconBolt, IconCircleCheck, IconClock, IconGridDots, IconLoader2, IconPlus, IconShieldCheck, IconTrash, IconX } from '@tabler/icons-react'
import { ActionIcon, Badge, Button, Group, Modal, Paper, Popover, SegmentedControl, Stack, Text, Textarea, TextInput, Title, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconAlertTriangle, IconArrowUp, IconCheck, IconFolder, IconPaperclip, IconPlayerStop } from '@tabler/icons-react'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { describeTool, PackSummary, ToolLineIcon } from '../chat/toolPresentation'
import { ShinyText } from '../../components/animations/ShinyText'
import { MarkdownContent } from '../../components/MarkdownContent'
import { MessageBody } from '../../components/MessageBody'
import { LazyDetails } from '../../components/LazyDetails'
import { useSpotlightSurface } from '../../components/animations/useSpotlightSurface'
import { SessionModelControl } from '../chat/SessionModelControl'
import { ApprovalModeControl } from '../chat/ApprovalModeControl'
import { memo, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type MouseEvent, type RefObject } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api } from '../../api/client'
import type { Agent, Command, Message, Session } from '../../domain/types'
import { EmptyState } from '../../components/EmptyState'
import { displaySessionTitle, isHiddenRailSession } from '../../components/sessionRailModel'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { selectApprovals, selectCommands, selectMessages, selectSessions, useAstrorderStore } from '../../state/store'
import { newCommandId, scopeKey } from '../../domain/semantics'
import { sharedServerOutbox } from '../mobile/serverOutbox'
import type { OutboxEntry } from '../mobile/mobileOutbox'
import { mobileOutboxStorage } from '../mobile/mobileOutboxStorage'
import { clipboardFiles } from '../chat/composerMedia'
import { submitBrowserCommand } from '../chat/commandActions'
import { usePersistentDraft } from '../chat/draftStorage'

const STORAGE_KEY = 'astrorder:monitor-sessions'
const AUTO_STORAGE_KEY = 'astrorder:monitor-auto-sessions'
const EXCLUDED_STORAGE_KEY = 'astrorder:monitor-excluded-sessions'
const SIZES_STORAGE_KEY = 'astrorder:monitor-slot-sizes'
const ORDER_STORAGE_KEY = 'astrorder:monitor-slot-order'
const STALE_AFTER_MS = 30 * 60 * 1000
const EMPTY_QUEUE_ITEMS: MonitorQueueItem[] = []
const DEFAULT_SLOT_SIZE: SlotSize = { height: 520 }
const FOCUSED_SLOT_SIZE: SlotSize = { height: 820 }
const MONITOR_CARD_INITIAL_ENTRIES = 20
const MONITOR_FOCUSED_INITIAL_ENTRIES = 40
const MONITOR_ENTRY_LOAD_STEP = 30
const MONITOR_TAIL_EAGER_ROWS = 6
const MONITOR_RESOURCE_OPTIONS = {
  includeTasks: false,
  skipBackfill: true,
  skipIdleSync: true,
  trackPerf: false,
} as const
const NOOP = () => {}

type MonitorDisplayEntry =
  | { type: 'pack'; key: string; pack: Message[] }
  | { type: 'message'; key: string; message: Message }

function isActivityMessage(message: Message | null | undefined) {
  return Boolean(message && (message.kind !== 'message' || message.role === 'tool'))
}

function useNearViewport(targetRef: RefObject<HTMLDivElement | null>, eager: boolean) {
  const [near, setNear] = useState(() => eager || typeof IntersectionObserver === 'undefined')
  useEffect(() => {
    if (eager || near) {
      if (!near) setNear(true)
      return
    }
    if (typeof IntersectionObserver === 'undefined') {
      setNear(true)
      return
    }
    const node = targetRef.current
    if (!node) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          setNear(true)
          observer.disconnect()
        }
      },
      { rootMargin: '240px 0px' },
    )
    observer.observe(node)
    return () => observer.disconnect()
  }, [eager, near, targetRef])
  return near
}

const MonitorActivityPackRow = memo(function MonitorActivityPackRow({
  pack,
  deferred,
}: {
  pack: Message[]
  deferred: boolean
}) {
  return (
    <div className={'monitor-activity-pack-row' + (deferred ? ' is-deferred-row' : '')}>
      <LazyDetails summary={<PackSummary pack={pack} />}>
        <div className="monitor-activity-pack-details">
          {pack.map((item) => {
            const d = describeTool(item)
            return (
              <div className="monitor-pack-detail-item" key={item.id}>
                <ToolLineIcon icon={d.iconKey} size={12} />
                <span>{d.target || d.fullTitle}</span>
              </div>
            )
          })}
        </div>
      </LazyDetails>
    </div>
  )
})

const MonitorMessageRow = memo(function MonitorMessageRow({
  message,
  session,
  onImageClick,
  deferred,
}: {
  message: Message
  session: Session
  onImageClick: (url: string) => void
  deferred: boolean
}) {
  const imageAttachmentNames = useMemo(
    () => message.attachments.filter((att) => att.media_type.startsWith('image/')).map((att) => att.name),
    [message.attachments],
  )
  const imageAttachments = useMemo(
    () => message.attachments.filter((att) => att.media_type?.startsWith('image/') && att.url),
    [message.attachments],
  )
  const renderMarkdown = useCallback(
    (value: string) => <MarkdownContent value={value} onImageClick={onImageClick} session={session} />,
    [onImageClick, session],
  )

  return (
    <div className={'monitor-message-row message-' + message.role + (deferred ? ' is-deferred-row' : '')}>
      <div className="monitor-message-bubble">
        <MessageBody
          value={message.text || ''}
          user={message.role === 'user'}
          onImageClick={onImageClick}
          attachmentNames={imageAttachmentNames}
          renderMarkdown={renderMarkdown}
          sessionId={session.id}
          connectionId={session.connection_id || undefined}
        />
        {imageAttachments.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginTop: 6 }}>
            {imageAttachments.map((att) => (
              <img
                key={att.id}
                src={att.url}
                alt={att.name}
                loading="lazy"
                decoding="async"
                style={{ maxWidth: '100%', maxHeight: 120, borderRadius: 6, cursor: 'zoom-in', objectFit: 'cover' }}
                onClick={() => onImageClick(att.url)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
})

type MonitorQueueItem =
  | { source: 'local'; entry: OutboxEntry; id: string; agentId: string; sessionId: string; text: string }
  | { source: 'server'; command: Command; id: string; agentId: string; sessionId: string; text: string }

function active(session: Session) {
  return session.status === 'running' || session.status === 'waiting_approval' || session.live === true
}

function isCompanionSubagentSession(session: Session): boolean {
  // Only explicit delegation/swarm subagent workers are companion tasks;
  // forked or handed-off sessions (`parent_session_id` / `parent_session_key` / `handoff_from_*`)
  // are normal top-level user sessions and must remain visible in the matrix.
  return Boolean(session.delegation_parent_session_id?.trim())
}

function activeMessageCommand(status: Session['status'], commands: Command[]) {
  if (status !== 'running' && status !== 'waiting_approval') return undefined
  return commands.find((command) =>
    (command.action === 'send' || command.action === 'enqueue') &&
    (command.state === 'running' || command.state === 'accepted'),
  )
}

function formatMonitorCommandText(raw?: string | null): string {
  const trimmed = (raw || '').trim()
  if (!trimmed) return '命令执行中…'
  if (/^【星序\s*[·・]/.test(trimmed)) {
    const dividerMatch = trimmed.match(/\n-{8,}\s*\n([\s\S]+)$/)
    if (dividerMatch && dividerMatch[1].trim()) {
      return dividerMatch[1].trim()
    }
  }
  return trimmed
}

function savedSessions(key = STORAGE_KEY): string[] {
  try {
    const value = JSON.parse(localStorage.getItem(key) || '[]')
    return Array.isArray(value) ? value.filter(item => typeof item === 'string') : []
  } catch { return [] }
}

export type SlotSize = { width?: number; height?: number }

function savedSlotSizes(): Record<number, SlotSize> {
  try {
    const value = JSON.parse(localStorage.getItem(SIZES_STORAGE_KEY) || '{}')
    return typeof value === 'object' && value ? value : {}
  } catch { return {} }
}

function savedOrder(): string[] {
  try {
    const value = JSON.parse(localStorage.getItem(ORDER_STORAGE_KEY) || '[]')
    return Array.isArray(value) ? value.filter(item => typeof item === 'string') : []
  } catch { return [] }
}

function MonitorStatusIcon({ status }: { status: Session['status'] }) {
  switch (status) {
    case 'running':
      return (
        <Tooltip label="正在运行" withArrow>
          <span className="monitor-state-pill is-running" aria-label="正在运行">
            <IconBolt size={15} />
          </span>
        </Tooltip>
      )
    case 'waiting_approval':
      return (
        <Tooltip label="等待审批" withArrow>
          <span className="monitor-state-pill is-waiting" aria-label="等待审批">
            <IconShieldCheck size={14} />
          </span>
        </Tooltip>
      )
    case 'error':
      return (
        <Tooltip label="异常" withArrow>
          <span className="monitor-state-pill is-error" aria-label="异常">
            <IconAlertTriangle size={14} />
          </span>
        </Tooltip>
      )
    default:
      return (
        <Tooltip label="空闲" withArrow>
          <span className="monitor-state-pill is-idle" aria-label="空闲">
            <IconCheck size={12} />
          </span>
        </Tooltip>
      )
  }
}

interface MonitorCardProps {
  session: Session
  agent?: Agent
  slotIndex?: number
  slotSize?: SlotSize
  gridColumns?: number
  isFloating?: boolean
  isDropTarget?: boolean
  isFocused?: boolean
  queuedItems?: MonitorQueueItem[]
  queueBusyId?: string | null
  onSendQueued?: (item: MonitorQueueItem) => void
  onEditQueued?: (item: MonitorQueueItem) => void
  onDeleteQueued?: (item: MonitorQueueItem) => void
  onToggleFocus?: () => void
  onToggleFocusKey?: (key: string) => void
  onOpen?: () => void
  onOpenSession?: (session: Session) => void
  onRemove?: () => void
  onRemoveKey?: (key: string) => void
  onHeaderMouseDown?: (index: number, e: MouseEvent<HTMLDivElement>, cardEl: HTMLElement | null) => void
  onResizeCornerStart?: (index: number, e: MouseEvent<HTMLDivElement>, wrapperEl: HTMLElement | null) => void
}

export const MonitorCard = memo(function MonitorCard({
  session,
  agent,
  slotIndex = 0,
  slotSize = DEFAULT_SLOT_SIZE,
  gridColumns = 2,
  isFloating = false,
  isDropTarget = false,
  isFocused = false,
  queuedItems = EMPTY_QUEUE_ITEMS,
  queueBusyId = null,
  onSendQueued,
  onEditQueued,
  onDeleteQueued,
  onToggleFocus,
  onToggleFocusKey,
  onOpen,
  onOpenSession,
  onRemove,
  onRemoveKey,
  onHeaderMouseDown,
  onResizeCornerStart,
}: MonitorCardProps) {
  const sessionKey = scopeKey(session.agent_id, session.id)
  const status = session.status === 'idle' && session.live ? 'running' : session.status
  const wrapperRef = useRef<HTMLDivElement>(null)
  const isBelowFold = !isFloating && !isFocused && slotIndex >= gridColumns * 2
  const isActiveSession = status === 'running' || status === 'waiting_approval'
  const nearViewport = useNearViewport(wrapperRef, !isBelowFold || isActiveSession)
  const resources = useSessionResources(session, nearViewport, false, MONITOR_RESOURCE_OPTIONS)
  const commands = useAstrorderStore(useShallow((state) => selectCommands(state, session.agent_id, session.id)))
  const messages = useAstrorderStore(useShallow((state) => selectMessages(state, session.agent_id, session.id)))
  const isInitialLoading = Boolean((resources?.messages?.isLoading || resources?.messages?.isPending) && messages.length === 0)
  const approvals = useAstrorderStore(useShallow((state) => selectApprovals(state, session.agent_id, session.id)))
  const { draft, setDraft } = usePersistentDraft(session.agent_id, session.id)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const [extraVisibleCount, setExtraVisibleCount] = useState(0)
  const scrollRef = useRef<HTMLDivElement>(null)
  const spotlight = useSpotlightSurface()
  const fileInputRef = useRef<HTMLInputElement>(null)
  const previousStatusRef = useRef(status)
  const previousActiveCommandRef = useRef<string | null>(null)
  const completionTimeoutRef = useRef<number | null>(null)
  const [completionFlash, setCompletionFlash] = useState(false)
  const activeCommand = activeMessageCommand(status, commands)
  const isStopAction = session.status === 'running' && !draft.text.trim() && !draft.attachments.length

  useEffect(() => {
    setExtraVisibleCount(0)
  }, [sessionKey])

  const addFiles = (files: File[]) => {
    if (!files.length) return
    setDraft({
      ...draft,
      attachments: [...draft.attachments, ...files.map(file => ({ key: crypto.randomUUID(), file }))],
    })
  }

  const visibleMessages = useMemo(() => {
    return messages.filter((message) => message.kind !== 'message' || message.role !== 'assistant' || message.text.trim() || message.attachments?.length || message.tool)
  }, [messages])

  const displayEntries = useMemo<MonitorDisplayEntry[]>(() => {
    const entries: MonitorDisplayEntry[] = []
    for (let idx = 0; idx < visibleMessages.length; idx++) {
      const message = visibleMessages[idx]
      if (isActivityMessage(message)) {
        const pack: Message[] = [message]
        while (idx + 1 < visibleMessages.length && isActivityMessage(visibleMessages[idx + 1])) {
          idx += 1
          pack.push(visibleMessages[idx])
        }
        entries.push({ type: 'pack', key: `pack:${message.id}`, pack })
      } else {
        entries.push({
          type: 'message',
          key: `${message.agent_id || ''}:${message.session_id || ''}:${message.id}`,
          message,
        })
      }
    }
    return entries
  }, [visibleMessages])

  const visibleEntryLimit = (isFocused ? MONITOR_FOCUSED_INITIAL_ENTRIES : MONITOR_CARD_INITIAL_ENTRIES) + extraVisibleCount
  const hiddenEntryCount = Math.max(0, displayEntries.length - visibleEntryLimit)
  const windowedEntries = useMemo(
    () => (hiddenEntryCount > 0 ? displayEntries.slice(hiddenEntryCount) : displayEntries),
    [displayEntries, hiddenEntryCount],
  )
  const tailSignature = `${visibleMessages.length}:${visibleMessages[visibleMessages.length - 1]?.id || ''}:${activeCommand?.id || ''}`

  useEffect(() => {
    if (!nearViewport) return
    const frameId = window.requestAnimationFrame(() => {
      if (scrollRef.current) {
        scrollRef.current.scrollTop = scrollRef.current.scrollHeight
      }
    })
    return () => window.cancelAnimationFrame(frameId)
  }, [nearViewport, tailSignature])

  useEffect(() => {
    const previousStatus = previousStatusRef.current
    const previousActiveCommand = previousActiveCommandRef.current
    previousStatusRef.current = status
    previousActiveCommandRef.current = activeCommand?.id || null
    const sessionFinished = ['running', 'waiting_approval'].includes(previousStatus) && status === 'idle'
    const commandFinished = previousActiveCommand !== null && !activeCommand
    if (!sessionFinished && !commandFinished) return
    setCompletionFlash(true)
    if (completionTimeoutRef.current !== null) window.clearTimeout(completionTimeoutRef.current)
    completionTimeoutRef.current = window.setTimeout(() => {
      setCompletionFlash(false)
      completionTimeoutRef.current = null
    }, 1900)
  }, [activeCommand, status])

  useEffect(() => () => {
    if (completionTimeoutRef.current !== null) window.clearTimeout(completionTimeoutRef.current)
  }, [])

  const scrollToBottom = useCallback(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' })
    }
  }, [])

  const handleOpen = useCallback(() => {
    if (onOpenSession) onOpenSession(session)
    else onOpen?.()
  }, [onOpen, onOpenSession, session])

  const handleRemove = useCallback(() => {
    if (onRemoveKey) onRemoveKey(sessionKey)
    else onRemove?.()
  }, [onRemove, onRemoveKey, sessionKey])

  const handleToggleFocus = useCallback(() => {
    if (onToggleFocusKey) onToggleFocusKey(sessionKey)
    else onToggleFocus?.()
  }, [onToggleFocus, onToggleFocusKey, sessionKey])

  const send = async (action: 'send' | 'stop' = 'send') => {
    const text = draft.text.trim()
    if (action === 'send' && !text && !draft.attachments.length) return
    if (sending) return
    setSending(true); setError('')
    try {
      const result = await submitBrowserCommand({
        commandId: newCommandId(),
        session,
        action,
        text: action === 'stop' ? '' : text,
        files: action === 'stop' ? [] : draft.attachments.map(item => item.file),
        targetId: action === 'stop' ? session.id : null,
        uploadAttachment: api.uploadAttachment,
        createCommand: api.createCommand,
      })
      const command = result.command
      useAstrorderStore.getState().mergeCommands([command])
      if (command.state === 'failed' || command.state === 'unknown') throw new Error(command.error || '消息发送结果未确认。')
      if (action === 'send') setDraft({ text: '', attachments: [], sessionRefs: [] })
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '消息发送失败。')
    } finally { setSending(false) }
  }

  const workspaceShort = session.workspace ? session.workspace.replace(/\\/g, "/").split("/").filter(Boolean).slice(-2).join("/") : ""

  const handlePreviewImage = useCallback((url: string) => {
    const desktop = (window as unknown as { astrorderDesktop?: { openPreview?: (payload: { images: string[]; index: number }) => Promise<void> } }).astrorderDesktop
    if (desktop?.openPreview) {
      try {
        const abs = new URL(url, window.location.origin).href
        void desktop.openPreview({ images: [abs], index: 0 }).catch(reason => setError(reason instanceof Error ? reason.message : '图片预览失败。'))
      } catch {
        void desktop.openPreview({ images: [url], index: 0 }).catch(reason => setError(reason instanceof Error ? reason.message : '图片预览失败。'))
      }
    } else {
      window.open(url, '_blank')
    }
  }, [])

  const cardHeight = slotSize.height || 520
  const cardWidth = slotSize.width
  const defaultColPercent = 100 / gridColumns
  const canToggleFocus = Boolean(onToggleFocus || onToggleFocusKey)
  const canRemove = Boolean(onRemove || onRemoveKey)

  return (
    <div
      ref={wrapperRef}
      data-slot-index={slotIndex}
      className={
        'monitor-card-wrapper'
        + (isBelowFold ? ' is-below-fold' : '')
        + (isDropTarget ? ' is-drop-target' : '')
        + (isFloating ? ' is-floating-entity' : '')
      }
      style={{
        width: isFloating ? '100%' : (cardWidth ? cardWidth + 'px' : undefined),
        flex: isFloating ? undefined : (cardWidth ? '0 0 ' + cardWidth + 'px' : `1 1 calc(${defaultColPercent}% - 16px)`),
        height: isFloating ? '100%' : cardHeight + 'px',
        position: 'relative',
      }}
    >
        <Paper
          {...spotlight}
          className={'monitor-card astr-spotlight-enabled monitor-' + status + (completionFlash ? ' is-completion-flash' : '') + (isFloating ? ' is-solid-lifted' : '')}
          data-testid={'monitor-card-' + session.agent_id + '-' + session.id}
          withBorder={false}
          style={{ height: '100%', minHeight: 0 }}
        >
          {/* Header - Pointer based drag to lift entire card */}
          <div
            className="monitor-card-header"
            onMouseDown={(e) => {
              if ((e.target as HTMLElement).closest('button, input, textarea, a, .mantine-ActionIcon-root')) return
              onHeaderMouseDown?.(slotIndex, e, wrapperRef.current)
            }}
            title="按住拖拽卡片头调整位置（整卡实体拎起）"
          >
            <Group justify="space-between" align="center" wrap="nowrap" style={{ width: '100%' }}>
              <Group gap="xs" wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                <Tooltip label={(agent?.name || 'Agent') + ' (' + (agent?.kind || 'agent') + ')'} withArrow>
                  <span className="monitor-agent-icon-badge">
                    <AgentBrandIcon kind={agent?.kind} size={14} />
                  </span>
                </Tooltip>
                <div className="monitor-card-title">
                  <Title order={3} size="h4">{displaySessionTitle(session)}</Title>
                </div>
              </Group>
              <Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
                <MonitorStatusIcon status={status} />
                {approvals.length > 0 && (
                  <Tooltip label={approvals.length + ' 个操作待审批'} withArrow>
                    <Badge size="xs" color="yellow" variant="light" leftSection={<IconShieldCheck size={11} />}>
                      {approvals.length}
                    </Badge>
                  </Tooltip>
                )}
                <div className="monitor-card-secondary-actions">
                  {session.workspace && (
                    <div className="monitor-card-path" title={session.workspace}>
                      <IconFolder size={11} style={{ display: 'inline', verticalAlign: '-1px', marginRight: 3 }} />
                      {workspaceShort || session.workspace}
                    </div>
                  )}
                  <Tooltip label="打开此会话" withArrow>
                    <ActionIcon variant="subtle" color="gray" size="sm" aria-label="打开会话" onClick={handleOpen}>
                      <IconArrowUpRight size={14} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label="回到底部" withArrow>
                    <ActionIcon variant="subtle" color="gray" size="sm" aria-label="回到底部" onClick={scrollToBottom}>
                      <IconArrowDown size={14} />
                    </ActionIcon>
                  </Tooltip>
                  {canToggleFocus && (
                    <Tooltip label={isFocused ? '退出聚焦 (Esc)' : '全屏聚焦'} withArrow>
                      <ActionIcon
                        variant="subtle"
                        color={isFocused ? 'indigo' : 'gray'}
                        size="sm"
                        aria-label={isFocused ? '退出聚焦' : '全屏聚焦'}
                        onClick={handleToggleFocus}
                      >
                        {isFocused ? <IconArrowsMinimize size={14} /> : <IconArrowsMaximize size={14} />}
                      </ActionIcon>
                    </Tooltip>
                  )}
                  {canRemove && (
                    <Tooltip label="移出矩阵" withArrow>
                      <ActionIcon variant="subtle" color="gray" size="sm" aria-label="移出矩阵" onClick={handleRemove}>
                        <IconX size={14} />
                      </ActionIcon>
                    </Tooltip>
                  )}
                </div>
              </Group>
            </Group>
          </div>

          {/* Mini-Transcript Conversation Body */}
          <div className="monitor-transcript-deck" ref={scrollRef} aria-label="会话记录">
            {isInitialLoading ? (
              <div className="monitor-transcript-loading">
                <span className="monitor-loading-spinner">
                  <IconLoader2 size={18} className="rotating-icon" />
                </span>
                <ShinyText text="正在同步会话记录…" speed={2} />
              </div>
            ) : windowedEntries.length === 0 && !activeCommand ? (
              <div className="monitor-transcript-empty">
                <Text size="xs" c="dimmed" style={{ opacity: 0.75 }}>暂无可显示的会话记录</Text>
              </div>
            ) : (
              <div className="monitor-messages-flow">
                {hiddenEntryCount > 0 && (
                  <button
                    type="button"
                    className="monitor-load-older-btn"
                    onClick={() => setExtraVisibleCount((count) => count + MONITOR_ENTRY_LOAD_STEP)}
                  >
                    {`显示更早 ${Math.min(hiddenEntryCount, MONITOR_ENTRY_LOAD_STEP)} 条（剩余 ${hiddenEntryCount} 条）`}
                  </button>
                )}
                {windowedEntries.map((entry, idx) => {
                  const deferred = idx < windowedEntries.length - MONITOR_TAIL_EAGER_ROWS
                  if (entry.type === 'pack') {
                    return <MonitorActivityPackRow key={entry.key} pack={entry.pack} deferred={deferred} />
                  }
                  return (
                    <MonitorMessageRow
                      key={entry.key}
                      message={entry.message}
                      session={session}
                      onImageClick={handlePreviewImage}
                      deferred={deferred}
                    />
                  )
                })}
                {activeCommand && (
                  <div className="monitor-active-command-row">
                    <IconClock size={13} className="activity-icon is-pulsing" />
                    <span className="command-text" title={formatMonitorCommandText(activeCommand.text)}>{formatMonitorCommandText(activeCommand.text)}</span>
                    <ShinyText text="执行中" speed={1.5} className="monitor-mini-tag" />
                  </div>
                )}
              </div>
            )}
          </div>

          {/* Full-Fidelity Miniature Composer Footer */}
          <div className="monitor-card-footer">
            {queuedItems.length > 0 && (
              <div className="monitor-card-queue monitor-queue" aria-label="待机队列">
                <Stack gap={6} mb={6}>
                  {queuedItems.map((item) => (
                    <Paper
                      key={`${item.agentId}::${item.id}`}
                      className="monitor-queue-item"
                      withBorder
                      radius="md"
                    >
                      <Group justify="space-between" gap="xs" wrap="nowrap" style={{ width: '100%' }}>
                        <Group gap={6} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                          <Badge size="xs" color="yellow" variant="light" style={{ flexShrink: 0 }}>
                            待机
                          </Badge>
                          <Text size="xs" lineClamp={1} style={{ wordBreak: 'break-all' }} title={item.text || '附件消息'}>
                            {item.text || '附件消息'}
                          </Text>
                        </Group>
                        <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
                          <Button
                            size="compact-xs"
                            variant="light"
                            color="indigo"
                            disabled={agent?.kind === 'antigravity' && (session.status === 'running' || session.status === 'waiting_approval')}
                            loading={queueBusyId === item.id}
                            onClick={() => void onSendQueued?.(item)}
                          >
                            发送
                          </Button>
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            onClick={() => onEditQueued?.(item)}
                          >
                            编辑
                          </Button>
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            color="red"
                            disabled={queueBusyId === item.id}
                            onClick={() => void onDeleteQueued?.(item)}
                          >
                            删除
                          </Button>
                        </Group>
                      </Group>
                    </Paper>
                  ))}
                </Stack>
              </div>
            )}
            <div className="monitor-composer-inner">
              {draft.attachments.length > 0 && (
                <div className="draft-attachments" aria-label="待发送附件">
                  {draft.attachments.map(item => (
                    <span className="draft-attachment" key={item.key} title={item.file.name}>
                      <IconPaperclip size={14} aria-hidden="true" />
                      <span className="draft-name">{item.file.name}</span>
                      <button type="button" className="draft-remove" aria-label={`移除 ${item.file.name}`} onClick={() => setDraft({ ...draft, attachments: draft.attachments.filter(candidate => candidate.key !== item.key) })}>
                        <IconX size={13} />
                      </button>
                    </span>
                  ))}
                </div>
              )}
              <Textarea
                className="monitor-composer-textarea"
                aria-label={'发送到 ' + displaySessionTitle(session)}
                placeholder="向此会话发送指令 (Enter 发送)…"
                variant="unstyled"
                autosize
                minRows={1}
                maxRows={3}
                value={draft.text}
                onChange={(e) => setDraft({ ...draft, text: e.currentTarget.value })}
                onPaste={(event) => {
                  const files = clipboardFiles(event)
                  if (!files.length) return
                  event.preventDefault()
                  addFiles(files)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault()
                    void send('send')
                  }
                }}
                disabled={sending}
              />
              <div className="monitor-composer-toolbar">
                <ActionIcon variant="subtle" color="gray" size="sm" aria-label="添加附件" disabled={sending} onClick={() => fileInputRef.current?.click()}>
                  <IconPaperclip size={15} />
                </ActionIcon>
                <input ref={fileInputRef} hidden type="file" multiple onChange={event => { addFiles(Array.from(event.currentTarget.files || [])); event.currentTarget.value = '' }} />
                <ApprovalModeControl session={session} compact agentKind={agent?.kind} enabled={nearViewport} />
                <SessionModelControl session={session} enabled={nearViewport} refetchOnMount={false} showPillQuota={false} />
                <span style={{ flex: 1 }} />
                <Button
                  size="xs"
                  radius="xl"
                  color={isStopAction ? 'red' : 'indigo'}
                  aria-label={isStopAction ? '停止' : '发送消息'}
                  disabled={sending || (!isStopAction && !draft.text.trim() && !draft.attachments.length)}
                  onClick={() => void send(isStopAction ? 'stop' : 'send')}
                  className="monitor-send-btn"
                >
                  {isStopAction ? <IconPlayerStop size={14} /> : <IconArrowUp size={14} />}
                </Button>
              </div>
            </div>
            {error && <Text size="xs" c="red" px={8}>{error}</Text>}
          </div>

          {/* Windows-like corner resizer handle */}
          {!isFloating && onResizeCornerStart && (
            <div
              className="monitor-card-resizer"
              onMouseDown={(e) => onResizeCornerStart(slotIndex, e, wrapperRef.current)}
              title="拖动右下角调整卡片窗口尺寸"
            />
          )}
        </Paper>
    </div>
  )
})


const MonitorListRow = memo(function MonitorListRow({
  session,
  agent,
  onOpen,
  onOpenSession,
  onRemove,
  onRemoveKey,
  queuedItems = EMPTY_QUEUE_ITEMS,
  queueBusyId = null,
  onSendQueued,
  onEditQueued,
  onDeleteQueued,
}: {
  session: Session
  agent?: Agent
  onOpen?: () => void
  onOpenSession?: (session: Session) => void
  onRemove?: () => void
  onRemoveKey?: (key: string) => void
  queuedItems?: MonitorQueueItem[]
  queueBusyId?: string | null
  onSendQueued?: (item: MonitorQueueItem) => void
  onEditQueued?: (item: MonitorQueueItem) => void
  onDeleteQueued?: (item: MonitorQueueItem) => void
}) {
  const sessionKey = scopeKey(session.agent_id, session.id)
  useSessionResources(session, true, false, MONITOR_RESOURCE_OPTIONS)
  const commands = useAstrorderStore(useShallow((state) => selectCommands(state, session.agent_id, session.id)))
  const messages = useAstrorderStore(useShallow((state) => selectMessages(state, session.agent_id, session.id)))
  const approvals = useAstrorderStore(useShallow((state) => selectApprovals(state, session.agent_id, session.id)))
  const latestActivity = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const message = messages[i]
      if (message.kind === 'thinking' || message.kind === 'tool') return message
    }
    return undefined
  }, [messages])
  const status = session.status === 'idle' && session.live ? 'running' : session.status
  const activeCommand = activeMessageCommand(status, commands)
  const workspaceShort = session.workspace ? session.workspace.replace(/\\/g, "/").split("/").filter(Boolean).slice(-2).join("/") : ""
  const handleOpen = useCallback(() => {
    if (onOpenSession) onOpenSession(session)
    else onOpen?.()
  }, [onOpen, onOpenSession, session])
  const handleRemove = useCallback(() => {
    if (onRemoveKey) onRemoveKey(sessionKey)
    else onRemove?.()
  }, [onRemove, onRemoveKey, sessionKey])
  const canRemove = Boolean(onRemove || onRemoveKey)

  return (
    <Paper
      className={'monitor-list-row monitor-' + status}
      data-testid={'monitor-card-' + session.agent_id + '-' + session.id}
      withBorder
      radius='md'
      p='xs'
    >
      <Group justify='space-between' align='center' wrap='nowrap' gap='md'>
        {/* Left: Agent Icon + Title + Path */}
        <Group gap='sm' wrap='nowrap' style={{ minWidth: 240, flex: '0 1 340px' }}>
          <Tooltip label={(agent?.name || 'Agent') + ' (' + (agent?.kind || 'agent') + ')'} withArrow>
            <span className='monitor-agent-icon-badge'>
              <AgentBrandIcon kind={agent?.kind} size={15} />
            </span>
          </Tooltip>
          <div className='monitor-row-title-block'>
            <span className='monitor-row-title' onClick={handleOpen} title={displaySessionTitle(session)}>
              {displaySessionTitle(session)}
            </span>
            {session.workspace && (
              <span className='monitor-row-path' title={session.workspace}>
                <IconFolder size={11} style={{ display: 'inline', verticalAlign: '-1px', marginRight: 3 }} />
                {workspaceShort || session.workspace}
              </span>
            )}
          </div>
        </Group>

        {/* Middle: Live State & Recent Activity Log */}
        <Group gap='sm' wrap='nowrap' style={{ flex: '1 1 auto', minWidth: 0 }}>
          <MonitorStatusIcon status={status} />
          <div className='monitor-row-activity-box'>
            {activeCommand ? (
              <span className='monitor-row-activity-text is-command'>
                <IconClock size={12} className='activity-icon is-pulsing' style={{ flexShrink: 0 }} />
                <span className='activity-line'>{formatMonitorCommandText(activeCommand.text)}</span>
                <ShinyText text='执行中' speed={1.5} className='monitor-mini-tag' />
              </span>
            ) : latestActivity ? (
              <span className='monitor-row-activity-text'>
                <ToolLineIcon icon={describeTool(latestActivity).iconKey} size={12} />
                <span className='activity-line'>{describeTool(latestActivity).target || describeTool(latestActivity).fullTitle}</span>
              </span>
            ) : (
              <span className='monitor-row-activity-empty'>暂无近期活动</span>
            )}
          </div>
          {approvals.length > 0 && (
            <Tooltip label={approvals.length + ' 个操作待审批'} withArrow>
              <Badge size='xs' color='yellow' variant='light' leftSection={<IconShieldCheck size={11} />}>
                {approvals.length} 待确认
              </Badge>
            </Tooltip>
          )}
          {queuedItems.length > 0 && (
            <Tooltip label={queuedItems.length + ' 条待机消息'} withArrow>
              <Badge size='xs' color='yellow' variant='light'>
                {queuedItems.length} 待机
              </Badge>
            </Tooltip>
          )}
        </Group>

        {/* Right: Time + Quick Jump & Close */}
        <Group gap='xs' wrap='nowrap' style={{ flexShrink: 0 }}>
          <span className='monitor-row-time'>
            {new Date(session.updated_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}
          </span>
          <Tooltip label='打开会话' withArrow>
            <ActionIcon variant='subtle' color='gray' size='sm' aria-label='打开会话' onClick={handleOpen}>
              <IconArrowUpRight size={15} />
            </ActionIcon>
          </Tooltip>
          {canRemove && (
            <Tooltip label='移出矩阵' withArrow>
              <ActionIcon variant='subtle' color='gray' size='sm' aria-label='移出矩阵' onClick={handleRemove}>
                <IconX size={14} />
              </ActionIcon>
            </Tooltip>
          )}
        </Group>
      </Group>
      {queuedItems.length > 0 && (
        <div className="monitor-list-queue monitor-queue" aria-label="待机队列">
          <Stack gap={6} mt="xs">
            {queuedItems.map((item) => (
              <Paper
                key={`${item.agentId}::${item.id}`}
                className="monitor-queue-item"
                withBorder
                radius="md"
              >
                <Group justify="space-between" gap="xs" wrap="nowrap" style={{ width: '100%' }}>
                  <Group gap={6} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                    <Badge size="xs" color="yellow" variant="light" style={{ flexShrink: 0 }}>
                      待机
                    </Badge>
                    <Text size="xs" lineClamp={1} style={{ wordBreak: 'break-all' }} title={item.text || '附件消息'}>
                      {item.text || '附件消息'}
                    </Text>
                  </Group>
                  <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
                    <Button
                      size="compact-xs"
                      variant="light"
                      color="indigo"
                      disabled={agent?.kind === 'antigravity' && (session.status === 'running' || session.status === 'waiting_approval')}
                      loading={queueBusyId === item.id}
                      onClick={() => void onSendQueued?.(item)}
                    >
                      发送
                    </Button>
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      onClick={() => onEditQueued?.(item)}
                    >
                      编辑
                    </Button>
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="red"
                      disabled={queueBusyId === item.id}
                      onClick={() => void onDeleteQueued?.(item)}
                    >
                      删除
                    </Button>
                  </Group>
                </Group>
              </Paper>
            ))}
          </Stack>
        </div>
      )}
    </Paper>
  )
})


export function MonitorPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const projectFilter = (searchParams.get('project') || '').trim()
  const [layout, setLayout] = useState('grid')
  const [pickerOpen, setPickerOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [manualKeys, setManualKeys] = useState(savedSessions)
  const [autoKeys, setAutoKeys] = useState(() => savedSessions(AUTO_STORAGE_KEY))
  const [excludedKeys, setExcludedKeys] = useState(() => savedSessions(EXCLUDED_STORAGE_KEY))
  const [editingQueued, setEditingQueued] = useState<MonitorQueueItem | null>(null)
  const [queuedText, setQueuedText] = useState('')
  const [queueBusyId, setQueueBusyId] = useState<string | null>(null)
  const [queueOutbox] = useState(() => sharedServerOutbox(mobileOutboxStorage))
  const queue = useSyncExternalStore(queueOutbox.subscribe, queueOutbox.snapshot)
  const [slotSizes, setSlotSizes] = useState<Record<number, SlotSize>>(savedSlotSizes)
  const [slotOrder, setSlotOrder] = useState<string[]>(savedOrder)
  const [resizingInfo, setResizingInfo] = useState<{ slotIndex: number; width: number; height: number; isSnapped?: boolean } | null>(null)
  const [layoutMenuOpened, setLayoutMenuOpened] = useState(false)
  const [pageDragOver, setPageDragOver] = useState(false)
  const [gridColumns, setGridColumns] = useState<number>(() => {
    try {
      const stored = localStorage.getItem('astrorder:monitor-grid-cols')
      return stored ? parseInt(stored, 10) : 2
    } catch {
      return 2
    }
  })
  const [hoverCols, setHoverCols] = useState<number>(2)
  const [focusedKey, setFocusedKey] = useState<string | null>(null)

  useEffect(() => {
    if (!focusedKey) return
    const handleEsc = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setFocusedKey(null)
    }
    window.addEventListener('keydown', handleEsc)
    return () => window.removeEventListener('keydown', handleEsc)
  }, [focusedKey])

  useEffect(() => {
    const handleUpdate = (e: Event) => {
      const customEvent = e as CustomEvent<string[]>
      if (Array.isArray(customEvent.detail)) {
        setManualKeys(customEvent.detail)
        setAutoKeys(savedSessions(AUTO_STORAGE_KEY))
        setExcludedKeys(savedSessions(EXCLUDED_STORAGE_KEY))
      }
    }
    window.addEventListener('astrorder:monitor-sessions-changed', handleUpdate)
    return () => window.removeEventListener('astrorder:monitor-sessions-changed', handleUpdate)
  }, [])

  useEffect(() => {
    void queueOutbox.load().catch(error => notifications.show({ color: 'red', message: error instanceof Error ? error.message : '待机队列加载失败' }))
  }, [queueOutbox])

  // Pointer-based physical lift & drag state
  const [liftedDrag, setLiftedDrag] = useState<{
    sourceIndex: number
    currentX: number
    currentY: number
    grabOffsetX: number
    grabOffsetY: number
    width: number
    height: number
    overIndex: number | null
  } | null>(null)
  const liftedDragRef = useRef(liftedDrag)
  liftedDragRef.current = liftedDrag

  const sessions = useAstrorderStore(useShallow((state) => selectSessions(state).filter(session => !isHiddenRailSession(session))))
  const agents = useAstrorderStore((state) => state.agents)
  const queuedServerCommands = useAstrorderStore(useShallow((state) =>
    Object.values(state.commands).filter((command) => command.state === 'queued'),
  ))

  const queuedCommands = useMemo<MonitorQueueItem[]>(() => {
    const local = queue.filter(entry => entry.state === 'queued').map(entry => ({ source: 'local' as const, entry, id: entry.payload.id, agentId: entry.payload.agent_id, sessionId: entry.payload.session_id, text: entry.payload.text || '' }))
    const localIds = new Set(local.map(item => `${item.agentId}::${item.sessionId}::${item.id}`))
    const server = queuedServerCommands.filter(command => !localIds.has(`${command.agent_id}::${command.session_id}::${command.id}`)).map(command => ({ source: 'server' as const, command, id: command.id, agentId: command.agent_id, sessionId: command.session_id, text: command.text || '' }))
    return [...local, ...server]
  }, [queuedServerCommands, queue])

  const queuedCommandsBySession = useMemo(() => {
    const map = new Map<string, MonitorQueueItem[]>()
    for (const item of queuedCommands) {
      const key = scopeKey(item.agentId, item.sessionId)
      const list = map.get(key) || []
      list.push(item)
      map.set(key, list)
    }
    return map
  }, [queuedCommands])

  const queuedSessionKeys = useMemo(() => new Set(queuedCommands.map(item => scopeKey(item.agentId, item.sessionId))), [queuedCommands])

  const sendQueued = useCallback(async (item: MonitorQueueItem) => {
    const state = useAstrorderStore.getState()
    const targetSession = state.sessions[scopeKey(item.agentId, item.sessionId)]
    if (state.agents[item.agentId]?.kind === 'antigravity' && (targetSession?.status === 'running' || targetSession?.status === 'waiting_approval')) return
    setQueueBusyId(item.id)
    try {
      if (item.source === 'local') await queueOutbox.flush(item.agentId, item.sessionId, item.id)
      else useAstrorderStore.getState().mergeCommands([await api.sendQueuedCommand(item.id, item.agentId, item.sessionId)])
      const failed = item.source === 'local' ? queueOutbox.snapshot().find(entry => entry.payload.id === item.id && ['failed', 'unknown'].includes(entry.state)) : null
      notifications.show(failed ? { color: 'red', message: failed.error || '消息发送未确认' } : { color: 'teal', message: '排队消息已发送' })
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '排队消息发送失败' })
    } finally {
      setQueueBusyId(null)
    }
  }, [queueOutbox])
  const deleteQueued = useCallback(async (item: MonitorQueueItem) => {
    setQueueBusyId(item.id)
    try {
      if (item.source === 'local') await queueOutbox.remove(item.agentId, item.sessionId, item.id)
      else useAstrorderStore.getState().mergeCommands([await api.deleteQueuedCommand(item.id, item.agentId, item.sessionId)])
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '排队消息删除失败' })
    } finally {
      setQueueBusyId(null)
    }
  }, [queueOutbox])
  const handleEditQueued = useCallback((item: MonitorQueueItem) => {
    setEditingQueued(item)
    setQueuedText(item.text)
  }, [])
  const handleOpenSession = useCallback((session: Session) => {
    navigate('/chat/' + encodeURIComponent(session.id) + '?agent_id=' + encodeURIComponent(session.agent_id))
  }, [navigate])
  const handleToggleFocusKey = useCallback((key: string) => {
    setFocusedKey(key)
  }, [])
  const handleClearFocus = useCallback(() => {
    setFocusedKey(null)
  }, [])
  const saveQueuedEdit = async () => {
    if (!editingQueued) return
    try {
      if (editingQueued.source === 'local') await queueOutbox.updateText(editingQueued.agentId, editingQueued.sessionId, editingQueued.id, queuedText.trim())
      else useAstrorderStore.getState().mergeCommands([await api.editQueuedCommand(editingQueued.id, editingQueued.agentId, editingQueued.sessionId, queuedText.trim())])
      setEditingQueued(null)
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '排队消息保存失败' })
    }
  }

  const manual = useMemo(() => new Set(manualKeys), [manualKeys])
  const automatic = useMemo(() => new Set(autoKeys), [autoKeys])
  const excluded = useMemo(() => new Set(excludedKeys), [excludedKeys])

  useEffect(() => {
    const next = new Set(autoKeys)
    for (const session of sessions) {
      if (isCompanionSubagentSession(session)) continue
      const key = scopeKey(session.agent_id, session.id)
      if ((active(session) || queuedSessionKeys.has(key)) && !manual.has(key) && !excluded.has(key)) next.add(key)
    }
    if (next.size === autoKeys.length && autoKeys.every(key => next.has(key))) return
    const values = [...next]
    setAutoKeys(values)
    try { localStorage.setItem(AUTO_STORAGE_KEY, JSON.stringify(values)) } catch {}
  }, [autoKeys, excluded, manual, queuedSessionKeys, sessions])

  const candidateSessions = useMemo(() => {
    return sessions.filter(session => {
      const key = scopeKey(session.agent_id, session.id)
      return !excluded.has(key) && (manual.has(key) || automatic.has(key))
    })
  }, [automatic, excluded, manual, sessions])

  const focusedSession = useMemo(() => {
    if (!focusedKey) return null
    return sessions.find(s => scopeKey(s.agent_id, s.id) === focusedKey) || null
  }, [focusedKey, sessions])

  const orderedSessions = useMemo(() => {
    const byKey = new Map(candidateSessions.map(s => [scopeKey(s.agent_id, s.id), s]))
    const result: Session[] = []
    const seen = new Set<string>()
    for (const key of slotOrder) {
      const s = byKey.get(key)
      if (s) {
        result.push(s)
        seen.add(key)
      }
    }
    // 对于不在 slotOrder 中的新加入会话，追加在末尾，同时按进入顺序固定
    const unpinned: Session[] = []
    for (const s of candidateSessions) {
      const k = scopeKey(s.agent_id, s.id)
      if (!seen.has(k)) {
        unpinned.push(s)
      }
    }
    if (unpinned.length > 0) {
      // 避免按 updated_at 动态重排；一旦出现未排序的新卡片，追加在末尾
      result.push(...unpinned)
    }
    return result
  }, [candidateSessions, slotOrder])

  // 伴星过滤：仅过滤显式委派的子任务（delegation_parent_session_id），fork/交接会话属于正常会话；用户显式加入的会话始终展示
  const gridVisibleSessions = useMemo(() => {
    const base = orderedSessions.filter((s) => {
      const key = scopeKey(s.agent_id, s.id)
      return manual.has(key) || !isCompanionSubagentSession(s)
    })
    if (!projectFilter) return base
    return base.filter((s) => {
      const label = (s.project_name || s.workspace?.split(/[/\\]/).filter(Boolean).pop() || '').trim()
      return label === projectFilter
    })
  }, [manual, orderedSessions, projectFilter])

  const tileProjectSessions = () => {
    if (!projectFilter) return
    const matching = sessions
      .filter((s) => !isCompanionSubagentSession(s))
      .filter((s) => {
        const label = (s.project_name || s.workspace?.split(/[/\\]/).filter(Boolean).pop() || '').trim()
        return label === projectFilter
      })
      .sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))
      .slice(0, 4)
    if (!matching.length) return
    const keys = matching.map((s) => scopeKey(s.agent_id, s.id))
    const nextManual = Array.from(new Set([...manualKeys, ...keys]))
    saveManual(nextManual)
    if (excludedKeys.some((k) => keys.includes(k))) {
      saveExcluded(excludedKeys.filter((k) => !keys.includes(k)))
    }
    notifications.show({ color: 'violet', message: `已将「${projectFilter}」最近 ${matching.length} 个会话平铺到矩阵` })
  }

  // 监听并固化顺序，防止任何会话因为 updated_at 改变而前后乱跳
  useEffect(() => {
    if (gridVisibleSessions.length > 0) {
      const currentKeys = gridVisibleSessions.map(s => scopeKey(s.agent_id, s.id))
      // 检查当前显示的会话是否有未记录在 slotOrder 中的
      const missing = currentKeys.filter(k => !slotOrder.includes(k))
      if (missing.length > 0) {
        saveNewOrder([...slotOrder, ...missing])
      }
    }
  }, [gridVisibleSessions, slotOrder])

  const candidates = useMemo(() => {
    const shown = new Set(gridVisibleSessions.map(session => scopeKey(session.agent_id, session.id)))
    const query = search.trim().toLowerCase()
    return sessions.filter(session => !isCompanionSubagentSession(session) && !shown.has(scopeKey(session.agent_id, session.id)) && (!query || (session.title + ' ' + displaySessionTitle(session) + ' ' + (session.workspace || '') + ' ' + (agents[session.agent_id]?.name || '')).toLowerCase().includes(query)))
  }, [agents, gridVisibleSessions, search, sessions])

  const appendSlotOrder = useCallback((keys: string[]) => {
    setSlotOrder((prev) => {
      const merged = Array.from(new Set([...prev, ...keys]))
      try { localStorage.setItem(ORDER_STORAGE_KEY, JSON.stringify(merged)) } catch {}
      return merged
    })
  }, [])

  const saveManual = useCallback((next: string[]) => {
    setManualKeys(next)
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)) } catch {}
    // 同步固化到 slotOrder，确保无论怎么刷新或触发更新，顺序保持稳定
    appendSlotOrder(next)
  }, [appendSlotOrder])

  const saveAutomatic = useCallback((next: string[]) => {
    setAutoKeys(next)
    try { localStorage.setItem(AUTO_STORAGE_KEY, JSON.stringify(next)) } catch {}
    // 同步固化到 slotOrder
    appendSlotOrder(next)
  }, [appendSlotOrder])

  const saveExcluded = useCallback((next: string[]) => {
    setExcludedKeys(next)
    try { localStorage.setItem(EXCLUDED_STORAGE_KEY, JSON.stringify(next)) } catch {}
  }, [])

  const addManual = (key: string) => {
    if (!manual.has(key)) saveManual([...manualKeys, key])
    if (automatic.has(key)) saveAutomatic(autoKeys.filter(item => item !== key))
    if (excluded.has(key)) saveExcluded(excludedKeys.filter(item => item !== key))
  }

  const removeFromMonitor = useCallback((key: string) => {
    setManualKeys((prev) => {
      if (!prev.includes(key)) return prev
      const next = prev.filter((item) => item !== key)
      try { localStorage.setItem(STORAGE_KEY, JSON.stringify(next)) } catch {}
      return next
    })
    setAutoKeys((prev) => {
      if (!prev.includes(key)) return prev
      const next = prev.filter((item) => item !== key)
      try { localStorage.setItem(AUTO_STORAGE_KEY, JSON.stringify(next)) } catch {}
      return next
    })
    setExcludedKeys((prev) => {
      if (prev.includes(key)) return prev
      const next = [...prev, key]
      try { localStorage.setItem(EXCLUDED_STORAGE_KEY, JSON.stringify(next)) } catch {}
      return next
    })
  }, [])

  const cleanStaleAutomaticSessions = () => {
    const now = Date.now()
    const messagesBySession = useAstrorderStore.getState().messages
    const stale = new Set(sessions.filter(session => {
      const key = scopeKey(session.agent_id, session.id)
      if (!automatic.has(key) || active(session) || queuedSessionKeys.has(key)) return false
      const messageTimes = Object.values(messagesBySession[key] || {}).map(message => Date.parse(message.created_at)).filter(Number.isFinite)
      const lastMessageAt = messageTimes.length ? Math.max(...messageTimes) : Date.parse(session.updated_at)
      return Number.isFinite(lastMessageAt) && now - lastMessageAt >= STALE_AFTER_MS
    }).map(session => scopeKey(session.agent_id, session.id)))
    saveAutomatic(autoKeys.filter(key => !stale.has(key)))
    notifications.show({ color: stale.size ? 'teal' : 'gray', message: stale.size ? `已清理 ${stale.size} 个过时会话` : '没有可清理的过时会话' })
  }

  const saveSizes = useCallback((next: Record<number, SlotSize>) => {
    setSlotSizes(next)
    try { localStorage.setItem(SIZES_STORAGE_KEY, JSON.stringify(next)) } catch {}
  }, [])

  // 切换并重排网格列数 (如 1 列, 2 列, 3 列, 4 列)
  const applyGridColumns = (cols: number) => {
    setGridColumns(cols)
    try { localStorage.setItem('astrorder:monitor-grid-cols', String(cols)) } catch {}
    // 清空各卡片的手工固定宽度，使其完全按设定列数均分
    setSlotSizes((prev) => {
      const next: Record<number, SlotSize> = {}
      Object.entries(prev).forEach(([idx, val]) => {
        next[Number(idx)] = { height: (val as SlotSize)?.height || 520 }
      })
      saveSizes(next)
      return next
    })
    setLayout('grid')
    setLayoutMenuOpened(false)
  }


  const saveNewOrder = useCallback((next: string[]) => {
    setSlotOrder(next)
    try { localStorage.setItem(ORDER_STORAGE_KEY, JSON.stringify(next)) } catch {}
  }, [])

  const gridVisibleSessionsRef = useRef(gridVisibleSessions)
  gridVisibleSessionsRef.current = gridVisibleSessions
  const gridColumnsRef = useRef(gridColumns)
  gridColumnsRef.current = gridColumns

  // Mouse down on header: initiates physical lift
  const handleHeaderMouseDown = useCallback((slotIndex: number, e: MouseEvent<HTMLDivElement>, wrapperEl: HTMLElement | null) => {
    if (e.button !== 0 || !wrapperEl) return
    e.preventDefault()
    const rect = wrapperEl.getBoundingClientRect()
    const grabOffsetX = e.clientX - rect.left
    const grabOffsetY = e.clientY - rect.top
    const startX = e.clientX
    const startY = e.clientY
    let hasMoved = false

    const onMouseMove = (moveEvt: globalThis.MouseEvent) => {
      const dist = Math.hypot(moveEvt.clientX - startX, moveEvt.clientY - startY)
      if (!hasMoved && dist < 4) return
      hasMoved = true

      // Find slot under cursor
      const targetSlotEl = document.elementFromPoint(moveEvt.clientX, moveEvt.clientY)?.closest('.monitor-card-wrapper')
      let overIdx: number | null = null
      if (targetSlotEl) {
        const raw = targetSlotEl.getAttribute('data-slot-index')
        if (raw !== null) overIdx = parseInt(raw, 10)
      }

      setLiftedDrag({
        sourceIndex: slotIndex,
        currentX: moveEvt.clientX,
        currentY: moveEvt.clientY,
        grabOffsetX,
        grabOffsetY,
        width: rect.width,
        height: rect.height,
        overIndex: overIdx,
      })
    }

    const onMouseUp = () => {
      window.removeEventListener('mousemove', onMouseMove)
      window.removeEventListener('mouseup', onMouseUp)
      const active = liftedDragRef.current
      if (active && active.overIndex !== null && active.overIndex !== active.sourceIndex) {
        const from = active.sourceIndex
        const to = active.overIndex
        const nextList = [...gridVisibleSessionsRef.current]
        if (nextList[from] && nextList[to]) {
          const temp = nextList[from]
          nextList[from] = nextList[to]
          nextList[to] = temp
          saveNewOrder(nextList.map(s => scopeKey(s.agent_id, s.id)))
        }
      }
      setLiftedDrag(null)
    }

    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseup', onMouseUp)
  }, [saveNewOrder])

  // Resize bottom-right corner
  const handleResizeCornerStart = useCallback((slotIndex: number, e: MouseEvent<HTMLDivElement>, wrapperEl: HTMLElement | null) => {
    e.preventDefault()
    e.stopPropagation()
    const startX = e.clientX
    const startY = e.clientY
    const rect = wrapperEl ? wrapperEl.getBoundingClientRect() : { width: 560, height: 520 }
    const initialW = rect.width
    const initialH = rect.height

    // 获取页面容器宽度，用于计算 50% 等分对齐
    const containerEl = wrapperEl?.parentElement
    const containerW = containerEl ? containerEl.clientWidth : window.innerWidth
    const defaultColW = Math.round((containerW - (gridColumnsRef.current - 1) * 16) / gridColumnsRef.current)
    const fullW = Math.round(containerW) // 单列全屏对齐宽度

    // 常见标准高度吸附点
    const snapHeights = [420, 520, 640, 760, 880]

    // 收集同屏其他卡片的实际宽高作为对齐目标
    const siblingWidths: number[] = [defaultColW, fullW]
    const siblingHeights: number[] = [...snapHeights]
    if (containerEl) {
      const allCards = containerEl.querySelectorAll<HTMLElement>('.monitor-card-wrapper')
      allCards.forEach((card) => {
        if (card !== wrapperEl) {
          const r = card.getBoundingClientRect()
          if (r.width > 200) siblingWidths.push(Math.round(r.width))
          if (r.height > 200) siblingHeights.push(Math.round(r.height))
        }
      })
    }

    const onMouseMove = (moveEvt: globalThis.MouseEvent) => {
      const dx = moveEvt.clientX - startX
      const dy = moveEvt.clientY - startY
      let rawW = Math.min(Math.max(initialW + dx, 360), containerW)
      let rawH = Math.min(Math.max(initialH + dy, 360), 960)

      // 对齐吸附逻辑（Snap Threshold: 16px）
      const SNAP_THRESHOLD = 16
      let snappedW = false
      let snappedH = false

      for (const targetW of siblingWidths) {
        if (Math.abs(rawW - targetW) <= SNAP_THRESHOLD) {
          rawW = targetW
          snappedW = true
          break
        }
      }

      for (const targetH of siblingHeights) {
        if (Math.abs(rawH - targetH) <= SNAP_THRESHOLD) {
          rawH = targetH
          snappedH = true
          break
        }
      }

      const finalW = Math.round(rawW)
      const finalH = Math.round(rawH)

      setResizingInfo({ slotIndex, width: finalW, height: finalH, isSnapped: snappedW || snappedH })
      setSlotSizes((prev) => ({
        ...prev,
        [slotIndex]: { width: finalW, height: finalH },
      }))
    }

    const onMouseUp = () => {
      window.removeEventListener('mousemove', onMouseMove)
      window.removeEventListener('mouseup', onMouseUp)
      setResizingInfo(null)
      setSlotSizes((latest) => {
        saveSizes(latest)
        return latest
      })
    }

    window.addEventListener('mousemove', onMouseMove)
    window.addEventListener('mouseup', onMouseUp)
  }, [saveSizes])

  const counts = useMemo(() => ({ running: gridVisibleSessions.filter(active).length, waiting: gridVisibleSessions.filter(session => session.status === 'waiting_approval').length }), [gridVisibleSessions])

  const liftedSession = liftedDrag !== null ? gridVisibleSessions[liftedDrag.sourceIndex] : null
  const visibleKeys = useMemo(() => new Set(gridVisibleSessions.map(s => scopeKey(s.agent_id, s.id))), [gridVisibleSessions])
  const unassignedQueued = useMemo(() => queuedCommands.filter(item => !visibleKeys.has(scopeKey(item.agentId, item.sessionId))), [queuedCommands, visibleKeys])

  return (
    <div
      className={`route-page monitor-page ${pageDragOver ? 'is-drop-active' : ''}`}
      onDragOver={(e) => {
        e.preventDefault()
        e.dataTransfer.dropEffect = 'copy'
        setPageDragOver(true)
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node)) {
          setPageDragOver(false)
        }
      }}
      onDrop={(e) => {
        e.preventDefault()
        setPageDragOver(false)
        let key = ''
        let title = '会话'
        const raw = e.dataTransfer.getData('application/x-astrorder-session')
        if (raw) {
          try {
            const data = JSON.parse(raw)
            key = data.key || scopeKey(data.agent_id, data.id)
            title = data.title || '会话'
          } catch {}
        }
        if (!key) {
          key = e.dataTransfer.getData('text/plain')
        }
        if (key && !visibleKeys.has(key)) {
          addManual(key)
          if (projectFilter) {
            const droppedSession = sessions.find(s => scopeKey(s.agent_id, s.id) === key)
            const droppedLabel = (droppedSession?.project_name || droppedSession?.workspace?.split(/[/\\]/).filter(Boolean).pop() || '').trim()
            if (droppedSession && droppedLabel !== projectFilter) {
              const next = new URLSearchParams(searchParams)
              next.delete('project')
              setSearchParams(next, { replace: true })
            }
          }
          notifications.show({
            color: 'teal',
            message: `已将“${title}”加入矩阵`,
          })
        } else if (key) {
          notifications.show({
            color: 'blue',
            message: `“${title}”已在矩阵中`,
          })
        }
      }}
    >
      <div className="monitor-ambient-backdrop" aria-hidden="true" style={{ position: 'absolute', inset: 0, pointerEvents: 'none', zIndex: 0 }} />
      {resizingInfo && (
        <div className="monitor-resizing-overlay">
          <div className={`monitor-dimension-tooltip ${resizingInfo.isSnapped ? 'is-snapped' : ''}`}>
            {resizingInfo.width} × {resizingInfo.height} px
            {resizingInfo.isSnapped && <span className="snap-indicator"> (已吸附对齐)</span>}
          </div>
        </div>
      )}

      {/* Opaque Solid Floating Entity when lifted */}
      {liftedDrag && liftedSession && (
        <div
          className="monitor-floating-portal-layer"
          style={{
            position: 'fixed',
            left: liftedDrag.currentX - liftedDrag.grabOffsetX,
            top: liftedDrag.currentY - liftedDrag.grabOffsetY,
            width: liftedDrag.width,
            height: liftedDrag.height,
            pointerEvents: 'none',
            zIndex: 99999,
          }}
        >
          <MonitorCard
            session={liftedSession}
            agent={agents[liftedSession.agent_id]}
            slotIndex={liftedDrag.sourceIndex}
            slotSize={{ width: liftedDrag.width, height: liftedDrag.height }}
            isFloating={true}
            queuedItems={queuedCommandsBySession.get(scopeKey(liftedSession.agent_id, liftedSession.id)) || EMPTY_QUEUE_ITEMS}
            queueBusyId={queueBusyId}
            onOpen={NOOP}
          />
        </div>
      )}

      <Group className="route-heading monitor-heading" justify="space-between" align="center" mb="lg" wrap="wrap">
        <Group gap="sm" align="center" wrap="wrap">
          <Title order={2} size="h2" style={{ margin: 0 }}>矩阵</Title>
          {projectFilter && (
            <Group gap={6} wrap="nowrap">
              <Badge
                size="md"
                variant="light"
                color="violet"
                rightSection={
                  <ActionIcon
                    size="xs"
                    variant="transparent"
                    color="violet"
                    aria-label="清除项目筛选"
                    onClick={() => {
                      const next = new URLSearchParams(searchParams)
                      next.delete('project')
                      setSearchParams(next, { replace: true })
                    }}
                  >
                    <IconX size={11} />
                  </ActionIcon>
                }
              >
                项目：{projectFilter}
              </Badge>
              <Button size="compact-xs" variant="light" color="violet" onClick={tileProjectSessions}>
                一键平铺活跃会话
              </Button>
            </Group>
          )}
        </Group>
        <Group gap="sm" wrap="wrap" align="center">
          <Group className="monitor-summary-bar" gap={6} align="center">
            <Group gap={5} className={`monitor-stat-item ${counts.running > 0 ? 'is-active' : ''}`}>
              <span className="monitor-stat-dot is-running" />
              <Text size="xs" fw={600}>{counts.running}</Text>
              <Text size="xs" c="dimmed">进行中</Text>
            </Group>
            {counts.waiting > 0 && (
              <Group gap={5} className="monitor-stat-item is-waiting">
                <span className="monitor-stat-dot is-waiting" />
                <Text size="xs" fw={600}>{counts.waiting}</Text>
                <Text size="xs" c="dimmed">待确认</Text>
              </Group>
            )}
            {queuedCommands.length > 0 && (
              <Group gap={5} className="monitor-stat-item">
                <Text size="xs" fw={600}>{queuedCommands.length}</Text>
                <Text size="xs" c="dimmed">待机</Text>
              </Group>
            )}
          <span className="monitor-stat-divider" />
          <Text size="xs" c="dimmed">
            {`${gridVisibleSessions.length} 个会话`}
          </Text>
        </Group>
        <Button variant="subtle" color="gray" size="xs" leftSection={<IconPlus size={14} />} onClick={() => setPickerOpen(true)}>添加会话</Button>
        <Button variant="subtle" color="gray" size="xs" leftSection={<IconTrash size={14} />} onClick={cleanStaleAutomaticSessions}>清理过时会话</Button>
        <Popover opened={layoutMenuOpened} onChange={setLayoutMenuOpened} position="bottom-end" shadow="md" radius="md">
          <Popover.Target>
            <Button
              variant="subtle"
              color="gray"
              size="xs"
              leftSection={<IconGridDots size={14} />}
              onClick={() => setLayoutMenuOpened((o) => !o)}
              title="切换卡片布局"
            >
              布局
            </Button>
          </Popover.Target>
          <Popover.Dropdown p="xs" className="monitor-grid-picker-dropdown">
            <div className="grid-picker-header">
              <Text size="xs" fw={600}>
                {hoverCols} 列布局
              </Text>
              <Text size="11px" c="dimmed">
                自动等分排列
              </Text>
            </div>
            <div className="grid-picker-cells">
              <div className="grid-picker-row cols-only">
                {[1, 2, 3, 4].map((c) => {
                  const isSelected = c <= hoverCols
                  const isCurrent = c === gridColumns
                  return (
                    <div
                      key={c}
                      className={`grid-picker-cell ${isSelected ? 'is-active' : ''} ${isCurrent ? 'is-current' : ''}`}
                      onMouseEnter={() => setHoverCols(c)}
                      onClick={() => applyGridColumns(c)}
                      title={`${c} 列排列`}
                    />
                  )
                })}
              </div>
            </div>
          </Popover.Dropdown>
        </Popover>
        <SegmentedControl
          value={layout}
          onChange={setLayout}
          data={[
            { label: '网格', value: 'grid' },
            { label: '列表', value: 'list' },
          ]}
          aria-label="矩阵布局"
        />
        </Group>
      </Group>
      {unassignedQueued.length > 0 && (
        <Paper className="monitor-queue monitor-unassigned-queue" withBorder radius="lg" p="md" mb="md" aria-label="待机队列">
          <Group justify="space-between" mb="sm"><div><Title order={3} size="h4">待机队列</Title><Text size="sm" c="dimmed">等待发送的未展示会话消息。</Text></div><Badge color="yellow" variant="light">{unassignedQueued.length} 项</Badge></Group>
          <Stack gap="xs">
            {unassignedQueued.map(item => {
              const session = sessions.find(session => scopeKey(session.agent_id, session.id) === scopeKey(item.agentId, item.sessionId))
              return (
                <Paper key={`${item.agentId}::${item.id}`} className="monitor-queue-item" withBorder p="xs" radius="md">
                  <Group justify="space-between" gap="xs" wrap="nowrap">
                    <button className="monitor-queue-link" type="button" onClick={() => session && navigate('/chat/' + encodeURIComponent(session.id) + '?agent_id=' + encodeURIComponent(session.agent_id))}>
                      <span>{item.text || '附件消息'}</span>
                      <small>{session?.title || '会话未返回'}</small>
                    </button>
                    <Group gap={6} wrap="nowrap">
                      <Button size="compact-xs" variant="light" loading={queueBusyId === item.id} onClick={() => void sendQueued(item)}>发送</Button>
                      <Button size="compact-xs" variant="subtle" onClick={() => { setEditingQueued(item); setQueuedText(item.text) }}>编辑</Button>
                      <Button size="compact-xs" variant="subtle" color="red" disabled={queueBusyId === item.id} onClick={() => void deleteQueued(item)}>删除</Button>
                    </Group>
                  </Group>
                </Paper>
              )
            })}
          </Stack>
        </Paper>
      )}
      {orderedSessions.length === 0 ? <EmptyState icon={<IconCircleCheck />} title="矩阵暂无会话" description="运行中的会话会自动加入，也可以手动添加会话。" />
        : layout === 'list' ? (
          <div className="monitor-list-view">
            <Stack gap={8}>
              {gridVisibleSessions.map((session) => {
                const key = scopeKey(session.agent_id, session.id)
                const sessionQueued = queuedCommandsBySession.get(key) || EMPTY_QUEUE_ITEMS
                const rowQueueBusyId = queueBusyId && sessionQueued.some((item) => item.id === queueBusyId) ? queueBusyId : null
                return (
                  <MonitorListRow
                    key={key}
                    session={session}
                    agent={agents[session.agent_id]}
                    queuedItems={sessionQueued}
                    queueBusyId={rowQueueBusyId}
                    onSendQueued={sendQueued}
                    onEditQueued={handleEditQueued}
                    onDeleteQueued={deleteQueued}
                    onOpenSession={handleOpenSession}
                    onRemoveKey={removeFromMonitor}
                  />
                )
              })}
            </Stack>
          </div>
        ) : (
          <div className="monitor-grid monitor-layout-grid">
            {gridVisibleSessions.map((session, slotIdx) => {
              const key = scopeKey(session.agent_id, session.id)
              const size = slotSizes[slotIdx] || DEFAULT_SLOT_SIZE
              const sessionQueued = queuedCommandsBySession.get(key) || EMPTY_QUEUE_ITEMS
              const cardQueueBusyId = queueBusyId && sessionQueued.some((item) => item.id === queueBusyId) ? queueBusyId : null
              const isBeingLifted = liftedDrag?.sourceIndex === slotIdx
              const isOverTarget = liftedDrag?.overIndex === slotIdx && !isBeingLifted

              // If this card is currently lifted, render the empty placeholder slot in its place!
              if (isBeingLifted) {
                const cardHeight = size.height || 520
                const cardWidth = size.width
                return (
                  <div
                    key={'placeholder-' + key}
                    data-slot-index={slotIdx}
                    className="monitor-card-placeholder-slot"
                    style={{
                      width: cardWidth ? cardWidth + 'px' : undefined,
                      flex: cardWidth ? '0 0 ' + cardWidth + 'px' : `1 1 calc(${100 / gridColumns}% - 16px)`,
                      height: cardHeight + 'px',
                    }}
                  >
                    <div className="placeholder-inner-box">
                      <span className="placeholder-label">卡片已拎起 · 原位占位</span>
                    </div>
                  </div>
                )
              }

              return (
                <MonitorCard
                  key={key}
                  session={session}
                  agent={agents[session.agent_id]}
                  slotIndex={slotIdx}
                  slotSize={size}
                  gridColumns={gridColumns}
                  isDropTarget={isOverTarget}
                  isFocused={false}
                  queuedItems={sessionQueued}
                  queueBusyId={cardQueueBusyId}
                  onSendQueued={sendQueued}
                  onEditQueued={handleEditQueued}
                  onDeleteQueued={deleteQueued}
                  onToggleFocusKey={handleToggleFocusKey}
                  onOpenSession={handleOpenSession}
                  onRemoveKey={removeFromMonitor}
                  onHeaderMouseDown={handleHeaderMouseDown}
                  onResizeCornerStart={handleResizeCornerStart}
                />
              )
            })}
          </div>
        )}
      {focusedSession && (
        <div
          className="monitor-focus-overlay"
          onClick={handleClearFocus}
          role="dialog"
          aria-label="聚焦卡片"
        >
          <div
            className="monitor-focus-dialog"
            onClick={(e) => e.stopPropagation()}
          >
            <MonitorCard
              session={focusedSession}
              agent={agents[focusedSession.agent_id]}
              slotIndex={-1}
              slotSize={FOCUSED_SLOT_SIZE}
              isFocused={true}
              queuedItems={queuedCommandsBySession.get(scopeKey(focusedSession.agent_id, focusedSession.id)) || EMPTY_QUEUE_ITEMS}
              queueBusyId={queueBusyId}
              onSendQueued={sendQueued}
              onEditQueued={handleEditQueued}
              onDeleteQueued={deleteQueued}
              onToggleFocus={handleClearFocus}
              onOpenSession={handleOpenSession}
            />
          </div>
        </div>
      )}
      <Modal opened={pickerOpen} onClose={() => { setPickerOpen(false); setSearch('') }} title="添加会话" centered>
        <TextInput aria-label="搜索会话" placeholder="搜索会话、项目或 Agent" value={search} onChange={event => setSearch(event.currentTarget.value)} mb="sm" />
        <div className="monitor-session-picker">
          {candidates.length ? (
            candidates.map((session) => {
              const key = scopeKey(session.agent_id, session.id)
              const agent = agents[session.agent_id]
              return (
                <button
                  key={key}
                  type="button"
                  className="monitor-picker-row"
                  onClick={() => {
                    addManual(key)
                    setPickerOpen(false)
                    setSearch('')
                  }}
                >
                  <span className="picker-title">{displaySessionTitle(session)}</span>
                  <span className="picker-agent-chip">
                    <AgentBrandIcon kind={agent?.kind} size={13} />
                    <span>{agent?.name || session.agent_id}</span>
                  </span>
                </button>
              )
            })
          ) : (
            <Text size="sm" c="dimmed" ta="center" py="lg">没有可添加的会话</Text>
          )}
        </div>
      </Modal>
      <Modal opened={Boolean(editingQueued)} onClose={() => setEditingQueued(null)} title="编辑排队消息" centered>
        <Textarea aria-label="排队消息内容" value={queuedText} onChange={event => setQueuedText(event.currentTarget.value)} minRows={4} autosize />
        <Group justify="flex-end" mt="md">
          <Button variant="default" onClick={() => setEditingQueued(null)}>取消</Button>
          <Button onClick={() => void saveQueuedEdit()} disabled={!queuedText.trim() && !(editingQueued?.source === 'local' && (editingQueued.entry.files.length || editingQueued.entry.attachments.length))}>保存</Button>
        </Group>
      </Modal>
    </div>
  )
}
