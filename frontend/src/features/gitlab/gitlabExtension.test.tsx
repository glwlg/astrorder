import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { gitlabApi, type GitLabPipelinesResponse } from '../../api/gitlab'
import type { Session } from '../../domain/types'
import {
  formatGitLabStatusQuote,
  GitLabComposerStatusPill,
} from './gitlabExtension'

const mockSession: Session = {
  id: 'sess-ci-1',
  agent_id: 'codex',
  title: 'CI Debugging',
  workspace: '/home/luwei/workspace/OpsCore',
  status: 'idle',
  updated_at: '2026-11-27T12:00:00Z',
  connection_id: 'ssh-wsl',
}

const mockPipelinesResp: GitLabPipelinesResponse = {
  configured: true,
  matched: true,
  repo: {
    host: 'gitlab.internal',
    project_path: 'acme/opscore',
    branch: 'main',
    head_sha: 'abcdef1234567890',
  },
  latest_pipeline: {
    id: 3184,
    status: 'failed',
    ref: 'main',
    sha: 'abcdef1234567890',
    short_sha: 'abcdef12',
    web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/3184',
    duration: 225,
    matches_head: true,
  },
  pipelines: [
    {
      id: 3184,
      status: 'failed',
      ref: 'main',
      sha: 'abcdef1234567890',
      short_sha: 'abcdef12',
      web_url: 'https://gitlab.internal/acme/opscore/-/pipelines/3184',
      duration: 225,
      matches_head: true,
    },
  ],
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function renderPill() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider env="test">
        <GitLabComposerStatusPill
          session={mockSession}
          statusUnavailable={false}
          statusLoading={false}
          branch="main"
        />
      </MantineProvider>
    </QueryClientProvider>,
  )
}

describe('GitLabComposerStatusPill context menu', () => {
  it('formats concise CI status quote with repo, pipeline, status, branch, and URL', () => {
    const text = formatGitLabStatusQuote(mockPipelinesResp)
    expect(text).toContain('[GitLab CI 状态] acme/opscore')
    expect(text).toContain('Pipeline #3184')
    expect(text).toContain('失败 (failed)')
    expect(text).toContain('`main` @ `abcdef12`')
    expect(text).toContain('https://gitlab.internal/acme/opscore/-/pipelines/3184')
  })

  it('opens a right-click context menu and quotes CI status or failed jobs into composer', async () => {
    vi.spyOn(gitlabApi, 'listPipelines').mockResolvedValue(mockPipelinesResp)
    vi.spyOn(gitlabApi, 'getPipelineDetail').mockResolvedValue({
      repo: mockPipelinesResp.repo,
      pipeline: mockPipelinesResp.latest_pipeline!,
      stages: [],
      jobs: [
        {
          id: 20107,
          name: 'check-edge-go-runtimes',
          stage: 'check',
          status: 'failed',
          duration: 44,
          failure_reason: 'script_failure',
        },
        {
          id: 20108,
          name: 'build-backend-image',
          stage: 'build',
          status: 'success',
          duration: 97,
        },
      ],
    })

    const quotedEvents: CustomEvent[] = []
    const onQuote = (e: Event) => quotedEvents.push(e as CustomEvent)
    window.addEventListener('astrorder:quote-to-composer', onQuote)

    renderPill()

    const pill = await screen.findByRole('button', { name: '查看 GitLab CI 流水线' })
    fireEvent.contextMenu(pill, { clientX: 240, clientY: 620 })

    const quoteStatusBtn = await screen.findByRole('menuitem', { name: '引用 CI 状态到对话框' })
    fireEvent.click(quoteStatusBtn)

    expect(quotedEvents).toHaveLength(1)
    expect(quotedEvents[0].detail.agentId).toBe('codex')
    expect(quotedEvents[0].detail.sessionId).toBe('sess-ci-1')
    expect(quotedEvents[0].detail.text).toContain('[GitLab CI 状态] acme/opscore')
    expect(quotedEvents[0].detail.text).toContain('Pipeline #3184')

    // Re-open context menu and quote failed jobs detail
    fireEvent.contextMenu(pill, { clientX: 240, clientY: 620 })
    const quoteFailedBtn = await screen.findByRole('menuitem', { name: '引用失败作业详情到对话框' })
    fireEvent.click(quoteFailedBtn)

    await waitFor(() => {
      expect(quotedEvents).toHaveLength(2)
    })
    expect(quotedEvents[1].detail.text).toContain('[GitLab CI 失败作业]')
    expect(quotedEvents[1].detail.text).toContain('check-edge-go-runtimes')
    expect(quotedEvents[1].detail.text).toContain('#20107')
    expect(quotedEvents[1].detail.text).toContain('script_failure')
    expect(quotedEvents[1].detail.text).not.toContain('build-backend-image')

    window.removeEventListener('astrorder:quote-to-composer', onQuote)
  })
})
