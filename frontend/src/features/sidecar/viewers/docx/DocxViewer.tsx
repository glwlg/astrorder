import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { ActionIcon, Badge, Button, Group, LoadingOverlay, Modal, Paper, Stack, Text, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconChevronDown,
  IconChevronUp,
  IconClipboardText,
  IconDownload,
  IconExternalLink,
  IconFileDiff,
  IconFileTypePdf,
  IconLayoutSidebar,
  IconMessageForward,
  IconRefresh,
  IconSearch,
  IconX,
} from '@tabler/icons-react'
import { api, type DocxBlock, type DocxPreviewResponse, type DocxRun } from '../../../../api/client'
import { copyMarkdownAsRichText } from '../../../office/export/richClipboard'
import { printMarkdownToPdf } from '../../../office/export/printPdf'
import { downloadFile } from '../../fileTransfer'
import type { ViewerContext } from '../../types'
import { diffDocxParagraphs } from './docxDiff'
import './docxViewer.css'

function renderRun(run: DocxRun, key: number): ReactNode {
  const style: CSSProperties = {}
  if (run.bold) style.fontWeight = 600
  if (run.italic) style.fontStyle = 'italic'
  const deco: string[] = []
  if (run.underline) deco.push('underline')
  if (run.strike) deco.push('line-through')
  if (deco.length > 0) style.textDecoration = deco.join(' ')
  if (run.color) style.color = run.color

  const content = run.code ? (
    <code key={key} className="docx-inline-code" style={style}>
      {run.text}
    </code>
  ) : (
    <span key={key} style={style}>
      {run.text}
    </span>
  )

  if (run.href && /^https?:\/\//i.test(run.href)) {
    return (
      <a key={key} href={run.href} target="_blank" rel="noopener noreferrer" style={{ color: '#2563eb' }}>
        {content}
      </a>
    )
  }
  return content
}

function blocksToPlainMarkdown(doc: DocxPreviewResponse): string {
  const lines: string[] = []
  for (const blk of doc.blocks) {
    const text = (blk.runs || []).map((r) => r.text).join('')
    if (blk.kind === 'heading') {
      const hashes = '#'.repeat(Math.min(Math.max(blk.level || 1, 1), 6))
      lines.push(`${hashes} ${text}`)
    } else if (blk.kind === 'list_item') {
      const indent = '  '.repeat(blk.level || 0)
      lines.push(`${indent}${blk.ordered ? '1.' : '-'} ${text}`)
    } else if (blk.kind === 'paragraph') {
      lines.push(text)
    } else if (blk.kind === 'table' && blk.rows && blk.rows.length > 0) {
      blk.rows.forEach((row, ri) => {
        const cols = row.cells.map((c) => c.text.replace(/\|/g, '\\|').replace(/\n/g, ' '))
        lines.push(`| ${cols.join(' | ')} |`)
        if (ri === 0) {
          lines.push(`| ${cols.map(() => '---').join(' | ')} |`)
        }
      })
    } else if (blk.kind === 'hr') {
      lines.push('---')
    }
    lines.push('')
  }
  return lines.join('\n').trim()
}

