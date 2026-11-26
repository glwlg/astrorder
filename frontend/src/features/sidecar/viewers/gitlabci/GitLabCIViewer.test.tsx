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
    expect(dispatched.some((e) => e.detail?.tab === 'integrations')).toBe(true)
    window.removeEventListener('astrorder:open-settings', listener)
  })
})
