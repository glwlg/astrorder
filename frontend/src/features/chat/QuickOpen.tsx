import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Loader, TextInput } from '@mantine/core'
import { IconFile, IconSearch } from '@tabler/icons-react'
import { api } from '../../api/client'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import { artifactViewerRegistry } from '../sidecar/registry'
import { useSidecarStore } from '../sidecar/sidecarStore'
import type { Session } from '../../domain/types'

interface QuickOpenProps {
  session: Session
  opened: boolean
  onClose: () => void
}

export function QuickOpen({ session, opened, onClose }: QuickOpenProps) {
  const [query, setQuery] = useState('')
  const [items, setItems] = useState<Array<{ path: string; name: string }>>([])
  const [loading, setLoading] = useState(false)
  const [activeIndex, setActiveIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const listRef = useRef<HTMLDivElement>(null)
  const openArtifact = useSidecarStore((s) => s.openArtifact)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  useEffect(() => {
    if (!opened) return
    setQuery('')
    setItems([])
    setActiveIndex(0)
    requestAnimationFrame(() => inputRef.current?.focus())
    // 预载前 30 个文件，未输入时即可直接回车
    void runSearch('')
  }, [opened])

  const runSearch = useCallback(
    async (q: string) => {
      setLoading(true)
      try {
        const data = await api.searchFiles({
          q,
          sessionId: session.id,
          connectionId: session.connection_id || undefined,
          limit: 30,
        })
        setItems(data.items)
        setActiveIndex(0)
      } catch {
        setItems([])
      } finally {
        setLoading(false)
      }
    },
    [session.id, session.connection_id],
  )

  const handleQueryChange = (value: string) => {
    setQuery(value)
    if (debounceRef.current) clearTimeout(debounceRef.current)
    debounceRef.current = setTimeout(() => void runSearch(value), 220)
  }

  const openItem = useCallback(
    (index: number) => {
      const item = items[index]
      if (!item) return
      const artifact = resolveArtifactFromPath(item.path, session, { name: item.name })
      const viewer = artifactViewerRegistry.findViewer(artifact)
      openArtifact(artifact, viewer?.id ?? 'monaco-viewer')
      onClose()
    },
    [items, session, openArtifact, onClose],
  )

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIndex((i) => Math.min(i + 1, items.length - 1))
      return
    }
    if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIndex((i) => Math.max(i - 1, 0))
      return
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      openItem(activeIndex)
    }
  }

  useEffect(() => {
    const el = listRef.current?.children[activeIndex] as HTMLElement | undefined
    el?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex])

  const emptyHint = useMemo(() => {
    if (loading) return null
    if (items.length) return null
    return query ? '没有匹配的文件' : '输入文件名快速打开'
  }, [loading, items.length, query])

  if (!opened) return null

  return createPortal(
    <div
      aria-label="快速打开文件"
      className="quick-open-backdrop"
      onMouseDown={onClose}
      role="dialog"
    >
      <div className="quick-open-card" onMouseDown={(e) => e.stopPropagation()}>
        <TextInput
          ref={inputRef}
          aria-label="搜索文件名"
          leftSection={<IconSearch size={15} />}
          onChange={(e) => handleQueryChange(e.currentTarget.value)}
          onKeyDown={handleKeyDown}
          placeholder="输入文件名…"
          rightSection={loading ? <Loader size={14} /> : null}
          value={query}
        />
        <div ref={listRef} className="quick-open-list">
          {items.map((item, index) => (
            <button
              aria-current={index === activeIndex}
              className={`quick-open-row ${index === activeIndex ? 'is-active' : ''}`}
              key={item.path}
              onClick={() => openItem(index)}
              onMouseEnter={() => setActiveIndex(index)}
              type="button"
            >
              <IconFile size={14} />
              <span className="quick-open-row-name">{item.name}</span>
              <small>{item.path}</small>
            </button>
          ))}
          {emptyHint && <div className="quick-open-empty">{emptyHint}</div>}
        </div>
      </div>
    </div>,
    document.body,
  )
}
