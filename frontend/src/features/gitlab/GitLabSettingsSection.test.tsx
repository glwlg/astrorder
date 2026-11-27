import { MantineProvider } from '@mantine/core'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { gitlabApi } from '../../api/gitlab'
import { GitLabSettingsSection } from './GitLabSettingsSection'

vi.mock('../../api/gitlab', () => ({
  gitlabApi: {
    listInstances: vi.fn(),
    createInstance: vi.fn(),
    updateInstance: vi.fn(),
    deleteInstance: vi.fn(),
    testInstance: vi.fn(),
  },
}))

describe('GitLabSettingsSection', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(gitlabApi.listInstances).mockResolvedValue({ items: [] })
    vi.mocked(gitlabApi.createInstance).mockResolvedValue({
      instance: {
        id: 'inst-1',
        name: '公司内网 GitLab',
        base_url: 'https://gitlab.company.com',
        host: 'gitlab.company.com',
        token_set: true,
        masked_token: 'glpa****1234',
        insecure_skip_verify: true,
        enabled: true,
        is_default: true,
        created_at: '2026-11-27T00:00:00Z',
        updated_at: '2026-11-27T00:00:00Z',
      },
    })
  })

  it('handles typing into modal inputs and switches without synthetic event null crashes', async () => {
    render(
      <MantineProvider>
        <GitLabSettingsSection embedded />
      </MantineProvider>,
    )

    const addBtn = await screen.findByRole('button', { name: '添加第一个 GitLab 实例' })
    fireEvent.click(addBtn)

    const nameInput = await screen.findByLabelText('实例名称')
    const urlInput = screen.getByLabelText(/GitLab 服务地址/)
    const tokenInput = screen.getByLabelText(/Personal Access Token/)
    const insecureSwitch = screen.getByLabelText(/跳过 TLS 自签名证书校验/)

    fireEvent.change(nameInput, { target: { value: '公司内网 GitLab' } })
    fireEvent.change(urlInput, { target: { value: 'https://gitlab.company.com' } })
    fireEvent.change(tokenInput, { target: { value: 'glpat-secret-1234' } })
    fireEvent.click(insecureSwitch)

    expect(nameInput).toHaveValue('公司内网 GitLab')
    expect(urlInput).toHaveValue('https://gitlab.company.com')
    expect(tokenInput).toHaveValue('glpat-secret-1234')
    expect(insecureSwitch).toBeChecked()

    fireEvent.click(screen.getByRole('button', { name: '保存配置' }))
    await waitFor(() => {
      expect(gitlabApi.createInstance).toHaveBeenCalledWith({
        name: '公司内网 GitLab',
        base_url: 'https://gitlab.company.com',
        token: 'glpat-secret-1234',
        insecure_skip_verify: true,
        enabled: true,
        is_default: true,
      })
    })
  })
})