export function DocxViewer({ artifact, session, toolbarAction, surface = 'desktop' }: ViewerContext) {
  const [doc, setDoc] = useState<DocxPreviewResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [showOutline, setShowOutline] = useState(() => surface !== 'mobile')
  const [selectedText, setSelectedText] = useState('')
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [activeMatchIdx, setActiveMatchIdx] = useState(0)

  // Two-version redline diff state
  const [compareModalOpen, setCompareModalOpen] = useState(false)
  const [compareQuery, setCompareQuery] = useState('')
  const [compareCandidates, setCompareCandidates] = useState<Array<{ path: string; name: string }>>([])
  const [searchingCandidates, setSearchingCandidates] = useState(false)
  const [compareManualPath, setCompareManualPath] = useState('')
  const [baselineDoc, setBaselineDoc] = useState<DocxPreviewResponse | null>(null)
  const [loadingBaseline, setLoadingBaseline] = useState(false)

  const paperRef = useRef<HTMLDivElement>(null)

  const filePath = artifact.path || artifact.name

  const loadDocument = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const data = await api.previewDocx({
        path: filePath,
        sessionId: session?.id || artifact.sessionId,
        connectionId: session?.connection_id || artifact.connectionId,
      })
      setDoc(data)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Word 文档解析失败')
    } finally {
      setLoading(false)
    }
  }, [artifact.connectionId, artifact.sessionId, filePath, session?.connection_id, session?.id])

  useEffect(() => {
    void loadDocument()
  }, [loadDocument, revision])

  const handlePaperMouseUp = () => {
    const sel = window.getSelection()
    if (!sel || sel.isCollapsed) {
      setSelectedText('')
      return
    }
    const text = sel.toString().trim()
    if (text.length >= 2) {
      setSelectedText(text)
    } else {
      setSelectedText('')
    }
  }

  const quoteToChat = (snippet: string) => {
    const quoted = snippet.length > 1200 ? `${snippet.slice(0, 1200)}…` : snippet
    const text = `请审阅并分析 Word 文档 \`${filePath}\` 中的以下片段：\n\n> ${quoted.replace(/\n/g, '\n> ')}\n`
    window.dispatchEvent(
      new CustomEvent('astrorder:quote-to-composer', {
        detail: {
          agentId: session?.agent_id || artifact.agentId,
          sessionId: session?.id || artifact.sessionId,
          text,
        },
      }),
    )
    setSelectedText('')
    notifications.show({ color: 'teal', message: '已将选中段落引用到对话输入框' })
  }

  const openComparePicker = async () => {
    setCompareModalOpen(true)
    setSearchingCandidates(true)
    try {
      const resp = await api.searchFiles({
        q: '.docx',
        sessionId: session?.id || artifact.sessionId,
        connectionId: session?.connection_id || artifact.connectionId || undefined,
        limit: 40,
      })
      const items = (resp.items || []).filter(
        (it) => it.name.toLowerCase().endsWith('.docx') && it.path !== filePath,
      )
      setCompareCandidates(items)
    } catch {
      setCompareCandidates([])
    } finally {
      setSearchingCandidates(false)
    }
  }

  const startCompareWithPath = async (targetPath: string) => {
    const cleaned = targetPath.trim()
    if (!cleaned) return
    setLoadingBaseline(true)
    try {
      const base = await api.previewDocx({
        path: cleaned,
        sessionId: session?.id || artifact.sessionId,
        connectionId: session?.connection_id || artifact.connectionId,
      })
      setBaselineDoc(base)
      setCompareModalOpen(false)
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '读取对比文档失败',
      })
    } finally {
      setLoadingBaseline(false)
    }
  }

  const diffSummary = useMemo(() => {
    if (!doc || !baselineDoc) return null
    return diffDocxParagraphs(baselineDoc.plain_paragraphs || [], doc.plain_paragraphs || [])
  }, [baselineDoc, doc])

  const filteredCandidates = useMemo(() => {
    const q = compareQuery.trim().toLowerCase()
    if (!q) return compareCandidates
    return compareCandidates.filter(
      (c) => c.name.toLowerCase().includes(q) || c.path.toLowerCase().includes(q),
    )
  }, [compareCandidates, compareQuery])

  const scrollToBlock = (id: string) => {
    const el = paperRef.current?.querySelector(`[data-docx-block-id="${CSS.escape(id)}"]`)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
  }

  const handleCopyRich = async () => {
    if (!doc) return
    try {
      await copyMarkdownAsRichText(blocksToPlainMarkdown(doc), { title: doc.title })
      notifications.show({ color: 'teal', message: '已复制富文本到剪贴板' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '复制失败' })
    }
  }

  const handlePrintPdf = async () => {
    if (!doc) return
    try {
      await printMarkdownToPdf({ title: doc.title, markdown: blocksToPlainMarkdown(doc) })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '打印 PDF 失败' })
    }
  }

  const orderedListMarkers = useMemo(() => {
    const markers = new Map<string, string>()
    if (!doc) return markers
    const counters = new Map<number, number>()
    for (const blk of doc.blocks) {
      if (blk.kind === 'list_item' && blk.ordered) {
        const lvl = Math.max(0, blk.level || 0)
        for (const existingLvl of Array.from(counters.keys())) {
          if (existingLvl > lvl) counters.delete(existingLvl)
        }
        const nextVal = (counters.get(lvl) || 0) + 1
        counters.set(lvl, nextVal)
        markers.set(blk.id, `${nextVal}.`)
      } else {
        counters.clear()
      }
    }
    return markers
  }, [doc])

  const searchMatches = useMemo(() => {
    const q = searchQuery.trim().toLowerCase()
    if (!q || !doc) return [] as string[]
    const ids: string[] = []
    for (const blk of doc.blocks) {
      let text = (blk.runs || []).map((r) => r.text).join('')
      if (blk.kind === 'table' && blk.rows) {
        text += ' ' + blk.rows.map((row) => row.cells.map((c) => c.text).join(' ')).join(' ')
      }
      if (text.toLowerCase().includes(q)) {
        ids.push(blk.id)
      }
    }
    return ids
  }, [doc, searchQuery])

  const activeMatchBlockId = searchMatches.length > 0 ? searchMatches[activeMatchIdx % searchMatches.length] : null

  useEffect(() => {
    setActiveMatchIdx(0)
  }, [searchQuery])

  useEffect(() => {
    if (!activeMatchBlockId) return
    const el = paperRef.current?.querySelector(`[data-docx-block-id="${CSS.escape(activeMatchBlockId)}"]`)
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    }
  }, [activeMatchBlockId, activeMatchIdx])

  const renderBlock = (blk: DocxBlock) => {
    const alignStyle: CSSProperties | undefined =
      blk.align && ['center', 'right', 'justify'].includes(blk.align)
        ? { textAlign: blk.align as CSSProperties['textAlign'] }
        : undefined
    const isMatch = searchQuery.trim().length > 0 && searchMatches.includes(blk.id)
    const isActiveMatch = activeMatchBlockId === blk.id
    const matchClass = isActiveMatch ? ' docx-block-match is-active' : isMatch ? ' docx-block-match' : ''

    switch (blk.kind) {
      case 'heading': {
        const lvl = Math.min(Math.max(blk.level || 1, 1), 6)
        const content = (blk.runs || []).map((r, idx) => renderRun(r, idx))
        if (lvl === 1) return <h1 key={blk.id} data-docx-block-id={blk.id} className={matchClass.trim() || undefined} style={alignStyle}>{content}</h1>
        if (lvl === 2) return <h2 key={blk.id} data-docx-block-id={blk.id} className={matchClass.trim() || undefined} style={alignStyle}>{content}</h2>
        if (lvl === 3) return <h3 key={blk.id} data-docx-block-id={blk.id} className={matchClass.trim() || undefined} style={alignStyle}>{content}</h3>
        return <h4 key={blk.id} data-docx-block-id={blk.id} className={matchClass.trim() || undefined} style={alignStyle}>{content}</h4>
      }
      case 'list_item':
        return (
          <div
            key={blk.id}
            data-docx-block-id={blk.id}
            className={`docx-list-item${matchClass}`}
            style={{ paddingLeft: `${(blk.level || 0) * 18}px`, ...alignStyle }}
          >
            <span className="docx-list-marker">
              {blk.ordered ? orderedListMarkers.get(blk.id) || '1.' : '•'}
            </span>
            <div>{(blk.runs || []).map((r, idx) => renderRun(r, idx))}</div>
          </div>
        )
      case 'table':
        return (
          <div key={blk.id} data-docx-block-id={blk.id} className={`docx-table-wrap${matchClass}`}>
            <table>
              <tbody>
                {(blk.rows || []).map((row, ri) => (
                  <tr key={ri}>
                    {row.cells.map((cell, ci) => {
                      const Tag = cell.is_header || row.is_header ? 'th' : 'td'
                      return (
                        <Tag key={ci} colSpan={cell.colspan || undefined} rowSpan={cell.rowspan || undefined}>
                          {cell.runs && cell.runs.length > 0
                            ? cell.runs.map((r, idx) => renderRun(r, idx))
                            : cell.text}
                        </Tag>
                      )
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      case 'image':
        return blk.image_data_uri ? (
          <div key={blk.id} data-docx-block-id={blk.id} className="docx-image-wrap">
            <img src={blk.image_data_uri} alt={blk.image_alt || '文档内嵌图片'} />
          </div>
        ) : null
      case 'hr':
        return <hr key={blk.id} data-docx-block-id={blk.id} />
      default:
        return (
          <p key={blk.id} data-docx-block-id={blk.id} className={matchClass.trim() || undefined} style={alignStyle}>
            {(blk.runs || []).map((r, idx) => renderRun(r, idx))}
          </p>
        )
    }
  }

  return (
    <div className="docx-viewer-shell">
      <Paper p="xs" withBorder style={{ flex: 'none', margin: '8px 8px 0', zIndex: 10, borderRadius: 10 }}>
        <Group justify="space-between" wrap="nowrap" gap="xs">
          <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
            <Tooltip label={showOutline ? '收起目录大纲' : '展开目录大纲'}>
              <ActionIcon
                variant={showOutline ? 'light' : 'subtle'}
                size="sm"
                aria-label="切换目录大纲"
                onClick={() => setShowOutline((v) => !v)}
              >
                <IconLayoutSidebar size={14} />
              </ActionIcon>
            </Tooltip>
            <Text size="xs" fw={600} truncate title={filePath} style={{ minWidth: 0 }}>
              {artifact.name}
            </Text>
            {doc && (
              <Badge size="xs" variant="light" color="gray" style={{ flexShrink: 0 }}>
                {doc.stats.words} 字 · {doc.stats.tables} 表
              </Badge>
            )}
          </Group>

          <Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
            {toolbarAction}
            {!baselineDoc && (
              searchOpen ? (
                <Group gap={4} wrap="nowrap">
                  <TextInput
                    size="xs"
                    w={160}
                    autoFocus
                    leftSection={<IconSearch size={13} />}
                    placeholder="搜索正文…"
                    aria-label="在文档内搜索"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.currentTarget.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && searchMatches.length > 0) {
                        e.preventDefault()
                        setActiveMatchIdx((prev) => (prev + (e.shiftKey ? -1 : 1) + searchMatches.length) % searchMatches.length)
                      } else if (e.key === 'Escape') {
                        setSearchOpen(false)
                        setSearchQuery('')
                      }
                    }}
                  />
                  {searchQuery.trim() && (
                    <Text size="xs" c="dimmed" style={{ minWidth: 36, textAlign: 'center', fontVariantNumeric: 'tabular-nums' }}>
                      {searchMatches.length > 0 ? `${(activeMatchIdx % searchMatches.length) + 1}/${searchMatches.length}` : '0/0'}
                    </Text>
                  )}
                  <ActionIcon
                    size="xs"
                    variant="subtle"
                    aria-label="上一个匹配"
                    disabled={searchMatches.length === 0}
                    onClick={() => setActiveMatchIdx((prev) => (prev - 1 + searchMatches.length) % searchMatches.length)}
                  >
                    <IconChevronUp size={13} />
                  </ActionIcon>
                  <ActionIcon
                    size="xs"
                    variant="subtle"
                    aria-label="下一个匹配"
                    disabled={searchMatches.length === 0}
                    onClick={() => setActiveMatchIdx((prev) => (prev + 1) % searchMatches.length)}
                  >
                    <IconChevronDown size={13} />
                  </ActionIcon>
                  <ActionIcon
                    size="xs"
                    variant="subtle"
                    aria-label="关闭搜索"
                    onClick={() => {
                      setSearchOpen(false)
                      setSearchQuery('')
                    }}
                  >
                    <IconX size={13} />
                  </ActionIcon>
                </Group>
              ) : (
                <Tooltip label="文档内搜索">
                  <ActionIcon variant="subtle" size="sm" aria-label="文档内搜索" onClick={() => setSearchOpen(true)}>
                    <IconSearch size={14} />
                  </ActionIcon>
                </Tooltip>
              )
            )}
            {baselineDoc ? (
              <Button
                size="compact-xs"
                variant="light"
                color="orange"
                leftSection={<IconX size={13} />}
                onClick={() => setBaselineDoc(null)}
              >
                退出比对
              </Button>
            ) : (
              <Button
                size="compact-xs"
                variant="light"
                color="indigo"
                leftSection={<IconFileDiff size={13} />}
                onClick={() => void openComparePicker()}
              >
                双版对比
              </Button>
            )}
            <Tooltip label="复制富文本">
              <ActionIcon variant="subtle" size="sm" aria-label="复制富文本" onClick={() => void handleCopyRich()}>
                <IconClipboardText size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="导出 PDF / 打印">
              <ActionIcon variant="subtle" size="sm" aria-label="导出 PDF" onClick={() => void handlePrintPdf()}>
                <IconFileTypePdf size={14} />
              </ActionIcon>
            </Tooltip>
            {artifact.path && (!artifact.connectionId || artifact.connectionId === 'local') && (
              <Tooltip label="用 Word / WPS 打开">
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  aria-label="系统应用打开"
                  onClick={() => void api.openFile(artifact.path!, 'open')}
                >
                  <IconExternalLink size={14} />
                </ActionIcon>
              </Tooltip>
            )}
            <Tooltip label="刷新">
              <ActionIcon variant="subtle" size="sm" aria-label="刷新 Word" onClick={() => setRevision((v) => v + 1)}>
                <IconRefresh size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="下载">
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="下载 Word"
                onClick={() => void downloadFile(`${artifact.readUrl}&download=1`, artifact.name)}
              >
                <IconDownload size={14} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </Paper>

      <div className="docx-viewer-body">
        {showOutline && !baselineDoc && doc && doc.outline.length > 0 && (
          <aside className="docx-outline-panel" aria-label="文档目录大纲">
            <div className="docx-outline-header">目录大纲 ({doc.outline.length})</div>
            <div className="docx-outline-list">
              {doc.outline.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  className="docx-outline-item"
                  style={{ paddingLeft: `${8 + Math.max(0, item.level - 1) * 12}px` }}
                  title={item.text}
                  onClick={() => scrollToBlock(item.id)}
                >
                  {item.text}
                </button>
              ))}
            </div>
          </aside>
        )}

        <div className="docx-canvas-scroll" onMouseUp={handlePaperMouseUp}>
          <LoadingOverlay visible={loading} />
          {selectedText && (
            <div className="docx-selection-bar" role="region" aria-label="划词引用操作栏">
              <Text size="xs" truncate style={{ maxWidth: 480 }}>
                已选中 {selectedText.length} 字：“{selectedText.slice(0, 48)}
                {selectedText.length > 48 ? '…' : ''}”
              </Text>
              <Group gap={6} wrap="nowrap">
                <Button
                  size="compact-xs"
                  leftSection={<IconMessageForward size={13} />}
                  onClick={() => quoteToChat(selectedText)}
                >
                  引用到当前对话
                </Button>
                <ActionIcon size="xs" variant="subtle" aria-label="取消选中" onClick={() => setSelectedText('')}>
                  <IconX size={12} />
                </ActionIcon>
              </Group>
            </div>
          )}

          {error ? (
            <div style={{ textAlign: 'center', padding: 36 }}>
              <Text size="sm" c="red" mb="sm">{error}</Text>
              <Button size="xs" variant="light" onClick={() => setRevision((v) => v + 1)}>重试</Button>
            </div>
          ) : diffSummary && baselineDoc ? (
            <div className="docx-paper" ref={paperRef} aria-label="Word 双版红线比对视图">
              <Group justify="space-between" mb="md" pb="xs" style={{ borderBottom: '1px solid rgba(148,163,184,0.35)' }}>
                <div>
                  <Text size="sm" fw={700}>双版修订红线比对</Text>
                  <Text size="xs" c="dimmed">
                    基线：{baselineDoc.name} → 当前：{doc?.name}
                  </Text>
                </div>
                <Group gap={6}>
                  <Badge color="teal" variant="light">+{diffSummary.addedCount} 新增段落</Badge>
                  <Badge color="red" variant="light">-{diffSummary.removedCount} 删除段落</Badge>
                  <Badge color="gray" variant="light">{diffSummary.unchangedCount} 未变</Badge>
                </Group>
              </Group>
              {diffSummary.entries.map((entry, idx) => (
                <div key={idx} className={`docx-diff-line is-${entry.kind}`}>
                  {entry.kind === 'added' ? '+ ' : entry.kind === 'removed' ? '- ' : ''}
                  {entry.text}
                </div>
              ))}
            </div>
          ) : doc ? (
            <article className="docx-paper" ref={paperRef} aria-label={`Word 文档内容：${doc.title}`}>
              {doc.blocks.length === 0 ? (
                <Text size="sm" c="dimmed">文档暂无可渲染正文。</Text>
              ) : (
                doc.blocks.map((blk) => renderBlock(blk))
              )}
            </article>
          ) : null}
        </div>
      </div>

      <Modal
        opened={compareModalOpen}
        onClose={() => setCompareModalOpen(false)}
        title="选择基线 Word (.docx) 进行红线比对"
        size="md"
        withinPortal
      >
        <Stack gap="sm">
          <Text size="xs" c="dimmed">
            将选定文档作为“旧版本（基线）”，与当前打开的 <strong>{artifact.name}</strong> 进行逐段修订比对。
          </Text>
          <TextInput
            size="xs"
            leftSection={<IconSearch size={14} />}
            placeholder="筛选当前工作区中的 .docx 文件…"
            value={compareQuery}
            onChange={(e) => setCompareQuery(e.currentTarget.value)}
          />
          <div style={{ maxHeight: 220, overflowY: 'auto', border: '1px solid var(--mantine-color-default-border)', borderRadius: 8, padding: 6 }}>
            {searchingCandidates ? (
              <Text size="xs" c="dimmed" p="xs">正在搜索工作区 .docx 文件…</Text>
            ) : filteredCandidates.length === 0 ? (
              <Text size="xs" c="dimmed" p="xs">未找到其他 .docx 文件，可在下方输入完整路径。</Text>
            ) : (
              <Stack gap={4}>
                {filteredCandidates.map((item) => (
                  <Button
                    key={item.path}
                    variant="subtle"
                    justify="flex-start"
                    size="xs"
                    loading={loadingBaseline}
                    onClick={() => void startCompareWithPath(item.path)}
                    title={item.path}
                  >
                    {item.name}
                  </Button>
                ))}
              </Stack>
            )}
          </div>
          <Group gap="xs" align="flex-end">
            <TextInput
              size="xs"
              style={{ flex: 1 }}
              label="或直接输入基线文件路径"
              placeholder="/path/to/old_version.docx"
              value={compareManualPath}
              onChange={(e) => setCompareManualPath(e.currentTarget.value)}
            />
            <Button
              size="xs"
              loading={loadingBaseline}
              disabled={!compareManualPath.trim()}
              onClick={() => void startCompareWithPath(compareManualPath)}
            >
              开始比对
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
