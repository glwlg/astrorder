import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, Tooltip } from '@mantine/core'
import { IconDeviceFloppy, IconDownload, IconMaximize, IconRefresh } from '@tabler/icons-react'
import { downloadFile } from '../../fileTransfer'
import type { ViewerContext } from '../../types'
import { renderMermaid } from '../../../../components/renderMermaid'
import { authenticatedApiFetch } from '../../../../api/client'
import { extractSvgIntrinsicSize, PanZoomCanvas, type PanZoomCanvasHandle } from '../../../../components/PanZoomCanvas'
import { DiagramLightboxModal } from '../../../../components/DiagramLightboxModal'

export function MermaidViewer({ artifact, onSave, onDirtyChange, toolbarAction, isActive = true }: ViewerContext) {
  const canvasRef = useRef<PanZoomCanvasHandle>(null)
  const onDirtyChangeRef = useRef(onDirtyChange)
  onDirtyChangeRef.current = onDirtyChange

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [code, setCode] = useState('')
  const [renderedSvg, setRenderedSvg] = useState('')
  const [fitVersion, setFitVersion] = useState(0)
  const [isEditing, setIsEditing] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [lightboxOpen, setLightboxOpen] = useState(false)
  const initialCodeRef = useRef('')
  const intrinsicSize = useMemo(() => extractSvgIntrinsicSize(renderedSvg), [renderedSvg])

  const renderChart = useCallback(async (chartCode: string, autoFit = false) => {
    setError(null)
    try {
      const { svg } = await renderMermaid(chartCode)
      setRenderedSvg(svg)
      if (autoFit) {
        setFitVersion((v) => v + 1)
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Mermaid 图表语法解析错误'
      setError(msg)
    }
  }, [])

  const inlineContent = typeof artifact.metadata?.inlineContent === 'string' ? artifact.metadata.inlineContent : null

  const fetchContent = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      let text = ''
      if (inlineContent !== null) {
        text = inlineContent
      } else {
        const res = await authenticatedApiFetch(artifact.readUrl)
        if (!res.ok) throw new Error(`读取失败 (${res.status})`)
        text = await res.text()
      }
      setCode(text)
      initialCodeRef.current = text
      setDirty(false)
      onDirtyChangeRef.current?.(false)
      await renderChart(text, true)
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : '文件读取失败'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }, [artifact.readUrl, inlineContent, renderChart])

  useEffect(() => {
    void fetchContent()
  }, [fetchContent])

  const handleCodeChange = (newVal: string) => {
    setCode(newVal)
    const isNowDirty = newVal !== initialCodeRef.current
    setDirty(isNowDirty)
    onDirtyChangeRef.current?.(isNowDirty)
    void renderChart(newVal, false)
  }

  const handleSave = useCallback(async () => {
    if (!onSave || !artifact.writable) return
    setSaving(true)
    try {
      const ok = await onSave(code)
      if (ok) {
        initialCodeRef.current = code
        setDirty(false)
        onDirtyChangeRef.current?.(false)
      }
    } finally {
      setSaving(false)
    }
  }, [artifact.writable, code, onSave])

  const handleDownloadSource = () => {
    if (artifact.readUrl) {
      const sep = artifact.readUrl.includes('?') ? '&' : '?'
      void downloadFile(`${artifact.readUrl}${sep}download=1`, artifact.name)
      return
    }
    const blob = new Blob([code], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = artifact.name || 'diagram.mmd'
    link.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  // 监听 Ctrl+S 快捷键保存
  useEffect(() => {
    if (!isActive) return
    const onKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && (e.key === 's' || e.key === 'S')) {
        e.preventDefault()
        e.stopPropagation()
        if (dirty && !saving) {
          void handleSave()
        }
      }
    }
    window.addEventListener('keydown', onKeyDown, { capture: true })
    return () => {
      window.removeEventListener('keydown', onKeyDown, { capture: true })
    }
  }, [dirty, saving, handleSave, isActive])

  return (
    <div className="mermaid-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0 }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0, flex: 'none' }}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            <Text size="xs" fw={600} truncate title={artifact.name}>
              {artifact.name}
            </Text>
            {dirty && (
              <Text size="xs" c="yellow" fw={500}>
                ● 未保存
              </Text>
            )}
          </Group>
          <Group gap={6} wrap="nowrap">
            {toolbarAction}
            <Button
              size="compact-xs"
              variant={isEditing ? 'light' : 'subtle'}
              onClick={() => setIsEditing(!isEditing)}
            >
              {isEditing ? '隐藏代码' : '编辑代码'}
            </Button>
            {artifact.writable && onSave && (
              <Tooltip label="保存修改到工作区">
                <Button
                  size="compact-xs"
                  variant="filled"
                  color="blue"
                  leftSection={<IconDeviceFloppy size={13} />}
                  disabled={!dirty || saving}
                  loading={saving}
                  onClick={() => void handleSave()}
                >
                  保存
                </Button>
              </Tooltip>
            )}
            <Tooltip label="弹窗放大查看">
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="弹窗放大查看图表"
                disabled={!renderedSvg}
                onClick={() => setLightboxOpen(true)}
              >
                <IconMaximize size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="重新加载">
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="重新加载"
                onClick={() => void fetchContent()}
              >
                <IconRefresh size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="下载源文件">
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="下载源文件"
                onClick={handleDownloadSource}
              >
                <IconDownload size={14} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </Paper>

      {isEditing && (
        <div style={{ borderBottom: '1px solid var(--astr-border)', padding: 8, background: 'var(--astr-surface-muted)', flex: 'none' }}>
          <textarea
            value={code}
            onChange={(e) => handleCodeChange(e.target.value)}
            style={{
              width: '100%',
              height: 120,
              fontFamily: 'monospace',
              fontSize: 12,
              padding: 6,
              borderRadius: 4,
              border: '1px solid var(--astr-border)',
              background: 'var(--astr-surface)',
              color: 'var(--astr-text)',
              resize: 'vertical',
            }}
          />
        </div>
      )}

      <div style={{ flex: 1, position: 'relative', minHeight: 0, display: 'flex', flexDirection: 'column' }}>
        <LoadingOverlay visible={loading} />
        {error && (
          <Paper p="sm" withBorder m="sm" style={{ background: '#fff0f0', borderColor: '#ffc0c0', color: '#c00', flex: 'none' }}>
            <Text size="xs" fw={500}>{error}</Text>
          </Paper>
        )}
        <PanZoomCanvas
          ref={canvasRef}
          dotGrid
          contentSize={intrinsicSize}
          fitKey={fitVersion}
          ariaLabel="Mermaid 缩放画布"
        >
          <div
            className="astr-diagram-svg-host"
            dangerouslySetInnerHTML={{ __html: renderedSvg }}
          />
        </PanZoomCanvas>
      </div>

      <DiagramLightboxModal
        opened={lightboxOpen}
        onClose={() => setLightboxOpen(false)}
        title={artifact.name || 'Mermaid 架构图'}
        svg={renderedSvg}
        downloadFilename={artifact.name.replace(/\.mmd$/i, '') + '.svg'}
      />
    </div>
  )
}
