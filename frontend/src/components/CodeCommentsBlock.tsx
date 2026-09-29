import { useState } from 'react'
import { HoverCard, Text } from '@mantine/core'
import { IconCheck, IconCopy, IconMessage } from '@tabler/icons-react'
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
  const [copied, setCopied] = useState(false)
  if (!comments.length) return null

  const copyComments = async () => {
    const text = comments.map((comment) => {
      const priority = comment.priority !== undefined ? `P${comment.priority}` : 'P2'
      const title = comment.title.replace(/^\[P\d+\]\s*/i, '')
      const file = cleanFilePath(comment.file)
      const range = comment.start
        ? `:${comment.start}${comment.end && comment.end !== comment.start ? `-${comment.end}` : ''}`
        : ''
      return `### [${priority}] ${title}\n\n位置：\`${file}${range}\`\n\n问题描述：${comment.body}`
    }).join('\n\n')
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      setTimeout(() => setCopied(false), 1600)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div className="code-comments-container">
      <div className="code-comments-header">
        <span className="code-comments-header-icon">
          <IconMessage size={14} />
        </span>
        <span className="code-comments-header-title">
          {comments.length} {comments.length === 1 ? 'comment' : 'comments'}
        </span>
        <button type="button" className="code-comments-copy" onClick={() => void copyComments()} aria-label={copied ? '已复制注释' : '复制注释'}>
          {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
          {copied ? '已复制' : '复制'}
        </button>
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
  const regex = /::code-comment\{((?:[^}"']|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')*)\}/g

  const cleanText = rawText.replace(regex, (_, argsStr: string) => {
    const attrs: Record<string, string> = {}
    const attrRegex = /(?:^|\s)(title|body|file|start|end|priority)\s*=\s*(?:"((?:\\.|[^"\\])*)"|'((?:\\.|[^'\\])*)'|([^\s}]+))/gi
    for (const match of argsStr.matchAll(attrRegex)) {
      attrs[match[1].toLowerCase()] = (match[2] ?? match[3] ?? match[4]).replace(/\\(["'\\])/g, '$1')
    }

    const { title, body, file, start: startStr, end: endStr, priority: priorityStr } = attrs

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
