import { UserInputCard, isUserInput } from '../../components/UserInputCard'
import { AsyncUserInputCard, isAsyncQuestion, hasLaterNativeInput } from '../../components/AsyncUserInputCard'
import { ArtifactReviewCard } from '../../components/ArtifactReviewCard'
import { collectTurnArtifactReviews, type ArtifactReviewItem } from '../../domain/artifactReview'
import { IconArrowDown, IconPaperclip, IconRefresh, IconTool, IconVectorTriangle } from '@tabler/icons-react'
import { ActionIcon, Anchor, Button, Group, Paper, Stack, Text, Tooltip } from '@mantine/core'
import { IconCheck, IconChecks, IconCopy, IconEdit, IconGitFork, IconHistory, IconPencil, IconShieldCheck, IconX } from '@tabler/icons-react'
import type { SessionCheckpoint } from '../../api/client'
import { mapCheckpointsToMessages } from '../../domain/turnCheckpoints'
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
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
import { HandoffSummaryCard, shouldShowHandoffSummaryCard } from './HandoffSummaryCard'
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
import { motion } from 'motion/react'
import { WorkbenchMark } from '../../components/WorkbenchMark'
import { agentKindLabel } from '../../components/AgentBrandIcon'
import { ExportMenu } from '../office/export/ExportMenu'
import { useIsOfficeExperience } from '../office/appProfile'
import {
  applyTranscriptCustomHighlights,
  clearTranscriptCustomHighlights,
  collectTranscriptMatches,
  TranscriptFindBar,
} from './TranscriptFindBar'

const OFFICE_EMPTY_PROMPTS = [
  '帮我起草一份本周工作周报',
  '帮我润色一段发给客户的方案说明',
  '把讨论要点整理成带负责人的行动清单',
  '为下周评审会生成 8 页 PPT 演示大纲',
  '帮我将以下资料整理为结构化对比表',
]

const zhDateFormatter = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
const zhTimeFormatter = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit' })
const INITIAL_VISIBLE_ROWS = 60
const VISIBLE_ROW_STEP = 60
const LIVE_UNDEFERRED_TAIL = 16

function formatMessageDate(iso: string): string {
  const ts = Date.parse(iso)
  return ts > 0 ? zhDateFormatter.format(ts) : ''
}

function formatMessageTime(iso: string): string {
  const ts = Date.parse(iso)
  return ts > 0 ? zhTimeFormatter.format(ts) : ''
}

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
  const defaultSession = useAstrorderStore((state) => {
    if (session) return undefined
    for (const key in state.sessions) return state.sessions[key]
    return undefined
  })
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

