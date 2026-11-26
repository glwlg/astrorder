import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { artifactViewerRegistry } from '../../registry'
import { resolveArtifactFromPath } from '../../resolver'
import { VideoViewer } from './VideoViewer'
import { cuesToWebVtt, formatTimecode, parseSubtitleText } from './subtitles'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
})

const SAMPLE_SESSION = {
  id: 'session-1',
  agent_id: 'agent-1',
  title: '宣传片',
  workspace: '/workspace/OpsCore',
  status: 'idle' as const,
  updated_at: '2026-11-24T00:00:00Z',
}

describe('subtitles helpers', () => {
  it('parses SRT and VTT blocks cleanly and formats precise timecodes', () => {
    const srt = [
      '\uFEFF1',
      '00:00:00,450 --> 00:00:04,010',
      '多款 AI 编程助手同时开工。',
      '',
      '2',
      '00:01:12,400 --> 00:01:16,500',
      '<i>星序 · 协作自有秩序。</i>',
    ].join('\n')
    const cues = parseSubtitleText(srt)
    expect(cues).toHaveLength(2)
    expect(cues[0].start).toBeCloseTo(0.45, 2)
    expect(cues[1].text).toBe('星序 · 协作自有秩序。')
    expect(formatTimecode(72.4)).toBe('01:12.40')

    const vtt = cuesToWebVtt(cues)
    expect(vtt).toContain('WEBVTT')
    expect(vtt).toContain('00:01:12.400 --> 00:01:16.500')
  })
})

