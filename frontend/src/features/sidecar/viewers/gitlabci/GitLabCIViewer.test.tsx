import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { gitlabApi } from '../../../../api/gitlab'
import type { ArtifactRef } from '../../../../domain/artifact'
import { GitLabCIViewer } from './GitLabCIViewer'

const artifact: ArtifactRef = {
  id: 'gitlabci:s1',
  name: 'GitLab CI',
  kind: 'workspace_file',
  mediaType: 'application/x-gitlab-ci',
  readUrl: '',
  writable: false,
  path: '/home/luwei/workspace/demo',
  sessionId: 's1',
  agentId: 'codex',
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

describe('GitLabCIViewer', () => {
  it('renders pipelines, stage jobs, and auto-loads failed job trace', async () => {
    vi.spyOn(gitlabApi, 'listPipelines').mockResolvedValue({
      configured: true,
      matched: true,
      repo: {
        host: 'gitlab.internal',
        project_path: 'acme/opscore',
        branch: 'main',
        head_sha: 'abcdef1234567890',
      },
      instance: {
        id: 'gl-1',
        name: '公司自建 GitLab',
        base_url: 'https://gitlab.internal',
        host: 'gitlab.internal',
        token_set: true,
        enabled: true,
        is_default: true,
        created_at: '2026-11-26T00:00:00Z',
        updated_at: '2026-11-26T00:00:00Z',
      },
      latest_pipeline: {
        id: 701,
        status: 'failed',
        ref: 'main',
        sha: 'abcdef1234567890',
        short_sha: 'abcdef12',
        web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/701',
        duration: 42,
        matches_head: true,
      },
      pipelines: [
        {
          id: 701,
          status: 'failed',
          ref: 'main',
          sha: 'abcdef1234567890',
          short_sha: 'abcdef12',
          web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/701',
          duration: 42,
          matches_head: true,
        },
      ],
    })

    vi.spyOn(gitlabApi, 'getPipelineDetail').mockResolvedValue({
      pipeline: {
        id: 701,
        status: 'failed',
        ref: 'main',
        sha: 'abcdef1234567890',
        short_sha: 'abcdef12',
        web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/701',
        duration: 42,
        matches_head: true,
      },
      stages: [
        {
          name: 'test',
          status: 'failed',
          jobs: [
            {
              id: 8001,
              name: 'unit-tests',
              stage: 'test',
              status: 'failed',
              duration: 18,
              failure_reason: 'script_failure',
            },
          ],
        },
      ],
      jobs: [
        {
          id: 8001,
          name: 'unit-tests',
          stage: 'test',
          status: 'failed',
          duration: 18,
          failure_reason: 'script_failure',
        },
      ],
    })

    vi.spyOn(gitlabApi, 'getJobTrace').mockResolvedValue({
      job_id: 8001,
      tail_lines: 200,
      total_lines: 3,
      truncated: false,
      trace: 'Running go test...\nFAIL TestPipeline\nexit status 1',
    })

    render(
      <MantineProvider env="test">
        <GitLabCIViewer artifact={artifact} />
      </MantineProvider>,
    )

    expect(await screen.findByText('acme/opscore')).toBeInTheDocument()
    expect(await screen.findByText('unit-tests')).toBeInTheDocument()
    expect(await screen.findByText(/FAIL TestPipeline/)).toBeInTheDocument()
  })

  it('guides user to settings when no GitLab instance is configured', async () => {
    vi.spyOn(gitlabApi, 'listPipelines').mockResolvedValue({
      configured: false,
      matched: false,
      reason: '尚未配置 GitLab 实例',
      pipelines: [],
    })
    const dispatched: CustomEvent[] = []
    const listener = (e: Event) => dispatched.push(e as CustomEvent)
    window.addEventListener('astrorder:open-settings', listener)

    render(
      <MantineProvider env="test">
        <GitLabCIViewer artifact={artifact} />
      </MantineProvider>,
    )

    const btn = await screen.findByRole('button', { name: '前往配置 GitLab 实例' })
    fireEvent.click(btn)
    expect(dispatched.some((e) => e.detail?.tab === 'plugins' && e.detail?.pluginId === 'gitlab-ci-viewer')).toBe(true)
    window.removeEventListener('astrorder:open-settings', listener)
  })

  it('opens job logs in a docked sibling pane outside the jobs scroller and supports switching, maximizing, and closing', async () => {
    vi.spyOn(gitlabApi, 'listPipelines').mockResolvedValue({
      configured: true,
      matched: true,
      repo: { host: 'gitlab.internal', project_path: 'acme/opscore', branch: 'main', head_sha: 'abcdef1234567890' },
      instance: {
        id: 'gl-1',
        name: '公司自建 GitLab',
        base_url: 'https://gitlab.internal',
        host: 'gitlab.internal',
        token_set: true,
        enabled: true,
        is_default: true,
        created_at: '2026-11-26T00:00:00Z',
        updated_at: '2026-11-26T00:00:00Z',
      },
      latest_pipeline: {
        id: 702,
        status: 'success',
        ref: 'main',
        sha: 'abcdef1234567890',
        short_sha: 'abcdef12',
        web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/702',
        duration: 60,
        matches_head: true,
      },
      pipelines: [
        {
          id: 702,
          status: 'success',
          ref: 'main',
          sha: 'abcdef1234567890',
          short_sha: 'abcdef12',
          web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/702',
          duration: 60,
          matches_head: true,
        },
      ],
    })

    const jobs = [
      { id: 9001, name: 'build-backend', stage: 'build', status: 'success', duration: 30 },
      { id: 9002, name: 'build-frontend', stage: 'build', status: 'success', duration: 25 },
    ]
    vi.spyOn(gitlabApi, 'getPipelineDetail').mockResolvedValue({
      pipeline: {
        id: 702,
        status: 'success',
        ref: 'main',
        sha: 'abcdef1234567890',
        short_sha: 'abcdef12',
        web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/702',
        duration: 60,
        matches_head: true,
      },
      stages: [{ name: 'build', status: 'success', jobs }],
      jobs,
    })

    const traceSpy = vi.spyOn(gitlabApi, 'getJobTrace').mockImplementation(async (jobId) => ({
      job_id: jobId,
      tail_lines: 200,
      total_lines: 2,
      truncated: false,
      trace: jobId === 9001 ? 'backend build ok' : 'frontend build ok',
    }))

    const { container } = render(
      <MantineProvider env="test">
        <GitLabCIViewer artifact={artifact} />
      </MantineProvider>,
    )

    const logBtn = await screen.findByRole('button', { name: '查看 build-frontend 日志' })
    fireEvent.click(logBtn)

    const dock = await screen.findByRole('region', { name: '作业日志 build-frontend' })
    expect(dock.closest('.gitlab-ci-detail-scroll')).toBeNull()
    expect(await screen.findByText('frontend build ok')).toBeInTheDocument()

    // Maximize hides the jobs scroller so the log occupies full height
    fireEvent.click(screen.getByRole('button', { name: '最大化日志' }))
    expect(container.querySelector('.gitlab-ci-detail-scroll')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '还原分屏' }))
    expect(container.querySelector('.gitlab-ci-detail-scroll')).not.toBeNull()

    // Close and re-click the SAME job's log button: dock reopens immediately
    fireEvent.click(screen.getByRole('button', { name: '关闭日志面板' }))
    expect(screen.queryByRole('region', { name: '作业日志 build-frontend' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: '查看 build-frontend 日志' }))
    expect(await screen.findByRole('region', { name: '作业日志 build-frontend' })).toBeInTheDocument()
    expect(traceSpy).toHaveBeenCalledWith(9002, expect.objectContaining({ tail: 200 }))
  })

  it('shows a selection toolbar when log text is highlighted and sends the snippet to composer', async () => {
    vi.spyOn(gitlabApi, 'listPipelines').mockResolvedValue({
      configured: true,
      matched: true,
      repo: { host: 'gitlab.internal', project_path: 'acme/opscore', branch: 'main', head_sha: 'abcdef1234567890' },
      instance: {
        id: 'gl-1',
        name: '公司自建 GitLab',
        base_url: 'https://gitlab.internal',
        host: 'gitlab.internal',
        token_set: true,
        enabled: true,
        is_default: true,
        created_at: '2026-11-26T00:00:00Z',
        updated_at: '2026-11-26T00:00:00Z',
      },
      latest_pipeline: {
        id: 703,
        status: 'failed',
        ref: 'main',
        sha: 'abcdef1234567890',
        short_sha: 'abcdef12',
        web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/703',
        duration: 44,
        matches_head: true,
      },
      pipelines: [
        {
          id: 703,
          status: 'failed',
          ref: 'main',
          sha: 'abcdef1234567890',
          short_sha: 'abcdef12',
          web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/703',
          duration: 44,
          matches_head: true,
        },
      ],
    })
    const failedJob = { id: 9101, name: 'check-edge-go-runtimes', stage: 'check', status: 'failed', duration: 44 }
    vi.spyOn(gitlabApi, 'getPipelineDetail').mockResolvedValue({
      pipeline: {
        id: 703,
        status: 'failed',
        ref: 'main',
        sha: 'abcdef1234567890',
        short_sha: 'abcdef12',
        web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/703',
        duration: 44,
        matches_head: true,
      },
      stages: [{ name: 'check', status: 'failed', jobs: [failedJob] }],
      jobs: [failedJob],
    })
    vi.spyOn(gitlabApi, 'getJobTrace').mockResolvedValue({
      job_id: 9101,
      tail_lines: 200,
      total_lines: 3,
      truncated: false,
      trace: 'step 1 ok\nERROR: missing go.sum entry\nexit code 1',
    })

    const quotedEvents: CustomEvent[] = []
    const onQuote = (e: Event) => quotedEvents.push(e as CustomEvent)
    window.addEventListener('astrorder:quote-to-composer', onQuote)

    const { container } = render(
      <MantineProvider env="test">
        <GitLabCIViewer artifact={artifact} />
      </MantineProvider>,
    )

    const pre = (await screen.findByText(/ERROR: missing go\.sum entry/)).closest('pre') as HTMLPreElement
    expect(pre).not.toBeNull()
    const textNode = pre.firstChild as Text
    const range = document.createRange()
    range.setStart(textNode, 10)
    range.setEnd(textNode, 37)
    const sel = window.getSelection()!
    sel.removeAllRanges()
    sel.addRange(range)
    fireEvent.mouseUp(pre)

    const toolbar = await screen.findByRole('toolbar', { name: '日志片段操作' })
    expect(toolbar).toBeInTheDocument()
    const sendBtn = container.querySelector('.gitlab-ci-selection-btn.is-primary') as HTMLButtonElement
    fireEvent.click(sendBtn)

    expect(quotedEvents).toHaveLength(1)
    expect(quotedEvents[0].detail.sessionId).toBe('s1')
    expect(quotedEvents[0].detail.agentId).toBe('codex')
    expect(quotedEvents[0].detail.text).toContain('[GitLab CI 日志片段]')
    expect(quotedEvents[0].detail.text).toContain('Pipeline #703')
    expect(quotedEvents[0].detail.text).toContain('check-edge-go-runtimes')
    expect(quotedEvents[0].detail.text).toContain('ERROR: missing go.sum entry')

    window.removeEventListener('astrorder:quote-to-composer', onQuote)
  })
})
