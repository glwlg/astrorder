import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { LocalHermesConnection, SshConnection } from '../../domain/types'
import { ConnectionList } from './ConnectionList'

const local: LocalHermesConnection = {
  kind: 'hermes',
  state: 'connected',
  available: true,
  version: 'Hermes Agent v-test',
  agent_id: 'local-agent',
  source_id: 'local-source',
  profile_name: 'hermes',
  runtime_id: 'local-agent',
  session_id: 'owned-session',
  detail: '本机连接已建立。',
}

const sshItems: SshConnection[] = [
  {
    id: 'ssh-wsl',
    display_name: 'WSL 开发环境',
    profile_name: 'default',
    state: 'error',
    settings: { host: '127.0.0.1', port: 22, user: 'luwei', ssh_config_alias: null, identity_file: null, hermes_path: null, workspace: null },
    detail: '插件部署失败；请查看诊断。',
    remote_os: 'Linux',
    agent_id: null,
    runtime_id: null,
  },
  {
    id: 'ssh-offline',
    display_name: '开发服务器',
    profile_name: 'development',
    state: 'disconnected',
    settings: { host: 'server.invalid', port: 22, user: 'developer', ssh_config_alias: null, identity_file: null, hermes_path: null, workspace: null },
    detail: '已断开。',
    remote_os: 'Linux',
    agent_id: null,
    runtime_id: null,
  },
]

describe('ConnectionList', () => {
  it('filters real local and SSH records without inventing connection rows', () => {
    const onSelect = vi.fn()
    render(
      <MantineProvider>
        <ConnectionList local={local} sshItems={sshItems} onAdd={vi.fn()} onSelect={onSelect} />
      </MantineProvider>,
    )

    expect(screen.getByRole('heading', { name: '连接管理' })).toBeInTheDocument()
    expect(screen.getByText(/3 个连接/)).toBeInTheDocument()
    expect(screen.getByText('本机 Hermes')).toBeInTheDocument()
    expect(screen.getByText('WSL 开发环境')).toBeInTheDocument()
    expect(screen.getByText('开发服务器')).toBeInTheDocument()
    expect(screen.queryByText('10分钟前')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('tab', { name: /需处理/ }))
    expect(screen.getByText('WSL 开发环境')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /开发服务器/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /本机 Hermes/ })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /WSL 开发环境/ }))
    expect(onSelect).toHaveBeenCalledWith(expect.objectContaining({ kind: 'ssh', id: 'ssh-wsl' }))
  })
})
