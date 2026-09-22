import { IconArrowDown, IconPaperclip, IconRefresh, IconTool, IconVectorTriangle } from '@tabler/icons-react'
import { ActionIcon, Anchor, Button, Group, Paper, Stack, Text, Tooltip } from '@mantine/core'
import { IconCheck, IconChecks, IconCopy, IconEdit, IconShieldCheck } from '@tabler/icons-react'
import { useMemo, useState } from 'react'
import type { Approval, Attachment, Message, OutboxEntry, Session } from '../../domain/types'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { MarkdownContent } from '../../components/MarkdownContent'
import { LazyDetails } from '../../components/LazyDetails'
import { MessageBody } from '../../components/MessageBody'
import { useOlderMessages } from '../../hooks/useOlderMessages'
import { describeTool, FileChangeDiffBlock, fileChangeDiffs, PackSummary, ShellOutputBlock, ToolLineIcon, unwrapCommand } from './toolPresentation'
import { ShinyText } from '../../components/animations/ShinyText'
import { StarBorder } from '../../components/animations/StarBorder'
import { AstrorderLoader } from '../../components/AnimatedStatus'
import { artifactViewerRegistry } from '../sidecar/registry'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { useAstrorderStore } from '../../state/store'
import { AnimatePresence, motion, useReducedMotion } from 'motion/react'

function attachmentHref(attachment: Attachment): string | undefined {
  try {
    const parsed = new URL(attachment.url, window.location.origin)
    if (parsed.origin !== window.location.origin) return undefined
    return `${parsed.pathname}${parsed.search}${parsed.hash}`
  } catch {
    return undefined
  }
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
            <button
              type="button"
              className="attachment-image-link"
              key={attachment.id}
              onClick={() => onGalleryClick ? onGalleryClick(href, allImageUrls) : onImageClick?.(href)}
              aria-label={`查看图片：${attachment.name}`}
            >
              <img className="attachment-image" src={href} alt={attachment.name} loading="lazy" />
            </button>
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
  onEdit,
  hasAssistantReplied = false,
}: {
  message: Message
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allImages: string[]) => void
  session?: Session | null
  isLatest?: boolean
  onEdit?: (text: string) => void
  hasAssistantReplied?: boolean
}) {
  const isUser = message.role === 'user'
  const isActivity = message.kind !== 'message' || message.role === 'tool'
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
    const toolArgs = (message.tool?.arguments && typeof message.tool.arguments === 'object') ? (message.tool.arguments as Record<string, unknown>) : {}
    const isCommand = desc.iconKey === 'terminal' || Boolean(toolArgs.command || toolArgs.cmd)
    const hasFileDiff = fileChangeDiffs(message).length > 0
    const unwrappedCmd = unwrapCommand(String(toolArgs.command || toolArgs.cmd || ''))
    const cmdStatus = desc.isFailed ? 'failed' : desc.isRunning ? 'running' : 'success'
    const foldTitle = isCommand
      ? `${desc.isRunning ? '运行' : desc.isFailed ? '运行失败' : '已运行'} ${unwrappedCmd || desc.target || desc.fullTitle}`
      : (desc.target || desc.fullTitle)
    return (
      <article className={`message-activity message-kind-${message.kind}`} data-testid={`message-${message.id}`}>
        <LazyDetails className={`activity-fold ${desc.isFailed ? 'is-failed' : ''}`} loading={desc.isRunning} summary={
          <span className="activity-fold-summary">
            <span className="activity-icon"><ToolLineIcon icon={desc.iconKey} size={14} /></span>
            <span className="activity-title">{foldTitle}</span>
            {desc.isFailed && <span className="activity-badge is-failed">失败</span>}
            {desc.isRunning && <span className="activity-badge is-running"><ShinyText text="执行中" speed={1.5} /></span>}
          </span>
        }>
          {isThinking ? (
            message.text && <div className="activity-thinking-content"><MarkdownContent value={message.text} /></div>
          ) : isCommand ? (
            <ShellOutputBlock command={unwrappedCmd || desc.fullTitle} output={message.text} status={cmdStatus} />
          ) : hasFileDiff ? (
            <FileChangeDiffBlock message={message} />
          ) : (
            <>
              {message.text && <pre className="tool-output">{message.text}</pre>}
              {message.tool?.arguments != null && Object.keys(message.tool.arguments).length > 0 && (
                <pre className="tool-payload">{JSON.stringify(message.tool.arguments, null, 2)}</pre>
              )}
            </>
          )}
          <AttachmentList attachments={message.attachments} onImageClick={onImageClick} session={session} />
        </LazyDetails>
      </article>
    )
  }
  return (
    <article
      className={`message-row message-${message.role} message-kind-${message.kind} ${isLatest ? 'is-latest-message' : ''}`}
      data-testid={`message-${message.id}`}
      aria-label={`${messageLabel(message)}消息`}
    >
      <Paper className={`message-bubble ${isUser ? 'message-bubble-user' : isActivity ? 'message-bubble-activity' : ''}`} withBorder={!isActivity} radius="lg" p="sm">
        {message.text && (
          <MessageBody
            value={message.text}
            user={isUser}
            onImageClick={onImageClick}
            onGalleryClick={onGalleryClick}
            attachmentNames={message.attachments.filter((attachment) => attachment.media_type.startsWith('image/')).map((attachment) => attachment.name)}
            renderMarkdown={(value) => <MarkdownContent value={value} onImageClick={onImageClick} session={session} />}
          />
        )}
        {message.tool && (
          <pre className="tool-payload">{JSON.stringify(message.tool, null, 2)}</pre>
        )}
        <AttachmentList attachments={message.attachments} onImageClick={onImageClick} onGalleryClick={onGalleryClick} session={session} />
      </Paper>
      <div className="message-meta">
        {isReviewMode && (
          <span className="message-review-mode-tag" style={{ fontSize: '11px', color: 'var(--astr-muted)', userSelect: 'none' }}>
            审查模式
          </span>
        )}
        {Date.parse(message.created_at) > 0 && <time dateTime={message.created_at}>{new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</time>}
        {isUser && (
          <span
            className="message-status-ticks"
            title={hasAssistantReplied ? 'Agent 已响应' : '已发送'}
            style={{ display: 'inline-flex', alignItems: 'center', color: hasAssistantReplied ? 'var(--astr-indigo, #5b6cff)' : 'var(--astr-muted)' }}
          >
            {hasAssistantReplied ? <IconChecks size={14} /> : <IconCheck size={14} />}
          </span>
        )}
        <div className="message-actions-bar">
          {message.text && (
            <Tooltip label={copied ? '已复制' : '复制内容'} position="top" withArrow>
              <ActionIcon
                variant="subtle"
                size="xs"
                color="gray"
                onClick={handleCopy}
                aria-label="复制消息"
                className="message-action-btn"
              >
                {copied ? <IconCheck size={13} color="var(--astr-teal, #12b886)" /> : <IconCopy size={13} />}
              </ActionIcon>
            </Tooltip>
          )}
          {isUser && onEdit && (
            <Tooltip label="编辑消息" position="top" withArrow>
              <ActionIcon
                variant="subtle"
                size="xs"
                color="gray"
                onClick={() => onEdit(message.text)}
                aria-label="编辑消息"
                className="message-action-btn"
              >
                <IconEdit size={13} />
              </ActionIcon>
            </Tooltip>
          )}
        </div>
      </div>
    </article>
  )
}

