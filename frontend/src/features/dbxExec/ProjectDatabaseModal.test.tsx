import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type ProjectDatabaseBinding } from '../../api/client'
import { ProjectDatabaseModal } from './ProjectDatabaseModal'
import { useProjectDbStore } from './projectDbStore'

describe('ProjectDatabaseModal', () => {
  beforeEach(() => {
    useProjectDbStore.setState({
      bindings: [],
      connections: [],
      hydrated: false,
      loadingBindings: false,
      loadingConnections: false,
      connectionsError: '',
      sessionTargetOverride: {},
    })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
  })

  it('loads DBX connections, locks DBX-marked PROD targets, and saves project binding', async () => {
    vi.spyOn(api, 'getProjectDatabaseBindings').mockResolvedValue({ bindings: [] })
    vi.spyOn(api, 'listDbxConnections').mockResolvedValue({
      connections: [
        {
          id: 'conn-prod',
          name: 'ops@10.20.64.38',
          db_type: 'postgresql',
          database: 'ops_metadata',
          is_prod: true,
        },
      ],
    })
    vi.spyOn(api, 'listDbxDatabases').mockResolvedValue({
      databases: ['ops_metadata', 'postgres'],
    })
    vi.spyOn(api, 'listDbxSchemas').mockResolvedValue({
      schemas: ['public'],
    })

    let savedPayload: ProjectDatabaseBinding | null = null
    vi.spyOn(api, 'saveProjectDatabaseBinding').mockImplementation(async (binding) => {
      savedPayload = binding
      return { binding, bindings: [binding] }
    })

    const onClose = vi.fn()
    render(
      <MantineProvider>
        <ProjectDatabaseModal
          opened
          onClose={onClose}
          projectKey="project:local\u0000opscore"
          workspaceKey="connection:local\u0000/home/luwei/workspace/OpsCore"
          projectLabel="OpsCore"
        />
      </MantineProvider>,
    )

    const connInput = await screen.findByPlaceholderText('选择已在 DBX 中配置的连接')
    fireEvent.click(connInput)
    const option = await screen.findByText('ops@10.20.64.38 [PROD] (postgresql)')
    fireEvent.click(option)

    const prodCheckbox = await screen.findByRole('checkbox', { name: /生产环境 PROD/ })
    expect(prodCheckbox).toBeChecked()
    expect(prodCheckbox).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: '添加到项目' }))
    expect(await screen.findByText(/已绑定目标（1）/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '保存关联' }))

    await waitFor(() => {
      expect(savedPayload).not.toBeNull()
      expect(onClose).toHaveBeenCalled()
    })
    expect(savedPayload!.targets).toHaveLength(1)
    expect(savedPayload!.targets[0]).toMatchObject({
      connection_id: 'conn-prod',
      database: 'ops_metadata',
      is_prod: true,
      is_default: true,
    })
  })
})
