import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MobileImageLightbox } from './MobileImageLightbox'

function dispatchPointer(
  target: Element,
  type: 'pointerdown' | 'pointermove' | 'pointerup' | 'pointercancel',
  init: { pointerId: number; clientX: number; clientY: number; pointerType?: string },
) {
  const EventCtor = typeof PointerEvent === 'function' ? PointerEvent : MouseEvent
  const event = new EventCtor(type, {
    bubbles: true,
    cancelable: true,
    clientX: init.clientX,
    clientY: init.clientY,
  })
  Object.defineProperty(event, 'pointerId', { value: init.pointerId })
  Object.defineProperty(event, 'pointerType', { value: init.pointerType ?? 'touch' })
  target.dispatchEvent(event)
}

describe('MobileImageLightbox', () => {
  beforeEach(() => {
    vi.stubGlobal('URL', class extends URL {
      static createObjectURL = vi.fn(() => 'blob:mobile-lightbox-img')
      static revokeObjectURL = vi.fn()
    })
  })

  afterEach(() => {
    cleanup()
    localStorage.clear()
    vi.unstubAllGlobals()
  })

  it('fetches authenticated /api/v1/files/raw images, supports pinch-to-zoom, pan, reset, and closes cleanly', async () => {
    localStorage.setItem('astrorder:token', 'lightbox-secret')
    const fetchMock = vi.fn(async () => new Response('png-bytes', { headers: { 'Content-Type': 'image/png' } }))
    vi.stubGlobal('fetch', fetchMock)

    const onClose = vi.fn()
    const src = '/api/v1/files/raw?path=%2Fhome%2Fluwei%2Fworkspace%2FOpsCore%2Fdocs%2F03-drill-arena.png&session_id=s1&connection_id=ssh-1'
    const view = render(<MobileImageLightbox src={src} onClose={onClose} />)

    const dialog = screen.getByRole('dialog', { name: '图片预览' })
    expect(dialog.tagName).toBe('DIALOG')
    expect(dialog).not.toHaveAttribute('role')
    expect(screen.getByText('03-drill-arena.png')).toBeInTheDocument()

    const img = await screen.findByRole('img', { name: '03-drill-arena.png' })
    expect(img).toHaveAttribute('src', 'blob:mobile-lightbox-img')
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer lightbox-secret')

    const stage = dialog.querySelector('.m-lightbox-stage') as HTMLElement
    vi.spyOn(stage, 'getBoundingClientRect').mockReturnValue({
      left: 0, top: 0, width: 390, height: 844, right: 390, bottom: 844, x: 0, y: 0, toJSON: () => ({}),
    })
    Object.defineProperty(stage, 'clientWidth', { configurable: true, value: 390 })
    Object.defineProperty(stage, 'clientHeight', { configurable: true, value: 844 })
    Object.defineProperty(img, 'offsetWidth', { configurable: true, value: 390 })
    Object.defineProperty(img, 'offsetHeight', { configurable: true, value: 500 })

    // Two-finger pinch zoom from distance 100 -> 250 => 2.5x scale
    dispatchPointer(stage, 'pointerdown', { pointerId: 1, clientX: 145, clientY: 422 })
    dispatchPointer(stage, 'pointerdown', { pointerId: 2, clientX: 245, clientY: 422 })
    dispatchPointer(stage, 'pointermove', { pointerId: 1, clientX: 70, clientY: 422 })
    dispatchPointer(stage, 'pointermove', { pointerId: 2, clientX: 320, clientY: 422 })

    await waitFor(() => expect(screen.getByRole('button', { name: '重置缩放' })).toHaveTextContent('250%'))
    expect(stage.dataset.scale).toBe('2.50')

    // Release both fingers -> stays zoomed without closing
    dispatchPointer(stage, 'pointerup', { pointerId: 2, clientX: 320, clientY: 422 })
    dispatchPointer(stage, 'pointerup', { pointerId: 1, clientX: 70, clientY: 422 })
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: '重置缩放' })).toHaveTextContent('250%')

    // Reset zoom button brings scale back to 100%
    fireEvent.click(screen.getByRole('button', { name: '重置缩放' }))
    expect(screen.getByRole('button', { name: '重置缩放' })).toHaveTextContent('100%')

    // Explicit close button closes lightbox
    fireEvent.click(screen.getByRole('button', { name: '关闭图片' }))
    expect(onClose).toHaveBeenCalledTimes(1)

    view.unmount()
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:mobile-lightbox-img')
  })

  it('shows error state with retry button when image fails to load and recovers on retry', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response('', { status: 422 }))
      .mockResolvedValueOnce(new Response('png-bytes', { headers: { 'Content-Type': 'image/png' } }))
    vi.stubGlobal('fetch', fetchMock)

    render(<MobileImageLightbox src="/api/v1/files/raw?path=%2Ftmp%2Fshot.png" onClose={vi.fn()} />)
    expect(await screen.findByText('读取图片失败 (422)')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(await screen.findByRole('img', { name: 'shot.png' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('renders data URL attachments directly and dismisses on backdrop tap at 1x zoom', () => {
    const onClose = vi.fn()
    const dataUrl = 'data:image/png;base64,iVBORw0KGgo='
    render(<MobileImageLightbox src={dataUrl} onClose={onClose} />)

    expect(screen.getByRole('img', { name: '图片预览' })).toHaveAttribute('src', dataUrl)
    const stage = document.querySelector('.m-lightbox-stage') as HTMLElement
    dispatchPointer(stage, 'pointerdown', { pointerId: 1, clientX: 20, clientY: 20 })
    dispatchPointer(stage, 'pointerup', { pointerId: 1, clientX: 20, clientY: 20 })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('supports multi-image gallery swiping, prev/next buttons, and keyboard arrows', () => {
    const images = [
      'data:image/png;base64,AAA=',
      'data:image/png;base64,BBB=',
      'data:image/png;base64,CCC=',
    ]
    const onIndexChange = vi.fn()
    const { rerender } = render(
      <MobileImageLightbox images={images} index={0} onIndexChange={onIndexChange} onClose={vi.fn()} />,
    )

    expect(screen.getByText('1 / 3')).toBeInTheDocument()
    const stage = document.querySelector('.m-lightbox-stage') as HTMLElement

    // Swipe left at 1x -> next image (index 1)
    dispatchPointer(stage, 'pointerdown', { pointerId: 1, clientX: 280, clientY: 300 })
    dispatchPointer(stage, 'pointermove', { pointerId: 1, clientX: 160, clientY: 304 })
    dispatchPointer(stage, 'pointerup', { pointerId: 1, clientX: 160, clientY: 304 })
    expect(onIndexChange).toHaveBeenCalledWith(1)

    rerender(<MobileImageLightbox images={images} index={1} onIndexChange={onIndexChange} onClose={vi.fn()} />)
    expect(screen.getByText('2 / 3')).toBeInTheDocument()

    // Keyboard ArrowRight -> index 2; ArrowLeft -> index 0
    fireEvent.keyDown(window, { key: 'ArrowRight' })
    expect(onIndexChange).toHaveBeenCalledWith(2)
    fireEvent.keyDown(window, { key: 'ArrowLeft' })
    expect(onIndexChange).toHaveBeenCalledWith(0)
  })
})
