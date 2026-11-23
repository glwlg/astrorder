import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { IconDownload, IconRefresh, IconX } from '@tabler/icons-react'
import { downloadFile } from '../sidecar/fileTransfer'
import { buildAuthenticatedVideoUrl, formatVideoDuration, videoMediaType } from '../sidecar/viewers/video/formats'
import '../sidecar/viewers/video/videoViewer.css'

const RATES = [1, 1.25, 1.5, 2, 0.75] as const

export interface MobileVideoItem {
  url: string
  name: string
}

export function MobileVideoLightbox({
  video,
  onClose,
}: {
  video: MobileVideoItem | null
  onClose: () => void
}) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const [rate, setRate] = useState<number>(1)
  const [revision, setRevision] = useState(0)
  const [meta, setMeta] = useState<{ width: number; height: number; duration: number } | null>(null)
  const [error, setError] = useState('')

  const streamUrl = useMemo(
    () => (video ? buildAuthenticatedVideoUrl(video.url, revision) : ''),
    [revision, video],
  )
  const mimeType = useMemo(
    () => (video ? videoMediaType(video.name) || 'video/mp4' : 'video/mp4'),
    [video],
  )

  useEffect(() => {
    setError('')
    setMeta(null)
    setRate(1)
    setRevision(0)
  }, [video?.url])

  useEffect(() => {
    if (videoRef.current) videoRef.current.playbackRate = rate
  }, [rate, streamUrl])

  useEffect(() => {
    if (!video) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose, video])

  if (!video || typeof document === 'undefined') return null

  const metaParts: string[] = []
  if (meta && meta.width > 0 && meta.height > 0) metaParts.push(`${meta.width}×${meta.height}`)
  if (meta) {
    const dur = formatVideoDuration(meta.duration)
    if (dur) metaParts.push(dur)
  }

  return createPortal(
    <div
      className="m-video-lightbox"
      role="dialog"
      aria-modal="true"
      aria-label={`视频播放：${video.name}`}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose()
      }}
    >
      <header className="m-video-lightbox-bar">
        <div className="m-video-lightbox-title">
          <span className="m-video-lightbox-name">{video.name}</span>
          {metaParts.length > 0 && <span className="m-video-lightbox-meta">{metaParts.join(' · ')}</span>}
        </div>
        <div className="m-video-lightbox-actions">
          <button
            type="button"
            className="m-video-lightbox-btn"
            aria-label={`播放倍速 ${rate}x`}
            onClick={() => setRate((current) => RATES[(RATES.indexOf(current as (typeof RATES)[number]) + 1) % RATES.length])}
          >
            {rate}×
          </button>
          <button
            type="button"
            className="m-video-lightbox-btn"
            aria-label="下载视频"
            onClick={() => void downloadFile(video.url, video.name)}
          >
            <IconDownload size={18} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="m-video-lightbox-btn"
            aria-label="关闭视频"
            onClick={onClose}
          >
            <IconX size={20} aria-hidden="true" />
          </button>
        </div>
      </header>

      <div
        className="m-video-lightbox-stage"
        onClick={(event) => {
          if (event.target === event.currentTarget) onClose()
        }}
      >
        {error ? (
          <div className="m-video-lightbox-error" role="alert">
            <span>{error}</span>
            <div style={{ display: 'flex', gap: 10 }}>
              <button type="button" className="m-video-lightbox-btn" onClick={() => { setError(''); setRevision(v => v + 1) }}>
                <IconRefresh size={16} aria-hidden="true" style={{ marginRight: 6 }} />
                重试
              </button>
              <button type="button" className="m-video-lightbox-btn" onClick={() => void downloadFile(video.url, video.name)}>
                <IconDownload size={16} aria-hidden="true" style={{ marginRight: 6 }} />
                下载
              </button>
            </div>
          </div>
        ) : (
          <video
            key={streamUrl}
            ref={videoRef}
            className="m-video-lightbox-player"
            controls
            playsInline
            preload="metadata"
            width={meta?.width || 1280}
            height={meta?.height || 720}
            aria-label={video.name}
            onLoadedMetadata={(event) => {
              const el = event.currentTarget
              el.playbackRate = rate
              setMeta({
                width: el.videoWidth || 0,
                height: el.videoHeight || 0,
                duration: el.duration,
              })
            }}
            onError={() => setError('视频无法在当前浏览器解码，可点击右上角下载到本地观看')}
          >
            <source src={streamUrl} type={mimeType} />
          </video>
        )}
      </div>
    </div>,
    document.body,
  )
}
