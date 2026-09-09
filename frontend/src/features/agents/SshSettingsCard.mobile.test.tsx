import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { SshConnection } from '../../domain/types'

vi.mock('@mantine/hooks', () => ({ useMediaQuery: () => true }))

import { SshSettingsCard } from './AgentsPage'

afterEach(cleanup)

const connection: SshConnection = {
  id: 'ssh-1',
  display_name: 'Remote',
  profile_name: 'default',
  state: 'configured',
  settings: {
    connection_id: 'ssh-1',
    display_name: 'Remote',
    profile_name: 'default',
    host: 'initial.example',
    port: 22,
    user: 'deploy',
    ssh_config_alias: null,
    identity_file: 'C:\\Users\\you\\.ssh\\id_ed25519',
    hermes_path: '/opt/hermes/bin/hermes',
    workspace: '/srv/astrorder-workspace',
  },
  detail: '填写并保存新的 SSH 连接。',
  remote_os: null,
  agent_id: null,
  runtime_id: null,
}

describe('mobile SSH setup steps', () => {
  it('cannot jump to deploy or submit without current identity confirmation', () => {
    const onDraftChange = vi.fn()
    const onConnect = vi.fn().mockResolvedValue(undefined)
    render(<MantineProvider><SshSettingsCard connection={connection} busy={false} onDraftChange={onDraftChange} onSave={vi.fn()} onConnect={onConnect} onDisconnect={vi.fn()} /></MantineProvider>)

    expect(screen.getByRole('group', { name: 'SSH 设置阶段' })).toBeInTheDocument()
    expect(screen.getByLabelText('SSH 主机')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '3 自动部署' }))
    expect(screen.getByLabelText('SSH 主机')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '部署并连接' })).not.toBeInTheDocument()

    fireEvent.change(screen.getByLabelText('SSH 主机'), { target: { value: 'remote.example' } })
    expect(onDraftChange).toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    expect(screen.getByRole('button', { name: '下一步' })).toBeDisabled()
    fireEvent.click(screen.getByRole('checkbox', { name: /known_hosts/ }))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    expect(screen.getByLabelText('远端 Hermes 路径')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '2 验证身份' }))
    fireEvent.change(screen.getByLabelText(/私钥文件引用/), { target: { value: 'C:\\Users\\you\\.ssh\\other_key' } })
    expect(screen.getByRole('button', { name: '下一步' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '3 自动部署' }))
    expect(screen.getByLabelText(/私钥文件引用/)).toBeInTheDocument()
    expect(onConnect).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('checkbox', { name: /known_hosts/ }))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.click(screen.getByRole('button', { name: '部署并连接' }))
    expect(onConnect).toHaveBeenCalledOnce()
  })

  it.each([
    ['SSH 主机', 'changed.example'],
    ['SSH 端口', '2222'],
  ])('invalidates host-key confirmation after changing %s', (label, value) => {
    render(<MantineProvider><SshSettingsCard connection={connection} busy={false} onSave={vi.fn()} onConnect={vi.fn()} onDisconnect={vi.fn()} /></MantineProvider>)

    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.click(screen.getByRole('checkbox', { name: /known_hosts/ }))
    fireEvent.click(screen.getByRole('button', { name: '下一步' }))
    fireEvent.click(screen.getByRole('button', { name: '1 基本信息' }))
    fireEvent.change(screen.getByLabelText(label), { target: { value } })
    fireEvent.click(screen.getByRole('button', { name: '2 验证身份' }))

    expect(screen.getByRole('button', { name: '下一步' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '3 自动部署' }))
    expect(screen.getByLabelText(/私钥文件引用/)).toBeInTheDocument()
  })
})
