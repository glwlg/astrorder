import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { ActionIcon, Badge, Button, Checkbox, Group, Paper, Text, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconBookmarkPlus,
  IconCamera,
  IconChevronLeft,
  IconChevronRight,
  IconDownload,
  IconMessagePlus,
  IconPhoto,
  IconPictureInPictureOn,
  IconPlayerSkipBack,
  IconPlayerSkipForward,
  IconRefresh,
  IconRepeat,
  IconSend,
  IconSubtitles,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import { authenticatedApiFetch } from '../../../../api/client'
import { downloadFile } from '../../fileTransfer'
import { resolveArtifactFromPath } from '../../resolver'
import { useSidecarStore } from '../../sidecarStore'
import type { ViewerContext } from '../../types'
import { buildAuthenticatedVideoUrl, formatVideoDuration, videoMediaType } from './formats'
import {
  captureVideoFrameAsPng,
  cuesToWebVtt,
  findActiveCueIndex,
  formatFrameFileName,
  formatTimecode,
  parseSubtitleText,
  type SubtitleCue,
} from './subtitles'
import './videoViewer.css'

const PLAYBACK_RATES = [0.5, 0.75, 1, 1.25, 1.5, 2] as const
const FRAME_STEP_SECONDS = 1 / 24
const NOTES_STORAGE_PREFIX = 'astrorder:video-notes:v1:'

interface ReviewNote {
  id: string
  time: number
  text: string
  thumbDataUrl?: string
  createdAt: number
}

interface SiblingAsset {
  name: string
  path: string
}

function parentDirOf(filePath?: string): string {
  if (!filePath) return ''
  const norm = filePath.replace(/[\\/]+$/, '')
  const idx = Math.max(norm.lastIndexOf('/'), norm.lastIndexOf('\\'))
  return idx > 0 ? norm.slice(0, idx) : ''
}

function fileStem(name: string): string {
  return name.replace(/\.[^./\\]+$/, '').toLowerCase()
}

function notesStorageKey(artifact: ViewerContext['artifact']): string {
  return `${NOTES_STORAGE_PREFIX}${encodeURIComponent((artifact.path || artifact.name || '').replace(/\\/g, '/').toLowerCase())}`
}

function loadPersistedNotes(key: string): ReviewNote[] {
  try {
    const raw = localStorage.getItem(key)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter(
      (item): item is ReviewNote =>
        Boolean(item)
        && typeof item === 'object'
        && typeof (item as ReviewNote).id === 'string'
        && typeof (item as ReviewNote).time === 'number'
        && typeof (item as ReviewNote).text === 'string',
    )
  } catch {
    return []
  }
}

function persistNotes(key: string, notes: ReviewNote[]): void {
  try {
    // Keep storage light: omit dataUrl if storage quota is tight
    localStorage.setItem(key, JSON.stringify(notes))
  } catch {
    try {
      const stripped = notes.map(({ thumbDataUrl: _drop, ...rest }) => rest)
      localStorage.setItem(key, JSON.stringify(stripped))
    } catch {
      // Ignore storage quota failures
    }
  }
}

function dataUrlToPngFile(dataUrl: string, fileName: string): File | null {
  try {
    const base64 = dataUrl.split(',')[1]
    if (!base64) return null
    const binary = atob(base64)
    const bytes = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
    return new File([bytes], fileName, { type: 'image/png' })
  } catch {
    return null
  }
}

export function VideoViewer({ artifact, session, isActive = true, toolbarAction }: ViewerContext) {
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const noteInputRef = useRef<HTMLInputElement | null>(null)
  const resumeTimeRef = useRef<number | null>(null)
  const resumeShouldPlayRef = useRef<boolean>(false)
  const noteFilesRef = useRef<Map<string, File>>(new Map())
  const noteInputId = useId()
  const subtitleSearchId = useId()

  const [revision, setRevision] = useState(0)
  const [playbackRate, setPlaybackRate] = useState<number>(1)
  const [meta, setMeta] = useState<{ width: number; height: number; duration: number } | null>(null)
  const [currentTime, setCurrentTime] = useState(0)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [capturing, setCapturing] = useState(false)

  // A-B loop state
  const [loopA, setLoopA] = useState<number | null>(null)
  const [loopB, setLoopB] = useState<number | null>(null)
  const [loopActive, setLoopActive] = useState(false)

  // Side/bottom drawer: closed | 'notes' | 'subtitles'
  const [drawerTab, setDrawerTab] = useState<'closed' | 'notes' | 'subtitles'>('closed')
  // Default OFF to avoid overlapping with burned-in subtitles on rendered videos
  const [showCaptions, setShowCaptions] = useState(false)
  const [subtitleQuery, setSubtitleQuery] = useState('')

  // Sibling subtitles & reference stills (poster / storyboard)
  const [subtitleFile, setSubtitleFile] = useState<SiblingAsset | null>(null)
  const [subtitleCues, setSubtitleCues] = useState<SubtitleCue[]>([])
  const [vttTrackUrl, setVttTrackUrl] = useState<string>('')
  const [siblingImages, setSiblingImages] = useState<SiblingAsset[]>([])

  // Review notes
  const storageKey = useMemo(() => notesStorageKey(artifact), [artifact])
  const [notes, setNotes] = useState<ReviewNote[]>(() => loadPersistedNotes(storageKey))
  const [noteDraft, setNoteDraft] = useState('')
  const [noteAttachFrame, setNoteAttachFrame] = useState(true)

  const openArtifact = useSidecarStore((s) => s.openArtifact)

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
    setNotes(loadPersistedNotes(storageKey))
    noteFilesRef.current.clear()
  }, [storageKey])

  useEffect(() => {
    setError('')
    setLoading(true)
    if (resumeTimeRef.current === null) {
      setMeta(null)
      setCurrentTime(0)
      setLoopA(null)
      setLoopB(null)
      setLoopActive(false)
    }
  }, [streamUrl])

  useEffect(() => {
    const el = videoRef.current
    if (el) {
      el.playbackRate = playbackRate
    }
  }, [playbackRate, streamUrl])

  // Auto-discover sibling .srt/.vtt subtitles and storyboard/poster stills in the same film directory
  useEffect(() => {
    const parent = parentDirOf(artifact.path)
    if (!parent || !artifact.sessionId) {
      setSubtitleFile(null)
      setSubtitleCues([])
      setSiblingImages([])
      return
    }
    let cancelled = false
    const treeUrl = `/api/v1/files/tree?depth=1&path=${encodeURIComponent(parent)}&session_id=${encodeURIComponent(artifact.sessionId)}&connection_id=${encodeURIComponent(artifact.connectionId || '')}`
    void authenticatedApiFetch(treeUrl)
      .then((res) => (res.ok ? res.json() : null))
      .then(async (data) => {
        if (cancelled || !data || !Array.isArray(data.items)) return
        const files: SiblingAsset[] = data.items
          .filter((item: { is_dir?: boolean; name?: string; path?: string }) => !item.is_dir && item.name && item.path)
          .map((item: { name: string; path: string }) => ({ name: item.name, path: item.path }))

        const videoStem = fileStem(artifact.name)
        const subCandidates = files.filter((f) => /\.(srt|vtt)$/i.test(f.name))
        subCandidates.sort((a, b) => {
          const score = (item: SiblingAsset) => {
            const stem = fileStem(item.name)
            if (stem === videoStem) return 0
            if (stem.startsWith('subtitles')) return 1
            return 2
          }
          return score(a) - score(b) || a.name.localeCompare(b.name)
        })

        const refImages = files
          .filter((f) => /^(poster|storyboard)/i.test(f.name) && /\.(jpe?g|png|webp)$/i.test(f.name))
          .sort((a, b) => a.name.localeCompare(b.name))
          .slice(0, 3)

        if (!cancelled) {
          setSiblingImages(refImages)
        }

        const chosenSub = subCandidates[0] || null
        if (!chosenSub) {
          if (!cancelled) {
            setSubtitleFile(null)
            setSubtitleCues([])
          }
          return
        }

        if (!cancelled) setSubtitleFile(chosenSub)
        const rawUrl = `/api/v1/files/raw?path=${encodeURIComponent(chosenSub.path)}&session_id=${encodeURIComponent(artifact.sessionId)}&connection_id=${encodeURIComponent(artifact.connectionId || '')}`
        const subRes = await authenticatedApiFetch(rawUrl)
        if (!subRes.ok || cancelled) return
        const subText = await subRes.text()
        if (!cancelled) {
          setSubtitleCues(parseSubtitleText(subText))
        }
      })
      .catch(() => {
        if (!cancelled) {
          setSubtitleFile(null)
          setSubtitleCues([])
          setSiblingImages([])
        }
      })
    return () => {
      cancelled = true
    }
  }, [artifact.connectionId, artifact.name, artifact.path, artifact.sessionId, revision])

  // Build WebVTT Blob URL for native <track kind="captions">
  useEffect(() => {
    if (subtitleCues.length === 0 || typeof URL === 'undefined' || typeof URL.createObjectURL !== 'function') {
      setVttTrackUrl('')
      return
    }
    const blob = new Blob([cuesToWebVtt(subtitleCues)], { type: 'text/vtt;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    setVttTrackUrl(url)
    return () => {
      URL.revokeObjectURL(url)
    }
  }, [subtitleCues])

  // Synchronize native <track> showing/disabled state
  useEffect(() => {
    const el = videoRef.current
    if (!el || !el.textTracks || el.textTracks.length === 0) return
    for (let i = 0; i < el.textTracks.length; i += 1) {
      el.textTracks[i].mode = showCaptions ? 'showing' : 'disabled'
    }
  }, [showCaptions, vttTrackUrl, streamUrl])

  const cycleRate = () => {
    setPlaybackRate((current) => {
      const idx = PLAYBACK_RATES.indexOf(current as (typeof PLAYBACK_RATES)[number])
      return PLAYBACK_RATES[(idx + 1) % PLAYBACK_RATES.length]
    })
  }

  const seekTo = useCallback((targetSeconds: number, pauseAfter = false) => {
    const el = videoRef.current
    if (!el) return
    const dur = Number.isFinite(el.duration) && el.duration > 0 ? el.duration : Number.MAX_SAFE_INTEGER
    const clamped = Math.min(Math.max(0, targetSeconds), dur)
    if (pauseAfter && !el.paused) {
      el.pause()
    }
    el.currentTime = clamped
    setCurrentTime(clamped)
  }, [])

  const stepFrame = useCallback(
    (frames: number) => {
      const el = videoRef.current
      if (!el) return
      seekTo(el.currentTime + frames * FRAME_STEP_SECONDS, true)
    },
    [seekTo],
  )

  const handleRefreshPreserveTime = useCallback(() => {
    const el = videoRef.current
    if (el && Number.isFinite(el.currentTime) && el.currentTime > 0) {
      resumeTimeRef.current = el.currentTime
      resumeShouldPlayRef.current = !el.paused
    } else {
      resumeTimeRef.current = null
      resumeShouldPlayRef.current = false
    }
    setRevision((v) => v + 1)
  }, [])

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

  const handleQuoteCurrentFrame = useCallback(async () => {
    const el = videoRef.current
    const t = el?.currentTime ?? currentTime
    const tc = formatTimecode(t)
    const rangeSuffix =
      loopA !== null && loopB !== null
        ? `（关注区间 ${formatTimecode(loopA)}–${formatTimecode(loopB)}）`
        : ''
    const quoteText = `[${artifact.name} @ ${tc}]${rangeSuffix} `

    setCapturing(true)
    try {
      let files: File[] | undefined
      if (el && el.videoWidth > 0 && el.videoHeight > 0) {
        const captured = await captureVideoFrameAsPng(el, artifact.name, t)
        if (captured) {
          files = [captured.file]
        }
      }
      window.dispatchEvent(
        new CustomEvent('astrorder:quote-to-composer', {
          detail: {
            agentId: artifact.agentId,
            sessionId: artifact.sessionId,
            text: quoteText,
            files,
          },
        }),
      )
      notifications.show({
        color: 'teal',
        message: files
          ? `已截取 ${tc} 画面并附带时间码填入对话框`
          : `已引用时间码 ${tc} 到对话框`,
      })
    } finally {
      setCapturing(false)
    }
  }, [artifact.agentId, artifact.name, artifact.sessionId, currentTime, loopA, loopB])

  const handleSaveCurrentFrame = useCallback(async () => {
    const el = videoRef.current
    if (!el || !el.videoWidth || !el.videoHeight) {
      notifications.show({ color: 'yellow', message: '请先等待视频画面加载完成再保存截帧' })
      return
    }
    const t = el.currentTime
    const captured = await captureVideoFrameAsPng(el, artifact.name, t)
    if (!captured) {
      notifications.show({ color: 'red', message: '截取当前画面失败' })
      return
    }
    const url = URL.createObjectURL(captured.file)
    const link = document.createElement('a')
    link.href = url
    link.download = captured.file.name
    document.body.appendChild(link)
    link.click()
    link.remove()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
    notifications.show({ color: 'teal', message: `已保存当前帧 ${captured.file.name}` })
  }, [artifact.name])

  const handleSetLoopA = () => {
    const t = videoRef.current?.currentTime ?? currentTime
    setLoopA(t)
    if (loopB !== null && loopB <= t) {
      setLoopB(null)
      setLoopActive(false)
    }
  }

  const handleSetLoopB = () => {
    const t = videoRef.current?.currentTime ?? currentTime
    const start = loopA ?? 0
    if (t <= start + 0.05) {
      notifications.show({ color: 'yellow', message: 'B 点需晚于 A 点' })
      return
    }
    if (loopA === null) setLoopA(0)
    setLoopB(t)
    setLoopActive(true)
    seekTo(start)
    void videoRef.current?.play().catch(() => {})
  }

  const clearLoop = () => {
    setLoopA(null)
    setLoopB(null)
    setLoopActive(false)
  }

  const openNoteComposer = useCallback(() => {
    const el = videoRef.current
    if (el && !el.paused) el.pause()
    setDrawerTab('notes')
    requestAnimationFrame(() => noteInputRef.current?.focus())
  }, [])

  const handleAddNote = async (event?: FormEvent) => {
    event?.preventDefault()
    const text = noteDraft.trim()
    if (!text) return
    const el = videoRef.current
    const t = el?.currentTime ?? currentTime
    const id = `note-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`
    let thumbDataUrl: string | undefined
    if (noteAttachFrame && el && el.videoWidth > 0 && el.videoHeight > 0) {
      const fullCap = await captureVideoFrameAsPng(el, artifact.name, t, 1920)
      if (fullCap) {
        noteFilesRef.current.set(id, fullCap.file)
      }
      const thumbCap = await captureVideoFrameAsPng(el, artifact.name, t, 320)
      if (thumbCap) {
        thumbDataUrl = thumbCap.dataUrl
      }
    }
    const next = [...notes, { id, time: t, text, thumbDataUrl, createdAt: Date.now() }].sort((a, b) => a.time - b.time)
    setNotes(next)
    persistNotes(storageKey, next)
    setNoteDraft('')
  }

  const handleDeleteNote = (id: string) => {
    noteFilesRef.current.delete(id)
    const next = notes.filter((n) => n.id !== id)
    setNotes(next)
    persistNotes(storageKey, next)
  }

  const handleClearNotes = () => {
    noteFilesRef.current.clear()
    setNotes([])
    persistNotes(storageKey, [])
  }

  const handleSendAllNotesToChat = () => {
    if (notes.length === 0) return
    const lines = notes.map((n, idx) => `${idx + 1}. [${formatTimecode(n.time)}] ${n.text}`)
    const summary = `以下为「${artifact.name}」的审片修改意见（请按时间码逐项调整）：\n${lines.join('\n')}\n`
    const files: File[] = []
    for (const note of notes) {
      const cached = noteFilesRef.current.get(note.id)
      if (cached) {
        files.push(cached)
      } else if (note.thumbDataUrl) {
        const reconstructed = dataUrlToPngFile(note.thumbDataUrl, formatFrameFileName(artifact.name, note.time))
        if (reconstructed) files.push(reconstructed)
      }
    }
    window.dispatchEvent(
      new CustomEvent('astrorder:quote-to-composer', {
        detail: {
          agentId: artifact.agentId,
          sessionId: artifact.sessionId,
          text: summary,
          files: files.length > 0 ? files : undefined,
        },
      }),
    )
    notifications.show({
      color: 'teal',
      message: `已汇总 ${notes.length} 条审片批注${files.length > 0 ? `及 ${files.length} 张截帧` : ''}到对话框`,
    })
  }

  const handleQuoteSubtitleCue = (cue: SubtitleCue) => {
    const label = subtitleFile?.name || artifact.name
    const text = `[${label} @ ${formatTimecode(cue.start)}–${formatTimecode(cue.end)}] “${cue.text}”\n修改建议：`
    window.dispatchEvent(
      new CustomEvent('astrorder:quote-to-composer', {
        detail: {
          agentId: artifact.agentId,
          sessionId: artifact.sessionId,
          text,
        },
      }),
    )
    notifications.show({ color: 'teal', message: `已引用第 ${cue.index} 句台词到对话框` })
  }

  const handleOpenSiblingImage = (img: SiblingAsset) => {
    const target = resolveArtifactFromPath(img.path, {
      id: artifact.sessionId,
      agent_id: artifact.agentId,
      connection_id: artifact.connectionId,
      workspace: session?.workspace || null,
      title: session?.title || '',
      status: session?.status || 'idle',
      updated_at: session?.updated_at || '',
    })
    openArtifact(target, 'image-viewer')
  }

  const handleViewerKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!isActive) return
    const target = event.target as HTMLElement | null
    if (
      target
      && (target.tagName === 'INPUT'
        || target.tagName === 'TEXTAREA'
        || target.tagName === 'SELECT'
        || target.isContentEditable)
    ) {
      return
    }
    if (event.metaKey || event.ctrlKey || event.altKey) return
    if (event.key === ',') {
      event.preventDefault()
      stepFrame(-1)
    } else if (event.key === '.') {
      event.preventDefault()
      stepFrame(1)
    } else if (event.key === 'm' || event.key === 'M') {
      event.preventDefault()
      openNoteComposer()
    }
  }

  const activeCueIndex = useMemo(
    () => findActiveCueIndex(subtitleCues, currentTime),
    [currentTime, subtitleCues],
  )

  const filteredCues = useMemo(() => {
    const q = subtitleQuery.trim().toLowerCase()
    if (!q) return subtitleCues
    return subtitleCues.filter((c) => c.text.toLowerCase().includes(q))
  }, [subtitleCues, subtitleQuery])

  const metaSummary = useMemo(() => {
    if (!meta) return ''
    const parts: string[] = []
    if (meta.width > 0 && meta.height > 0) parts.push(`${meta.width}×${meta.height}`)
    const dur = formatVideoDuration(meta.duration)
    if (dur) parts.push(dur)
    return parts.join(' · ')
  }, [meta])

  const drawerOpen = drawerTab !== 'closed'

  return (
    <div
      className="sidecar-video-viewer"
      tabIndex={-1}
      onKeyDown={handleViewerKeyDown}
    >
      <Paper p="xs" withBorder className="sidecar-video-toolbar">
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
            <Tooltip label="截取当前画面与精确时间码直接附加到对话输入框">
              <Button
                size="compact-xs"
                variant="light"
                loading={capturing}
                className="sidecar-video-control-btn"
                leftSection={<IconCamera size={13} aria-hidden="true" />}
                onClick={() => void handleQuoteCurrentFrame()}
              >
                截帧引用
              </Button>
            </Tooltip>
            <Tooltip label="保存当前帧为 PNG 图片">
              <ActionIcon
                variant="subtle"
                className="sidecar-video-control-btn"
                aria-label="保存当前帧"
                onClick={() => void handleSaveCurrentFrame()}
              >
                <IconPhoto size={15} aria-hidden="true" />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="审片打点批注（快捷键 M）">
              <Button
                size="compact-xs"
                variant={drawerTab === 'notes' ? 'filled' : 'subtle'}
                color={drawerTab === 'notes' ? 'indigo' : 'gray'}
                className="sidecar-video-control-btn"
                aria-pressed={drawerTab === 'notes'}
                leftSection={<IconBookmarkPlus size={13} aria-hidden="true" />}
                onClick={() => setDrawerTab((cur) => (cur === 'notes' ? 'closed' : 'notes'))}
              >
                批注{notes.length > 0 ? ` (${notes.length})` : ''}
              </Button>
            </Tooltip>
            {subtitleCues.length > 0 && (
              <>
                <Tooltip
                  label={
                    showCaptions
                      ? `隐藏外挂字幕：${subtitleFile?.name || ''}`
                      : `显示外挂字幕（默认关闭，避免与内嵌字幕重叠）：${subtitleFile?.name || ''}`
                  }
                >
                  <Button
                    size="compact-xs"
                    variant={showCaptions ? 'light' : 'subtle'}
                    color={showCaptions ? 'indigo' : 'gray'}
                    className="sidecar-video-control-btn"
                    aria-pressed={showCaptions}
                    onClick={() => setShowCaptions((v) => !v)}
                  >
                    {showCaptions ? '字幕开' : '字幕关'}
                  </Button>
                </Tooltip>
                <Tooltip label={`同目录台词文件：${subtitleFile?.name || ''}`}>
                  <Button
                    size="compact-xs"
                    variant={drawerTab === 'subtitles' ? 'filled' : 'subtle'}
                    color={drawerTab === 'subtitles' ? 'indigo' : 'gray'}
                    className="sidecar-video-control-btn"
                    aria-pressed={drawerTab === 'subtitles'}
                    leftSection={<IconSubtitles size={13} aria-hidden="true" />}
                    onClick={() => setDrawerTab((cur) => (cur === 'subtitles' ? 'closed' : 'subtitles'))}
                  >
                    台词 ({subtitleCues.length})
                  </Button>
                </Tooltip>
              </>
            )}
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
            <Tooltip label="重新加载新渲染版本（自动保留当前秒数）">
              <ActionIcon
                variant="subtle"
                className="sidecar-video-control-btn"
                aria-label="刷新视频"
                onClick={handleRefreshPreserveTime}
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

      <div className={`sidecar-video-workspace${drawerOpen ? ' has-drawer' : ''}`}>
        <div className="sidecar-video-main">
          <div className="sidecar-video-stage">
            {error ? (
              <div className="sidecar-video-error" role="alert">
                <Text size="sm" fw={500}>{error}</Text>
                <Text size="xs" c="dimmed" mt={6}>
                  若视频使用浏览器不支持的封装或编码（如部分 MKV / HEVC），可直接下载后使用本地播放器观看。
                </Text>
                <Group justify="center" gap="xs" mt="md">
                  <Button size="xs" variant="light" onClick={handleRefreshPreserveTime}>
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
                    if (el.textTracks && el.textTracks.length > 0) {
                      for (let i = 0; i < el.textTracks.length; i += 1) {
                        el.textTracks[i].mode = showCaptions ? 'showing' : 'disabled'
                      }
                    }
                    const dur = Number.isFinite(el.duration) ? el.duration : 0
                    setMeta({
                      width: el.videoWidth || 0,
                      height: el.videoHeight || 0,
                      duration: dur,
                    })
                    if (resumeTimeRef.current !== null) {
                      const target = dur > 0 ? Math.min(resumeTimeRef.current, Math.max(0, dur - 0.04)) : resumeTimeRef.current
                      el.currentTime = target
                      setCurrentTime(target)
                      const shouldPlay = resumeShouldPlayRef.current
                      resumeTimeRef.current = null
                      resumeShouldPlayRef.current = false
                      if (shouldPlay) {
                        void el.play().catch(() => {})
                      }
                    }
                    setLoading(false)
                  }}
                  onTimeUpdate={(event) => {
                    const el = event.currentTarget
                    const t = el.currentTime
                    if (loopActive && loopA !== null && loopB !== null && t >= loopB) {
                      el.currentTime = loopA
                      setCurrentTime(loopA)
                      return
                    }
                    setCurrentTime(t)
                  }}
                  onSeeked={(event) => {
                    setCurrentTime(event.currentTarget.currentTime)
                  }}
                  onCanPlay={() => setLoading(false)}
                  onError={() => {
                    setLoading(false)
                    setError('视频加载或解码失败，文件可能已损坏或编码格式不受当前浏览器支持')
                  }}
                >
                  <source src={streamUrl} type={mimeType} />
                  {vttTrackUrl && showCaptions && (
                    <track
                      kind="captions"
                      src={vttTrackUrl}
                      srcLang="zh"
                      label={subtitleFile?.name || '字幕'}
                      default
                    />
                  )}
                </video>
                {loading && (
                  <div className="sidecar-video-loading-pill">
                    正在读取视频元数据…
                  </div>
                )}
              </div>
            )}
          </div>

          {!error && (
            <div className="sidecar-video-transport" role="group" aria-label="逐帧审片与区间循环控制">
              <span className="sidecar-video-timecode" title="当前精确时间码">
                {formatTimecode(currentTime)}
              </span>

              <div className="sidecar-video-transport-group">
                <Tooltip label="后退 2 秒">
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    className="sidecar-video-control-btn"
                    leftSection={<IconPlayerSkipBack size={12} aria-hidden="true" />}
                    onClick={() => seekTo((videoRef.current?.currentTime ?? currentTime) - 2)}
                  >
                    -2s
                  </Button>
                </Tooltip>
                <Tooltip label="上一帧（快捷键 , ）">
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    className="sidecar-video-control-btn"
                    leftSection={<IconChevronLeft size={13} aria-hidden="true" />}
                    onClick={() => stepFrame(-1)}
                  >
                    -1帧
                  </Button>
                </Tooltip>
                <Tooltip label="下一帧（快捷键 . ）">
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    className="sidecar-video-control-btn"
                    rightSection={<IconChevronRight size={13} aria-hidden="true" />}
                    onClick={() => stepFrame(1)}
                  >
                    +1帧
                  </Button>
                </Tooltip>
                <Tooltip label="前进 2 秒">
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    className="sidecar-video-control-btn"
                    rightSection={<IconPlayerSkipForward size={12} aria-hidden="true" />}
                    onClick={() => seekTo((videoRef.current?.currentTime ?? currentTime) + 2)}
                  >
                    +2s
                  </Button>
                </Tooltip>
              </div>

              <span className="sidecar-video-divider" aria-hidden="true" />

              <div className="sidecar-video-transport-group">
                <Button
                  size="compact-xs"
                  variant={loopA !== null ? 'light' : 'subtle'}
                  className="sidecar-video-control-btn"
                  onClick={handleSetLoopA}
                >
                  {loopA !== null ? `A ${formatTimecode(loopA)}` : '设 A'}
                </Button>
                <Button
                  size="compact-xs"
                  variant={loopB !== null ? 'light' : 'subtle'}
                  className="sidecar-video-control-btn"
                  onClick={handleSetLoopB}
                >
                  {loopB !== null ? `B ${formatTimecode(loopB)}` : '设 B'}
                </Button>
                <Button
                  size="compact-xs"
                  variant={loopActive ? 'filled' : 'subtle'}
                  color={loopActive ? 'indigo' : 'gray'}
                  className="sidecar-video-control-btn"
                  disabled={loopA === null || loopB === null}
                  aria-pressed={loopActive}
                  leftSection={<IconRepeat size={12} aria-hidden="true" />}
                  onClick={() => setLoopActive((v) => !v)}
                >
                  A–B 循环
                </Button>
                {(loopA !== null || loopB !== null) && (
                  <ActionIcon
                    size="sm"
                    variant="subtle"
                    color="gray"
                    className="sidecar-video-control-btn"
                    aria-label="清除 A-B 区间"
                    onClick={clearLoop}
                  >
                    <IconX size={13} aria-hidden="true" />
                  </ActionIcon>
                )}
              </div>

              <div className="sidecar-video-transport-spacer" />

              {siblingImages.length > 0 && (
                <div className="sidecar-video-transport-group">
                  {siblingImages.map((img) => (
                    <Tooltip key={img.path} label={`打开同目录参考图：${img.name}`}>
                      <Button
                        size="compact-xs"
                        variant="subtle"
                        color="gray"
                        className="sidecar-video-control-btn"
                        leftSection={<IconPhoto size={12} aria-hidden="true" />}
                        onClick={() => handleOpenSiblingImage(img)}
                      >
                        {img.name.toLowerCase().startsWith('poster') ? '封面' : '分镜图'}
                      </Button>
                    </Tooltip>
                  ))}
                </div>
              )}

              <Button
                size="compact-xs"
                variant="light"
                color="indigo"
                className="sidecar-video-control-btn"
                leftSection={<IconBookmarkPlus size={12} aria-hidden="true" />}
                onClick={openNoteComposer}
              >
                + 打点 (M)
              </Button>
            </div>
          )}
        </div>

        {drawerOpen && (
          <aside className="sidecar-video-drawer" aria-label={drawerTab === 'notes' ? '审片批注列表' : '同步台词字幕'}>
            <div className="sidecar-video-drawer-header">
              <Group gap={6}>
                <Button
                  size="compact-xs"
                  variant={drawerTab === 'notes' ? 'light' : 'subtle'}
                  color={drawerTab === 'notes' ? 'indigo' : 'gray'}
                  aria-pressed={drawerTab === 'notes'}
                  onClick={() => setDrawerTab('notes')}
                >
                  审片批注 ({notes.length})
                </Button>
                {subtitleCues.length > 0 && (
                  <Button
                    size="compact-xs"
                    variant={drawerTab === 'subtitles' ? 'light' : 'subtle'}
                    color={drawerTab === 'subtitles' ? 'indigo' : 'gray'}
                    aria-pressed={drawerTab === 'subtitles'}
                    onClick={() => setDrawerTab('subtitles')}
                  >
                    台词 ({subtitleCues.length})
                  </Button>
                )}
              </Group>
              <ActionIcon
                size="sm"
                variant="subtle"
                color="gray"
                aria-label="收起侧栏"
                onClick={() => setDrawerTab('closed')}
              >
                <IconX size={14} aria-hidden="true" />
              </ActionIcon>
            </div>

            {drawerTab === 'notes' ? (
              <div className="sidecar-video-drawer-body">
                <form className="sidecar-video-note-form" onSubmit={(e) => void handleAddNote(e)}>
                  <label htmlFor={noteInputId} className="sidecar-video-note-label">
                    在 <span className="sidecar-video-timecode">{formatTimecode(currentTime)}</span> 添加审片意见
                  </label>
                  <TextInput
                    id={noteInputId}
                    ref={noteInputRef}
                    size="xs"
                    placeholder="例如：标题字号偏小 / 转场慢半拍…"
                    value={noteDraft}
                    onChange={(e) => setNoteDraft(e.currentTarget.value)}
                  />
                  <Group justify="space-between" align="center" gap={6}>
                    <Checkbox
                      size="xs"
                      label="附带当前画面截帧"
                      checked={noteAttachFrame}
                      onChange={(e) => setNoteAttachFrame(e.currentTarget.checked)}
                    />
                    <Button size="compact-xs" type="submit" disabled={!noteDraft.trim()}>
                      记录
                    </Button>
                  </Group>
                </form>

                {notes.length > 0 && (
                  <Group justify="space-between" gap={6} className="sidecar-video-notes-actions">
                    <Button
                      size="compact-xs"
                      variant="filled"
                      color="dark"
                      leftSection={<IconSend size={12} aria-hidden="true" />}
                      onClick={handleSendAllNotesToChat}
                    >
                      汇总发给 AI 修改
                    </Button>
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      onClick={handleClearNotes}
                    >
                      清空
                    </Button>
                  </Group>
                )}

                {notes.length === 0 ? (
                  <Text size="xs" c="dimmed" className="sidecar-video-drawer-empty">
                    暂停到任意画面，按 M 或在上方输入修改意见，看完全片后可一键汇总给 AI 批量修改代码。
                  </Text>
                ) : (
                  <ul className="sidecar-video-list">
                    {notes.map((note) => (
                      <li key={note.id} className="sidecar-video-note-item">
                        <div className="sidecar-video-note-row">
                          <button
                            type="button"
                            className="sidecar-video-tc-chip"
                            onClick={() => seekTo(note.time, true)}
                          >
                            {formatTimecode(note.time)}
                          </button>
                          <div className="sidecar-video-note-content">
                            <p className="sidecar-video-note-text">{note.text}</p>
                            {note.thumbDataUrl && (
                              <img
                                src={note.thumbDataUrl}
                                alt={`${formatTimecode(note.time)} 审片截帧`}
                                className="sidecar-video-note-thumb"
                              />
                            )}
                          </div>
                          <ActionIcon
                            size="xs"
                            variant="subtle"
                            color="gray"
                            aria-label={`删除 ${formatTimecode(note.time)} 的批注`}
                            onClick={() => handleDeleteNote(note.id)}
                          >
                            <IconTrash size={12} aria-hidden="true" />
                          </ActionIcon>
                        </div>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            ) : (
              <div className="sidecar-video-drawer-body">
                <div className="sidecar-video-sub-toolbar">
                  <label htmlFor={subtitleSearchId} className="sidecar-video-note-label">
                    {subtitleFile?.name || '字幕台词'}
                  </label>
                  <Group gap={6} wrap="nowrap">
                    <TextInput
                      id={subtitleSearchId}
                      size="xs"
                      style={{ flex: 1 }}
                      placeholder="搜索台词…"
                      value={subtitleQuery}
                      onChange={(e) => setSubtitleQuery(e.currentTarget.value)}
                    />
                    <Button
                      size="compact-xs"
                      variant={showCaptions ? 'light' : 'subtle'}
                      aria-pressed={showCaptions}
                      onClick={() => setShowCaptions((v) => !v)}
                    >
                      {showCaptions ? '画面字幕开' : '画面字幕关'}
                    </Button>
                  </Group>
                </div>

                {filteredCues.length === 0 ? (
                  <Text size="xs" c="dimmed" className="sidecar-video-drawer-empty">
                    没有匹配的台词行。
                  </Text>
                ) : (
                  <ul className="sidecar-video-list">
                    {filteredCues.map((cue) => {
                      const isCurrent = activeCueIndex >= 0 && subtitleCues[activeCueIndex]?.id === cue.id
                      return (
                        <li
                          key={cue.id}
                          className={`sidecar-video-cue-item${isCurrent ? ' is-active' : ''}`}
                        >
                          <div className="sidecar-video-cue-row">
                            <button
                              type="button"
                              className="sidecar-video-cue-jump"
                              aria-current={isCurrent ? 'true' : undefined}
                              onClick={() => seekTo(cue.start)}
                            >
                              <Badge size="xs" variant={isCurrent ? 'filled' : 'light'} color={isCurrent ? 'indigo' : 'gray'} className="sidecar-video-timecode">
                                {formatTimecode(cue.start)}
                              </Badge>
                              <span className="sidecar-video-cue-text">{cue.text}</span>
                            </button>
                            <Tooltip label="引用该句台词到对话框修改文案或重配音">
                              <ActionIcon
                                size="xs"
                                variant="subtle"
                                color="indigo"
                                aria-label={`引用第 ${cue.index} 句台词到对话`}
                                onClick={() => handleQuoteSubtitleCue(cue)}
                              >
                                <IconMessagePlus size={13} aria-hidden="true" />
                              </ActionIcon>
                            </Tooltip>
                          </div>
                        </li>
                      )
                    })}
                  </ul>
                )}
              </div>
            )}
          </aside>
        )}
      </div>
    </div>
  )
}
