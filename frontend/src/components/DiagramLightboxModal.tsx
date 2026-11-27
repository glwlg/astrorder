import { useEffect, useMemo, useRef, type TouchEvent } from 'react'
import { Modal } from '@mantine/core'
import { IconDownload, IconLayoutSidebarRight, IconSitemap, IconX } from '@tabler/icons-react'
import { extractSvgIntrinsicSize, PanZoomCanvas, type PanZoomCanvasHandle } from './PanZoomCanvas'
import './panZoomCanvas.css'

export interface DiagramLightboxModalProps {
  opened: boolean
  onClose: () => void
  title?: string
  svg: string
  downloadFilename?: string
  onOpenInSidecar?: () => void
}

export function DiagramLightboxModal({
  opened,
  onClose,
  title = 'Mermaid 架构图',
  svg,
  downloadFilename = 'diagram.svg',
  onOpenInSidecar,
}: DiagramLightboxModalProps) {
  const canvasRef = useRef<PanZoomCanvasHandle>(null)
  const intrinsicSize = useMemo(() => extractSvgIntrinsicSize(svg), [svg])

  useEffect(() => {
    if (!opened) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === '+' || event.key === '=') {
        event.preventDefault()
        canvasRef.current?.zoomIn()
      } else if (event.key === '-' || event.key === '_') {
        event.preventDefault()
        canvasRef.current?.zoomOut()
      } else if (event.key === '0') {
        event.preventDefault()
        canvasRef.current?.fitToViewport()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [opened])

  const handleDownloadSvg = () => {
    if (!svg) return
    const blob = new Blob([svg], { type: 'image/svg+xml;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = downloadFilename.endsWith('.svg') ? downloadFilename : `${downloadFilename}.svg`
    link.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }

  // React synthetic events bubble across Portals through the React tree;
  // isolate modal touch/context interactions from underlying MobileTranscript handlers.
  const stopTouchPropagation = (event: TouchEvent) => {
    event.stopPropagation()
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      centered
      withCloseButton={false}
      size="min(94vw, 1360px)"
      radius="lg"
      zIndex={460}
      className="astr-diagram-modal"
      overlayProps={{ opacity: 0.45, blur: 4 }}
      aria-label={title}
    >
      <div
        className="astr-diagram-modal-shell"
        onTouchStart={stopTouchPropagation}
        onTouchMove={stopTouchPropagation}
        onTouchEnd={stopTouchPropagation}
        onTouchCancel={stopTouchPropagation}
        onContextMenu={(event) => event.stopPropagation()}
      >
        <div className="astr-diagram-modal-header">
          <div className="astr-diagram-modal-title-group">
            <IconSitemap size={16} aria-hidden="true" style={{ color: 'var(--astr-muted, #52525b)', flexShrink: 0 }} />
            <span className="astr-diagram-modal-title">{title}</span>
            <span className="astr-diagram-modal-hint">滚轮缩放 · 拖拽平移 · 双击适应窗口</span>
          </div>
          <div className="astr-diagram-modal-actions">
            {onOpenInSidecar && (
              <button
                type="button"
                className="astr-diagram-modal-btn"
                onClick={() => {
                  onClose()
                  onOpenInSidecar()
                }}
              >
                <IconLayoutSidebarRight size={14} aria-hidden="true" />
                <span>侧栏打开</span>
              </button>
            )}
            <button
              type="button"
              className="astr-diagram-modal-btn"
              onClick={handleDownloadSvg}
            >
              <IconDownload size={14} aria-hidden="true" />
              <span>导出 SVG</span>
            </button>
            <button
              type="button"
              className="astr-diagram-modal-close"
              aria-label="关闭图表预览"
              title="关闭 (Esc)"
              onClick={onClose}
            >
              <IconX size={18} aria-hidden="true" />
            </button>
          </div>
        </div>

        <PanZoomCanvas
          ref={canvasRef}
          dotGrid
          contentSize={intrinsicSize}
          fitKey={opened ? `${title}:${svg.length}` : undefined}
          ariaLabel={`${title}缩放画布`}
        >
          <div className="astr-diagram-svg-host" dangerouslySetInnerHTML={{ __html: svg }} />
        </PanZoomCanvas>
      </div>
    </Modal>
  )
}
