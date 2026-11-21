import { MantineProvider } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Session } from '../domain/types'
import { MarkdownContent } from './MarkdownContent'

const localSession: Session = {
  id: 'link-menu-local',
  agent_id: 'local-codex',
  title: '本地会话',
  workspace: 'C:/workspace/project',
  connection_id: 'local',
  status: 'idle',
  updated_at: '2026-11-06T00:00:00Z',
}

const remoteSession: Session = {
  ...localSession,
  id: 'link-menu-remote',
  workspace: '/home/fixture/project',
  connection_id: 'wsl:Ubuntu',
}

function renderMarkdown(value: string, session: Session | null = localSession) {
  return render(<MantineProvider><MarkdownContent value={value} session={session} /></MantineProvider>)
}

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  vi.spyOn(api, 'getWorkspaceRootFiles').mockImplementation(async (_id, workspace) => ({ root: workspace!, items: [] }))
  vi.spyOn(api, 'checkWorkspaceFiles').mockResolvedValue({ existing: [] })
  vi.spyOn(notifications, 'show').mockReturnValue('open-notification')
  vi.spyOn(notifications, 'hide').mockReturnValue('open-notification')
  vi.spyOn(notifications, 'update').mockReturnValue('open-notification')
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop
})

describe('Markdown link context menu', () => {
  it.each([
    ['普通网址', 'https://example.test/view?name=a%2Fb&part=1#section'],
    ['HTML 工件网址', 'https://example.test/report.html?file=%E4%B8%AD&view=latest#part'],
  ])('opens %s with the complete URL', async (_label, url) => {
    const openExternal = vi.fn().mockResolvedValue(undefined)
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { openExternal }
    renderMarkdown(`[打开链接](${url})`)

    fireEvent.contextMenu(screen.getByText('打开链接'), { clientX: 100, clientY: 120 })
    fireEvent.click(await screen.findByRole('menuitem', { name: '在默认浏览器中打开' }))

    expect(openExternal).toHaveBeenCalledWith(url)
  })

  it('reports an error when the default browser cannot be opened', async () => {
    const openExternal = vi.fn().mockRejectedValue(new Error('浏览器不可用'))
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { openExternal }
    renderMarkdown('[网址](https://example.test/view)')

    fireEvent.contextMenu(screen.getByRole('link', { name: /网址/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '在默认浏览器中打开' }))

    await waitFor(() => expect(notifications.show).toHaveBeenCalledWith(expect.objectContaining({
      title: '打开失败',
      message: '浏览器不可用',
    })))
  })

  it('keeps web links usable in a browser without the desktop bridge', async () => {
    const url = 'https://example.test/page?part=%2Fdata#section'
    renderMarkdown(`[网址](${url})`)

    fireEvent.contextMenu(screen.getByRole('link', { name: /网址/ }))
    const item = await screen.findByRole('menuitem', { name: '在默认浏览器中打开' })
    expect(item).toHaveAttribute('href', url)
    expect(item).toHaveAttribute('target', '_blank')
  })

  it('does not treat other URL schemes as file links', () => {
    renderMarkdown('[脚本](javascript:payload.pdf) [邮箱](mailto:user@example.test.pdf)')
    expect(screen.queryByRole('button', { name: /脚本/ })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /邮箱/ })).toHaveAttribute('href', 'mailto:user@example.test.pdf')
  })

  it('opens local files with their system association and strips line numbers', async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ status: 'ok' }))
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('[设计文档](docs/design.md:23)')

    fireEvent.contextMenu(screen.getByRole('button', { name: /设计文档/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/system/open-file')
    expect(JSON.parse(fetch.mock.calls[0][1].body as string)).toEqual({
      path: 'C:/workspace/project/docs/design.md',
      action: 'open',
    })
  })

  it.each(['wsl:Ubuntu', 'ssh:server'])('stages a %s file without modifying the clipboard before opening it', async (connectionId) => {
    const session = { ...remoteSession, connection_id: connectionId }
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ local_paths: ['C:\\Temp\\copy-123\\设计文档.pdf'], clipboard_set: false }))
      .mockResolvedValueOnce(jsonResponse({ status: 'ok' }))
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('[远程文档](<file:///home/fixture/project/docs/设计文档.pdf>)', session)

    fireEvent.contextMenu(screen.getByRole('button', { name: /远程文档/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/files/stage')
    expect(JSON.parse(fetch.mock.calls[0][1].body as string)).toEqual({
      paths: ['/home/fixture/project/docs/设计文档.pdf'],
      session_id: session.id,
      connection_id: connectionId,
      copy_to_clipboard: false,
    })
    expect(fetch.mock.calls[1][0]).toBe('/api/v1/system/open-file')
    expect(JSON.parse(fetch.mock.calls[1][1].body as string)).toEqual({
      path: 'C:\\Temp\\copy-123\\设计文档.pdf',
      action: 'open',
    })
  })

  it('offers the same file action for inline images', async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ status: 'ok' }))
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('![预览图片](images/design.png)')

    fireEvent.contextMenu(screen.getByAltText('预览图片'))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
    expect(JSON.parse(fetch.mock.calls[0][1].body as string).path).toBe('C:/workspace/project/images/design.png')
  })

  it('opens filenames containing a literal percent character', async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ status: 'ok' }))
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('[进度文档](file:///C:/workspace/project/100%25.pdf)')

    fireEvent.contextMenu(screen.getByRole('button', { name: /进度文档/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
    expect(JSON.parse(fetch.mock.calls[0][1].body as string).path).toBe('C:/workspace/project/100%.pdf')
  })

  it('opens Windows paths containing backslashes and spaces', async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ status: 'ok' }))
    vi.stubGlobal('fetch', fetch)
    renderMarkdown(String.raw`[安装包](<P:\workspace\Astrorder Setup.exe>)`)

    fireEvent.contextMenu(screen.getByRole('button', { name: /安装包/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
    expect(JSON.parse(fetch.mock.calls[0][1].body as string).path).toBe(String.raw`P:\workspace\Astrorder Setup.exe`)
  })

  it('offers the file action for links with unregistered extensions', async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ status: 'ok' }))
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('[数据文件](output/data.custom)')

    fireEvent.contextMenu(screen.getByRole('button', { name: /数据文件/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1))
    expect(JSON.parse(fetch.mock.calls[0][1].body as string).path).toBe('C:/workspace/project/output/data.custom')
  })

  it('reports staging errors and does not attempt to open a file', async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ detail: '远程文件不存在' }, 404))
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('[远程文档](docs/missing.pdf)', remoteSession)

    fireEvent.contextMenu(screen.getByRole('button', { name: /远程文档/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(notifications.update).toHaveBeenCalledWith(expect.objectContaining({
      title: '打开文件失败',
      message: '远程文件不存在',
      color: 'red',
    })))
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('reports a system file opener failure', async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn().mockResolvedValue(jsonResponse({ detail: '系统无法打开文件' }, 500))
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('[文件](docs/design.md)')

    fireEvent.contextMenu(screen.getByRole('button', { name: /文件/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(notifications.update).toHaveBeenCalledWith(expect.objectContaining({
      title: '打开文件失败',
      message: '系统无法打开文件',
    })))
  })

  it('does not open a server-side file from a browser without the desktop bridge', async () => {
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('[本地文件](docs/design.md)')

    fireEvent.contextMenu(screen.getByRole('button', { name: /本地文件/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(notifications.update).toHaveBeenCalledWith(expect.objectContaining({
      message: '请在星序桌面端打开文件',
    })))
    expect(fetch).not.toHaveBeenCalled()
  })

  it('does not guess which session owns a file when the session is unavailable', async () => {
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    const fetch = vi.fn()
    vi.stubGlobal('fetch', fetch)
    renderMarkdown('[文件](/home/other/project/data.pdf)', null)

    fireEvent.contextMenu(screen.getByRole('button', { name: /文件/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '用系统默认应用打开' }))

    await waitFor(() => expect(notifications.update).toHaveBeenCalledWith(expect.objectContaining({
      message: '无法确定文件所在的会话',
    })))
    expect(fetch).not.toHaveBeenCalled()
  })

  it('downloads remote temp directory files directly from the file context menu', async () => {
    const saveDownload = vi.fn().mockResolvedValue(true)
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = { saveDownload }
    renderMarkdown('[远程临时报表](/tmp/report.xlsx)', remoteSession)

    fireEvent.contextMenu(screen.getByRole('button', { name: /远程临时报表/ }))
    fireEvent.click(await screen.findByRole('menuitem', { name: '下载到本地' }))

    await waitFor(() => {
      expect(saveDownload).toHaveBeenCalledWith(
        '/api/v1/files/raw?path=%2Ftmp%2Freport.xlsx&download=1&session_id=link-menu-remote&connection_id=wsl%3AUbuntu',
        'report.xlsx',
      )
    })
  })
})
