import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MarkdownContent } from './MarkdownContent'
import { api } from '../api/client'
import type { Session } from '../domain/types'
import { artifactViewerRegistry } from '../features/sidecar/registry'

const session: Session = {
  id: 'autolink-test',
  agent_id: 'local-codex',
  title: '测试',
  workspace: 'C:/workspace/autolink-test',
  status: 'idle',
  updated_at: '2026-09-29T00:00:00Z',
}

describe('MarkdownContent Auto-Link Artifacts', () => {
  it('only links bare filenames confirmed in the session workspace root', async () => {
    const drawioDisposable = artifactViewerRegistry.register({
      id: 'drawio-viewer',
      pluginId: 'drawio-viewer',
      source: 'esm',
      title: '矢量流程图',
      icon: () => null,
      extensions: ['.drawio', '.drawio.xml', '.drawio.svg'],
      component: () => null,
      capabilities: { canEdit: true },
    })
    try {
      vi.spyOn(api, 'getWorkspaceRootFiles').mockResolvedValue({
        root: session.workspace!,
        items: [{ name: '集团对接_方案一_直连中台结果库.drawio', is_dir: false }],
      })
      const text = '方案一交付物：集团对接_方案一_直连中台结果库.drawio，周报_20260929.html 不在这里'
      render(
        <MantineProvider>
          <MarkdownContent value={text} session={session} />
        </MantineProvider>,
      )

      expect(screen.queryByRole('button', { name: /集团对接_方案一_直连中台结果库\.drawio/ })).toBeNull()
      const btn = await screen.findByRole('button', { name: /集团对接_方案一_直连中台结果库\.drawio/ })
      expect(btn).toBeInTheDocument()
      expect(btn).toHaveClass('markdown-file-link')
      expect(screen.queryByRole('button', { name: /周报_20260929\.html/ })).toBeNull()
    } finally {
      drawioDisposable.dispose()
      vi.restoreAllMocks()
    }
  })

  it('renders markdown local image link as preview button', () => {
    const text = '高清预览图：[集团对接_方案一.png](C:/Users/test/集团对接_方案一.png)'
    render(
      <MantineProvider>
        <MarkdownContent value={text} />
      </MantineProvider>,
    )

    const btn = screen.getByRole('button', { name: /集团对接_方案一\.png/ })
    expect(btn).toBeInTheDocument()
    expect(btn).toHaveClass('markdown-image-link')
  })

  it('automatically detects backticked windows image path and renders previewable link', () => {
    const text = '原始文件：`P:\\workspace\\example-org\\game\\ExampleGame\\.codex-tmp\\asset-generation\\test_vfx\\raw_taiji_bagua_circle.png`'
    render(
      <MantineProvider>
        <MarkdownContent value={text} />
      </MantineProvider>,
    )

    const btn = screen.getByRole('button', { name: /raw_taiji_bagua_circle\.png/ })
    expect(btn).toBeInTheDocument()
    expect(btn).toHaveClass('markdown-image-link')
  })

  it('checks a remote session against its own workspace', async () => {
    const remoteSession = { ...session, id: 'autolink-remote', workspace: '/home/fixture/project', connection_id: 'ssh-debian' }
    const getRootFiles = vi.spyOn(api, 'getWorkspaceRootFiles').mockResolvedValue({
      root: '/home/fixture/project',
      items: [{ name: 'remote.md', is_dir: false }],
    })
    render(
      <MantineProvider>
        <MarkdownContent value="remote.md missing.md" session={remoteSession} />
      </MantineProvider>,
    )

    expect(await screen.findByRole('button', { name: /remote\.md/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /missing\.md/ })).toBeNull()
    expect(getRootFiles).toHaveBeenCalledWith(remoteSession.id, remoteSession.workspace, 'ssh-debian')
    vi.restoreAllMocks()
  })

  it('automatically detects backticked markdown file path and renders file link', () => {
    const text = '设计文档：`docs/battle-vfx-deep-design.md`'
    render(
      <MantineProvider>
        <MarkdownContent value={text} />
      </MantineProvider>,
    )

    const btn = screen.getByRole('button', { name: /battle-vfx-deep-design\.md/ })
    expect(btn).toBeInTheDocument()
    expect(btn).toHaveClass('markdown-file-link')
  })

  it('automatically detects windows installer path with spaces and renders file link', () => {
    const text = '安装包路径：`P:\\workspace\\example-org\\ai\\astrorder\\desktop\\release\\Astrorder Setup 0.1.0.exe`'
    render(
      <MantineProvider>
        <MarkdownContent value={text} />
      </MantineProvider>,
    )

    const btn = screen.getByRole('button', { name: /Astrorder Setup 0\.1\.0\.exe/ })
    expect(btn).toBeInTheDocument()
    expect(btn).toHaveClass('markdown-file-link')
  })
})
