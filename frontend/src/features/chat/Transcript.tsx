import { IconArrowDown, IconPaperclip, IconRefresh, IconTool } from '@tabler/icons-react'
import { Anchor, Button, Group, Paper, Stack, Text } from '@mantine/core'
import type { Attachment, Message, OutboxEntry } from '../../domain/types'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { MarkdownContent } from '../../components/MarkdownContent'
import { LazyDetails } from '../../components/LazyDetails'
import { useOlderMessages } from '../../hooks/useOlderMessages'

function attachmentHref(attachment: Attachment): string | undefined {
  try {
    const parsed = new URL(attachment.url, window.location.origin)
    if (parsed.origin !== window.location.origin) return undefined
    return `${parsed.pathname}${parsed.search}${parsed.hash}`
  } catch {
    return undefined
  }
}

function AttachmentList({ attachments }: { attachments: Attachment[] }) {
  if (attachments.length === 0) return null
  return (
    <div className="message-attachments">
      {attachments.map((attachment) => {
        const href = attachmentHref(attachment)
        const isImage = attachment.media_type.startsWith('image/') && href
        return isImage ? (
          <a className="attachment-image-link" href={href} target="_blank" rel="noreferrer" key={attachment.id}>
            <img className="attachment-image" src={href} alt={attachment.name} loading="lazy" />
          </a>
        ) : href ? (
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

function MessageItem({ message }: { message: Message }) {
  const isUser = message.role === 'user'
  const isActivity = message.kind !== 'message' || message.role === 'tool'
  if (isActivity) return (
    <article className={`message-activity message-kind-${message.kind}`} data-testid={`message-${message.id}`}>
      <LazyDetails className="activity-fold" summary={<>{messageLabel(message)}{message.tool?.status === 'running' ? ' · 运行中' : message.tool?.status === 'failed' ? ' · 失败' : ''}</>}>
        {message.text && <MarkdownContent value={message.text} />}
        {message.tool?.arguments != null && <pre className="tool-payload">{JSON.stringify(message.tool.arguments, null, 2)}</pre>}
        <AttachmentList attachments={message.attachments} />
      </LazyDetails>
    </article>
  )
  return (
    <article
      className={`message-row message-${message.role} message-kind-${message.kind}`}
      data-testid={`message-${message.id}`}
      aria-label={`${messageLabel(message)}消息`}
    >
      <div className="message-meta">
        <span>{messageLabel(message)}</span>
        {Date.parse(message.created_at) > 0 && <time dateTime={message.created_at}>{new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</time>}
      </div>
      <Paper className={`message-bubble ${isUser ? 'message-bubble-user' : isActivity ? 'message-bubble-activity' : ''}`} withBorder={!isActivity} radius="lg" p="sm">
        {message.text && <MarkdownContent value={message.text} />}
        {message.tool && (
          <pre className="tool-payload">{JSON.stringify(message.tool, null, 2)}</pre>
        )}
        <AttachmentList attachments={message.attachments} />
      </Paper>
    </article>
  )
}

export function Transcript({
  messages,
  outbox,
  loading,
  error,
  onRetry,
  hasMoreHistory = false,
  loadingOlder = false,
  onLoadOlder,
}: {
  messages: Message[]
  outbox: OutboxEntry[]
  loading?: boolean
  error?: string
  onRetry?: () => void
  hasMoreHistory?: boolean
  loadingOlder?: boolean
  onLoadOlder?: () => unknown
}) {
  const visibleMessages = messages.filter((message) => message.kind !== 'message' || message.role !== 'assistant' || message.text.trim() || message.attachments.length || message.tool)
  const isActivity = (message: Message | null) => Boolean(message && (message.kind !== 'message' || message.role === 'tool'))
  const contentVersion = `${messages.map((item) => item.id).join(',')}|${outbox.map((item) => `${item.command.id}:${item.status}`).join(',')}`
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
          variant="light"
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
              <LazyDetails summary={`思考与工具 · ${pack.length} 项`}>
                <div className="activity-timeline">{pack.map((item) => <MessageItem message={item} key={item.id} />)}</div>
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
              <MessageItem message={message} />
            </div>
          )
        })}
      </div>
    </section>
  )
}
