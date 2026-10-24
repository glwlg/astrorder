import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Session } from '../domain/types'
import { MarkdownContent } from './MarkdownContent'

const generatedImage = '/home/luwei/.codex/generated_images/01a10e85-1f2f-7913-ab78-7be247c1c86c/exec-1d4977e4-e5ed-491e-af04-e5d4f36e9934.png'
const session: Session = {
  id: 'image thread &1',
  agent_id: 'remote-codex',
  title: '图片测试',
  workspace: '/home/luwei/project',
  connection_id: 'wsl:Ubuntu',
  status: 'idle',
  updated_at: '2026-10-20T00:00:00Z',
}

beforeEach(() => {
  vi.spyOn(api, 'getWorkspaceRootFiles').mockResolvedValue({ root: session.workspace!, items: [] })
  vi.spyOn(api, 'checkWorkspaceFiles').mockResolvedValue({ existing: [] })
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('Markdown filesystem images', () => {
  it.each([
    ['WSL generated image outside the project', generatedImage, session, generatedImage],
    ['SSH generated image outside the project', generatedImage, { ...session, connection_id: 'ssh:image-host' }, generatedImage],
    ['Windows absolute image', 'C:/Users/luwei/output/design.png', { ...session, connection_id: 'local', workspace: 'C:/project' }, 'C:/Users/luwei/output/design.png'],
    ['relative image', 'output/design.png', session, '/home/luwei/project/output/design.png'],
    ['image with spaces and Unicode', '/home/luwei/output/小妖 设计 #1.png', session, '/home/luwei/output/小妖 设计 #1.png'],
  ])('routes %s through the owning filesystem', (_name, path, owner, expectedPath) => {
    const onImageClick = vi.fn()
    render(<MantineProvider><MarkdownContent value={`![游戏设计图](<${path}>)`} session={owner} onImageClick={onImageClick} /></MantineProvider>)

    const src = screen.getByAltText('游戏设计图').getAttribute('src')!
    const url = new URL(src, 'http://localhost')
    expect(url.pathname).toBe('/api/v1/files/raw')
    expect(url.searchParams.get('path')).toBe(expectedPath)
    expect(url.searchParams.get('session_id')).toBe(owner.id)
    expect(url.searchParams.get('connection_id')).toBe(owner.connection_id)
    fireEvent.click(screen.getByRole('button', { name: '放大图片：游戏设计图' }))
    expect(onImageClick).toHaveBeenCalledWith(src)
  })

  it.each([
    'https://example.test/design.png',
    '/api/v1/attachments/design.png',
    '/api/v1/files/raw?path=%2Fhome%2Fimage.png&session_id=other',
  ])('preserves image URL %s', src => {
    render(<MantineProvider><MarkdownContent value={`![图片](${src})`} session={session} /></MantineProvider>)
    expect(screen.getByAltText('图片')).toHaveAttribute('src', src)
  })

  it('does not render executable or protocol-relative image URLs', () => {
    const { container } = render(<MantineProvider><MarkdownContent value={'![脚本](javascript:alert%281%29) ![外站](//example.test/design.png)'} session={session} /></MantineProvider>)
    expect(container.querySelector('img')).toBeNull()
  })
})
