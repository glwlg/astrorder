import { readFileSync } from 'node:fs'
import path from 'node:path'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MobileVideoLightbox } from './MobileVideoLightbox'

afterEach(() => {
  cleanup()
})

describe('MobileVideoLightbox', () => {
  it('renders a mobile-friendly streaming video dialog with playsInline, speed cycle, and dismiss actions', () => {
    const onClose = vi.fn()
    render(
      <MobileVideoLightbox
        video={{ url: '/api/v1/files/raw?path=%2Ftmp%2Fdemo.mp4', name: 'demo.mp4' }}
        onClose={onClose}
      />,
    )

    const dialog = screen.getByRole('dialog', { name: '视频播放：demo.mp4' })
    const video = dialog.querySelector('video')!
    expect(video).toBeTruthy()
    expect(video.className).toContain('m-video-lightbox-player')
    expect(video.hasAttribute('controls')).toBe(true)
    expect(video.hasAttribute('playsinline')).toBe(true)
    expect(video.getAttribute('preload')).toBe('metadata')
    expect(video.querySelector('source')?.getAttribute('type')).toBe('video/mp4')

    fireEvent.click(screen.getByRole('button', { name: '播放倍速 1x' }))
    expect(screen.getByRole('button', { name: '播放倍速 1.25x' })).toBeInTheDocument()

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByRole('button', { name: '关闭视频' }))
    expect(onClose).toHaveBeenCalledTimes(2)
  })

  it('constrains high-resolution videos to fit the mobile lightbox viewport without cropping', () => {
    render(
      <MobileVideoLightbox
        video={{ url: '/api/v1/files/raw?path=%2Ftmp%2Fpromo-2k.mp4', name: 'promo-2k.mp4' }}
        onClose={() => {}}
      />,
    )

    const video = screen.getByRole('dialog').querySelector('video') as HTMLVideoElement
    Object.defineProperty(video, 'videoWidth', { value: 2560, configurable: true })
    Object.defineProperty(video, 'videoHeight', { value: 1440, configurable: true })
    Object.defineProperty(video, 'duration', { value: 61, configurable: true })
    fireEvent.loadedMetadata(video)

    expect(screen.getByText('2560×1440 · 01:01')).toBeInTheDocument()
    expect(video.style.aspectRatio).toBe('2560 / 1440')

    const css = readFileSync(path.resolve(__dirname, '../sidecar/viewers/video/videoViewer.css'), 'utf8')
    expect(css).toContain('.m-video-lightbox-player')
    expect(css).toMatch(/\.m-video-lightbox-player[\s\S]*max-inline-size:\s*100%/)
    expect(css).toMatch(/\.m-video-lightbox-player[\s\S]*max-block-size:\s*100%/)
    expect(css).toMatch(/\.m-video-lightbox-player[\s\S]*object-fit:\s*contain/)
  })
})
