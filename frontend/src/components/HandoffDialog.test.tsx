import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Agent, Session } from '../domain/types'
import { useBackgroundTasks } from '../state/backgroundTasks'
import { HandoffDialog } from './HandoffDialog'

const sourceSession: Session = {
  id: 'codex-1',
  agent_id: 'remote-codex',
  title: 'Codex 工作',
  workspace: '/repo',
  connection_id: 'ssh-wsl',
  status: 'idle',
  updated_at: '2026-09-07T10:00:00Z',
  source_id: 'source-remote-codex',
}

const agyAgent: Agent = {
  id: 'remote-agy',
  kind: 'antigravity-ssh',
  name: 'WSL AGY',
  status: 'ready',
  capabilities: [],
  limitation: null,
  source_id: 'source-remote-agy',
  connection_id: 'ssh-wsl',
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  for (const task of Object.values(useBackgroundTasks.getState().tasks)) {
    useBackgroundTasks.getState().dismiss(task.id)
  }
})

describe('HandoffDialog Antigravity model + effort separation', () => {
  it('groups multi-effort Antigravity variants into one base model and exposes its exact effort choices', async () => {
    vi.spyOn(api, 'getSessionModels').mockResolvedValue({
      items: [
        { provider: 'antigravity', model: 'gemini-4-neon-high', label: 'Gemini 4 Neon (High)' },
        { provider: 'antigravity', model: 'gemini-4-neon-xhigh', label: 'Gemini 4 Neon (XHigh)' },
        { provider: 'antigravity', model: 'gemini-4-neon-low', label: 'Gemini 4 Neon (Low)' },
        { provider: 'antigravity', model: 'gemini-pro-orchestrator', label: 'Orchestrator (Carbon)' },
      ],
    })
    const handoff = vi.spyOn(api, 'handoffSession').mockResolvedValue({
      ...sourceSession,
      id: 'agy-target',
      agent_id: agyAgent.id,
    })

    render(
      <MantineProvider>
        <HandoffDialog
          source={sourceSession}
          targets={[agyAgent]}
          sessions={[sourceSession]}
          onClose={vi.fn()}
          onTransferred={vi.fn()}
          onOpenTransferred={vi.fn()}
        />
      </MantineProvider>,
    )

    // Open model dropdown and verify duplicates are collapsed.
    const modelTrigger = await screen.findByRole('button', { name: '模型' })
    await waitFor(() => expect(modelTrigger).not.toBeDisabled())
    fireEvent.click(modelTrigger)

    expect(await screen.findByRole('menuitem', { name: /Gemini 4 Neon/ })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: /Gemini 4 Neon \(High\)/ })).not.toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /Orchestrator \(Carbon\)/ })).toBeInTheDocument()

    // Select Gemini 4 Neon -> reveals thinking effort selector with Neon's supported levels.
    fireEvent.click(screen.getByRole('menuitem', { name: /Gemini 4 Neon/ }))
    const effortTrigger = await screen.findByRole('button', { name: '思考程度' })
    fireEvent.click(effortTrigger)
    expect(await screen.findByRole('menuitem', { name: '超高' })).toBeInTheDocument()
    expect(screen.queryByRole('menuitem', { name: '最高' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('menuitem', { name: '超高' }))

    fireEvent.click(screen.getByRole('button', { name: '转交' }))
    await waitFor(() =>
      expect(handoff).toHaveBeenCalledWith('codex-1', 'remote-codex', 'remote-agy', expect.any(String), {
        provider: 'antigravity',
        model: 'gemini-4-neon-xhigh',
        effort: '',
      }),
    )
  })
})