describe('VideoViewer', () => {
  it('resolves video extensions in registry, renders streaming controls, frame stepping, A-B loop, and preserves time on refresh', () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    )
    const artifact = resolveArtifactFromPath('films/ai-ops-platform.mp4', SAMPLE_SESSION)

    const matched = artifactViewerRegistry.findViewer(artifact)
    expect(matched?.id).toBe('video-viewer')

    const { container } = render(
      <MantineProvider>
        <VideoViewer artifact={artifact} />
      </MantineProvider>,
    )

    const video = container.querySelector('video')!
    expect(video).toBeTruthy()
    expect(video.hasAttribute('controls')).toBe(true)
    expect(video.hasAttribute('playsinline')).toBe(true)
    expect(video.getAttribute('preload')).toBe('metadata')
    expect(video.getAttribute('width')).toBe('1280')
    expect(video.getAttribute('height')).toBe('720')

    const source = video.querySelector('source')!
    expect(source.getAttribute('src')).toContain('/api/v1/files/raw?')
    expect(source.getAttribute('type')).toBe('video/mp4')
    expect(fetchSpy).toHaveBeenCalled()

    // Playback rate cycle
    const rateBtn = screen.getByRole('button', { name: '播放倍速 1x' })
    fireEvent.click(rateBtn)
    expect(screen.getByRole('button', { name: '播放倍速 1.25x' })).toBeInTheDocument()
    expect(video.playbackRate).toBe(1.25)

    // Metadata updates badge
    Object.defineProperty(video, 'videoWidth', { configurable: true, value: 1920 })
    Object.defineProperty(video, 'videoHeight', { configurable: true, value: 1080 })
    Object.defineProperty(video, 'duration', { configurable: true, value: 62 })
    fireEvent.loadedMetadata(video)
    expect(screen.getByText('1920×1080 · 01:02')).toBeInTheDocument()

    // Frame step (+1 frame from 10.0s -> ~10.04s)
    video.pause = vi.fn()
    video.play = vi.fn().mockResolvedValue(undefined)
    video.currentTime = 10
    fireEvent.click(screen.getByRole('button', { name: '+1帧' }))
    expect(video.currentTime).toBeCloseTo(10 + 1 / 24, 3)

    // A-B loop seeks back to A when passing B
    video.currentTime = 12
    fireEvent.click(screen.getByRole('button', { name: '设 A' }))
    video.currentTime = 18
    fireEvent.click(screen.getByRole('button', { name: '设 B' }))
    video.currentTime = 19
    fireEvent.timeUpdate(video)
    expect(video.currentTime).toBeCloseTo(12, 2)

    // Refresh preserves current timestamp on newly mounted <video>
    video.currentTime = 33.5
    fireEvent.click(screen.getByRole('button', { name: '刷新视频' }))
    const reloadedVideo = container.querySelector('video')!
    Object.defineProperty(reloadedVideo, 'duration', { configurable: true, value: 62 })
    fireEvent.loadedMetadata(reloadedVideo)
    expect(reloadedVideo.currentTime).toBeCloseTo(33.5, 2)
  })

  it('auto-loads sibling SRT subtitles, records review notes, and dispatches quote-to-composer', async () => {
    const srtContent = '1\n00:00:05,000 --> 00:00:09,500\n本机优先，原生直连四大智能体。\n'
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input) => {
      const url = typeof input === 'string' ? input : input instanceof Request ? input.url : String(input)
      if (url.includes('/api/v1/files/tree')) {
        return new Response(
          JSON.stringify({
            items: [
              { name: 'astrorder-keynote.mp4', path: '/workspace/films/astrorder-keynote/astrorder-keynote.mp4', is_dir: false },
              { name: 'subtitles_zh.srt', path: '/workspace/films/astrorder-keynote/subtitles_zh.srt', is_dir: false },
              { name: 'poster.jpg', path: '/workspace/films/astrorder-keynote/poster.jpg', is_dir: false },
            ],
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        )
      }
      if (url.includes('subtitles_zh.srt')) {
        return new Response(srtContent, { status: 200, headers: { 'Content-Type': 'text/plain; charset=utf-8' } })
      }
      return new Response('', { status: 404 })
    })

    const quotedEvents: Array<{ text?: string; files?: File[] }> = []
    const onQuote = (e: Event) => {
      quotedEvents.push((e as CustomEvent<{ text?: string; files?: File[] }>).detail)
    }
    window.addEventListener('astrorder:quote-to-composer', onQuote)

    const artifact = resolveArtifactFromPath('/workspace/films/astrorder-keynote/astrorder-keynote.mp4', SAMPLE_SESSION)
    const { container } = render(
      <MantineProvider>
        <VideoViewer artifact={artifact} />
      </MantineProvider>,
    )

    // Sibling subtitle button & poster button appear, with on-video captions OFF by default
    const subBtn = await screen.findByRole('button', { name: /台词 \(1\)/ })
    const captionToggle = screen.getByRole('button', { name: '字幕关' })
    expect(captionToggle).toHaveAttribute('aria-pressed', 'false')
    expect(container.querySelector('video track')).toBeNull()
    expect(screen.getByRole('button', { name: '封面' })).toBeInTheDocument()

    // Toggling captions ON mounts the native <track>, toggling OFF removes it
    fireEvent.click(captionToggle)
    expect(screen.getByRole('button', { name: '字幕开' })).toHaveAttribute('aria-pressed', 'true')
    expect(container.querySelector('video track')).not.toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '字幕开' }))
    expect(screen.getByRole('button', { name: '字幕关' })).toHaveAttribute('aria-pressed', 'false')
    expect(container.querySelector('video track')).toBeNull()

    // Open subtitle rail & quote a cue
    fireEvent.click(subBtn)
    expect(await screen.findByText('本机优先，原生直连四大智能体。')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '引用第 1 句台词到对话' }))
    expect(quotedEvents[0]?.text).toContain('subtitles_zh.srt @ 00:05.00–00:09.50')

    // Switch to notes drawer, add a review note at 15.2s, and batch send to AI
    const video = container.querySelector('video')!
    video.pause = vi.fn()
    video.currentTime = 15.2
    fireEvent.seeked(video)

    fireEvent.click(screen.getByRole('button', { name: '+ 打点 (M)' }))
    const noteInput = await screen.findByPlaceholderText('例如：标题字号偏小 / 转场慢半拍…')
    fireEvent.change(noteInput, { target: { value: '这里卡片间距再拉大 12px' } })
    fireEvent.click(screen.getByRole('button', { name: '记录' }))

    expect(await screen.findByText('这里卡片间距再拉大 12px')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '汇总发给 AI 修改' }))

    await waitFor(() => {
      expect(quotedEvents.some((e) => e.text?.includes('00:15.20') && e.text?.includes('这里卡片间距再拉大 12px'))).toBe(true)
    })

    window.removeEventListener('astrorder:quote-to-composer', onQuote)
  })
})
