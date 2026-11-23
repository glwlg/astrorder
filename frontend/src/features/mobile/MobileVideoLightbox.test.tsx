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
})
