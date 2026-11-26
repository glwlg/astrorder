import { useEffect, useRef, type KeyboardEvent } from 'react'
import { ActionIcon, Button, Tooltip } from '@mantine/core'
import { IconChevronDown, IconChevronUp, IconHistory, IconSearch, IconX } from '@tabler/icons-react'
import './transcriptFindBar.css'

export const FIND_HIGHLIGHT_ALL = 'transcript-find-matches'
export const FIND_HIGHLIGHT_ACTIVE = 'transcript-find-active'

export interface TranscriptFindHit {
  rowIndex: number
  messageId: string
  occurrenceIndex: number
  packId?: string
}

export interface SearchableRowLike {
  kind: 'message' | 'pack' | 'compaction' | 'artifact'
  id: string
  message?: { id: string; text: string }
  pack?: Array<{ id: string; text: string; tool?: { name?: string; output?: string } | null }>
}

function countOccurrences(haystack: string, needle: string, caseSensitive: boolean): number {
  if (!haystack || !needle) return 0
  const src = caseSensitive ? haystack : haystack.toLowerCase()
  const q = caseSensitive ? needle : needle.toLowerCase()
  let count = 0
  let pos = src.indexOf(q)
  while (pos !== -1) {
    count += 1
    pos = src.indexOf(q, pos + q.length)
  }
  return count
}

/**
 * Computes ordered matches across all loaded visual rows (including rows currently
 * folded behind the transcript window limit so off-window matches can auto-expand).
 */
export function collectTranscriptMatches(
  rows: readonly SearchableRowLike[],
  query: string,
  caseSensitive: boolean,
): TranscriptFindHit[] {
  const needle = query.trim()
  if (!needle) return []
  const hits: TranscriptFindHit[] = []
  for (let rowIndex = 0; rowIndex < rows.length; rowIndex++) {
    const row = rows[rowIndex]
    if (row.kind === 'message' || row.kind === 'compaction') {
      const msg = row.message
      if (!msg?.text) continue
      const n = countOccurrences(msg.text, needle, caseSensitive)
      for (let occ = 0; occ < n; occ++) {
        hits.push({ rowIndex, messageId: msg.id, occurrenceIndex: occ })
      }
    } else if (row.kind === 'pack' && row.pack) {
      for (const msg of row.pack) {
        const combined = [msg.text, msg.tool?.name, msg.tool?.output].filter(Boolean).join('\n')
        const n = countOccurrences(combined, needle, caseSensitive)
        for (let occ = 0; occ < n; occ++) {
          hits.push({ rowIndex, messageId: msg.id, occurrenceIndex: occ, packId: row.id })
        }
      }
    }
  }
  return hits
}

export function clearTranscriptCustomHighlights(): void {
  if (typeof CSS === 'undefined' || !('highlights' in CSS) || !CSS.highlights) return
  CSS.highlights.delete(FIND_HIGHLIGHT_ALL)
  CSS.highlights.delete(FIND_HIGHLIGHT_ACTIVE)
}

function isExcludedFromFind(node: Node): boolean {
  const el = node.parentElement
  if (!el) return true
  return Boolean(
    el.closest('.message-actions-bar, .transcript-date-divider, .history-window-controls, .transcript-find-bar, [aria-hidden="true"]'),
  )
}

/**
 * Walks rendered text nodes inside `container` using TreeWalker and registers
 * CSS Custom Highlight API ranges without mutating React-managed DOM.
 * Returns the active `Range` (if found in the rendered DOM) so the caller can
 * scroll the exact matched line into the viewport center.
 */
export function applyTranscriptCustomHighlights(
  container: HTMLElement | null,
  query: string,
  caseSensitive: boolean,
  activeHit: TranscriptFindHit | null,
): Range | null {
  clearTranscriptCustomHighlights()
  const needle = query.trim()
  if (!container || !needle) return null

  const normalizedNeedle = caseSensitive ? needle : needle.toLowerCase()
  const allRanges: Range[] = []
  let activeRange: Range | null = null

  // Track occurrence index within the active target message element
  const escapeCss = (id: string) =>
    typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(id) : id.replace(/"/g, '\\"')
  const escapedId = activeHit ? escapeCss(activeHit.messageId) : ''
  const escapedPackId = activeHit?.packId ? escapeCss(activeHit.packId) : ''
  const activeMessageEl = activeHit
    ? container.querySelector<HTMLElement>(`[data-turn-id="${escapedId}"], [data-testid="message-${escapedId}"]`) ||
      (escapedPackId ? container.querySelector<HTMLElement>(`[data-turn-id="${escapedPackId}"]`) : null)
    : null
  let activeMsgOccCounter = 0

  const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT)
  let current = walker.nextNode()
  while (current) {
    if (!isExcludedFromFind(current)) {
      const raw = current.textContent || ''
      if (raw.length >= normalizedNeedle.length) {
        const hay = caseSensitive ? raw : raw.toLowerCase()
        let pos = hay.indexOf(normalizedNeedle)
        while (pos !== -1) {
          const range = new Range()
          range.setStart(current, pos)
          range.setEnd(current, pos + normalizedNeedle.length)
          allRanges.push(range)

          if (activeMessageEl && activeMessageEl.contains(current)) {
            if (activeHit && activeMsgOccCounter === activeHit.occurrenceIndex && !activeRange) {
              activeRange = range
            }
            activeMsgOccCounter += 1
          }
          pos = hay.indexOf(normalizedNeedle, pos + normalizedNeedle.length)
        }
      }
    }
    current = walker.nextNode()
  }

  // Fallback if activeHit.occurrenceIndex was inside markdown syntax stripped in rendered DOM
  if (!activeRange && activeMessageEl) {
    activeRange = allRanges.find((r) => activeMessageEl.contains(r.startContainer)) || null
  }

  if (
    typeof CSS !== 'undefined' &&
    'highlights' in CSS &&
    CSS.highlights &&
    typeof Highlight !== 'undefined' &&
    allRanges.length > 0
  ) {
    const allHl = new Highlight(...allRanges)
    allHl.priority = 1
    CSS.highlights.set(FIND_HIGHLIGHT_ALL, allHl)
    if (activeRange) {
      const activeHl = new Highlight(activeRange)
      activeHl.priority = 2
      CSS.highlights.set(FIND_HIGHLIGHT_ACTIVE, activeHl)
    }
  }

  return activeRange
}

