import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { artifactViewerRegistry } from '../../registry'
import { resolveArtifactFromPath } from '../../resolver'
import { AudioViewer } from './AudioViewer'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

const SAMPLE_SESSION = {
  id: 'session-1',
  agent_id: 'agent-1',
  title: '配音试听',
  workspace: '/workspace/films/astrorder-keynote',
  status: 'idle' as const,
  updated_at: '2026-11-26T00:00:00Z',
}

describe('AudioViewer', () => {
  it('resolves audio extensions in registry, loops A-B range, preserves playhead on refresh, and quotes timecode', () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            { name: 's01.wav', path: '/workspace/films/astrorder-keynote/voices/s01.wav', is_dir: false },
            { name: 's02.wav', path: '/workspace/films/astrorder-keynote/voices/s02.wav', is_dir: false },
          ],
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const artifact = resolveArtifactFromPath('/workspace/films/astrorder-keynote/voices/s01.wav', SAMPLE_SESSION)
    expect(artifactViewerRegistry.findViewer(artifact)?.id).toBe('audio-viewer')
    expect(
      artifactViewerRegistry.findViewer(resolveArtifactFromPath('out/qa_audio_preview.mp3', SAMPLE_SESSION))?.id,
    ).toBe('audio-viewer')

    const quoted: Array<{ text?: string }> = []
    const onQuote = (e: Event) => {
      quoted.push((e as CustomEvent<{ text?: string }>).detail)
    }
    window.addEventListener('astrorder:quote-to-composer', onQuote)

    const { container } = render(
      <MantineProvider>
        <AudioViewer artifact={artifact} />
      </MantineProvider>,
    )

    const audio = container.querySelector('audio')!
    expect(audio).toBeTruthy()
    expect(audio.hasAttribute('controls')).toBe(true)
    expect(audio.getAttribute('preload')).toBe('metadata')
    expect(audio.querySelector('source')?.getAttribute('type')).toBe('audio/wav')

    // Playback rate cycle
    fireEvent.click(screen.getByRole('button', { name: '播放倍速 1x' }))
    expect(screen.getByRole('button', { name: '播放倍速 1.25x' })).toBeInTheDocument()
    expect(audio.playbackRate).toBe(1.25)

    // A-B loop & quote timecode
    audio.play = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(audio, 'duration', { configurable: true, value: 18 })
    fireEvent.loadedMetadata(audio)
    audio.currentTime = 3.2
    fireEvent.click(screen.getByRole('button', { name: '设 A' }))
    audio.currentTime = 7.5
    fireEvent.click(screen.getByRole('button', { name: '设 B' }))
    audio.currentTime = 8.1
    fireEvent.timeUpdate(audio)
    expect(audio.currentTime).toBeCloseTo(3.2, 2)

    fireEvent.click(screen.getByRole('button', { name: '引用时间点' }))
    expect(quoted[0]?.text).toContain('[s01.wav @ 00:03.20]（区间 00:03.20–00:07.50）')

    // Refresh preserves playhead
    audio.currentTime = 6.4
    fireEvent.click(screen.getByRole('button', { name: '刷新音频' }))
    const reloaded = container.querySelector('audio')!
    Object.defineProperty(reloaded, 'duration', { configurable: true, value: 18 })
    fireEvent.loadedMetadata(reloaded)
    expect(reloaded.currentTime).toBeCloseTo(6.4, 2)

    window.removeEventListener('astrorder:quote-to-composer', onQuote)
  })
})
