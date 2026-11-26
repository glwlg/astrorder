import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Button, Group, Paper, Text, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconChevronLeft,
  IconChevronRight,
  IconDownload,
  IconMessagePlus,
  IconMusic,
  IconPlayerTrackNext,
  IconPlayerTrackPrev,
  IconRefresh,
  IconRepeat,
  IconX,
} from '@tabler/icons-react'
import { authenticatedApiFetch } from '../../../../api/client'
import { downloadFile } from '../../fileTransfer'
import { resolveArtifactFromPath } from '../../resolver'
import { useSidecarStore } from '../../sidecarStore'
import type { ViewerContext } from '../../types'
import { buildAuthenticatedVideoUrl } from '../video/formats'
import { formatTimecode } from '../video/subtitles'
import { audioFormatBadge, audioMediaType } from './formats'
import './audioViewer.css'

const PLAYBACK_RATES = [0.75, 1, 1.25, 1.5, 2] as const

interface SiblingAudio {
  name: string
  path: string
}

function parentDirOf(filePath?: string): string {
  if (!filePath) return ''
  const norm = filePath.replace(/[\\/]+$/, '')
  const idx = Math.max(norm.lastIndexOf('/'), norm.lastIndexOf('\\'))
  return idx > 0 ? norm.slice(0, idx) : ''
}

