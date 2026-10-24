import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../../../domain/artifact'
import { ImageViewer } from './ImageViewer'

const artifact: ArtifactRef = { id: 'image', sessionId: 's', agentId: 'a', connectionId: 'ssh-images', name: 'favicon.svg', kind: 'workspace_file', readUrl: '/api/v1/files/raw?path=icon.svg&session_id=s&connection_id=ssh-images', mediaType: 'text/plain', writable: false }

describe('ImageViewer', () => {
  beforeEach(() => {
    vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
    vi.stubGlobal('URL', class extends URL {
      static createObjectURL = vi.fn(() => 'blob:image-fixture')
      static revokeObjectURL = vi.fn()
    })
  })
  afterEach(() => { cleanup(); localStorage.clear(); vi.unstubAllGlobals() })

  it('loads remote SVG bytes with authentication and releases the image on close', async () => {
    localStorage.setItem('astrorder:token', 'image-fixture-token')
    const fetchMock = vi.fn(async () => new Response('<svg xmlns="http://www.w3.org/2000/svg"/>', { headers: { 'Content-Type': 'application/octet-stream' } }))
    vi.stubGlobal('fetch', fetchMock)
    const view = render(<MantineProvider><ImageViewer artifact={artifact} /></MantineProvider>)
    const image = await screen.findByRole('img', { name: artifact.name })
    expect(image).toHaveAttribute('src', 'blob:image-fixture')
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe(artifact.readUrl)
    expect(new Headers(init.headers).get('Authorization')).toBe('Bearer image-fixture-token')
    const blob = vi.mocked(URL.createObjectURL).mock.calls[0][0] as Blob
    expect(blob.type).toBe('image/svg+xml')
    fireEvent.load(image)
    fireEvent.click(screen.getByRole('button', { name: '原始尺寸' }))
    expect(screen.getByRole('button', { name: '适应窗口' })).toBeInTheDocument()
    view.unmount()
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:image-fixture')
  })

  it('shows a failed read and retries the same file', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response('', { status: 404 })).mockResolvedValueOnce(new Response('image'))
    vi.stubGlobal('fetch', fetchMock)
    render(<MantineProvider><ImageViewer artifact={artifact} /></MantineProvider>)
    expect(await screen.findByText('读取图片失败 (404)')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    fireEvent.error(await screen.findByRole('img', { name: artifact.name }))
    expect(await screen.findByText('图片解码失败，文件可能已损坏或格式不受支持')).toBeInTheDocument()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
  })
})
