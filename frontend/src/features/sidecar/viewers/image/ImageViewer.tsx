import { useEffect, useRef, useState } from 'react'
import { ActionIcon, Button, Group, LoadingOverlay, Paper, Text, Tooltip } from '@mantine/core'
import { IconDownload, IconMaximize, IconRefresh } from '@tabler/icons-react'
import { authenticatedApiFetch } from '../../../../api/client'
import { ImageLightbox } from '../../../../components/ImageLightbox'
import { PanZoomCanvas, type IntrinsicSize, type PanZoomCanvasHandle } from '../../../../components/PanZoomCanvas'
import { downloadFile } from '../../fileTransfer'
import type { ViewerContext } from '../../types'
import { imageMediaType } from './formats'

export function ImageViewer({ artifact, toolbarAction }: ViewerContext) {
  const canvasRef = useRef<PanZoomCanvasHandle>(null)
  const [source, setSource] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)
  const [actualSize, setActualSize] = useState(false)
  const [naturalSize, setNaturalSize] = useState<IntrinsicSize | null>(null)
  const [fitVersion, setFitVersion] = useState(0)
  const [lightboxOpen, setLightboxOpen] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    let objectUrl = ''
    setLoading(true)
    setError('')
    setSource('')
    void (async () => {
      try {
        const response = await authenticatedApiFetch(artifact.readUrl, { signal: controller.signal })
        if (!response.ok) throw new Error(`读取图片失败 (${response.status})`)
        const blob = await response.blob()
        if (controller.signal.aborted) return
        const type = imageMediaType(artifact.name) || (blob.type.startsWith('image/') ? blob.type : artifact.mediaType.startsWith('image/') ? artifact.mediaType : 'application/octet-stream')
        objectUrl = URL.createObjectURL(new Blob([blob], { type }))
        setSource(objectUrl)
      } catch (reason) {
        if (controller.signal.aborted) return
        setError(reason instanceof Error ? reason.message : '图片读取失败')
        setLoading(false)
      }
    })()
    return () => {
      controller.abort()
      if (objectUrl) URL.revokeObjectURL(objectUrl)
    }
  }, [artifact.readUrl, artifact.name, artifact.mediaType, revision])

  const handleToggleActualSize = () => {
    setActualSize((prev) => {
      const next = !prev
      if (next) {
        canvasRef.current?.reset()
      } else {
        canvasRef.current?.fitToViewport()
      }
      return next
    })
  }

  return (
    <div className="sidecar-image-viewer" style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0, position: 'relative' }}>
      <Paper p="xs" withBorder className="sidecar-image-toolbar" style={{ flex: 'none', margin: '8px 8px 0', zIndex: 10, borderRadius: 10 }}>
        <Group justify="space-between" wrap="nowrap" gap="xs">
          <Text size="xs" fw={600} truncate title={artifact.path || artifact.name} style={{ minWidth: 0 }}>{artifact.name}</Text>
          <Group gap="xs" wrap="nowrap" style={{ flexShrink: 0 }}>
            {toolbarAction}
            <Button size="compact-xs" variant="subtle" onClick={handleToggleActualSize}>
              {actualSize ? '适应窗口' : '原始尺寸'}
            </Button>
            <Tooltip label="弹窗放大">
              <ActionIcon
                variant="subtle"
                aria-label="弹窗放大图片"
                disabled={!source || Boolean(error)}
                onClick={() => setLightboxOpen(true)}
              >
                <IconMaximize size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="刷新">
              <ActionIcon variant="subtle" aria-label="刷新图片" onClick={() => setRevision((value) => value + 1)}>
                <IconRefresh size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="下载">
              <ActionIcon variant="subtle" aria-label="下载图片" onClick={() => void downloadFile(artifact.readUrl, artifact.name)}>
                <IconDownload size={14} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </Paper>

      <div className="sidecar-image-canvas" style={{ flex: 1, minHeight: 0, position: 'relative', display: 'flex', overflow: 'hidden' }}>
        <LoadingOverlay visible={loading} />
        {error ? (
          <div className="sidecar-image-error" style={{ margin: 'auto', textAlign: 'center', maxWidth: '100%', padding: 16 }}>
            <Text size="sm">{error}</Text>
            <Button size="xs" mt="md" variant="light" onClick={() => setRevision((value) => value + 1)}>重试</Button>
          </div>
        ) : source ? (
          <PanZoomCanvas
            ref={canvasRef}
            dotGrid
            contentSize={naturalSize}
            fitKey={fitVersion}
            ariaLabel={`${artifact.name} 缩放画布`}
          >
            <img
              src={source}
              alt={artifact.name}
              draggable={false}
              style={
                naturalSize
                  ? {
                      display: 'block',
                      width: '100%',
                      height: '100%',
                      objectFit: 'contain',
                    }
                  : {
                      display: 'block',
                      width: 'auto',
                      height: 'auto',
                      maxWidth: actualSize ? 'none' : '82vw',
                      maxHeight: actualSize ? 'none' : '82vh',
                      objectFit: 'contain',
                    }
              }
              onLoad={(event) => {
                setLoading(false)
                const img = event.currentTarget
                if (img.naturalWidth > 0 && img.naturalHeight > 0) {
                  setNaturalSize({ width: img.naturalWidth, height: img.naturalHeight })
                } else {
                  setNaturalSize(null)
                }
                setFitVersion((v) => v + 1)
              }}
              onError={() => {
                setLoading(false)
                setError('图片解码失败，文件可能已损坏或格式不受支持')
              }}
            />
          </PanZoomCanvas>
        ) : null}
      </div>

      {lightboxOpen && source && (
        <ImageLightbox src={source} onClose={() => setLightboxOpen(false)} />
      )}
    </div>
  )
}
