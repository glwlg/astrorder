import { useCallback, useEffect, useMemo, useState } from 'react'
import { ActionIcon, Badge, Button, Group, LoadingOverlay, Text, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconChevronLeft,
  IconChevronRight,
  IconCopy,
  IconDownload,
  IconExternalLink,
  IconLayoutSidebar,
  IconMessageForward,
  IconRefresh,
} from '@tabler/icons-react'
import { api, type PptxPreviewResponse, type PptxSlidePreview } from '../../../../api/client'
import { ExportMenu } from '../../../office/export/ExportMenu'
import { downloadFile } from '../../fileTransfer'
import type { ViewerContext } from '../../types'
import './pptxViewer.css'

function slideToMarkdown(slide: PptxSlidePreview): string {
  const lines: string[] = [`## 第 ${slide.index} 页：${slide.title}`]
  for (const p of slide.paragraphs || []) {
    const indent = '  '.repeat(Math.max(0, p.level || 0))
    lines.push(`${indent}- ${p.text}`)
  }
  for (const tbl of slide.tables || []) {
    if (!tbl.rows.length) continue
    lines.push('')
    tbl.rows.forEach((row, idx) => {
      lines.push(`| ${row.map((c) => c.replace(/\|/g, '\\|')).join(' | ')} |`)
      if (idx === 0) {
        lines.push(`| ${row.map(() => '---').join(' | ')} |`)
      }
    })
  }
  if (slide.notes?.trim()) {
    lines.push('', `> 演讲备注：${slide.notes.trim()}`)
  }
  return lines.join('\n')
}

