import { MantineProvider } from '@mantine/core'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
import type { Project } from '../domain/types'
import { Sidebar } from './Sidebar'

const zeroSessionProject: Project = {
  id: 'project-row-empty',
  source_id: 'source-remote',
  connection_id: 'ssh-wsl',
  project_id: 'empty-project-id',
  project_name: '无会话项目',
  workspace: '/empty',
  session_count: 0,
  updated_at: '2026-09-07T09:00:00Z',
}

describe('Sidebar project catalog wiring', () => {
  it('forwards zero-session projects to the two-level session rail without a connection parent', () => {
    render(
      <MantineProvider>
        <MemoryRouter>
          <Sidebar
            sessions={[]}
            agents={{}}
            projects={[zeroSessionProject]}
            onSelectSession={vi.fn()}
          />
        </MemoryRouter>
      </MantineProvider>,
    )

    expect(screen.getByRole('button', { name: /无会话项目.*0 个会话/ })).toBeInTheDocument()
    expect(screen.getByText('ssh-wsl')).toBeInTheDocument()
    expect(screen.queryByText('连接 ssh-wsl')).not.toBeInTheDocument()
  })
})
