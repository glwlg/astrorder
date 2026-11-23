import { UserInputCard, isUserInput } from '../../components/UserInputCard'
import { AsyncUserInputCard, isAsyncQuestion, hasLaterNativeInput } from '../../components/AsyncUserInputCard'
import { IconArrowDown, IconPaperclip, IconRefresh, IconTool, IconVectorTriangle } from '@tabler/icons-react'
import { ActionIcon, Anchor, Button, Group, Paper, Stack, Text, Tooltip } from '@mantine/core'
import { IconCheck, IconChecks, IconCopy, IconEdit, IconGitFork, IconHistory, IconShieldCheck, IconX } from '@tabler/icons-react'
import type { SessionCheckpoint } from '../../api/client'
import { mapCheckpointsToMessages } from '../../domain/turnCheckpoints'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { TurnMinimap } from './TurnMinimap'
import { InlineNotice } from '../../components/InlineNotice'
import type { Approval, Attachment, Message, OutboxEntry, Session } from '../../domain/types'
import { attachmentHref } from '../../domain/attachmentHref'
import { hasMessageContent, isMessageActivity, normalizeMessagePresentation } from '../../domain/messagePresentation'
import { activityPacks } from '../../domain/activityPacks'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { MarkdownContent } from '../../components/MarkdownContent'
import { LazyDetails } from '../../components/LazyDetails'
import { MessageBody } from '../../components/MessageBody'
import { useOlderMessages } from '../../hooks/useOlderMessages'
import { activityDuration, activityState, describeTool, extractToolArgs, FileChangeDiffBlock, fileChangeDiffs, formatCommandRowDisplay, PackSummary, ShellOutputBlock, ToolLineIcon, unwrapCommand } from './toolPresentation'
import { ToolOutputBlock } from './ToolOutputBlock'
import { isCompactionMessage, CompactionDivider } from './CompactionDivider'
import { ShinyText } from '../../components/animations/ShinyText'
import { StarBorder } from '../../components/animations/StarBorder'
import { GlareHover } from '../../components/animations/GlareHover'
import { AstrorderLoader } from '../../components/AnimatedStatus'
import { artifactViewerRegistry } from '../sidecar/registry'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { LIVE_ACTIVITY_MS, useAstrorderStore } from '../../state/store'
import { scopeKey } from '../../domain/semantics'
import { useReducedMotion } from '@mantine/hooks'
import { AnimatePresence, motion } from 'motion/react'
import { WorkbenchMark } from '../../components/WorkbenchMark'
import { agentKindLabel } from '../../components/AgentBrandIcon'

