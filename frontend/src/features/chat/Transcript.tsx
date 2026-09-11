import { IconArrowDown, IconPaperclip, IconRefresh, IconTool, IconVectorTriangle } from '@tabler/icons-react'
import { Anchor, Button, Group, Paper, Stack, Text } from '@mantine/core'
import { IconShieldCheck } from '@tabler/icons-react'
import type { Approval, Attachment, Message, OutboxEntry, Session } from '../../domain/types'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { MarkdownContent } from '../../components/MarkdownContent'
import { LazyDetails } from '../../components/LazyDetails'
import { MessageBody } from '../../components/MessageBody'
import { useOlderMessages } from '../../hooks/useOlderMessages'
import { describeTool, PackSummary, ToolLineIcon } from './toolPresentation'
import { artifactViewerRegistry } from '../sidecar/registry'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { useAstrorderStore } from '../../state/store'

function attachmentHref(attachment: Attachment): string | undefined {
  try {
    const parsed = new URL(attachment.url, window.location.origin)
    if (parsed.origin !== window.location.origin) return undefined
    return `${parsed.pathname}${parsed.search}${parsed.hash}`
  } catch {
    return undefined
  }
}

function AttachmentList({
  attachments,
  onImageClick,
  session,
}: {
  attachments: Attachment[]
  onImageClick?: (url: string) => void
  session?: Session | null
}) {
  const defaultSession = useAstrorderStore((state) => Object.values(state.sessions)[0] as Session | undefined)
  const currentSession = session || defaultSession
  const openArtifact = useSidecarStore((state) => state.openArtifact)

  if (attachments.length === 0) return null
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
              onClick={() => onImageClick?.(href)}
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
  session,
}: {
  message: Message
  onImageClick?: (url: string) => void
  session?: Session | null
}) {
  const isUser = message.role === 'user'
  const isActivity = message.kind !== 'message' || message.role === 'tool'
  if (isActivity) {
    const desc = describeTool(message)
    const isThinking = message.kind === 'thinking'
    return (
      <article className={`message-activity message-kind-${message.kind}`} data-testid={`message-${message.id}`}>
        <LazyDetails className={`activity-fold ${desc.isFailed ? 'is-failed' : ''}`} summary={
          <span className="activity-fold-summary">
            <span className="activity-icon"><ToolLineIcon icon={desc.iconKey} size={14} /></span>
            <span className="activity-title">{desc.target || desc.fullTitle}</span>
            {desc.isFailed && <span className="activity-badge is-failed">失败</span>}
            {desc.isRunning && <span className="activity-badge is-running">执行中</span>}
          </span>
        }>
          {message.text && (
            isThinking ? (
              <div className="activity-thinking-content"><MarkdownContent value={message.text} /></div>
            ) : (
              <pre className="tool-output">{message.text}</pre>
            )
          )}
          {message.tool?.arguments != null && Object.keys(message.tool.arguments).length > 0 && (
            <pre className="tool-payload">{JSON.stringify(message.tool.arguments, null, 2)}</pre>
          )}
          <AttachmentList attachments={message.attachments} onImageClick={onImageClick} session={session} />
        </LazyDetails>
      </article>
    )
  }
  return (
    <article
      className={`message-row message-${message.role} message-kind-${message.kind}`}
      data-testid={`message-${message.id}`}
      aria-label={`${messageLabel(message)}消息`}
    >
      <div className="message-meta">
        {Date.parse(message.created_at) > 0 && <time dateTime={message.created_at}>{new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</time>}
      </div>
      <Paper className={`message-bubble ${isUser ? 'message-bubble-user' : isActivity ? 'message-bubble-activity' : ''}`} withBorder={!isActivity} radius="lg" p="sm">
        {message.text && (
          <MessageBody
            value={message.text}
            user={isUser}
            onImageClick={onImageClick}
            renderMarkdown={(value) => <MarkdownContent value={value} onImageClick={onImageClick} session={session} />}
          />
        )}
        {message.tool && (
          <pre className="tool-payload">{JSON.stringify(message.tool, null, 2)}</pre>
        )}
        <AttachmentList attachments={message.attachments} onImageClick={onImageClick} session={session} />
      </Paper>
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
  session,
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
  session?: Session | null
}) {
  const visibleMessages = messages.filter((message) => message.kind !== 'message' || message.role !== 'assistant' || message.text.trim() || message.attachments.length || message.tool)
  const isActivity = (message: Message | null) => Boolean(message && (message.kind !== 'message' || message.role === 'tool'))
  const contentVersion = `${messages.map((item) => item.id).join(',')}|${outbox.map((item) => `${item.command.id}:${item.status}`).join(',')}|${approvals.map((item) => item.id).join(',')}|${composerHeight ?? 0}`
  const {
    setContainerRef,
    following,
    onScroll,
    scrollToBottom,
    capturePrependAnchor,
  } = useStickToBottom<HTMLDivElement>({ contentVersion })
  const older = useOlderMessages({ hasMore: hasMoreHistory, loading: loadingOlder, load: () => onLoadOlder?.(), capture: capturePrependAnchor })
  return (
    <section className="transcript-wrap" aria-label="会话记录">
      {!following && (
        <Button
          className="return-bottom"
          size="xs"
          variant="subtle"
          color="gray"
          leftSection={<IconArrowDown size={15} />}
          onClick={scrollToBottom}
          data-testid="return-bottom"
        >
          回到底部
        </Button>
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
          {visibleMessages.map((message, idx) => {
            const prevMessage = idx > 0 ? visibleMessages[idx - 1] : null
            if (isActivity(message)) {
              if (isActivity(prevMessage)) return null
              const pack: Message[] = []
              for (let i = idx; i < visibleMessages.length && isActivity(visibleMessages[i]); i++) pack.push(visibleMessages[i])
              return <section className="activity-pack" aria-label="思考与工具" key={message.id}>
                <LazyDetails summary={<PackSummary pack={pack} />}>
                  <div className="activity-timeline">{pack.map((item) => <MessageItem message={item} key={item.id} onImageClick={onImageClick} session={session} />)}</div>
                </LazyDetails>
              </section>
            }
            const currentDate = new Date(message.created_at).toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
            const prevDate = prevMessage ? new Date(prevMessage.created_at).toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }) : null
            const showDateDivider = Date.parse(message.created_at) > 0 && currentDate !== prevDate
            return (
              <div key={message.id} style={{ display: 'contents' }}>
                {showDateDivider && (
                  <div style={{ textAlign: 'center', margin: '14px 0 6px', width: '100%' }}>
                    <span style={{ fontSize: '11px', color: 'var(--astr-muted)', background: 'var(--astr-surface-muted)', padding: '2px 10px', borderRadius: '10px' }}>
                      {currentDate}
                    </span>
                  </div>
                )}
                <MessageItem message={message} onImageClick={onImageClick} session={session} />
              </div>
            )
          })}
          {approvals.length > 0 && (
            <section className="transcript-approvals" aria-label="对话待处理审批">
              <Stack gap="xs" mt="xs">
                {approvals.map((approval) => (
                  <Paper className="approval-card transcript-approval-card" withBorder p="sm" radius="md" key={approval.id}>
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
                ))}
              </Stack>
            </section>
          )}
        </div>
      </div>
    </section>
  )
}