const MessageItem = memo(function MessageItem({
  message,
  onImageClick,
  onGalleryClick,
  session,
  isLatest,
  inlineMeta = false,
  onEdit,
  onFork,
  forkActionLabel = '从此回复分叉新会话',
  hasAssistantReplied = false,
  isAgentAccepted = false,
  asyncInput,
  showAuthorMark = false,
  authorLabel = 'Agent',
  checkpoint,
  onOpenCheckpoint,
  isOfficeMode = false,
  forceOpenActivity = false,
}: {
  message: Message
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allImages: string[]) => void
  session?: Session | null
  isLatest?: boolean
  inlineMeta?: boolean
  onEdit?: (text: string) => void
  onFork?: () => void
  forkActionLabel?: string
  hasAssistantReplied?: boolean
  isAgentAccepted?: boolean
  asyncInput?: { commands: import('../../domain/types').Command[]; continued: boolean; readOnly: boolean }
  showAuthorMark?: boolean
  authorLabel?: string
  checkpoint?: SessionCheckpoint
  onOpenCheckpoint?: (cp: SessionCheckpoint) => void
  isOfficeMode?: boolean
  forceOpenActivity?: boolean
}) {
  const isUser = message.role === 'user'
  const isActivity = isMessageActivity(message)
  const isReviewMode = !isOfficeMode && isUser && (
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
      <article className={`message-activity message-kind-${message.kind}${forceOpenActivity ? ' is-find-active' : ''}`} data-status={state} data-testid={`message-${message.id}`}>
        <span className="activity-node" aria-hidden="true">{!isThinking && (state === 'completed' ? <IconCheck /> : state === 'failed' ? <IconX /> : null)}</span>
        <LazyDetails className={`activity-fold ${desc.isFailed ? 'is-failed' : ''}`} loading={desc.isRunning} forceOpen={forceOpenActivity} summary={
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
  const formattedTime = formatMessageTime(message.created_at)
  const metadata = (
    <span className={`message-meta ${inlineMeta ? 'message-meta-inline' : ''}`}>
      {isReviewMode && <span className="message-review-mode-tag">审查模式</span>}
      {formattedTime && <time dateTime={message.created_at}>{formattedTime}</time>}
      {isUser && <span className="message-status-ticks" title={hasAssistantReplied ? 'Agent 已响应' : isAgentAccepted ? 'Agent 已接收' : '已发送'} style={{ display: 'inline-flex', alignItems: 'center', color: hasAssistantReplied || isAgentAccepted ? 'var(--astr-indigo, #5b6cff)' : 'var(--astr-muted)' }}>
        {hasAssistantReplied || isAgentAccepted ? <IconChecks size={14} /> : <IconCheck size={14} />}
      </span>}
      <span className="message-actions-bar">
        {message.text && <Tooltip label={copied ? '已复制' : '复制内容'} position="top" withArrow>
          <ActionIcon variant="subtle" size="xs" color="gray" onClick={handleCopy} aria-label="复制消息" className="message-action-btn">
            {copied ? <IconCheck size={13} color="var(--astr-teal, #12b886)" /> : <IconCopy size={13} />}
          </ActionIcon>
        </Tooltip>}
        {!isUser && message.text && message.text.trim().length >= 32 && (
          <Tooltip label="在富文档写作台中继续编辑并导出 Word / PPT" position="top" withArrow>
            <ActionIcon
              variant="subtle"
              size="xs"
              color="gray"
              aria-label="在写作台编辑"
              className="message-action-btn"
              onClick={() => {
                const firstHeading = message.text
                  .split('\n')
                  .map((l) => l.trim())
                  .find((l) => l.startsWith('#'))
                  ?.replace(/^#+\s*/, '')
                  .trim()
                const baseTitle = firstHeading || session?.title || 'AI 成品文档'
                const safeName = `${baseTitle.replace(/[<>:"/\\|?*]+/g, '_').slice(0, 40) || 'AI_成品文档'}.md`
                useSidecarStore.getState().openArtifact(
                  {
                    id: `canvas:${message.id}`,
                    sessionId: session?.id || message.session_id,
                    agentId: session?.agent_id || message.agent_id,
                    connectionId: session?.connection_id || undefined,
                    name: safeName,
                    kind: 'workspace_file',
                    readUrl: '',
                    mediaType: 'text/markdown',
                    writable: false,
                    metadata: {
                      inlineContent: message.text,
                      inlineEditable: true,
                      preferRich: true,
                    },
                  },
                  'monaco-viewer',
                )
              }}
            >
              <IconPencil size={13} />
            </ActionIcon>
          </Tooltip>
        )}
        {message.text && (
          <ExportMenu
            markdown={message.text}
            title={session?.title || ''}
            fileName={session?.title || 'document'}
            variant="icon"
            workspace={session?.workspace || undefined}
            sessionId={session?.id}
            connectionId={session?.connection_id}
          />
        )}
        {!isUser && onFork && <Tooltip label={forkActionLabel} position="top" withArrow>
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
        {!isOfficeMode && message.tool && (
          <pre className="tool-payload">{JSON.stringify(message.tool, null, 2)}</pre>
        )}
        <AttachmentList attachments={message.attachments} onImageClick={onImageClick} onGalleryClick={onGalleryClick} session={session} />
        {inlineMeta && metadata}
      </Paper>
      {!inlineMeta && metadata}
    </article>
  )
})

const ACTIVITY_PACK_COLLAPSE_GRACE_MS = 4_500

const ActivityPackSection = memo(function ActivityPackSection({
  id,
  pack,
  isRunning,
  definitivelyEnded,
  isDeferred,
  onImageClick,
  onGalleryClick,
  session,
  activeFindMessageId = null,
}: {
  id: string
  pack: Message[]
  isRunning: boolean
  definitivelyEnded: boolean
  isDeferred: boolean
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allImages: string[]) => void
  session?: Session | null
  activeFindMessageId?: string | null
}) {
  const [autoOpen, setAutoOpen] = useState(isRunning)
  const packSize = pack.length
  const hasFindMatch = Boolean(activeFindMessageId)

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
    <section
      className={`activity-pack${isDeferred && !hasFindMatch ? ' is-deferred-turn' : ''}${hasFindMatch ? ' is-find-active' : ''}`}
      aria-label="思考与工具"
      data-turn-id={id}
    >
      <LazyDetails
        autoOpen={autoOpen}
        forceOpen={hasFindMatch}
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
              forceOpenActivity={activeFindMessageId === item.id}
            />
          ))}
        </div>
      </LazyDetails>
    </section>
  )
})

type VisualRow =
  | { kind: 'compaction'; id: string; message: Message }
  | { kind: 'pack'; id: string; pack: Message[]; shouldOpen: boolean; definitivelyEnded: boolean; isDeferred: boolean }
  | {
      kind: 'artifact'
      id: string
      artifact: ArtifactReviewItem
      continued: boolean
      turnRunning: boolean
      turnEndAt: string
      isDeferred: boolean
    }
  | {
      kind: 'message'
      id: string
      idx: number
      message: Message
      dateDivider: string | null
      hasAssistantReplied: boolean
      isAgentAccepted: boolean
      isFinalReply: boolean
      showAuthorMark: boolean
      isLatest: boolean
      isDeferred: boolean
      continuedNativeInput: boolean
    }