export function Transcript({
  messages,
  outbox,
  approvals = [],
  canApprove = true,
  onApproval,
  composerHeight,
  loading,
  error,
  onRetry,
  hasMoreHistory = false,
  loadingOlder = false,
  onLoadOlder,
  onImageClick,
  onGalleryClick,
  session,
  onEditLastUserMessage,
}: {
  messages: Message[]
  outbox: OutboxEntry[]
  approvals?: Approval[]
  canApprove?: boolean
  onApproval?: (approval: Approval, action: 'approve' | 'cancel') => void
  composerHeight?: number
  loading?: boolean
  error?: string
  onRetry?: () => void
  hasMoreHistory?: boolean
  loadingOlder?: boolean
  onLoadOlder?: () => unknown
  onImageClick?: (url: string) => void
  onGalleryClick?: (url: string, allImages: string[]) => void
  session?: Session | null
  onEditLastUserMessage?: (text: string) => void
}) {
  const reducedMotion = useReducedMotion()
  const enter = reducedMotion ? {} : { opacity: 0, y: 10, scale: 0.99 }
  const visibleMessages = messages.filter((message) => (
    message.kind === 'thinking'
      ? message.text.trim() || message.attachments.length || message.tool
      : message.kind !== 'message' || message.role !== 'assistant' || message.text.trim() || message.attachments.length || message.tool
  ))
  const isActivity = (message: Message | null) => Boolean(message && (message.kind !== 'message' || message.role === 'tool' || (message.role === 'assistant' && !message.text.trim())))
  const outboundVersion = outbox.map((item) => item.command.id).join(',')
  const contentVersion = `${messages.map((item) => `${item.id}:${item.text.length}`).join(',')}|${outbox.map((item) => `${item.command.id}:${item.status}`).join(',')}|${approvals.map((item) => item.id).join(',')}|${composerHeight ?? 0}`
  const {
    setContainerRef,
    following,
    onScroll,
    scrollToBottom,
    capturePrependAnchor,
  } = useStickToBottom<HTMLDivElement>({ contentVersion, forceFollowVersion: outboundVersion })
  const older = useOlderMessages({ hasMore: hasMoreHistory, loading: loadingOlder, load: () => onLoadOlder?.(), capture: capturePrependAnchor })
  const busy = session?.status === 'running' || session?.status === 'waiting_approval'
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
          aria-label="回到底部"
          onClick={scrollToBottom}
          data-testid="return-bottom"
        >
          {busy ? <span className="return-bottom-wave" aria-hidden="true"><i /><i /><i /></span> : <IconArrowDown size={17} />}
        </button>
      )}
      <div
        className="transcript"
        ref={setContainerRef}
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
          {error && (
            <Paper className="transcript-error" withBorder p="sm" radius="md">
              <Group justify="space-between" gap="xs">
                <Text size="sm" c="red">{error}</Text>
                {onRetry && <Button size="compact-xs" variant="subtle" onClick={onRetry} leftSection={<IconRefresh size={14} />}>重试</Button>}
              </Group>
            </Paper>
          )}
          {!loading && !error && messages.length === 0 && outbox.length === 0 && (
            <Stack className="transcript-empty" align="center" gap="xs">
              <IconTool size={25} stroke={1.4} aria-hidden="true" />
              <Text c="dimmed" size="sm">这个会话还没有可显示的记录。</Text>
              <Text c="dimmed" size="xs">发送后，只有服务端确认的消息会进入正式记录。</Text>
            </Stack>
          )}
          <AnimatePresence initial={false}>
          {visibleMessages.map((message, idx) => {
            const prevMessage = idx > 0 ? visibleMessages[idx - 1] : null
            // 判断过程块：连续的非普通用户消息（包括思考、工具活动等过程性输出）
            // 当后续出现了最终的 assistant 回复（或会话结束生成完毕），该过程块自动收起为类似 Codex 的“用时 X 分钟 Y 秒”或“过程概要”
            if (isActivity(message)) {
              if (isActivity(prevMessage)) return null
              const pack: Message[] = []
              for (let i = idx; i < visibleMessages.length && isActivity(visibleMessages[i]); i++) pack.push(visibleMessages[i])
              const remaining = visibleMessages.slice(idx + pack.length)
              const isLatestActivity = !remaining.some(isActivity)
              const hasLaterUserMessage = remaining.some(m => m.role === 'user' && m.kind === 'message')
              // 如果该会话仍然在运行，且该活动块之后尚未出现 assistant 的最终文本答复，则保持展开；
              // 一旦会话完成（!busy）或当前轮次已产出了最终文本回复，该活动块自动折叠收起，对齐 Codex
              const hasSubsequentFinalReply = remaining.some(m => m.role === 'assistant' && m.kind === 'message' && m.text.trim())
              const isPackRunning = Boolean(busy && isLatestActivity && !hasLaterUserMessage)
              const shouldOpen = isPackRunning && !hasSubsequentFinalReply
              return (
                <motion.section
                  className="activity-pack"
                  aria-label="思考与工具"
                  key={message.id}
                  initial={enter}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.28, ease: [0.2, 0.8, 0.2, 1] }}
                >
                  <LazyDetails
                    key={`activity-pack-${message.id}-${shouldOpen}`}
                    defaultOpen={shouldOpen}
                    loading={shouldOpen}
                    summary={<PackSummary pack={pack} isRunning={shouldOpen} />}
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
            const currentDate = new Date(message.created_at).toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
            const prevDate = prevMessage ? new Date(prevMessage.created_at).toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }) : null
            const showDateDivider = Date.parse(message.created_at) > 0 && currentDate !== prevDate
            const hasAssistantReplied = message.role === 'user' && visibleMessages.slice(idx + 1).some(m => m.role !== 'user')
            return (
              <motion.div className="transcript-message-entry" key={message.id} initial={enter} animate={{ opacity: 1, y: 0, scale: 1 }} exit={{ opacity: 0 }} transition={{ duration: 0.28, ease: [0.2, 0.8, 0.2, 1] }}>
                {showDateDivider && (
                  <div style={{ textAlign: 'center', margin: '14px 0 6px', width: '100%' }}>
                    <span style={{ fontSize: '11px', color: 'var(--astr-muted)', background: 'var(--astr-surface-muted)', padding: '2px 10px', borderRadius: '10px' }}>
                      {currentDate}
                    </span>
                  </div>
                )}
                <MessageItem
                  message={message}
                  onImageClick={onImageClick}
                  onGalleryClick={onGalleryClick}
                  session={session}
                  isLatest={idx === visibleMessages.length - 1}
                  onEdit={message.id === lastUserMessageId ? onEditLastUserMessage : undefined}
                  hasAssistantReplied={hasAssistantReplied}
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
                {approvals.map((approval) => (
                  <StarBorder color="#eab308" speed="3.5s" borderRadius="var(--mantine-radius-md, 8px)" key={approval.id}>
                    <Paper className="approval-card transcript-approval-card" withBorder p="sm" radius="md" style={{ background: 'transparent' }}>
                    <Group gap={6} mb={4}>
                      <IconShieldCheck size={16} style={{ color: 'var(--astr-yellow)' }} />
                      <Text size="sm" fw={600}>{approval.title}</Text>
                    </Group>
                    {approval.detail && (
                      <div className="approval-card-detail">
                        <MarkdownContent value={approval.detail} onImageClick={onImageClick} />
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
    </section>
  )
}
