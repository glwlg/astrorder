import { IconFold } from '@tabler/icons-react'
import type { Message } from '../../domain/types'
import { LazyDetails } from '../../components/LazyDetails'

export function isCompactionMessage(message: Message | null | undefined): boolean {
  if (!message) return false
  const toolName = String(message.tool?.name || '')
  if (toolName === 'contextCompaction' || toolName === 'compaction') return true
  if (message.text && /^(?:\[CONTEXT COMPACTION|正在压缩上下文|上下文压缩完成)/i.test(message.text.trim())) return true
  return false
}

export function CompactionDivider({ message }: { message: Message }) {
  const text = (message.text || '').trim()
  const hasDetail = Boolean(text && !/^(?:正在压缩上下文|上下文压缩完成)$/.test(text))

  return (
    <div className="compaction-divider" data-testid={`compaction-${message.id}`} aria-label="上下文压缩分割线">
      <div className="compaction-divider-line" />
      <div className="compaction-divider-pill">
        <IconFold size={13} className="compaction-divider-icon" />
        <span className="compaction-divider-text">上下文已压缩</span>
      </div>
      <div className="compaction-divider-line" />
      {hasDetail && (
        <div className="compaction-divider-detail">
          <LazyDetails summary="查看压缩摘要与详情">
            <pre className="tool-output">{text}</pre>
          </LazyDetails>
        </div>
      )}
    </div>
  )
}