export function AttachmentList({
  attachments,
  onImageClick,
  session,
  onGalleryClick,
}: {
  attachments: Attachment[]
  onImageClick?: (url: string) => void
  session?: Session | null
  onGalleryClick?: (url: string, allImages: string[]) => void
}) {
  const defaultSession = useAstrorderStore((state) => Object.values(state.sessions)[0] as Session | undefined)
  const currentSession = session || defaultSession
  const openArtifact = useSidecarStore((state) => state.openArtifact)

  if (attachments.length === 0) return null
  const allImageUrls = attachments
    .filter((a) => a.media_type.startsWith('image/'))
    .map((a) => attachmentHref(a))
    .filter(Boolean) as string[]
  return (
    <div className="message-attachments">
      {attachments.map((attachment) => {
        const href = attachmentHref(attachment)
        const isImage = attachment.media_type.startsWith('image/') && href
        const isDrawio = /\.(drawio|drawio\.xml|drawio\.svg)$/i.test(attachment.name)

        if (isImage) {
          return (
            <GlareHover key={attachment.id} borderRadius={9} glareOpacity={0.15}>
            <button
              type="button"
              className="attachment-image-link"
              onClick={() => onGalleryClick ? onGalleryClick(href, allImageUrls) : onImageClick?.(href)}
              aria-label={`查看图片：${attachment.name}`}
            >
              <img className="attachment-image" src={href} alt={attachment.name} loading="lazy" />
            </button>
            </GlareHover>
          )
        }

        if (isDrawio && href && currentSession) {
          return (
            <button
              type="button"
              className="attachment-file attachment-artifact-link"
              key={attachment.id}
              onClick={() => {
                const artifact = resolveArtifactFromPath(href, currentSession, {
                  name: attachment.name,
                  mediaType: attachment.media_type,
                })
                const viewer = artifactViewerRegistry.findViewer(artifact)
                if (viewer) {
                  openArtifact(artifact, viewer.id)
                } else {
                  window.open(href, '_blank')
                }
              }}
              title={`在右侧打开图表：${attachment.name}`}
            >
              <IconVectorTriangle size={15} aria-hidden="true" style={{ color: 'var(--astr-indigo)' }} />
              <span>{attachment.name}</span>
            </button>
          )
        }

        // 检查是否能由任何内置工件查看器处理（如 HTML、Mermaid、Excalidraw、Diff、3D 模型等）
        if (href && currentSession) {
          const artifact = resolveArtifactFromPath(href, currentSession, {
            name: attachment.name,
            mediaType: attachment.media_type,
          })
          const viewer = artifactViewerRegistry.findViewer(artifact)
          if (viewer) {
            const Icon = viewer.icon
            return (
              <button
                type="button"
                className="attachment-file attachment-artifact-link"
                key={attachment.id}
                onClick={() => openArtifact(artifact, viewer.id)}
                title={`在右侧打开${viewer.title}：${attachment.name}`}
              >
                <span style={{ display: 'inline-flex', color: 'var(--astr-indigo)' }}>
                  <Icon size={15} />
                </span>
                <span>{attachment.name}</span>
              </button>
            )
          }
        }

        return href ? (
          <Anchor className="attachment-file" href={href} target="_blank" rel="noreferrer" key={attachment.id}>
            <IconPaperclip size={15} aria-hidden="true" />
            <span>{attachment.name}</span>
          </Anchor>
        ) : (
          <span className="attachment-file attachment-unavailable" key={attachment.id}>
            <IconPaperclip size={15} aria-hidden="true" />
            <span>{attachment.name}（链接不可用）</span>
          </span>
        )
      })}
    </div>
  )
}

function messageLabel(message: Message): string {
  if (message.kind === 'thinking') return '思考'
  if (message.kind === 'tool' || message.role === 'tool') return message.tool?.name ? `工具 · ${String(message.tool.name)}` : '工具活动'
  if (message.role === 'user') return '我'
  if (message.role === 'system') return '系统'
  return 'Agent'
}