export function PptxViewer({ artifact, session, toolbarAction, isActive = true }: ViewerContext) {
  const [deck, setDeck] = useState<PptxPreviewResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [showOutline, setShowOutline] = useState(true)
  const [revision, setRevision] = useState(0)

  const filePath = artifact.path || artifact.name

  const loadDeck = useCallback(async () => {
    if (!filePath) {
      setError('缺少 PPT 文件路径')
      setLoading(false)
      return
    }
    setLoading(true)
    setError('')
    try {
      const res = await api.previewPptx({
        path: filePath,
        sessionId: session?.id || artifact.sessionId,
        connectionId: session?.connection_id || artifact.connectionId || null,
      })
      setDeck(res)
      setActiveIndex(0)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '加载 PPT 失败')
    } finally {
      setLoading(false)
    }
  }, [artifact.connectionId, artifact.sessionId, filePath, session?.connection_id, session?.id])

  useEffect(() => {
    void loadDeck()
  }, [loadDeck, revision])

  const slides = deck?.slides || []
  const currentSlide = slides[Math.min(activeIndex, Math.max(0, slides.length - 1))] || null
  const allSlidesMarkdown = useMemo(() => {
    if (!slides.length) return ''
    const header = deck?.title ? `# ${deck.title}\n\n` : ''
    return `${header}${slides.map(slideToMarkdown).join('\n\n')}`
  }, [deck?.title, slides])

  useEffect(() => {
    if (!isActive || !slides.length) return
    const onKeyDown = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement | null
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return
      if (e.key === 'ArrowRight' || e.key === 'PageDown') {
        setActiveIndex((prev) => Math.min(slides.length - 1, prev + 1))
      } else if (e.key === 'ArrowLeft' || e.key === 'PageUp') {
        setActiveIndex((prev) => Math.max(0, prev - 1))
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [isActive, slides.length])

  const quoteToChat = (scope: 'slide' | 'all') => {
    if (!deck || !currentSlide) return
    const text =
      scope === 'slide'
        ? `请参考 PPT《${deck.name}》第 ${currentSlide.index} 页内容：\n\n${slideToMarkdown(currentSlide)}`
        : `请参考 PPT《${deck.name}》（共 ${deck.slide_count} 页）大纲与要点：\n\n${slides.map(slideToMarkdown).join('\n\n')}`
    window.dispatchEvent(
      new CustomEvent('astrorder:quote-to-composer', {
        detail: {
          agentId: session?.agent_id || artifact.agentId,
          sessionId: session?.id || artifact.sessionId,
          text: text.slice(0, 6000),
        },
      }),
    )
    notifications.show({
      color: 'teal',
      message: scope === 'slide' ? `已引用第 ${currentSlide.index} 页到对话` : '已引用整份 PPT 大纲到对话',
    })
  }

  const copyCurrentSlide = async () => {
    if (!currentSlide) return
    try {
      await navigator.clipboard.writeText(slideToMarkdown(currentSlide))
      notifications.show({ color: 'teal', message: `已复制第 ${currentSlide.index} 页内容` })
    } catch {
      notifications.show({ color: 'red', message: '复制失败' })
    }
  }

  const openNative = async () => {
    try {
      await api.openFile(filePath, 'open')
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '无法在系统应用中打开' })
    }
  }

  const isCoverSlide = useMemo(() => {
    if (!currentSlide) return false
    return (
      currentSlide.index === 1 &&
      (currentSlide.paragraphs?.length || 0) <= 2 &&
      (currentSlide.tables?.length || 0) === 0 &&
      (currentSlide.images?.length || 0) === 0
    )
  }, [currentSlide])

  return (
    <div className="pptx-viewer-root" aria-label={`PPT 预览：${artifact.name}`}>
      <div className="pptx-viewer-toolbar">
        <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
          <Tooltip label={showOutline ? '收起页签大纲' : '展开页签大纲'}>
            <ActionIcon
              variant={showOutline ? 'light' : 'subtle'}
              size="sm"
              aria-label="切换页签大纲"
              onClick={() => setShowOutline((v) => !v)}
            >
              <IconLayoutSidebar size={15} />
            </ActionIcon>
          </Tooltip>
          <Text size="xs" fw={600} truncate title={artifact.name} maw={220}>
            {deck?.title || artifact.name}
          </Text>
          {deck && (
            <Badge size="xs" variant="light" color="orange">
              {activeIndex + 1} / {deck.slide_count} 页
            </Badge>
          )}
        </Group>

        <Group gap={4} wrap="nowrap">
          {slides.length > 1 && (
            <>
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="上一页"
                disabled={activeIndex <= 0}
                onClick={() => setActiveIndex((prev) => Math.max(0, prev - 1))}
              >
                <IconChevronLeft size={15} />
              </ActionIcon>
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="下一页"
                disabled={activeIndex >= slides.length - 1}
                onClick={() => setActiveIndex((prev) => Math.min(slides.length - 1, prev + 1))}
              >
                <IconChevronRight size={15} />
              </ActionIcon>
            </>
          )}
          <Button
            size="compact-xs"
            variant="light"
            color="indigo"
            leftSection={<IconMessageForward size={13} />}
            disabled={!currentSlide}
            onClick={() => quoteToChat('slide')}
          >
            引用当前页
          </Button>
          <Button
            size="compact-xs"
            variant="subtle"
            color="gray"
            disabled={!slides.length}
            onClick={() => quoteToChat('all')}
          >
            引用整份
          </Button>
          <Tooltip label="复制当前页">
            <ActionIcon variant="subtle" size="sm" aria-label="复制当前页" disabled={!currentSlide} onClick={() => void copyCurrentSlide()}>
              <IconCopy size={14} />
            </ActionIcon>
          </Tooltip>
          <ExportMenu
            markdown={allSlidesMarkdown}
            title={deck?.title || artifact.name}
            fileName={(deck?.name || artifact.name).replace(/\.pptx$/i, '')}
            workspace={session?.workspace}
            sessionId={session?.id || artifact.sessionId}
            connectionId={session?.connection_id || artifact.connectionId}
            variant="button"
            buttonLabel="导出"
          />
          {toolbarAction}
          <Tooltip label="重新加载">
            <ActionIcon variant="subtle" size="sm" aria-label="重新加载" onClick={() => setRevision((r) => r + 1)}>
              <IconRefresh size={14} />
            </ActionIcon>
          </Tooltip>
          {(!artifact.connectionId || artifact.connectionId === 'local') && (
            <Tooltip label="系统默认应用打开">
              <ActionIcon variant="subtle" size="sm" aria-label="外部打开" onClick={() => void openNative()}>
                <IconExternalLink size={14} />
              </ActionIcon>
            </Tooltip>
          )}
          {artifact.readUrl && (
            <Tooltip label="下载">
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="下载文件"
                onClick={() => void downloadFile(`${artifact.readUrl}&download=1`, artifact.name)}
              >
                <IconDownload size={14} />
              </ActionIcon>
            </Tooltip>
          )}
        </Group>
      </div>

      <div className="pptx-viewer-body">
        <LoadingOverlay visible={loading} />
        {error ? (
          <div style={{ padding: 28, textAlign: 'center', width: '100%' }}>
            <Text size="sm" c="dimmed">{error}</Text>
            <Button size="xs" mt="md" variant="light" onClick={() => setRevision((r) => r + 1)}>
              重试
            </Button>
          </div>
        ) : (
          <>
            {showOutline && slides.length > 0 && (
              <aside className="pptx-outline-rail" aria-label="幻灯片页签">
                {slides.map((sl, idx) => (
                  <button
                    key={sl.index}
                    type="button"
                    className={`pptx-thumb-btn${idx === activeIndex ? ' is-active' : ''}`}
                    onClick={() => setActiveIndex(idx)}
                  >
                    <div className="pptx-thumb-meta">
                      <span>第 {sl.index} 页</span>
                      <span>
                        {sl.tables?.length ? '表 ' : ''}
                        {sl.images?.length ? '图' : ''}
                      </span>
                    </div>
                    <div className="pptx-thumb-title">{sl.title}</div>
                  </button>
                ))}
              </aside>
            )}

            <main className="pptx-stage-scroll">
              {currentSlide && (
                <>
                  <section className={`pptx-slide-card${isCoverSlide ? ' is-cover' : ''}`} aria-label={`第 ${currentSlide.index} 页`}>
                    <div className="pptx-slide-top-accent" aria-hidden="true" />
                    <h2 className="pptx-slide-title">{currentSlide.title}</h2>

                    {currentSlide.paragraphs.length > 0 && (
                      <div className="pptx-slide-paragraphs">
                        {currentSlide.paragraphs.map((p, idx) => (
                          <p
                            key={idx}
                            className={`pptx-slide-para${p.bold ? ' is-bold' : ''}`}
                            style={{ paddingLeft: (p.level || 0) * 20 }}
                          >
                            {p.text}
                          </p>
                        ))}
                      </div>
                    )}

                    {(currentSlide.tables || []).map((tbl, ti) => (
                      <div key={ti} className="pptx-slide-table-wrap">
                        <table className="pptx-slide-table">
                          {tbl.rows.length > 0 && (
                            <thead>
                              <tr>
                                {tbl.rows[0].map((cell, ci) => (
                                  <th key={ci}>{cell}</th>
                                ))}
                              </tr>
                            </thead>
                          )}
                          {tbl.rows.length > 1 && (
                            <tbody>
                              {tbl.rows.slice(1).map((row, ri) => (
                                <tr key={ri}>
                                  {row.map((cell, ci) => (
                                    <td key={ci}>{cell}</td>
                                  ))}
                                </tr>
                              ))}
                            </tbody>
                          )}
                        </table>
                      </div>
                    ))}

                    {(currentSlide.images || []).length > 0 && (
                      <div className="pptx-slide-images">
                        {currentSlide.images!.map((uri, ii) => (
                          <img key={ii} src={uri} alt={`幻灯片插图 ${ii + 1}`} />
                        ))}
                      </div>
                    )}

                    <div className="pptx-slide-footer">
                      {currentSlide.index} / {deck?.slide_count}
                    </div>
                  </section>

                  {currentSlide.notes?.trim() && (
                    <section className="pptx-notes-panel" aria-label="演讲备注">
                      <h4>演讲备注</h4>
                      <p>{currentSlide.notes}</p>
                    </section>
                  )}
                </>
              )}
            </main>
          </>
        )}
      </div>
    </div>
  )
}