export function AudioViewer({ artifact, session, toolbarAction }: ViewerContext) {
  const audioRef = useRef<HTMLAudioElement | null>(null)
  const resumeTimeRef = useRef<number | null>(null)
  const [revision, setRevision] = useState(0)
  const [playbackRate, setPlaybackRate] = useState<number>(1)
  const [currentTime, setCurrentTime] = useState(0)
  const [duration, setDuration] = useState(0)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  // A-B loop state
  const [loopA, setLoopA] = useState<number | null>(null)
  const [loopB, setLoopB] = useState<number | null>(null)
  const [loopActive, setLoopActive] = useState(false)

  // Sibling audio tracks in the same directory (ideal for auditioning voices/*.wav or music/*.wav)
  const [siblings, setSiblings] = useState<SiblingAudio[]>([])
  const openArtifact = useSidecarStore((s) => s.openArtifact)

  const streamUrl = useMemo(
    () => buildAuthenticatedVideoUrl(artifact.readUrl, revision),
    [artifact.readUrl, revision],
  )
  const mimeType = useMemo(
    () => audioMediaType(artifact.name) || (artifact.mediaType.startsWith('audio/') ? artifact.mediaType : 'audio/mpeg'),
    [artifact.mediaType, artifact.name],
  )
  const formatLabel = useMemo(() => audioFormatBadge(artifact.name), [artifact.name])

  useEffect(() => {
    setError('')
    setLoading(true)
    if (resumeTimeRef.current === null) {
      setCurrentTime(0)
      setDuration(0)
      setLoopA(null)
      setLoopB(null)
      setLoopActive(false)
    }
  }, [streamUrl])

  useEffect(() => {
    const el = audioRef.current
    if (el) {
      el.playbackRate = playbackRate
    }
  }, [playbackRate, streamUrl])

  useEffect(() => {
    const parent = parentDirOf(artifact.path)
    if (!parent || !artifact.sessionId) {
      setSiblings([])
      return
    }
    let cancelled = false
    const url = `/api/v1/files/tree?depth=1&path=${encodeURIComponent(parent)}&session_id=${encodeURIComponent(artifact.sessionId)}&connection_id=${encodeURIComponent(artifact.connectionId || '')}`
    void authenticatedApiFetch(url)
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (cancelled || !data || !Array.isArray(data.items)) return
        const items: SiblingAudio[] = data.items
          .filter((item: { is_dir?: boolean; name?: string; path?: string }) => !item.is_dir && item.name && item.path && Boolean(audioMediaType(item.name)))
          .map((item: { name: string; path: string }) => ({ name: item.name, path: item.path }))
          .sort((a: SiblingAudio, b: SiblingAudio) => a.name.localeCompare(b.name, 'zh-CN', { numeric: true }))
        setSiblings(items)
      })
      .catch(() => {
        if (!cancelled) setSiblings([])
      })
    return () => {
      cancelled = true
    }
  }, [artifact.connectionId, artifact.path, artifact.sessionId])

  const currentSiblingIdx = useMemo(() => {
    if (!artifact.path || siblings.length <= 1) return -1
    const norm = artifact.path.replace(/\\/g, '/').toLowerCase()
    return siblings.findIndex((item) => item.path.replace(/\\/g, '/').toLowerCase() === norm)
  }, [artifact.path, siblings])

  const openSiblingTrack = useCallback(
    (track: SiblingAudio) => {
      const target = resolveArtifactFromPath(track.path, {
        id: artifact.sessionId,
        agent_id: artifact.agentId,
        connection_id: artifact.connectionId,
        workspace: session?.workspace || null,
        title: session?.title || '',
        status: session?.status || 'idle',
        updated_at: session?.updated_at || '',
      })
      openArtifact(target, 'audio-viewer')
    },
    [artifact.agentId, artifact.connectionId, artifact.sessionId, openArtifact, session?.status, session?.title, session?.updated_at, session?.workspace],
  )

  const cycleRate = () => {
    setPlaybackRate((current) => {
      const idx = PLAYBACK_RATES.indexOf(current as (typeof PLAYBACK_RATES)[number])
      return PLAYBACK_RATES[(idx + 1) % PLAYBACK_RATES.length]
    })
  }

  const seekBy = (delta: number) => {
    const el = audioRef.current
    if (!el) return
    const maxT = Number.isFinite(el.duration) && el.duration > 0 ? el.duration : Number.MAX_SAFE_INTEGER
    const next = Math.min(Math.max(0, el.currentTime + delta), maxT)
    el.currentTime = next
    setCurrentTime(next)
  }

  const handleRefreshPreserveTime = () => {
    const el = audioRef.current
    if (el && Number.isFinite(el.currentTime) && el.currentTime > 0) {
      resumeTimeRef.current = el.currentTime
    } else {
      resumeTimeRef.current = null
    }
    setRevision((v) => v + 1)
  }

  const handleSetLoopA = () => {
    const t = audioRef.current?.currentTime ?? currentTime
    setLoopA(t)
    if (loopB !== null && loopB <= t) {
      setLoopB(null)
      setLoopActive(false)
    }
  }

  const handleSetLoopB = () => {
    const t = audioRef.current?.currentTime ?? currentTime
    const start = loopA ?? 0
    if (t <= start + 0.05) {
      notifications.show({ color: 'yellow', message: 'B 点需晚于 A 点' })
      return
    }
    if (loopA === null) setLoopA(0)
    setLoopB(t)
    setLoopActive(true)
    if (audioRef.current) {
      audioRef.current.currentTime = start
      void audioRef.current.play().catch(() => {})
    }
  }

  const clearLoop = () => {
    setLoopA(null)
    setLoopB(null)
    setLoopActive(false)
  }

  const quoteTimecodeToChat = () => {
    const t = audioRef.current?.currentTime ?? currentTime
    const rangeSuffix =
      loopA !== null && loopB !== null
        ? `（区间 ${formatTimecode(loopA)}–${formatTimecode(loopB)}）`
        : ''
    const text = `[${artifact.name} @ ${formatTimecode(t)}]${rangeSuffix} `
    window.dispatchEvent(
      new CustomEvent('astrorder:quote-to-composer', {
        detail: {
          agentId: artifact.agentId,
          sessionId: artifact.sessionId,
          text,
        },
      }),
    )
    notifications.show({ color: 'teal', message: `已引用音频时间点 ${formatTimecode(t)} 到对话` })
  }

  return (
    <div className="sidecar-audio-viewer">
      <Paper p="xs" withBorder className="sidecar-audio-toolbar">
        <div className="sidecar-audio-toolbar-row">
          <div className="sidecar-audio-title-group">
            <Text size="xs" fw={600} className="sidecar-audio-filename" title={artifact.path || artifact.name}>
              {artifact.name}
            </Text>
            <span className="sidecar-audio-format-badge">{formatLabel}</span>
          </div>

          <Group gap={6} wrap="wrap" className="sidecar-audio-actions">
            {toolbarAction}
            <Tooltip label="引用当前时间点到对话输入框">
              <Button
                size="compact-xs"
                variant="light"
                className="sidecar-audio-control-btn"
                leftSection={<IconMessagePlus size={13} aria-hidden="true" />}
                onClick={quoteTimecodeToChat}
              >
                引用时间点
              </Button>
            </Tooltip>
            <Tooltip label="切换播放倍速">
              <Button
                size="compact-xs"
                variant="subtle"
                className="sidecar-audio-control-btn"
                aria-label={`播放倍速 ${playbackRate}x`}
                onClick={cycleRate}
              >
                {playbackRate}×
              </Button>
            </Tooltip>
            <Tooltip label="重新加载（保留当前播放秒数）">
              <ActionIcon
                variant="subtle"
                className="sidecar-audio-control-btn"
                aria-label="刷新音频"
                onClick={handleRefreshPreserveTime}
              >
                <IconRefresh size={15} aria-hidden="true" />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="下载音频">
              <ActionIcon
                variant="subtle"
                className="sidecar-audio-control-btn"
                aria-label="下载音频"
                onClick={() => void downloadFile(artifact.readUrl, artifact.name)}
              >
                <IconDownload size={15} aria-hidden="true" />
              </ActionIcon>
            </Tooltip>
          </Group>
        </div>
      </Paper>

      <div className="sidecar-audio-stage">
        {error ? (
          <div className="sidecar-audio-error" role="alert">
            <Text size="sm" fw={500}>{error}</Text>
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
                下载音频
              </Button>
            </Group>
          </div>
        ) : (
          <Paper withBorder className="sidecar-audio-card">
            <div className="sidecar-audio-hero">
              <div className="sidecar-audio-disc" aria-hidden="true">
                <IconMusic size={24} />
              </div>
              <div className="sidecar-audio-meta">
                <Text size="sm" fw={600} className="sidecar-audio-card-title">
                  {artifact.name}
                </Text>
                <Text size="xs" c="dimmed" className="sidecar-audio-timecode">
                  {formatTimecode(currentTime)} / {duration > 0 ? formatTimecode(duration) : '--:--.--'}
                  {loading ? ' · 正在读取音轨…' : ''}
                </Text>
              </div>
            </div>

            <audio
              key={streamUrl}
              ref={audioRef}
              className="sidecar-audio-element"
              controls
              preload="metadata"
              aria-label={`音频播放器：${artifact.name}`}
              onLoadedMetadata={(event) => {
                const el = event.currentTarget
                el.playbackRate = playbackRate
                const nextDur = Number.isFinite(el.duration) ? el.duration : 0
                setDuration(nextDur)
                if (resumeTimeRef.current !== null) {
                  const target = nextDur > 0 ? Math.min(resumeTimeRef.current, Math.max(0, nextDur - 0.05)) : resumeTimeRef.current
                  el.currentTime = target
                  setCurrentTime(target)
                  resumeTimeRef.current = null
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
              onCanPlay={() => setLoading(false)}
              onError={() => {
                setLoading(false)
                setError('音频加载或解码失败，请重试或下载到本地播放')
              }}
            >
              <source src={streamUrl} type={mimeType} />
            </audio>

            <div className="sidecar-audio-transport" role="group" aria-label="精细听音与区间循环">
              <Button
                size="compact-xs"
                variant="subtle"
                className="sidecar-audio-control-btn"
                leftSection={<IconPlayerTrackPrev size={12} aria-hidden="true" />}
                onClick={() => seekBy(-2)}
              >
                -2s
              </Button>
              <Button
                size="compact-xs"
                variant="subtle"
                className="sidecar-audio-control-btn"
                rightSection={<IconPlayerTrackNext size={12} aria-hidden="true" />}
                onClick={() => seekBy(2)}
              >
                +2s
              </Button>

              <span className="sidecar-audio-divider" aria-hidden="true" />

              <Button
                size="compact-xs"
                variant={loopA !== null ? 'light' : 'subtle'}
                className="sidecar-audio-control-btn"
                onClick={handleSetLoopA}
              >
                {loopA !== null ? `A ${formatTimecode(loopA)}` : '设 A'}
              </Button>
              <Button
                size="compact-xs"
                variant={loopB !== null ? 'light' : 'subtle'}
                className="sidecar-audio-control-btn"
                onClick={handleSetLoopB}
              >
                {loopB !== null ? `B ${formatTimecode(loopB)}` : '设 B'}
              </Button>
              <Button
                size="compact-xs"
                variant={loopActive ? 'filled' : 'subtle'}
                color={loopActive ? 'indigo' : 'gray'}
                className="sidecar-audio-control-btn"
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
                  className="sidecar-audio-control-btn"
                  aria-label="清除 A-B 区间"
                  onClick={clearLoop}
                >
                  <IconX size={13} aria-hidden="true" />
                </ActionIcon>
              )}
            </div>

            {siblings.length > 1 && currentSiblingIdx >= 0 && (
              <div className="sidecar-audio-siblings" role="group" aria-label="同目录音轨切换">
                <Button
                  size="compact-xs"
                  variant="subtle"
                  className="sidecar-audio-control-btn"
                  disabled={currentSiblingIdx <= 0}
                  leftSection={<IconChevronLeft size={13} aria-hidden="true" />}
                  onClick={() => openSiblingTrack(siblings[currentSiblingIdx - 1])}
                >
                  上一条
                </Button>
                <Text size="xs" c="dimmed" className="sidecar-audio-sibling-counter">
                  同目录音轨 {currentSiblingIdx + 1} / {siblings.length}
                </Text>
                <Button
                  size="compact-xs"
                  variant="subtle"
                  className="sidecar-audio-control-btn"
                  disabled={currentSiblingIdx >= siblings.length - 1}
                  rightSection={<IconChevronRight size={13} aria-hidden="true" />}
                  onClick={() => openSiblingTrack(siblings[currentSiblingIdx + 1])}
                >
                  下一条
                </Button>
              </div>
            )}
          </Paper>
        )}
      </div>
    </div>
  )
}
