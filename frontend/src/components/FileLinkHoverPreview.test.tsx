import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearStoredAppProfileForTest, setAppProfile } from '../features/office/appProfile'
import { FileLinkHoverPreview } from './FileLinkHoverPreview'

const mockAuthenticatedApiFetch = vi.fn()

vi.mock('../api/client', () => ({
  authenticatedApiFetch: (...args: unknown[]) => mockAuthenticatedApiFetch(...args),
}))

describe('FileLinkHoverPreview worktree fallback presentation', () => {
  beforeEach(() => {
    mockAuthenticatedApiFetch.mockReset()
  })

  afterEach(() => {
    cleanup()
    clearStoredAppProfileForTest()
  })

  it('renders worktree origin bar and forwards real resolved path to copy/open actions', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    const resolvedPath = '/tmp/opscore-wave4/backend/migrations/biz-system/003_audit.sql'
    const worktreePath = '/tmp/opscore-wave4'
    const headers = new Headers({
      'X-Astrorder-Resolved-Path': encodeURIComponent(resolvedPath),
      'X-Astrorder-Worktree-Path': encodeURIComponent(worktreePath),
      'X-Astrorder-Worktree-Branch': encodeURIComponent('feat/wave4'),
    })
    const bodyText = 'ALTER TABLE biz_system_audit_logs ADD COLUMN trace_id TEXT;\n'
    mockAuthenticatedApiFetch.mockResolvedValue({
      ok: true,
      status: 200,
      headers,
      arrayBuffer: async () => new TextEncoder().encode(bodyText).buffer,
    })

    const onCopyFile = vi.fn().mockResolvedValue(true)
    const onOpen = vi.fn()
    const onClose = vi.fn()

    render(
      <FileLinkHoverPreview
        target={{
          rect: { left: 120, right: 320, top: 100, bottom: 124, width: 200, height: 24 },
          path: '/home/luwei/workspace/OpsCore/backend/migrations/biz-system/003_audit.sql',
          displayName: '003_audit.sql',
          readUrl: '/api/v1/files/raw?path=%2Fhome%2Fluwei%2Fworkspace%2FOpsCore%2Fbackend%2Fmigrations%2Fbiz-system%2F003_audit.sql&session_id=s1',
          isImage: false,
          isNonPreviewable: false,
        }}
        onMouseEnter={() => {}}
        onMouseLeave={() => {}}
        onCopyFile={onCopyFile}
        onOpen={onOpen}
        onClose={onClose}
      />,
    )

    await waitFor(() => {
      expect(screen.getByText(/来自 Worktree:/)).toBeTruthy()
    })
    expect(screen.getByText('/tmp/opscore-wave4')).toBeTruthy()
    expect(screen.getByText('(feat/wave4)')).toBeTruthy()

    fireEvent.click(screen.getByLabelText('复制 Worktree 真实路径'))
    expect(writeText).toHaveBeenCalledWith(resolvedPath)

    fireEvent.click(screen.getByLabelText('复制文件到剪贴板'))
    await waitFor(() => {
      expect(onCopyFile).toHaveBeenCalledWith(resolvedPath)
    })

    fireEvent.click(screen.getByLabelText('在侧边栏打开文件'))
    expect(onOpen).toHaveBeenCalledWith(resolvedPath)
  })

  it('hides Xftp tooltip and Worktree bar in office mode', async () => {
    setAppProfile('office', { syncPluginStore: false })
    const headers = new Headers({
      'X-Astrorder-Resolved-Path': encodeURIComponent('/tmp/office-wt/report.md'),
      'X-Astrorder-Worktree-Path': encodeURIComponent('/tmp/office-wt'),
      'X-Astrorder-Worktree-Branch': encodeURIComponent('feat/draft'),
    })
    mockAuthenticatedApiFetch.mockResolvedValue({
      ok: true,
      status: 200,
      headers,
      arrayBuffer: async () => new TextEncoder().encode('# 季度报告\n').buffer,
    })

    render(
      <FileLinkHoverPreview
        target={{
          rect: { left: 120, right: 320, top: 100, bottom: 124, width: 200, height: 24 },
          path: '/workspace/office/report.md',
          displayName: 'report.md',
          readUrl: '/api/v1/files/raw?path=%2Fworkspace%2Foffice%2Freport.md&session_id=s-office',
          isImage: false,
          isNonPreviewable: false,
        }}
        onMouseEnter={() => {}}
        onMouseLeave={() => {}}
        onCopyFile={vi.fn().mockResolvedValue(true)}
        onOpen={vi.fn()}
        onClose={vi.fn()}
      />,
    )

    await waitFor(() => {
      expect(screen.getByText('季度报告')).toBeTruthy()
    })
    expect(screen.queryByText(/来自 Worktree:/)).toBeNull()
    const copyFileBtn = screen.getByLabelText('复制文件到剪贴板')
    expect(copyFileBtn.getAttribute('title')).not.toContain('Xftp')
  })
})
