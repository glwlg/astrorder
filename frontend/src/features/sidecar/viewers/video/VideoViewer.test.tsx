import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { artifactViewerRegistry } from '../../registry'
import { resolveArtifactFromPath } from '../../resolver'
import { VideoViewer } from './VideoViewer'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('VideoViewer', () => {
  it('resolves video extensions in registry and renders streaming video controls without blob fetch', () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    const artifact = resolveArtifactFromPath('films/ai-ops-platform.mp4', {
      id: 'session-1',
      agent_id: 'agent-1',
      title: '宣传片',
      workspace: '/workspace/OpsCore',
      status: 'idle',
      updated_at: '2026-11-24T00:00:00Z',
    })

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
    expect(fetchSpy).not.toHaveBeenCalled()

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
  })
})