export interface TranscriptFindBarProps {
  opened: boolean
  query: string
  caseSensitive: boolean
  activeIndex: number
  totalMatches: number
  hasMoreHistory?: boolean
  loadingOlder?: boolean
  focusSignal: number
  onQueryChange: (value: string) => void
  onToggleCaseSensitive: () => void
  onPrev: () => void
  onNext: () => void
  onLoadOlder?: () => void
  onClose: () => void
}

export function TranscriptFindBar({
  opened,
  query,
  caseSensitive,
  activeIndex,
  totalMatches,
  hasMoreHistory,
  loadingOlder,
  focusSignal,
  onQueryChange,
  onToggleCaseSensitive,
  onPrev,
  onNext,
  onLoadOlder,
  onClose,
}: TranscriptFindBarProps) {
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!opened) return
    const input = inputRef.current
    if (!input) return
    input.focus()
    input.select()
  }, [opened, focusSignal])

  if (!opened) return null

  const hasQuery = query.trim().length > 0
  const countLabel = !hasQuery
    ? ''
    : totalMatches > 0
      ? `${activeIndex + 1} / ${totalMatches}`
      : '无结果'

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.nativeEvent.isComposing) return
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      onClose()
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      event.stopPropagation()
      if (totalMatches > 0) {
        if (event.shiftKey) onPrev()
        else onNext()
      }
    }
  }

  return (
    <div className="transcript-find-bar" role="search" aria-label="在当前会话中搜索">
      <IconSearch size={14} className="transcript-find-icon" aria-hidden="true" />
      <input
        ref={inputRef}
        type="search"
        className="transcript-find-input"
        aria-label="搜索会话内容"
        placeholder="搜索会话内容…"
        value={query}
        onChange={(e) => onQueryChange(e.currentTarget.value)}
        onKeyDown={handleKeyDown}
      />
      {hasQuery && (
        <span
          className={`transcript-find-count${totalMatches === 0 ? ' is-empty' : ''}`}
          role="status"
          aria-live="polite"
        >
          {countLabel}
        </span>
      )}
      <Tooltip label="区分大小写" position="bottom" withArrow>
        <button
          type="button"
          className={`transcript-find-case-btn${caseSensitive ? ' is-active' : ''}`}
          aria-label="区分大小写"
          aria-pressed={caseSensitive}
          onClick={onToggleCaseSensitive}
        >
          Aa
        </button>
      </Tooltip>
      <span className="transcript-find-sep" aria-hidden="true" />
      <Tooltip label="上一个匹配 (Shift+Enter)" position="bottom" withArrow>
        <ActionIcon
          size="xs"
          variant="subtle"
          color="gray"
          aria-label="上一个匹配"
          disabled={totalMatches === 0}
          onClick={onPrev}
        >
          <IconChevronUp size={14} />
        </ActionIcon>
      </Tooltip>
      <Tooltip label="下一个匹配 (Enter)" position="bottom" withArrow>
        <ActionIcon
          size="xs"
          variant="subtle"
          color="gray"
          aria-label="下一个匹配"
          disabled={totalMatches === 0}
          onClick={onNext}
        >
          <IconChevronDown size={14} />
        </ActionIcon>
      </Tooltip>
      {hasMoreHistory && onLoadOlder && (
        <Tooltip label="加载更早的会话记录以扩大搜索范围" position="bottom" withArrow>
          <Button
            size="compact-xs"
            variant="subtle"
            color="gray"
            loading={loadingOlder}
            leftSection={<IconHistory size={12} />}
            onClick={onLoadOlder}
          >
            更早
          </Button>
        </Tooltip>
      )}
      <Tooltip label="关闭搜索 (Esc)" position="bottom" withArrow>
        <ActionIcon
          size="xs"
          variant="subtle"
          color="gray"
          aria-label="关闭会话搜索"
          onClick={onClose}
        >
          <IconX size={14} />
        </ActionIcon>
      </Tooltip>
    </div>
  )
}
