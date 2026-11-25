import { useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Button, Group, Paper, Text, Tooltip } from '@mantine/core'
import { IconDownload, IconPictureInPictureOn, IconRefresh } from '@tabler/icons-react'
import { downloadFile } from '../../fileTransfer'
import type { ViewerContext } from '../../types'
import { buildAuthenticatedVideoUrl, formatVideoDuration, videoMediaType } from './formats'
import './videoViewer.css'

const PLAYBACK_RATES = [0.5, 0.75, 1, 1.25, 1.5, 2] as const

export function VideoViewer({ artifact, toolbarAction }: ViewerContext) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const [revision, setRevision] = useState(0)
  const [playbackRate, setPlaybackRate] = useState<number>(1)
  const [meta, setMeta] = useState<{ width: number; height: number; duration: number } | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const streamUrl = useMemo(
    () => buildAuthenticatedVideoUrl(artifact.readUrl, revision),
    [artifact.readUrl, revision],
  )
  const mimeType = useMemo(
    () => videoMediaType(artifact.name) || (artifact.mediaType.startsWith('video/') ? artifact.mediaType : 'video/mp4'),
    [artifact.mediaType, artifact.name],
  )
  const canPiP = typeof document !== 'undefined' && 'pictureInPictureEnabled' in document && Boolean(document.pictureInPictureEnabled)

  useEffect(() => {
    setError('')
    setMeta(null)
    setLoading(true)
  }, [streamUrl])

  useEffect(() => {
    const el = videoRef.current
    if (el) {
      el.playbackRate = playbackRate
    }
  }, [playbackRate, streamUrl])

  const cycleRate = () => {
    setPlaybackRate((current) => {
      const idx = PLAYBACK_RATES.indexOf(current as (typeof PLAYBACK_RATES)[number])
      return PLAYBACK_RATES[(idx + 1) % PLAYBACK_RATES.length]
    })
  }

  const togglePiP = async () => {
    const el = videoRef.current as (HTMLVideoElement & { requestPictureInPicture?: () => Promise<unknown> }) | null
    if (!el || typeof el.requestPictureInPicture !== 'function') return
    try {
      const doc = document as Document & { pictureInPictureElement?: Element | null; exitPictureInPicture?: () => Promise<void> }
      if (doc.pictureInPictureElement === el && typeof doc.exitPictureInPicture === 'function') {
        await doc.exitPictureInPicture()
      } else {
        await el.requestPictureInPicture()
      }
    } catch {
      // Ignore user-cancelled or browser-blocked PiP requests.
    }
  }

  const metaSummary = useMemo(() => {
    if (!meta) return ''
    const parts: string[] = []
    if (meta.width > 0 && meta.height > 0) parts.push(`${meta.width}×${meta.height}`)
    const dur = formatVideoDuration(meta.duration)
    if (dur) parts.push(dur)
    return parts.join(' · ')
  }, [meta])

  return (
    <div className="sidecar-video-viewer">
      <Paper
        p="xs"
        withBorder
        className="sidecar-video-toolbar"
      >
        <div className="sidecar-video-toolbar-row">
          <div className="sidecar-video-title-group">
            <Text size="xs" fw={600} className="sidecar-video-filename" title={artifact.path || artifact.name}>
              {artifact.name}
            </Text>
            {metaSummary && (
              <span className="sidecar-video-meta-badge">{metaSummary}</span>
            )}
          </div>
          <Group gap={6} wrap="wrap" className="sidecar-video-actions">
            {toolbarAction}
            <Tooltip label="切换播放倍速">
              <Button
                size="compact-xs"
                variant="subtle"
                className="sidecar-video-control-btn"
                aria-label={`播放倍速 ${playbackRate}x`}
                onClick={cycleRate}
              >
                {playbackRate}×
              </Button>
            </Tooltip>
            {canPiP && (
              <Tooltip label="画中画">
                <ActionIcon
                  variant="subtle"
                  className="sidecar-video-control-btn"
                  aria-label="画中画"
                  onClick={() => void togglePiP()}
                >
                  <IconPictureInPictureOn size={15} aria-hidden="true" />
                </ActionIcon>
              </Tooltip>
            )}
            <Tooltip label="重新加载">
              <ActionIcon
                variant="subtle"
                className="sidecar-video-control-btn"
                aria-label="刷新视频"
                onClick={() => setRevision((v) => v + 1)}
              >
                <IconRefresh size={15} aria-hidden="true" />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="下载视频">
              <ActionIcon
                variant="subtle"
                className="sidecar-video-control-btn"
                aria-label="下载视频"
                onClick={() => void downloadFile(artifact.readUrl, artifact.name)}
              >
                <IconDownload size={15} aria-hidden="true" />
              </ActionIcon>
            </Tooltip>
          </Group>
        </div>
      </Paper>

      <div className="sidecar-video-stage">
        {error ? (
          <div className="sidecar-video-error" role="alert">
            <Text size="sm" fw={500}>{error}</Text>
            <Text size="xs" c="dimmed" mt={6}>
              若视频使用浏览器不支持的封装或编码（如部分 MKV / HEVC），可直接下载后使用本地播放器观看。
            </Text>
            <Group justify="center" gap="xs" mt="md">
              <Button size="xs" variant="light" onClick={() => setRevision((v) => v + 1)}>
                重试播放
              </Button>
              <Button
                size="xs"
                variant="default"
                leftSection={<IconDownload size={14} aria-hidden="true" />}
                onClick={() => void downloadFile(artifact.readUrl, artifact.name)}
              >
                下载视频
              </Button>
            </Group>
          </div>
        ) : (
          <div className="sidecar-video-frame">
            <video
              key={streamUrl}
              ref={videoRef}
              className="sidecar-video-element"
              controls
              playsInline
              preload="metadata"
              width={meta?.width || 1280}
              height={meta?.height || 720}
              aria-label={`视频播放器：${artifact.name}`}
              onLoadedMetadata={(event) => {
                const el = event.currentTarget
                el.playbackRate = playbackRate
                setMeta({
                  width: el.videoWidth || 0,
                  height: el.videoHeight || 0,
                  duration: el.duration,
                })
                setLoading(false)
              }}
              onCanPlay={() => setLoading(false)}
              onError={() => {
                setLoading(false)
                setError('视频加载或解码失败，文件可能已损坏或编码格式不受当前浏览器支持')
              }}
            >
              <source src={streamUrl} type={mimeType} />
            </video>
            {loading && (
              <div className="sidecar-video-loading-pill" aria-live="polite">
                正在读取视频元数据…
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