export const Transcript = memo(function Transcript({
  messages,
  activityBreaks,
  outbox,
  approvals = [],
  canApprove = true,
  onApproval,
  composerHeight,
  loading,
  historyResolved = true,
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
  forkActionLabel,
  checkpoints,
  onOpenCheckpoint,
  highlightMessageId,
  enableFind = false,
}: {
  messages: Message[]
  activityBreaks?: readonly string[]
  outbox: OutboxEntry[]
  approvals?: Approval[]
  canApprove?: boolean
  onApproval?: (approval: Approval, action: 'approve' | 'cancel') => void
  composerHeight?: number
  loading?: boolean
  historyResolved?: boolean
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
  forkActionLabel?: string
  checkpoints?: SessionCheckpoint[]
  onOpenCheckpoint?: (cp: SessionCheckpoint) => void
  highlightMessageId?: string | null
  enableFind?: boolean
}) {
  const _reducedMotion = useReducedMotion(undefined, { getInitialValueInEffect: false })
  void _reducedMotion
  const agentKind = useAstrorderStore(state => session ? state.agents[session.agent_id]?.kind : undefined)
  const isOfficeMode = useIsOfficeExperience(session)
  const authorLabel = isOfficeMode ? 'AI 助手' : agentKindLabel(agentKind)
  const liveActivityAt = useAstrorderStore((state) => (
    session ? state.liveActivityAt[scopeKey(session.agent_id, session.id)] : undefined
  ))
  const liveActive = typeof liveActivityAt === 'number' && Date.now() - liveActivityAt >= 0 && Date.now() - liveActivityAt < LIVE_ACTIVITY_MS
  const busy = session?.status === 'running' || session?.status === 'waiting_approval' || liveActive

  const visibleMessages = useMemo(
    () => messages.map(normalizeMessagePresentation).filter((message) => isCompactionMessage(message) || hasMessageContent(message)),
    [messages],
  )
  const packs = useMemo(() => activityPacks(visibleMessages, activityBreaks), [visibleMessages, activityBreaks])
  const turnArtifacts = useMemo(
    () => collectTurnArtifactReviews(visibleMessages, packs, { busy }),
    [busy, packs, visibleMessages],
  )
  const checkpointByMsgId = useMemo(
    () => mapCheckpointsToMessages(visibleMessages, checkpoints ?? []),
    [visibleMessages, checkpoints],
  )

  const lastUserMessageId = useMemo(() => {
    for (let i = visibleMessages.length - 1; i >= 0; i--) {
      if (visibleMessages[i].role === 'user') return visibleMessages[i].id
    }
    return null
  }, [visibleMessages])

  // Freeze the deferred-turn cutoff once initial history loads so live sends/streaming
  // never toggle content-visibility:auto onto previously rendered turns (which would
  // collapse their off-screen height from real px to contain-intrinsic-size and jump scroll).
  const [deferredCutoff, setDeferredCutoff] = useState<number | null>(null)
  const effectiveDeferredCutoff =
    deferredCutoff === null
      ? Math.max(0, visibleMessages.length - LIVE_UNDEFERRED_TAIL)
      : deferredCutoff
  useEffect(() => {
    if (deferredCutoff === null && !loading && visibleMessages.length > 0) {
      setDeferredCutoff(Math.max(0, visibleMessages.length - LIVE_UNDEFERRED_TAIL))
    }
  }, [deferredCutoff, loading, visibleMessages.length])

  const visualRows = useMemo<VisualRow[]>(() => {
    const n = visibleMessages.length
    if (n === 0) return []
    const isAct = (m: Message) => !isCompactionMessage(m) && isMessageActivity(m)

    // Precompute O(N) suffix flags in a single reverse pass instead of O(N^2) slices.
    const hasLaterActivity = new Uint8Array(n)
    const hasLaterUser = new Uint8Array(n)
    const hasLaterNonUser = new Uint8Array(n)
    const hasLaterFinalText = new Uint8Array(n)
    const hasLaterAssistantInTurn = new Uint8Array(n)

    let seenActivity = false
    let seenUser = false
    let seenNonUser = false
    let seenFinalText = false
    let seenAssistantBeforeNextUser = false
    for (let i = n - 1; i >= 0; i--) {
      hasLaterActivity[i] = seenActivity ? 1 : 0
      hasLaterUser[i] = seenUser ? 1 : 0
      hasLaterNonUser[i] = seenNonUser ? 1 : 0
      hasLaterFinalText[i] = seenFinalText ? 1 : 0
      hasLaterAssistantInTurn[i] = seenAssistantBeforeNextUser ? 1 : 0

      const m = visibleMessages[i]
      if (isAct(m)) seenActivity = true
      if (m.role === 'user') {
        seenAssistantBeforeNextUser = false
        if (m.kind === 'message') seenUser = true
      } else {
        seenNonUser = true
        if (m.role === 'assistant' && !isAct(m)) {
          seenAssistantBeforeNextUser = true
          if (m.kind === 'message' && m.text.trim()) seenFinalText = true
        }
      }
    }

    // Index commands once per render in O(C) instead of scanning commands per user message.
    const commandById = new Map<string, import('../../domain/types').Command>()
    const latestCommandByText = new Map<string, { state: string; ts: number }>()
    for (const cmd of commands) {
      commandById.set(cmd.id, cmd)
      const trimmed = cmd.text.trim()
      if (!trimmed) continue
      const ts = Date.parse(cmd.created_at) || 0
      const prev = latestCommandByText.get(trimmed)
      if (!prev || ts > prev.ts) {
        latestCommandByText.set(trimmed, { state: cmd.state, ts })
      }
    }

    const today = zhDateFormatter.format(Date.now())
    const rows: VisualRow[] = []
    let prevDate: string | null = null

    for (let idx = 0; idx < n; idx++) {
      const message = visibleMessages[idx]
      if (isCompactionMessage(message)) {
        rows.push({ kind: 'compaction', id: message.id, message })
        continue
      }
      if (isAct(message)) {
        const pack = packs.get(idx)
        if (!pack) continue
        const endIdx = idx + pack.length - 1
        const isLatestActivity = !hasLaterActivity[endIdx]
        const laterUser = Boolean(hasLaterUser[endIdx])
        const laterFinalReply = Boolean(hasLaterFinalText[endIdx])
        const isPackRunning = Boolean(busy && isLatestActivity && !laterUser)
        const shouldOpen = isPackRunning && !laterFinalReply
        const definitivelyEnded = laterFinalReply || laterUser
        const isDeferred = !shouldOpen && idx + pack.length < effectiveDeferredCutoff
        rows.push({ kind: 'pack', id: message.id, pack, shouldOpen, definitivelyEnded, isDeferred })
        const packArtifacts = turnArtifacts.byPackStart.get(idx)
        if (packArtifacts) {
          for (const entry of packArtifacts) {
            rows.push({
              kind: 'artifact',
              id: `artifact:${entry.artifact.messageId}:${entry.artifact.path || entry.artifact.sourceIndex}`,
              artifact: entry.artifact,
              continued: entry.continued,
              turnRunning: entry.turnRunning,
              turnEndAt: entry.turnEndAt,
              isDeferred,
            })
          }
        }
        continue
      }

      const prevMessage = idx > 0 ? visibleMessages[idx - 1] : null
      const currentDate = formatMessageDate(message.created_at)
      const prevMsgDate = prevMessage ? formatMessageDate(prevMessage.created_at) : null
      const showDateDivider = Boolean(currentDate) && currentDate !== prevMsgDate && (prevMsgDate !== null || currentDate !== today)
      prevDate = currentDate || prevDate

      const hasAssistantReplied = message.role === 'user' && Boolean(hasLaterNonUser[idx])
      let isAgentAccepted = hasAssistantReplied
      if (!isAgentAccepted && message.role === 'user') {
        const matchedState = message.command_id
          ? commandById.get(message.command_id)?.state
          : latestCommandByText.get(message.text.trim())?.state
        isAgentAccepted = matchedState === 'accepted' || matchedState === 'running' || matchedState === 'completed'
      }

      const isFinalReply = message.role === 'assistant'
        && !hasLaterAssistantInTurn[idx]
        && !(busy && !hasLaterUser[idx])
      const showAuthorMark = message.role === 'assistant' && (isFinalReply || !prevMessage || prevMessage.role === 'user')
      const continuedNativeInput = isAsyncQuestion(message) ? hasLaterNativeInput(visibleMessages, idx) : false

      rows.push({
        kind: 'message',
        id: message.id,
        idx,
        message,
        dateDivider: showDateDivider ? currentDate : null,
        hasAssistantReplied,
        isAgentAccepted,
        isFinalReply,
        showAuthorMark,
        isLatest: idx === n - 1,
        isDeferred: idx < effectiveDeferredCutoff,
        continuedNativeInput,
      })
      const msgArtifacts = turnArtifacts.byMessageIndex.get(idx)
      if (msgArtifacts) {
        for (const entry of msgArtifacts) {
          rows.push({
            kind: 'artifact',
            id: `artifact:${entry.artifact.messageId}:${entry.artifact.path || entry.artifact.sourceIndex}`,
            artifact: entry.artifact,
            continued: entry.continued,
            turnRunning: entry.turnRunning,
            turnEndAt: entry.turnEndAt,
            isDeferred: idx < effectiveDeferredCutoff,
          })
        }
      }
    }
    return rows
  }, [busy, commands, effectiveDeferredCutoff, packs, turnArtifacts, visibleMessages])

  // Anchor the initial hidden row slice once history finishes loading so appending new turns
  // at the bottom never evicts rows from the top of the DOM (which causes upward scroll jumps).
  const [hiddenStart, setHiddenStart] = useState<number | null>(null)
  const [activeHighlightId, setActiveHighlightId] = useState<string | null>(null)
  const highlightScrolledRef = useRef<string | null>(null)
  const highlightLoadAttemptsRef = useRef<number>(0)

  // In-session Ctrl+F find state
  const [findOpen, setFindOpen] = useState(false)
  const [findQuery, setFindQuery] = useState('')
  const [findCaseSensitive, setFindCaseSensitive] = useState(false)
  const [findActiveIdx, setFindActiveIdx] = useState(0)
  const [findFocusSignal, setFindFocusSignal] = useState(0)
  const [findNavSeq, setFindNavSeq] = useState(0)
  const prevFindFocusRef = useRef<HTMLElement | null>(null)

  const findMatches = useMemo(
    () => (findOpen ? collectTranscriptMatches(visualRows, findQuery, findCaseSensitive) : []),
    [findCaseSensitive, findOpen, findQuery, visualRows],
  )
  const safeFindIndex = findMatches.length > 0 ? ((findActiveIdx % findMatches.length) + findMatches.length) % findMatches.length : 0
  const activeFindHit = findMatches[safeFindIndex] || null

  const openFindBar = useCallback(() => {
    const active = document.activeElement
    if (active instanceof HTMLElement && !active.closest('.transcript-find-bar')) {
      prevFindFocusRef.current = active
    }
    const sel = window.getSelection()?.toString().trim() || ''
    if (sel && sel.length <= 80 && !sel.includes('\n')) {
      setFindQuery(sel)
      setFindActiveIdx(0)
    }
    setFindOpen(true)
    setFindFocusSignal((n) => n + 1)
    setFindNavSeq((n) => n + 1)
  }, [])

  const closeFindBar = useCallback(() => {
    setFindOpen(false)
    clearTranscriptCustomHighlights()
    const prev = prevFindFocusRef.current
    prevFindFocusRef.current = null
    if (prev && document.contains(prev)) {
      prev.focus()
    }
  }, [])

  useEffect(() => {
    if (!enableFind) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.altKey || event.shiftKey) return
      if (!(event.ctrlKey || event.metaKey) || event.key.toLowerCase() !== 'f') return
      const target = event.target instanceof Element ? event.target : null
      if (target?.closest('.monaco-editor, .xterm, .agent-graph-root, #session-sidecar')) return
      if (document.querySelector('[role="dialog"], [role="alertdialog"]')) return
      event.preventDefault()
      event.stopPropagation()
      openFindBar()
    }
    const onOpenFindEvent = () => openFindBar()
    window.addEventListener('keydown', onKeyDown, { capture: true })
    window.addEventListener('astrorder:open-transcript-find', onOpenFindEvent)
    return () => {
      window.removeEventListener('keydown', onKeyDown, { capture: true })
      window.removeEventListener('astrorder:open-transcript-find', onOpenFindEvent)
      clearTranscriptCustomHighlights()
    }
  }, [enableFind, openFindBar])

  const hiddenRowCount =
    hiddenStart === null
      ? Math.max(0, visualRows.length - INITIAL_VISIBLE_ROWS)
      : Math.min(hiddenStart, Math.max(0, visualRows.length - 1))
  useEffect(() => {
    if (hiddenStart === null && !loading && visualRows.length > 0) {
      setHiddenStart(Math.max(0, visualRows.length - INITIAL_VISIBLE_ROWS))
    }
  }, [hiddenStart, loading, visualRows.length])
  const renderedRows = useMemo(
    () => (hiddenRowCount > 0 ? visualRows.slice(hiddenRowCount) : visualRows),
    [hiddenRowCount, visualRows],
  )
  const renderedMessages = useMemo(() => {
    if (hiddenRowCount === 0) return visibleMessages
    const out: Message[] = []
    for (const row of renderedRows) {
      if (row.kind === 'pack') out.push(...row.pack)
      else if (row.kind === 'artifact') continue
      else out.push(row.message)
    }
    return out
  }, [hiddenRowCount, renderedRows, visibleMessages])

  const lastMessage = visibleMessages[visibleMessages.length - 1]
  const isWaiting = Boolean(session?.status === 'running' && (!lastMessage || lastMessage.role === 'user'))
  const seenOutboxIdsRef = useRef<Set<string>>(new Set())
  const sendFollowVersionRef = useRef(0)
  for (const item of outbox) {
    if (!seenOutboxIdsRef.current.has(item.command.id)) {
      seenOutboxIdsRef.current.add(item.command.id)
      sendFollowVersionRef.current += 1
    }
  }
  const sendFollowVersion = sendFollowVersionRef.current
  const lastToolSig = lastMessage?.tool
    ? `${lastMessage.tool.status ?? ''}:${typeof lastMessage.tool.output === 'string' ? lastMessage.tool.output.length : 0}`
    : ''
  const contentVersion = `${visibleMessages.length}:${visualRows.length}:${lastMessage?.id ?? ''}:${lastMessage?.text.length ?? 0}:${lastToolSig}:${isWaiting ? 1 : 0}|${outbox.map((item) => `${item.command.id}:${item.status}`).join(',')}|${approvals.map((item) => item.id).join(',')}|${composerHeight ?? 0}|${hiddenRowCount}`
  const {
    setContainerRef,
    setContentRef,
    following,
    onScroll,
    scrollToBottom,
    capturePrependAnchor,
    pauseFollow,
  } = useStickToBottom<HTMLDivElement>({ contentVersion, forceFollowVersion: sendFollowVersion })
  const [scrollerEl, setScrollerEl] = useState<HTMLDivElement | null>(null)
  const bindTranscriptRef = useCallback(
    (node: HTMLDivElement | null) => {
      setContainerRef(node)
      setScrollerEl(node)
    },
    [setContainerRef],
  )

  const sessionKey = session ? `${session.agent_id}::${session.id}` : ''
  const followingRef = useRef(following)
  followingRef.current = following
  useEffect(() => {
    if (!sessionKey) return
    const onComposerHeight = (event: Event) => {
      const detail = (event as CustomEvent<{ key?: string }>).detail
      if (detail?.key === sessionKey && followingRef.current) {
        scrollToBottom()
      }
    }
    const onSessionSubmitted = (event: Event) => {
      const key = (event as CustomEvent<string>).detail
      if (key === sessionKey) {
        scrollToBottom()
      }
    }
    window.addEventListener('astrorder:composer-height', onComposerHeight)
    window.addEventListener('astrorder:session-submitted', onSessionSubmitted)
    return () => {
      window.removeEventListener('astrorder:composer-height', onComposerHeight)
      window.removeEventListener('astrorder:session-submitted', onSessionSubmitted)
    }
  }, [scrollToBottom, sessionKey])

  const older = useOlderMessages({
    hasMore: hiddenRowCount > 0 || hasMoreHistory,
    loading: loadingOlder,
    load: () => {
      if (hiddenRowCount > 0) {
        capturePrependAnchor()
        setHiddenStart((current) => Math.max(0, (current ?? hiddenRowCount) - VISIBLE_ROW_STEP))
        return
      }
      return onLoadOlder?.()
    },
    capture: capturePrependAnchor,
  })

  useEffect(() => {
    highlightScrolledRef.current = null
    highlightLoadAttemptsRef.current = 0
  }, [highlightMessageId, sessionKey])

  useEffect(() => {
    if (!highlightMessageId || highlightScrolledRef.current === highlightMessageId) return
    const targetRowIdx = visualRows.findIndex((row) =>
      row.kind === 'pack'
        ? row.pack.some((m) => m.id === highlightMessageId)
        : row.kind === 'artifact'
          ? false
          : row.message.id === highlightMessageId,
    )
    if (targetRowIdx >= 0) {
      if (targetRowIdx < hiddenRowCount) {
        setHiddenStart(Math.max(0, targetRowIdx - 10))
        return
      }
      const container = scrollerEl
      if (!container) return
      const selector = `[data-testid="message-${typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(highlightMessageId) : highlightMessageId.replace(/"/g, '\\"')}"]`
      const targetEl = container.querySelector<HTMLElement>(selector)
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
  }, [hasMoreHistory, hiddenRowCount, highlightMessageId, loadingOlder, onLoadOlder, scrollerEl, visualRows])

  // Auto-expand folded rows & collapsed activity packs when navigating to a Ctrl+F hit,
  // pause auto-follow so live streaming cannot snap back to bottom, apply CSS Custom
  // Highlights, and scroll the exact matched range into the viewport center.
  useEffect(() => {
    if (!findOpen || !findQuery.trim()) {
      clearTranscriptCustomHighlights()
      return
    }
    if (activeFindHit) {
      pauseFollow()
    }
    if (activeFindHit && activeFindHit.rowIndex < hiddenRowCount) {
      setHiddenStart(Math.max(0, activeFindHit.rowIndex - 10))
      return
    }
    if (!scrollerEl) return
    const inner = scrollerEl.querySelector<HTMLElement>('.transcript-inner')
    if (!inner) return

    let frame2 = 0
    const locateAndScroll = () => {
      const activeRange = applyTranscriptCustomHighlights(inner, findQuery, findCaseSensitive, activeFindHit)
      if (!activeFindHit) return
      pauseFollow()
      const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches
      const behavior: ScrollBehavior = reduced ? 'auto' : 'smooth'
      const scrollerRect = scrollerEl.getBoundingClientRect()

      if (
        activeRange &&
        typeof activeRange.getBoundingClientRect === 'function' &&
        typeof scrollerEl.scrollTo === 'function' &&
        scrollerRect.height > 0
      ) {
        const rangeRect = activeRange.getBoundingClientRect()
        if (rangeRect.height > 0 || rangeRect.width > 0) {
          const targetTop =
            scrollerEl.scrollTop + (rangeRect.top - scrollerRect.top) - scrollerRect.height * 0.38
          scrollerEl.scrollTo({
            top: Math.max(0, targetTop),
            behavior,
          })
          return
        }
      }

      const escapeCss = (id: string) =>
        typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(id) : id.replace(/"/g, '\\"')
      const escaped = escapeCss(activeFindHit.messageId)
      const escapedPack = activeFindHit.packId ? escapeCss(activeFindHit.packId) : ''
      const msgEl =
        scrollerEl.querySelector<HTMLElement>(`[data-turn-id="${escaped}"], [data-testid="message-${escaped}"]`) ||
        (escapedPack ? scrollerEl.querySelector<HTMLElement>(`[data-turn-id="${escapedPack}"]`) : null)
      if (!msgEl) return

      if (typeof scrollerEl.scrollTo === 'function' && scrollerRect.height > 0) {
        const msgRect = msgEl.getBoundingClientRect()
        if (msgRect.height > 0) {
          const targetTop =
            scrollerEl.scrollTop + (msgRect.top - scrollerRect.top) - scrollerRect.height * 0.38
          scrollerEl.scrollTo({
            top: Math.max(0, targetTop),
            behavior,
          })
          return
        }
      }
      msgEl.scrollIntoView({ behavior, block: 'center' })
    }

    // Two-frame settle guarantees collapsed LazyDetails mounts and content-visibility: visible
    // layout pass complete before measuring getBoundingClientRect().
    const frame1 = window.requestAnimationFrame(() => {
      frame2 = window.requestAnimationFrame(locateAndScroll)
    })
    return () => {
      window.cancelAnimationFrame(frame1)
      if (frame2) window.cancelAnimationFrame(frame2)
    }
  }, [activeFindHit, findCaseSensitive, findNavSeq, findOpen, findQuery, hiddenRowCount, pauseFollow, scrollerEl, visualRows.length])

  const forkHandlerRef = useRef(onForkAtMessage)
  forkHandlerRef.current = onForkAtMessage
  const forkCallbackMapRef = useRef(new Map<string, () => void>())
  const getForkCallback = useCallback((message: Message, idx: number) => {
    const key = `${message.id}:${idx}`
    let fn = forkCallbackMapRef.current.get(key)
    if (!fn) {
      fn = () => forkHandlerRef.current?.(message, idx)
      forkCallbackMapRef.current.set(key, fn)
    }
    return fn
  }, [])
  const showHandoffCard = shouldShowHandoffSummaryCard({
    session,
    loading,
    historyResolved,
    hasMoreHistory,
    hiddenRowCount,
    messages: visibleMessages,
  })
  return (
    <section className="transcript-wrap" aria-label="会话记录">
      {enableFind && (
        <TranscriptFindBar
          opened={findOpen}
          query={findQuery}
          caseSensitive={findCaseSensitive}
          activeIndex={safeFindIndex}
          totalMatches={findMatches.length}
          hasMoreHistory={hasMoreHistory}
          loadingOlder={loadingOlder}
          focusSignal={findFocusSignal}
          onQueryChange={(val) => {
            setFindQuery(val)
            setFindActiveIdx(0)
            setFindNavSeq((n) => n + 1)
          }}
          onToggleCaseSensitive={() => {
            setFindCaseSensitive((v) => !v)
            setFindActiveIdx(0)
            setFindNavSeq((n) => n + 1)
          }}
          onPrev={() => {
            setFindActiveIdx((i) => (findMatches.length > 0 ? (i - 1 + findMatches.length) % findMatches.length : 0))
            setFindNavSeq((n) => n + 1)
          }}
          onNext={() => {
            setFindActiveIdx((i) => (findMatches.length > 0 ? (i + 1) % findMatches.length : 0))
            setFindNavSeq((n) => n + 1)
          }}
          onLoadOlder={onLoadOlder ? () => void older.request() : undefined}
          onClose={closeFindBar}
        />
      )}
      {!following && (
        <button
          type="button"
          className="return-bottom"
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
        <div className="transcript-inner" ref={setContentRef}>
          {showHandoffCard && session && (
            <HandoffSummaryCard
              key={`handoff:${session.agent_id}:${session.id}`}
              session={session}
              renderMarkdown={(summary) => (
                <MarkdownContent value={summary} onImageClick={onImageClick} session={session} />
              )}
            />
          )}
          {hiddenRowCount > 0 ? (
            <Group justify="center" gap="xs" className="history-window-controls">
              <Button
                className="history-button"
                size="compact-sm"
                variant="subtle"
                onClick={() => {
                  capturePrependAnchor()
                  setHiddenStart((current) => Math.max(0, (current ?? hiddenRowCount) - VISIBLE_ROW_STEP))
                }}
              >
                {`显示更早 ${Math.min(hiddenRowCount, VISIBLE_ROW_STEP)} 条记录（共折叠 ${hiddenRowCount} 条）`}
              </Button>
              {hiddenRowCount > VISIBLE_ROW_STEP && (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="gray"
                  onClick={() => {
                    capturePrependAnchor()
                    setHiddenStart(0)
                  }}
                >
                  展开全部
                </Button>
              )}
            </Group>
          ) : hasMoreHistory && (
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
              {isOfficeMode && !session?.handoff_context?.trim() ? (
                <>
                  <Text fw={600} size="sm">开始新的办公对话</Text>
                  <Text c="dimmed" size="xs">选择一个常见场景，或直接在下方输入你的工作需求</Text>
                  <Group justify="center" gap={6} mt={6} maw={560}>
                    {OFFICE_EMPTY_PROMPTS.map((starter) => (
                      <Button
                        key={starter}
                        size="compact-xs"
                        variant="default"
                        radius="xl"
                        onClick={() => {
                          window.dispatchEvent(
                            new CustomEvent('astrorder:quote-to-composer', {
                              detail: { agentId: session?.agent_id, sessionId: session?.id, text: starter },
                            }),
                          )
                        }}
                      >
                        {starter}
                      </Button>
                    ))}
                  </Group>
                </>
              ) : (
                <>
                  <IconTool size={25} stroke={1.4} aria-hidden="true" />
                  {session?.handoff_context?.trim() ? (
                    <>
                      <Text c="dimmed" size="sm">会话已转交就绪</Text>
                      <Text c="dimmed" size="xs">可展开上方「交接摘要」查看背景；发送首条消息时会自动附带给当前 Agent。</Text>
                    </>
                  ) : (
                    <>
                      <Text c="dimmed" size="sm">这个会话还没有可显示的记录。</Text>
                      <Text c="dimmed" size="xs">发送后，只有服务端确认的消息会进入正式记录。</Text>
                    </>
                  )}
                </>
              )}
            </Stack>
          )}
          {renderedRows.map((row) => {
            if (row.kind === 'compaction') {
              return (
                <div key={`compaction-${row.id}`}>
                  <CompactionDivider message={row.message} />
                </div>
              )
            }
            if (row.kind === 'pack') {
              const packActiveMsgId = findOpen && activeFindHit?.packId === row.id ? activeFindHit.messageId : null
              return (
                <ActivityPackSection
                  key={`activity-pack-${row.id}`}
                  id={row.id}
                  pack={row.pack}
                  isRunning={row.shouldOpen}
                  definitivelyEnded={row.definitivelyEnded}
                  isDeferred={row.isDeferred && !packActiveMsgId}
                  onImageClick={onImageClick}
                  onGalleryClick={onGalleryClick}
                  session={session}
                  activeFindMessageId={packActiveMsgId}
                />
              )
            }
            if (row.kind === 'artifact') {
              return (
                <div
                  key={row.id}
                  className={`transcript-artifact-entry${row.isDeferred ? ' is-deferred-turn' : ''}`}
                >
                  <ArtifactReviewCard
                    artifact={row.artifact}
                    session={session}
                    commands={commands}
                    continued={row.continued}
                    turnRunning={row.turnRunning}
                    turnEndAt={row.turnEndAt}
                    readOnly={session?.control_state === 'readonly'}
                  />
                </div>
              )
            }
            const { message, idx } = row
            const isFindActiveMsg = Boolean(findOpen && activeFindHit?.messageId === message.id)
            return (
              <div className={`transcript-message-entry${row.isDeferred && !isFindActiveMsg ? ' is-deferred-turn' : ''}${activeHighlightId === message.id ? ' is-search-highlight' : ''}${isFindActiveMsg ? ' is-find-active' : ''}`} data-turn-id={message.id} key={message.id}>
                {row.dateDivider && (
                  <div className="transcript-date-divider">
                    <span>{row.dateDivider}</span>
                  </div>
                )}
                <MessageItem
                  message={message}
                  onImageClick={onImageClick}
                  onGalleryClick={onGalleryClick}
                  session={session}
                  isLatest={row.isLatest}
                  inlineMeta={message.role === 'assistant' && !row.isFinalReply}
                  showAuthorMark={row.showAuthorMark}
                  authorLabel={authorLabel}
                  onEdit={message.id === lastUserMessageId ? onEditLastUserMessage : undefined}
                  onFork={onForkAtMessage && row.isFinalReply ? getForkCallback(message, idx) : undefined}
                  forkActionLabel={forkActionLabel}
                  hasAssistantReplied={row.hasAssistantReplied}
                  isAgentAccepted={row.isAgentAccepted}
                  asyncInput={isAsyncQuestion(message) ? { commands, continued: row.continuedNativeInput, readOnly: session?.control_state === 'readonly' } : undefined}
                  checkpoint={checkpointByMsgId.get(message.id)}
                  onOpenCheckpoint={onOpenCheckpoint}
                  isOfficeMode={isOfficeMode}
                />
              </div>
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
        visibleMessages={renderedMessages}
        activityPacks={packs}
        approvals={approvals}
        scroller={scrollerEl}
      />
    </section>
  )
})