function MessageItem({
  message,
  onImageClick,
  onGalleryClick,
  session,
  isLatest,
  inlineMeta = false,
  onEdit,
  onFork,
  hasAssistantReplied = false,
  isAgentAccepted = false,
  asyncInput,
  showAuthorMark = false,
  authorLabel = 'Agent',
  checkpoint,
  onOpenCheckpoint,
}: {
  message: Message
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allImages: string[]) => void
  session?: Session | null
  isLatest?: boolean
  inlineMeta?: boolean
  onEdit?: (text: string) => void
  onFork?: () => void
  hasAssistantReplied?: boolean
  isAgentAccepted?: boolean
  asyncInput?: { commands: import('../../domain/types').Command[]; continued: boolean; readOnly: boolean }
  showAuthorMark?: boolean
  authorLabel?: string
  checkpoint?: SessionCheckpoint
  onOpenCheckpoint?: (cp: SessionCheckpoint) => void
}) {
  const isUser = message.role === 'user'
  const isActivity = isMessageActivity(message)
  const isReviewMode = isUser && (
    message.text.includes('检查我未提交的更改') ||
    message.text.includes('审查我的更改') ||
    message.text.startsWith('/review')
  )
  const [copied, setCopied] = useState(false)
  const handleCopy = () => {
    if (!message.text) return
    void navigator.clipboard.writeText(message.text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1600)
  }
  if (isActivity) {
    const desc = describeTool(message)
    const isThinking = message.kind === 'thinking'
    const toolArgs = extractToolArgs(message.tool)
    const rawCmd = String(toolArgs.command || toolArgs.cmd || toolArgs.CommandLine || toolArgs.Command || '')
    const rawCwd = String(toolArgs.cwd || toolArgs.Cwd || '')
    const rawExitCode = (message.tool as { exit_code?: unknown } | null | undefined)?.exit_code
    const exitCode = typeof rawExitCode === 'number' && Number.isFinite(rawExitCode) ? rawExitCode : null
    const isCommand = desc.iconKey === 'terminal' || Boolean(rawCmd)
    const hasFileDiff = fileChangeDiffs(message).length > 0
    const unwrappedCmd = unwrapCommand(rawCmd)
    const cmdDisplay = isCommand ? formatCommandRowDisplay(desc, unwrappedCmd) : null
    const cmdStatus = desc.isFailed ? 'failed' : desc.isRunning ? 'running' : 'success'
    const state = activityState(message)
    const duration = activityDuration(message)
    const foldTitle = desc.target || desc.fullTitle
    return (
      <article className={`message-activity message-kind-${message.kind}`} data-status={state} data-testid={`message-${message.id}`}>
        <span className="activity-node" aria-hidden="true">{!isThinking && (state === 'completed' ? <IconCheck /> : state === 'failed' ? <IconX /> : null)}</span>
        <LazyDetails className={`activity-fold ${desc.isFailed ? 'is-failed' : ''}`} loading={desc.isRunning} summary={
          <span className="activity-fold-summary">
            <span className="activity-icon"><ToolLineIcon icon={desc.iconKey} size={14} /></span>
            <span className="activity-title" title={cmdDisplay ? cmdDisplay.tooltip : desc.fullTitle}>
              {cmdDisplay ? (
                cmdDisplay.isHumanSummary
                  ? <>{cmdDisplay.action} <span className="activity-cmd-summary">{cmdDisplay.text}</span></>
                  : <>{cmdDisplay.action} <code className="activity-target">{cmdDisplay.text}</code></>
              ) : !isThinking && desc.target ? (
                <>{desc.action} <code className="activity-target">{desc.target}</code></>
              ) : foldTitle}
            </span>
            <span className="activity-item-meta">
              {desc.isFailed ? <><span className="activity-badge is-failed">失败</span><span className="activity-detail-link">查看详情</span></>
                : desc.isRunning ? <span className="activity-badge is-running">执行中</span>
                  : !isThinking && state === 'completed' ? <span>已完成</span> : null}
              {duration && <span className="activity-item-duration">· {duration}</span>}
            </span>
          </span>
        }>
          {isThinking ? (
            message.text && <div className="activity-thinking-content"><MarkdownContent value={message.text} session={session} /></div>
          ) : isCommand ? (
            <ShellOutputBlock
              command={unwrappedCmd || desc.fullTitle}
              output={message.text}
              status={cmdStatus}
              summary={desc.summary}
              cwd={rawCwd || undefined}
              exitCode={exitCode}
            />
          ) : hasFileDiff ? (
            <FileChangeDiffBlock message={message} session={session} />
          ) : (
            <ToolOutputBlock
              text={message.text}
              payload={Object.keys(toolArgs).length > 0 ? toolArgs : null}
              toolName={String(message.tool?.name || '')}
              session={session}
              onImageClick={onImageClick}
            />
          )}
          <AttachmentList attachments={message.attachments} onImageClick={onImageClick} session={session} />
        </LazyDetails>
      </article>
    )
  }
  const metadata = (
    <span className={`message-meta ${inlineMeta ? 'message-meta-inline' : ''}`}>
      {isReviewMode && <span className="message-review-mode-tag">审查模式</span>}
      {Date.parse(message.created_at) > 0 && <time dateTime={message.created_at}>{new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</time>}
      {isUser && <span className="message-status-ticks" title={hasAssistantReplied ? 'Agent 已响应' : isAgentAccepted ? 'Agent 已接收' : '已发送'} style={{ display: 'inline-flex', alignItems: 'center', color: hasAssistantReplied || isAgentAccepted ? 'var(--astr-indigo, #5b6cff)' : 'var(--astr-muted)' }}>
        {hasAssistantReplied || isAgentAccepted ? <IconChecks size={14} /> : <IconCheck size={14} />}
      </span>}
      <span className="message-actions-bar">
        {message.text && <Tooltip label={copied ? '已复制' : '复制内容'} position="top" withArrow>
          <ActionIcon variant="subtle" size="xs" color="gray" onClick={handleCopy} aria-label="复制消息" className="message-action-btn">
            {copied ? <IconCheck size={13} color="var(--astr-teal, #12b886)" /> : <IconCopy size={13} />}
          </ActionIcon>
        </Tooltip>}
        {!isUser && onFork && <Tooltip label="从此回复分叉新会话" position="top" withArrow>
          <ActionIcon variant="subtle" size="xs" color="indigo" onClick={onFork} aria-label="分叉会话" className="message-action-btn message-fork-btn"><IconGitFork size={13} /></ActionIcon>
        </Tooltip>}
        {isUser && onEdit && <Tooltip label="编辑消息" position="top" withArrow>
          <ActionIcon variant="subtle" size="xs" color="gray" onClick={() => onEdit(message.text)} aria-label="编辑消息" className="message-action-btn"><IconEdit size={13} /></ActionIcon>
        </Tooltip>}
      </span>
      {!isUser && checkpoint && onOpenCheckpoint && (
        <button
          type="button"
          className="cp-inline-pill"
          title={`查看检查点 #${checkpoint.turn_number} 差异或安全回滚`}
          onClick={() => onOpenCheckpoint(checkpoint)}
        >
          <IconHistory size={12} aria-hidden="true" />
          <span>
            #{checkpoint.turn_number}
            {checkpoint.files_changed > 0 ? ` · ${checkpoint.files_changed} 文件` : ''}
          </span>
          {checkpoint.insertions > 0 && <span className="cp-stat-add">+{checkpoint.insertions}</span>}
          {checkpoint.deletions > 0 && <span className="cp-stat-del">-{checkpoint.deletions}</span>}
        </button>
      )}
    </span>
  )
  return (
    <article
      className={`message-row message-${message.role} message-kind-${message.kind} ${isLatest ? 'is-latest-message' : ''}`}
      data-testid={`message-${message.id}`}
      aria-label={`${messageLabel(message)}消息`}
    >
      {showAuthorMark && <div className="message-author">
        <span className="message-author-mark" aria-hidden="true"><WorkbenchMark size={20} /></span>
        <span>{authorLabel}</span>
      </div>}
      <Paper className={`message-bubble ${inlineMeta ? 'message-bubble-inline' : ''} ${isUser ? 'message-bubble-user' : isActivity ? 'message-bubble-activity' : ''}`} withBorder={!isActivity} radius="lg">
        {asyncInput ? <AsyncUserInputCard message={message} {...asyncInput} /> : message.text && (
          <MessageBody
            value={message.text}
            user={isUser}
            onImageClick={onImageClick}
            onGalleryClick={onGalleryClick}
            attachmentNames={message.attachments.filter((attachment) => attachment.media_type.startsWith('image/')).map((attachment) => attachment.name)}
            renderMarkdown={(value) => <MarkdownContent value={value} onImageClick={onImageClick} onGalleryClick={onGalleryClick} session={session} />}
            sessionId={session?.id}
            connectionId={session?.connection_id || undefined}
          />
        )}
        {message.tool && (
          <pre className="tool-payload">{JSON.stringify(message.tool, null, 2)}</pre>
        )}
        <AttachmentList attachments={message.attachments} onImageClick={onImageClick} onGalleryClick={onGalleryClick} session={session} />
        {inlineMeta && metadata}
      </Paper>
      {!inlineMeta && metadata}
    </article>
  )
}

const ACTIVITY_PACK_COLLAPSE_GRACE_MS = 4_500

function ActivityPackSection({
  id,
  pack,
  isRunning,
  definitivelyEnded,
  isDeferred,
  enter,
  onImageClick,
  onGalleryClick,
  session,
}: {
  id: string
  pack: Message[]
  isRunning: boolean
  definitivelyEnded: boolean
  isDeferred: boolean
  enter: { opacity?: number; y?: number; scale?: number }
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allImages: string[]) => void
  session?: Session | null
}) {
  const [autoOpen, setAutoOpen] = useState(isRunning)
  const packSize = pack.length

  useEffect(() => {
    if (isRunning) {
      setAutoOpen(true)
      return
    }
    if (definitivelyEnded) {
      setAutoOpen(false)
      return
    }
    if (!autoOpen) return
    const timer = window.setTimeout(() => setAutoOpen(false), ACTIVITY_PACK_COLLAPSE_GRACE_MS)
    return () => window.clearTimeout(timer)
  }, [autoOpen, definitivelyEnded, isRunning, packSize])

  return (
    <motion.section
      className={`activity-pack${isDeferred ? ' is-deferred-turn' : ''}`}
      aria-label="思考与工具"
      data-turn-id={id}
      initial={enter}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0 }}
      transition={{ duration: 0.28, ease: [0.2, 0.8, 0.2, 1] }}
    >
      <LazyDetails
        autoOpen={autoOpen}
        loading={isRunning}
        summary={<PackSummary pack={pack} isRunning={isRunning} />}
      >
        <div className="activity-timeline">
          {pack.map((item) => (
            <MessageItem
              message={item}
              key={item.id}
              onImageClick={onImageClick}
              onGalleryClick={onGalleryClick}
              session={session}
            />
          ))}
        </div>
      </LazyDetails>
    </motion.section>
  )
}

