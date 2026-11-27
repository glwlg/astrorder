import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type AiProvidersConfig, type ToolchainReport } from '../../api/client'
import { useAstrorderStore } from '../../state/store'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { AiProvidersSettingsSection } from '../services/AiProvidersSettingsSection'
import { GeneralSettingsPage } from '../settings/GeneralSettingsPage'
import { OnboardingWizard } from './OnboardingWizard'

vi.mock('@mantine/notifications', () => ({ notifications: { show: vi.fn() } }))

beforeEach(() => {
  localStorage.clear()
  usePluginSettingsStore.setState({ hydrated: true })
  useAstrorderStore.getState().resetRuntime()
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAstrorderStore.getState().resetRuntime()
})

describe('OnboardingWizard', () => {
  it('reuses installed logged-in Agent without API key, injects CLI/skills, and starts guided session', async () => {
    useAstrorderStore.setState({
      agents: {
        'codex-local': {
          id: 'codex-local',
          kind: 'codex',
          name: 'OpenAI Codex',
          status: 'ready',
          connection_id: 'local',
          capabilities: ['chat', 'events'],
          limitation: null,
        },
      },
    })

    const report: ToolchainReport = {
      environment_id: 'local',
      target_kind: 'local',
      target_name: '本机',
      os: 'windows',
      checked_at: '2026-11-27T05:00:00Z',
      items: [
        {
          id: 'codex',
          kind: 'agent',
          name: 'OpenAI Codex',
          description: 'Codex CLI',
          installed: true,
          auth_configured: true,
          version: '0.42.1',
          registered_in_daemon: true,
          recipes: [],
        },
      ],
    }

    vi.spyOn(api, 'getToolchainStatus').mockResolvedValue(report)
    vi.spyOn(api, 'fetchAiProviderModels').mockResolvedValue({
      ok: true,
      models: ['gpt-5.4', 'gpt-5.3-codex'],
      count: 2,
    })
    const configureSpy = vi.spyOn(api, 'configureOnboarding').mockResolvedValue({
      ok: true,
      environment_id: 'local',
      agent_id: 'codex-local',
      agent_kind: 'codex',
      workspace: '/home/user/AstrorderWorkspace',
      credentials_written: false,
      universal_llm_mode: 'agent',
      cli_ready: true,
      injected_skills: ['astrorder-sessions', 'astrorder-workspace'],
      requires_daemon_restart: false,
    })
    const createSessionSpy = vi.spyOn(api, 'createSession').mockResolvedValue({
      id: 'sess-onboard-1',
      agent_id: 'codex-local',
      title: '星序初始化配置助手',
      workspace: '/home/user/AstrorderWorkspace',
      status: 'idle',
      updated_at: '2026-11-27T05:00:00Z',
    })
    const setModelSpy = vi.spyOn(api, 'setSessionModel').mockResolvedValue({
      provider: '',
      model: 'gpt-5.4',
    })
    const createCmdSpy = vi.spyOn(api, 'createCommand').mockResolvedValue({
      id: 'cmd-1',
      agent_id: 'codex-local',
      session_id: 'sess-onboard-1',
      action: 'send',
      state: 'queued',
      text: '',
      attachments: [],
      error: null,
      created_at: '2026-11-27T05:00:00Z',
    })

    render(
      <MantineProvider>
        <MemoryRouter>
          <OnboardingWizard ready hasExistingData={false} forceOpen />
        </MemoryRouter>
      </MantineProvider>,
    )

    // Step 1 -> Step 2
    fireEvent.click(screen.getByRole('button', { name: /下一步：选择 AI 引擎/ }))
    expect(await screen.findByText(/已登录 · 可免填 Key/)).toBeInTheDocument()

    // Step 2 -> Step 3
    fireEvent.click(screen.getByRole('button', { name: /下一步：凭据与模型/ }))
    expect(await screen.findByText(/已检测到 OpenAI Codex 登录态，无需填写 API Key/)).toBeInTheDocument()
    await waitFor(() => expect(api.fetchAiProviderModels).toHaveBeenCalledWith({ protocol: 'agent', agent_id: 'codex-local' }))

    // Step 3 -> Step 4 (auto-configures + launches guided assistant session)
    fireEvent.click(screen.getByRole('button', { name: /完成配置并启动配置助手/ }))

    await waitFor(() => {
      expect(configureSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          environment_id: 'local',
          agent_kind: 'codex',
          agent_id: 'codex-local',
          model: 'gpt-5.4',
          skip_key: true,
          inject_capabilities: true,
          sync_universal_llm: true,
        }),
      )
    })
    await waitFor(() => {
      expect(createSessionSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          agent_id: 'codex-local',
          workspace: '/home/user/AstrorderWorkspace',
          title: '星序初始化配置助手',
        }),
      )
    })
    expect(setModelSpy).toHaveBeenCalledWith('sess-onboard-1', 'codex-local', '', 'gpt-5.4')
    expect(createCmdSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        agent_id: 'codex-local',
        session_id: 'sess-onboard-1',
        action: 'send',
      }),
    )
  })

  it('recommends OpenAI Codex one-click install when no local Agent is installed', async () => {
    const emptyReport: ToolchainReport = {
      environment_id: 'local',
      target_kind: 'local',
      target_name: '本机',
      os: 'windows',
      checked_at: '2026-11-27T05:00:00Z',
      items: [
        { id: 'codex', kind: 'agent', name: 'OpenAI Codex', description: '', installed: false, recipes: [] },
        { id: 'hermes', kind: 'agent', name: 'Hermes Agent', description: '', installed: false, recipes: [] },
      ],
    }
    vi.spyOn(api, 'getToolchainStatus').mockResolvedValue(emptyReport)
    const installSpy = vi.spyOn(api, 'startToolchainInstallJob').mockResolvedValue({
      id: 'job-codex-1',
      title: '初始化安装 OpenAI Codex',
      environment_id: 'local',
      tool_ids: ['codex'],
      status: 'running',
      detail: '正在安装 Node.js 与 Codex…',
      log: '',
      created_at: '2026-11-27T05:00:00Z',
      current: 1,
      total: 2,
      target: '本机',
    })

    render(
      <MantineProvider>
        <MemoryRouter>
          <OnboardingWizard ready hasExistingData={false} forceOpen />
        </MemoryRouter>
      </MantineProvider>,
    )

    fireEvent.click(screen.getByRole('button', { name: /下一步：选择 AI 引擎/ }))
    expect(await screen.findByText('本机尚未检测到已安装的 AI 助手引擎')).toBeInTheDocument()
    expect(screen.getByText('推荐安装')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /一键安装 OpenAI Codex/ }))
    await waitFor(() => {
      expect(installSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          environment_id: 'local',
          tool_ids: ['codex'],
          include_deps: true,
        }),
      )
    })
  })

  it('supports one-click browser OAuth login with polling in Step 3 and transitions cleanly to keyless mode', async () => {
    const installedUnauthedReport: ToolchainReport = {
      environment_id: 'local',
      target_kind: 'local',
      target_name: '本机',
      os: 'windows',
      checked_at: '2026-11-27T05:00:00Z',
      items: [
        {
          id: 'codex',
          kind: 'agent',
          name: 'OpenAI Codex',
          description: 'Codex CLI',
          installed: true,
          auth_configured: false,
          version: '0.42.1',
          registered_in_daemon: true,
          recipes: [],
        },
      ],
    }
    const authedReport: ToolchainReport = {
      ...installedUnauthedReport,
      items: [{ ...installedUnauthedReport.items[0], auth_configured: true }],
    }

    const statusSpy = vi
      .spyOn(api, 'getToolchainStatus')
      .mockResolvedValueOnce(installedUnauthedReport)
      .mockResolvedValue(authedReport)
    vi.spyOn(api, 'getBootstrap').mockResolvedValue({
      protocol_version: 1,
      cursor: 1,
      agents: [],
      sessions: [],
    })
    const startOAuthSpy = vi.spyOn(api, 'startToolchainOAuth').mockResolvedValue({
      id: 'oauth-codex-1',
      environment_id: 'local',
      agent_kind: 'codex',
      mode: 'browser',
      status: 'waiting_user',
      detail: '请在浏览器中完成授权登录',
      auth_url: 'https://auth.openai.com/authorize?state=demo',
      device_code: 'ABCD-1234',
      browser_opened: true,
      auth_configured: false,
    })
    const pollOAuthSpy = vi.spyOn(api, 'getToolchainOAuthStatus').mockResolvedValue({
      id: 'oauth-codex-1',
      environment_id: 'local',
      agent_kind: 'codex',
      mode: 'browser',
      status: 'succeeded',
      detail: '授权完成',
      auth_configured: true,
      agent_status: { ...installedUnauthedReport.items[0], auth_configured: true },
    })

    render(
      <MantineProvider>
        <MemoryRouter>
          <OnboardingWizard ready hasExistingData={false} forceOpen />
        </MemoryRouter>
      </MantineProvider>,
    )

    // Step 1 -> Step 2 -> Step 3
    fireEvent.click(screen.getByRole('button', { name: /下一步：选择 AI 引擎/ }))
    expect(await screen.findByText(/已安装 · 待配置/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /下一步：凭据与模型/ }))

    // Trigger browser OAuth one-click login
    const oauthBtn = await screen.findByRole('button', { name: '浏览器一键登录' })
    fireEvent.click(oauthBtn)

    await waitFor(() => {
      expect(startOAuthSpy).toHaveBeenCalledWith({
        environment_id: 'local',
        agent_kind: 'codex',
        mode: 'browser',
        open_browser: true,
      })
    })
    expect(await screen.findByText('ABCD-1234')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '打开授权页' })).toBeInTheDocument()

    // Wait for status poll (1500ms interval) to complete and switch seamlessly into keyless mode
    await waitFor(
      () => {
        expect(pollOAuthSpy).toHaveBeenCalledWith('oauth-codex-1')
        expect(screen.getByText(/已检测到 OpenAI Codex 登录态，无需填写 API Key/)).toBeInTheDocument()
      },
      { timeout: 4000 },
    )
    expect(statusSpy).toHaveBeenCalledTimes(2)
  })
})

