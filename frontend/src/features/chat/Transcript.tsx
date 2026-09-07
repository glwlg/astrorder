import { IconArrowDown, IconPaperclip, IconRefresh, IconTool } from '@tabler/icons-react'
import { Anchor, Badge, Button, Group, Paper, Stack, Text } from '@mantine/core'
import type { Attachment, Message, OutboxEntry } from '../../domain/types'
import { useStickToBottom } from '../../hooks/useStickToBottom'
import { MarkdownContent } from '../../components/MarkdownContent'

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
  return (
    <article
      className={`message-row message-${message.role} message-kind-${message.kind}`}
      data-testid={`message-${message.id}`}
      aria-label={`${messageLabel(message)}消息`}
    >
      <div className="message-meta">
        <span>{messageLabel(message)}</span>
        <time dateTime={message.created_at}>{new Date(message.created_at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })}</time>
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

const outboxStateLabels: Record<string, string> = {
  submitting: '提交中',
  received: '已收到',
  queued: '已排队',
  accepted: '已接受',
  running: '执行中',
  completed: '已完成',
  failed: '失败',
  unknown: '结果未知',
  cancelled: '已取消',
}

function OutboxReceipt({ entry }: { entry: OutboxEntry }) {
  const state = entry.status
  const color = state === 'failed' ? 'red' : state === 'unknown' ? 'yellow' : state === 'completed' ? 'teal' : 'indigo'
  return (
    <Paper className="outbox-receipt" withBorder radius="md" p="sm" data-testid={`outbox-${entry.command.id}`}>
      <Group justify="space-between" align="flex-start" gap="xs" wrap="nowrap">
        <div className="outbox-copy">
          <Text size="xs" fw={700}>发送记录 · {entry.command.text || '图片/附件'}</Text>
          {entry.command.attachments.length > 0 && (
            <Text size="xs" c="dimmed">{entry.command.attachments.map((item) => item.name).join('、')}</Text>
          )}
          {entry.error && <Text size="xs" c="red">{entry.error}</Text>}
        </div>
        <Badge color={color} variant="light">{outboxStateLabels[state] || state}</Badge>
      </Group>
      {state === 'unknown' && (
        <Text className="outbox-help" size="xs" c="dimmed">
          服务连接中断，未自动重发。请等待事件回放或明确再次提交。
        </Text>
      )}
    </Paper>
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
  onLoadOlder?: () => void
}) {
  const contentVersion = `${messages.map((item) => item.id).join(',')}|${outbox.map((item) => `${item.command.id}:${item.status}`).join(',')}`
  const {
    setContainerRef,
    following,
    onScroll,
    scrollToBottom,
    capturePrependAnchor,
  } = useStickToBottom<HTMLDivElement>({ contentVersion })
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
        onScroll={onScroll}
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
            onClick={() => {
              capturePrependAnchor()
              onLoadOlder?.()
            }}
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
        {messages.map((message) => <MessageItem key={message.id} message={message} />)}
        {outbox.length > 0 && (
          <div className="outbox-list" aria-label="发送记录">
            <Text className="outbox-heading" size="xs" c="dimmed">发送记录（不等同于正式消息）</Text>
            {outbox.map((entry) => <OutboxReceipt entry={entry} key={entry.command.id} />)}
          </div>
        )}
      </div>
    </section>
  )
}