export function Transcript({
  messages,
  activityBreaks,
  outbox,
  approvals = [],
  canApprove = true,
  onApproval,
  composerHeight,
  loading,
  error,
  notice,
  onRetry,
  hasMoreHistory = false,
  loadingOlder = false,
  onLoadOlder,
  onImageClick,
  onGalleryClick,
  session,
  commands = [],
  onEditLastUserMessage,
  onForkAtMessage,
  checkpoints = [],
  onOpenCheckpoint,
  highlightMessageId,
}: {
  messages: Message[]
  activityBreaks?: readonly string[]
  outbox: OutboxEntry[]
  approvals?: Approval[]
  canApprove?: boolean
  onApproval?: (approval: Approval, action: 'approve' | 'cancel') => void
  composerHeight?: number
  loading?: boolean
  error?: string
  notice?: ReactNode
  onRetry?: () => void
  hasMoreHistory?: boolean
  loadingOlder?: boolean
  onLoadOlder?: () => unknown
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allImages: string[]) => void
  session?: Session | null
  commands?: import('../../domain/types').Command[]
  onEditLastUserMessage?: (text: string) => void
  onForkAtMessage?: (message: Message, turnIndex: number) => void
  checkpoints?: SessionCheckpoint[]
  onOpenCheckpoint?: (cp: SessionCheckpoint) => void
  highlightMessageId?: string | null
}) {
  const reducedMotion = useReducedMotion(undefined, { getInitialValueInEffect: false })
  const agentKind = useAstrorderStore(state => session ? state.agents[session.agent_id]?.kind : undefined)
  const enter = reducedMotion ? {} : { opacity: 0, y: 10, scale: 0.99 }
  const visibleMessages = messages.map(normalizeMessagePresentation).filter((message) => isCompactionMessage(message) || hasMessageContent(message))
  const isActivity = (message: Message | null) => Boolean(message && !isCompactionMessage(message) && isMessageActivity(message))
  const packs = activityPacks(visibleMessages, activityBreaks)
  const checkpointByMsgId = useMemo(
    () => mapCheckpointsToMessages(visibleMessages, checkpoints),
    [visibleMessages, checkpoints],
  )
  const [activeHighlightId, setActiveHighlightId] = useState<string | null>(null)
  const highlightScrolledRef = useRef<string | null>(null)
  const highlightLoadAttemptsRef = useRef<number>(0)
  const outboundVersion = outbox.map((item) => item.command.id).join(',')
  const contentVersion = `${messages.map((item) => `${item.id}:${item.text.length}`).join(',')}|${outbox.map((item) => `${item.command.id}:${item.status}`).join(',')}|${approvals.map((item) => item.id).join(',')}|${composerHeight ?? 0}`
  const {
    setContainerRef,
    following,
    onScroll,
    scrollToBottom,
    capturePrependAnchor,
  } = useStickToBottom<HTMLDivElement>({ contentVersion, forceFollowVersion: outboundVersion })
  const [scrollerEl, setScrollerEl] = useState<HTMLDivElement | null>(null)
  const bindTranscriptRef = useCallback(
    (node: HTMLDivElement | null) => {
      setContainerRef(node)
      setScrollerEl(node)
    },
    [setContainerRef],
  )
  const older = useOlderMessages({ hasMore: hasMoreHistory, loading: loadingOlder, load: () => onLoadOlder?.(), capture: capturePrependAnchor })
  const sessionKey = session ? `${session.agent_id}::${session.id}` : ''
  useEffect(() => {
    highlightScrolledRef.current = null
    highlightLoadAttemptsRef.current = 0
  }, [highlightMessageId, sessionKey])
  useEffect(() => {
    if (!highlightMessageId || highlightScrolledRef.current === highlightMessageId) return
    const exists = visibleMessages.some((m) => m.id === highlightMessageId)
    if (exists) {
      const container = scrollerEl
      if (!container) return
      const escaped = typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(highlightMessageId) : highlightMessageId.replace(/"/g, '\\"')
      const targetEl = container.querySelector<HTMLElement>(`[data-testid="message-${escaped}"]`)
      if (!targetEl) return
      highlightScrolledRef.current = highlightMessageId
      targetEl.scrollIntoView({ behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth', block: 'center' })
      setActiveHighlightId(highlightMessageId)
      const timer = window.setTimeout(() => {
        setActiveHighlightId((current) => (current === highlightMessageId ? null : current))
      }, 2600)
      return () => window.clearTimeout(timer)
    }
    if (hasMoreHistory && !loadingOlder && onLoadOlder && highlightLoadAttemptsRef.current < 3) {
      highlightLoadAttemptsRef.current += 1
      void onLoadOlder()
    }
  }, [hasMoreHistory, highlightMessageId, loadingOlder, onLoadOlder, scrollerEl, visibleMessages])
  const liveActivityAt = useAstrorderStore((state) => (
    session ? state.liveActivityAt[scopeKey(session.agent_id, session.id)] : undefined
  ))
  const liveActive = typeof liveActivityAt === 'number' && Date.now() - liveActivityAt >= 0 && Date.now() - liveActivityAt < LIVE_ACTIVITY_MS
  const busy = session?.status === 'running' || session?.status === 'waiting_approval' || liveActive
  const lastVisibleMessage = visibleMessages[visibleMessages.length - 1]
  const isWaiting = Boolean(session?.status === 'running' && (!lastVisibleMessage || lastVisibleMessage.role === 'user'))

  const lastUserMessageId = useMemo(() => {
    for (let i = visibleMessages.length - 1; i >= 0; i--) {
      if (visibleMessages[i].role === 'user') return visibleMessages[i].id
    }
    return null
  }, [visibleMessages])
  return (
    <section className="transcript-wrap" aria-label="会话记录">
      {!following && (
        <button
          type="button"
          className="return-bottom"
            data-liquid-surface="control"
            data-liquid-snapshot=".chat-workbench"
          aria-label="回到底部"
          onClick={scrollToBottom}
          data-testid="return-bottom"
        >
          {busy ? <span className="return-bottom-wave" aria-hidden="true"><i /><i /><i /></span> : <IconArrowDown size={17} />}
        </button>
      )}
      <div
        className="transcript"
        ref={bindTranscriptRef}
        onScroll={event => { onScroll(); older.onScroll(event) }}
        onWheel={older.onWheel}
        onTouchStart={older.onTouchStart}
        onTouchMove={older.onTouchMove}
        role="log"
        aria-live="polite"
        aria-relevant="additions text"
      >
        <div className="transcript-inner">
          {hasMoreHistory && (
            <Button
              className="history-button"
              size="compact-sm"
              variant="subtle"
              loading={loadingOlder}
              onClick={() => void older.request()}
            >
              {loadingOlder ? '正在读取更早记录…' : '读取更早记录'}
            </Button>
          )}
          {loading && messages.length === 0 && <Text className="transcript-status" c="dimmed">正在读取会话记录…</Text>}
          {notice || (error && <InlineNotice className="transcript-error" title="暂时无法读取会话" alert actions={onRetry && <Button size="compact-xs" variant="default" onClick={onRetry} leftSection={<IconRefresh size={14} />}>重试</Button>}>{error}</InlineNotice>)}
          {!loading && !error && messages.length === 0 && outbox.length === 0 && (
            <Stack className="transcript-empty" align="center" gap="xs">
              <IconTool size={25} stroke={1.4} aria-hidden="true" />
              <Text c="dimmed" size="sm">这个会话还没有可显示的记录。</Text>
              <Text c="dimmed" size="xs">发送后，只有服务端确认的消息会进入正式记录。</Text>
            </Stack>
          )}
          <AnimatePresence initial={false}>
          {visibleMessages.map((message, idx) => {
            if (isCompactionMessage(message)) {
              return (
                <motion.div
                  key={`compaction-${message.id}`}
                  initial={enter}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.28, ease: [0.2, 0.8, 0.2, 1] }}
                >
                  <CompactionDivider message={message} />
                </motion.div>
              )
            }
            const prevMessage = idx > 0 ? visibleMessages[idx - 1] : null
            // 判断过程块：连续的非普通用户消息（包括思考、工具活动等过程性输出）
            // 当后续出现了最终的 assistant 回复（或会话结束生成完毕），该过程块自动收起为类似 Codex 的“用时 X 分钟 Y 秒”或“过程概要”
            if (isActivity(message)) {
              const pack = packs.get(idx)
              if (!pack) return null
              const remaining = visibleMessages.slice(idx + pack.length)
              const isLatestActivity = !remaining.some(isActivity)
              const hasLaterUserMessage = remaining.some(m => m.role === 'user' && m.kind === 'message')
              // 如果该会话仍然在运行，且该活动块之后尚未出现 assistant 的最终文本答复，则保持展开；
              // 一旦会话完成（!busy）或当前轮次已产出了最终文本回复，该活动块自动折叠收起，对齐 Codex
              const hasSubsequentFinalReply = remaining.some(m => m.role === 'assistant' && m.kind === 'message' && m.text.trim())
              const isPackRunning = Boolean(busy && isLatestActivity && !hasLaterUserMessage)
              const shouldOpen = isPackRunning && !hasSubsequentFinalReply
              const isDeferredPack = !shouldOpen && idx + pack.length < visibleMessages.length - 6
              const definitivelyEnded = Boolean(hasSubsequentFinalReply || hasLaterUserMessage)
              return (
                <ActivityPackSection
                  key={`activity-pack-${message.id}`}
                  id={message.id}
                  pack={pack}
                  isRunning={shouldOpen}
                  definitivelyEnded={definitivelyEnded}
                  isDeferred={isDeferredPack}
                  enter={enter}
                  onImageClick={onImageClick}
                  onGalleryClick={onGalleryClick}
                  session={session}
                />
              )
            }
            const currentDate = new Date(message.created_at).toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
            const prevDate = prevMessage ? new Date(prevMessage.created_at).toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }) : null
            const today = new Date().toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
            const showDateDivider = Date.parse(message.created_at) > 0 && currentDate !== prevDate && (prevDate !== null || currentDate !== today)
            const hasAssistantReplied = message.role === 'user' && visibleMessages.slice(idx + 1).some(m => m.role !== 'user')
            const matchedCmd = message.command_id
              ? commands.find(c => c.id === message.command_id)
              : commands.filter(c => c.text.trim() === message.text.trim()).sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at))[0]
            const isAgentAccepted = Boolean(hasAssistantReplied || (matchedCmd && (matchedCmd.state === 'accepted' || matchedCmd.state === 'running' || matchedCmd.state === 'completed')))
            const nextUserOffset = visibleMessages.slice(idx + 1).findIndex(item => item.role === 'user')
            const turnRemainder = visibleMessages.slice(idx + 1, nextUserOffset < 0 ? undefined : idx + 1 + nextUserOffset)
            const isFinalReply = message.role === 'assistant' && !isActivity(message)
              && !turnRemainder.some(item => item.role === 'assistant' && !isActivity(item))
              && !(busy && nextUserOffset < 0)
            const canForkTurn = isFinalReply
            const isDeferredEntry = idx < visibleMessages.length - 6
            return (
              <motion.div className={`transcript-message-entry${isDeferredEntry ? ' is-deferred-turn' : ''}${activeHighlightId === message.id ? ' is-search-highlight' : ''}`} data-turn-id={message.id} key={message.id} initial={enter} animate={{ opacity: 1, y: 0, scale: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.28, ease: [0.2, 0.8, 0.2, 1] }}>
                {showDateDivider && (
                  <div className="transcript-date-divider">
                    <span>{currentDate}</span>
                  </div>
                )}
                <MessageItem
                  message={message}
                  onImageClick={onImageClick}
                  onGalleryClick={onGalleryClick}
                  session={session}
                  isLatest={idx === visibleMessages.length - 1}
                  inlineMeta={message.role === 'assistant' && !isFinalReply}
                  showAuthorMark={message.role === 'assistant' && (isFinalReply || !prevMessage || prevMessage.role === 'user')}
                  authorLabel={agentKindLabel(agentKind)}
                  onEdit={message.id === lastUserMessageId ? onEditLastUserMessage : undefined}
                  onFork={onForkAtMessage && canForkTurn ? () => onForkAtMessage(message, idx) : undefined}
                  hasAssistantReplied={hasAssistantReplied}
                  isAgentAccepted={isAgentAccepted}
                  asyncInput={isAsyncQuestion(message) ? { commands, continued: hasLaterNativeInput(visibleMessages, idx), readOnly: session?.control_state === 'readonly' } : undefined}
                  checkpoint={checkpointByMsgId.get(message.id)}
                  onOpenCheckpoint={onOpenCheckpoint}
                />
              </motion.div>
            )
          })}
          {isWaiting && (
            <motion.div
              key="waiting-response-indicator"
              className="message-row message-assistant message-waiting-row"
              initial={{ opacity: 0, y: 8, scale: 0.98 }}
              animate={{ opacity: 1, y: 0, scale: 1 }}
              exit={{ opacity: 0, scale: 0.95 }}
              transition={{ duration: 0.22 }}
            >
              <div className="message-waiting-bubble">
                <span className="waiting-spinner">
                  <AstrorderLoader size={15} />
                </span>
                <ShinyText text="正在思考并准备回复…" speed={1.8} className="waiting-text" />
                <span className="waiting-dots" aria-hidden="true">
                  <i /><i /><i />
                </span>
              </div>
            </motion.div>
          )}
          </AnimatePresence>
          {approvals.length > 0 && (
            <section className="transcript-approvals" aria-label="对话待处理审批">
              <Stack gap="xs" mt="xs">
                {approvals.map((approval) => isUserInput(approval) ? <UserInputCard key={approval.id} approval={approval} /> : (
                  <StarBorder color="#eab308" speed="3.5s" borderRadius="var(--mantine-radius-md, 8px)" key={approval.id}>
                    <Paper className="approval-card transcript-approval-card" withBorder p="sm" radius="md" style={{ background: 'transparent' }}>
                    <Group gap={6} mb={4}>
                      <IconShieldCheck size={16} style={{ color: 'var(--astr-yellow)' }} />
                      <Text size="sm" fw={600}>{approval.title}</Text>
                    </Group>
                    {approval.detail && (
                      <div className="approval-card-detail">
                        <MarkdownContent value={approval.detail} onImageClick={onImageClick} session={session} />
                      </div>
                    )}
                    {canApprove && onApproval ? (
                      <Group mt="xs" gap="xs">
                        <button
                          className="approval-button approval-approve"
                          type="button"
                          onClick={() => onApproval(approval, 'approve')}
                        >
                          允许
                        </button>
                        <button
                          className="approval-button approval-cancel"
                          type="button"
                          onClick={() => onApproval(approval, 'cancel')}
                        >
                          取消
                        </button>
                      </Group>
                    ) : (
                      <Text size="xs" c="dimmed" mt="xs">Agent 未报告审批能力，操作已禁用。</Text>
                    )}
                  </Paper>
                  </StarBorder>
                ))}
              </Stack>
            </section>
          )}
        </div>
      </div>
      <TurnMinimap
        visibleMessages={visibleMessages}
        activityPacks={packs}
        approvals={approvals}
        scroller={scrollerEl}
      />
    </section>
  )
}
