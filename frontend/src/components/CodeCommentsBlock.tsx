import { HoverCard, Text } from '@mantine/core'
import { IconMessage } from '@tabler/icons-react'
import './CodeCommentsBlock.css'

export interface CodeCommentItem {
  title: string
  body: string
  file: string
  start?: number
  end?: number
  priority?: number
}

function cleanFilePath(file: string): string {
  return file
    .replace(/^<\s*\/?>\s*/, '')
    .replace(/\s*\[(?:代码|code)\]\s*$/, '')
    .replace(/[`'"]/g, '')
    .trim()
}

function displayFilePath(cleanPath: string, start?: number, end?: number): string {
  const parts = cleanPath.split(/[\/\\]/).filter(Boolean)
  const short = parts.length > 2 ? parts.slice(-2).join('/') : cleanPath
  if (start) {
    const range = end && end !== start ? `${start}-${end}` : `${start}`
    return `${short}:${range}`
  }
  return short
}

export function CodeCommentsBlock({ comments }: { comments: CodeCommentItem[] }) {
  if (!comments.length) return null

  return (
    <div className="code-comments-container">
      <div className="code-comments-header">
        <span className="code-comments-header-icon">
          <IconMessage size={14} />
        </span>
        <span className="code-comments-header-title">
          {comments.length} {comments.length === 1 ? 'comment' : 'comments'}
        </span>
      </div>
      <div className="code-comments-list">
        {comments.map((comment, idx) => {
          const cleanFile = cleanFilePath(comment.file)
          const filePathStr = displayFilePath(cleanFile, comment.start, comment.end)
          const priorityLabel = comment.priority !== undefined ? `P${comment.priority}` : 'P2'
          const displayTitle = comment.title.replace(/^\[P\d+\]\s*/i, '')

          return (
            <HoverCard
              key={idx}
              width={480}
              shadow="md"
              position="top-start"
              withArrow
              openDelay={120}
              closeDelay={150}
            >
              <HoverCard.Target>
                <div className="code-comment-row" tabIndex={0} role="button">
                  <span className={`code-comment-badge badge-${priorityLabel.toLowerCase()}`}>
                    {priorityLabel}
                  </span>
                  <span className="code-comment-title" title={displayTitle}>
                    {displayTitle}
                  </span>
                  <span className="code-comment-file" title={cleanFile}>
                    {filePathStr}
                  </span>
                </div>
              </HoverCard.Target>
              <HoverCard.Dropdown className="code-comment-popover">
                <div className="popover-meta-row">
                  <span className={`code-comment-badge badge-${priorityLabel.toLowerCase()}`}>
                    {priorityLabel}
                  </span>
                  <span className="popover-file-path" title={cleanFile}>
                    {filePathStr}
                  </span>
                </div>
                <Text size="sm" fw={600} className="popover-title">
                  {displayTitle}
                </Text>
                <Text size="xs" className="popover-body">
                  {comment.body}
                </Text>
              </HoverCard.Dropdown>
            </HoverCard>
          )
        })}
      </div>
    </div>
  )
}

export function extractCodeComments(rawText: string): {
  comments: CodeCommentItem[]
  cleanText: string
} {
  const comments: CodeCommentItem[] = []
  const regex = /::code-comment\{([\s\S]*?)\}/g

  const cleanText = rawText.replace(regex, (_, argsStr: string) => {
    const getAttr = (name: string): string => {
      const m = argsStr.match(new RegExp(`${name}=["']([^"']*)["']`, 'i'))
      if (m) return m[1]
      const unquoted = argsStr.match(new RegExp(`${name}=([^\\s}]+)`, 'i'))
      return unquoted ? unquoted[1] : ''
    }

    const title = getAttr('title')
    const body = getAttr('body')
    const file = getAttr('file')
    const startStr = getAttr('start')
    const endStr = getAttr('end')
    const priorityStr = getAttr('priority')

    if (title || body || file) {
      comments.push({
        title: title || '未命名注释',
        body: body || '',
        file: file || '',
        start: startStr ? parseInt(startStr, 10) : undefined,
        end: endStr ? parseInt(endStr, 10) : undefined,
        priority: priorityStr ? parseInt(priorityStr, 10) : undefined,
      })
    }
    return ''
  }).replace(/\n{3,}/g, '\n\n').trim()

  return { comments, cleanText }
}