describe('AiProvidersSettingsSection keyless Agent mode', () => {
  it('lets users bind universal LLM directly to a connected Agent without API key', async () => {
    useAstrorderStore.setState({
      agents: {
        'codex-local': {
          id: 'codex-local',
          kind: 'codex',
          name: 'OpenAI Codex',
          status: 'ready',
          connection_id: 'local',
          capabilities: ['chat', 'events'],
          limitation: null,
        },
      },
    })

    const emptyCfg: AiProvidersConfig = {
      providers: [],
      default_provider_id: '',
      default_model: '',
      default_reasoning: 'low',
      effective_configured: false,
    }
    vi.spyOn(api, 'getAiProviders').mockResolvedValue(emptyCfg)
    vi.spyOn(api, 'fetchAiProviderModels').mockResolvedValue({
      ok: true,
      models: ['gpt-5.4', 'o4-mini'],
      count: 2,
    })
    const saveSpy = vi.spyOn(api, 'saveAiProvider').mockResolvedValue({
      provider: {
        id: 'prv_agent_1',
        name: 'OpenAI Codex（免 Key 直连）',
        protocol: 'agent',
        base_url: '',
        api_key_set: false,
        agent_id: 'codex-local',
        agent_kind: 'codex',
        models: ['gpt-5.4', 'o4-mini'],
        enabled: true,
      },
      config: {
        providers: [
          {
            id: 'prv_agent_1',
            name: 'OpenAI Codex（免 Key 直连）',
            protocol: 'agent',
            base_url: '',
            api_key_set: false,
            agent_id: 'codex-local',
            agent_kind: 'codex',
            models: ['gpt-5.4', 'o4-mini'],
            enabled: true,
          },
        ],
        default_provider_id: 'prv_agent_1',
        default_model: 'gpt-5.4',
        default_reasoning: 'low',
        effective_configured: true,
        effective_model: 'gpt-5.4',
        effective_provider_name: 'OpenAI Codex（免 Key 直连）',
      },
    })

    render(
      <MantineProvider>
        <AiProvidersSettingsSection />
      </MantineProvider>,
    )

    await waitFor(() => expect(api.fetchAiProviderModels).toHaveBeenCalledWith({ protocol: 'agent', agent_id: 'codex-local' }))
    fireEvent.click(screen.getByRole('button', { name: '保存为默认通用模型' }))

    await waitFor(() => {
      expect(saveSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          protocol: 'agent',
          agent_id: 'codex-local',
          agent_kind: 'codex',
          set_as_default: true,
          default_model: 'gpt-5.4',
        }),
      )
    })
    expect(await screen.findByText('免 Key · Agent 直连')).toBeInTheDocument()
  })

  it('opens OnboardingWizard when clicking 重新运行向导 in GeneralSettingsPage', async () => {
    render(
      <MantineProvider>
        <MemoryRouter>
          <GeneralSettingsPage />
          <OnboardingWizard ready hasExistingData />
        </MemoryRouter>
      </MantineProvider>,
    )

    const reopenBtn = await screen.findByRole('button', { name: '重新运行向导' })
    fireEvent.click(reopenBtn)
    expect(await screen.findByText('欢迎使用星序 · 1 分钟完成初始化')).toBeInTheDocument()
  })
})
