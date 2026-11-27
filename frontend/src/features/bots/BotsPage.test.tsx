import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, type BotDocsResponse, type BotSummary } from '../../api/client'
import { useAstrorderStore } from '../../state/store'
import { BotsPage } from './BotsPage'

const sampleBot: BotSummary = {
  id: 'bot_video1',
  slug: 'video-studio',
  name: '视频制作师',
  description: '负责 2K 宣传片脚本、配音、渲染、混音与封面交付',
  icon: 'video',
  workspace: '/home/luwei/workspace/video',
  connection_id: 'ssh-c5f75ce2e7c847daabe75ea7',
  default_agent_id: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7',
  office_mode: true,
  tags: ['视频制作'],
  created_at: '2026-11-26T04:00:00Z',
  updated_at: '2026-11-26T04:00:00Z',
  has_agents_md: true,
  agents_md_excerpt: '优先消费派单与 inbox 素材，必要时可回溯源项目。',
  skills: [
    {
      slug: 'promo-video-production',
      name: 'promo-video-production',
      description: 'End-to-end 2K keynote promo video production workflow.',
      path: '/home/luwei/workspace/video/.agents/skills/promo-video-production/SKILL.md',
    },
  ],
  session_count: 1,
  running_count: 0,
}

const sampleDocs: BotDocsResponse = {
  bot: sampleBot,
  agents_md_path: '/home/luwei/workspace/video/AGENTS.md',
  agents_md_exists: true,
  agents_md: '# 视频制作师\n\n## 角色定位\n优先消费 inbox 素材，交付 2K 视频并遵守安全边界。',
  skills: [
    {
      slug: 'promo-video-production',
      name: 'promo-video-production',
      description: 'End-to-end 2K keynote promo video production workflow.',
      path: '/home/luwei/workspace/video/.agents/skills/promo-video-production/SKILL.md',
      content: '---\nname: promo-video-production\ndescription: 2K promo video workflow.\n---\n\n# Pipeline',
    },
  ],
}

function renderBotsPage() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <MemoryRouter initialEntries={['/bots']}>
          <BotsPage />
        </MemoryRouter>
      </MantineProvider>
    </QueryClientProvider>,
  )
}

describe('BotsPage', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
    vi.spyOn(api, 'getSessionModels').mockResolvedValue({
      items: [
        { provider: 'antigravity', model: 'gemini-4-neon-low', label: 'Gemini 4 Neon (Low)' },
        { provider: 'antigravity', model: 'gemini-4-neon-medium', label: 'Gemini 4 Neon (Medium)' },
        { provider: 'antigravity', model: 'gemini-4-neon-high', label: 'Gemini 4 Neon (High)' },
        { provider: 'antigravity', model: 'gemini-4-neon-xhigh', label: 'Gemini 4 Neon (XHigh)' },
        { provider: 'antigravity', model: 'claude-opus-5-5-high', label: 'Claude Opus 5.5 (High)' },
        { provider: 'antigravity', model: 'claude-opus-5-5-medium', label: 'Claude Opus 5.5 (Medium)' },
      ],
    })
    useAstrorderStore.setState({
      agents: {
        'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7': {
          id: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7',
          kind: 'antigravity-ssh',
          name: 'WSL · Antigravity',
          connection_id: 'ssh-c5f75ce2e7c847daabe75ea7',
          status: 'ready',
          capabilities: ['chat', 'stop'],
        } as any,
      },
      sessions: {
        'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7::s-promo': {
          id: 's-promo',
          agent_id: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7',
          title: '智能运维平台宣传片制作',
          workspace: '/home/luwei/workspace/video',
          connection_id: 'ssh-c5f75ce2e7c847daabe75ea7',
          status: 'idle',
          updated_at: '2026-11-26T04:00:00Z',
        } as any,
      },
      projects: {},
    })
  })

  afterEach(() => {
    cleanup()
  })

  it('shows human-readable env/agent names, dispatches via spotlight composer, and edits AGENTS.md & Skills', async () => {
    vi.spyOn(api, 'listBots').mockResolvedValue({ items: [sampleBot] })
    vi.spyOn(api, 'getBotDocs').mockResolvedValue(sampleDocs)
    const saveAgentsSpy = vi.spyOn(api, 'saveBotAgentsMd').mockResolvedValue({
      ok: true,
      path: sampleDocs.agents_md_path,
      content: '# Updated AGENTS',
    })
    const invokeSpy = vi.spyOn(api, 'invokeBot').mockResolvedValue({
      ok: true,
      bot: sampleBot,
      job_id: 'job-20261126-1',
      session: {
        key: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7::new-job-1',
        id: 'new-job-1',
        agent_id: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7',
        title: 'OpsCore 宣传片',
      },
    })

    renderBotsPage()

    expect(await screen.findByRole('heading', { level: 2, name: /视频制作师/ })).toBeInTheDocument()
    // Must render clean human-readable labels instead of raw connection UUIDs
    expect(screen.queryByText(/ssh-c5f75ce2e7c847daabe75ea7/)).not.toBeInTheDocument()
    expect(screen.getAllByText('WSL').length).toBeGreaterThan(0)
    expect(screen.getByText('智能运维平台宣传片制作')).toBeInTheDocument()

    // Expand inline title input inside spotlight composer and dispatch
    fireEvent.click(screen.getByRole('button', { name: '任务标题' }))
    fireEvent.change(screen.getByLabelText('任务标题'), { target: { value: 'OpsCore 宣传片' } })
    fireEvent.change(screen.getByLabelText('任务简报（Prompt）'), { target: { value: '制作 90 秒 2K 宣传片' } })
    fireEvent.click(screen.getByRole('button', { name: /派生会话执行/ }))

    await waitFor(() => {
      expect(invokeSpy).toHaveBeenCalledWith('bot_video1', {
        title: 'OpsCore 宣传片',
        prompt: '制作 90 秒 2K 宣传片',
        attach_paths: undefined,
      })
    })

    // Switch to AGENTS.md tab and save edits
    fireEvent.click(screen.getByRole('tab', { name: /人设 AGENTS\.md/ }))
    const editor = await screen.findByLabelText('AGENTS.md 内容编辑器')
    fireEvent.change(editor, { target: { value: '# Updated AGENTS' } })
    fireEvent.click(screen.getByRole('button', { name: /保存 AGENTS\.md/ }))

    await waitFor(() => {
      expect(saveAgentsSpy).toHaveBeenCalledWith('bot_video1', '# Updated AGENTS')
    })

    // Switch to Skills tab and see discovered promo-video-production skill
    fireEvent.click(screen.getByRole('tab', { name: /技能库 Skills/ }))
    expect(screen.getAllByRole('button', { name: /promo-video-production/ }).length).toBeGreaterThan(0)
  })

  it('supports creating a Bot with AI optimization', async () => {
    vi.spyOn(api, 'listBots').mockResolvedValue({ items: [] })
    const optimizeSpy = vi.spyOn(api, 'optimizeBotConfig').mockResolvedValue({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      suggested_name: '视频制作师',
      suggested_slug: 'video-studio',
      suggested_description: '2K 宣传片 Bot',
      suggested_tags: ['视频制作'],
      agents_md: '# 视频制作师\n\nAI 优化后的工作规范',
    })
    const createSpy = vi.spyOn(api, 'createBot').mockResolvedValue({ bot: sampleBot })

    renderBotsPage()

    expect(await screen.findByText(/把你的专属工作区打造成可随时调用的 Bot/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /直接新建 Bot/ }))

    const workspaceInput = screen.getByLabelText(/专属工作区路径/) as HTMLInputElement
    expect(workspaceInput.placeholder).not.toContain('/home/luwei')
    expect(workspaceInput.placeholder).toContain('~/Documents/my-project')

    fireEvent.change(screen.getByLabelText('Bot 名称'), { target: { value: '视频制作师' } })
    fireEvent.change(screen.getByLabelText(/专属工作区路径/), { target: { value: '/home/luwei/workspace/video' } })
    fireEvent.change(screen.getByLabelText(/基础设定/), { target: { value: '专业宣传片制作' } })
    fireEvent.click(screen.getByRole('button', { name: /AI 智能优化为 AGENTS\.md/ }))

    await waitFor(() => {
      expect(optimizeSpy).toHaveBeenCalled()
    })
    expect((screen.getByLabelText('工作区角色宪章（AGENTS.md）') as HTMLTextAreaElement).value).toContain(
      'AI 优化后的工作规范',
    )

    fireEvent.click(screen.getByRole('button', { name: /保存并创建 Bot/ }))
    await waitFor(() => {
      expect(createSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          name: '视频制作师',
          slug: 'video-studio',
          workspace: '/home/luwei/workspace/video',
          default_approval_mode: 'full_access',
          write_agents_md: true,
        }),
      )
    })
  })

  it('distills an existing session into AGENTS.md by default without forcing SKILL.md, and supports optional Skill generation', async () => {
    vi.spyOn(api, 'listBots').mockResolvedValue({ items: [] })
    const distillSpy = vi.spyOn(api, 'distillSessionToBot').mockImplementation(async (payload) => ({
      ok: true,
      mode: 'llm',
      llm_configured: true,
      source_session_key: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7::s-promo',
      source_session_title: '智能运维平台宣传片制作',
      workspace: '/home/luwei/workspace/video',
      connection_id: 'ssh-c5f75ce2e7c847daabe75ea7',
      default_agent_id: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7',
      suggested_name: '视频制作师',
      suggested_slug: 'video-studio',
      suggested_description: '2K 宣传片全链路 Bot',
      suggested_tags: ['视频制作'],
      agents_md: '# 视频制作师\n\n## 技能调用指引\n- 优先调用 `promo-video-production` 完成 2K 宣传片制作',
      skill_generated: Boolean(payload.generate_skill),
      skill_slug: payload.generate_skill ? 'promo-video-production' : '',
      skill_name: payload.generate_skill ? 'promo-video-production' : '',
      skill_description: payload.generate_skill ? '2K keynote promo video production.' : '',
      skill_md: payload.generate_skill
        ? '---\nname: promo-video-production\ndescription: 2K keynote promo video production.\n---\n\n# Steps'
        : '',
      has_existing_agents_md: false,
      existing_skills: [
        {
          slug: 'promo-video-production',
          name: 'promo-video-production',
          description: '2K keynote promo video production.',
          path: '/home/luwei/workspace/video/.agents/skills/promo-video-production/SKILL.md',
        },
      ],
      used_skills: ['promo-video-production'],
      message_count: 12,
    }))
    const createSpy = vi.spyOn(api, 'createBot').mockResolvedValue({ bot: sampleBot })

    renderBotsPage()

    expect(await screen.findByText(/把你的专属工作区打造成可随时调用的 Bot/)).toBeInTheDocument()
    fireEvent.click(screen.getAllByRole('button', { name: /从已有会话孵化 Bot/ })[0])

    fireEvent.change(screen.getByLabelText('选择要提炼经验的源会话'), {
      target: { value: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7::s-promo' },
    })
    // Default mode: generates AGENTS.md only (does not show or force SKILL.md)
    expect(screen.queryByLabelText(/沉淀技能文件/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /^AI 总结为 AGENTS\.md$/ }))

    await waitFor(() => {
      expect(distillSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          session_key: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7::s-promo',
          generate_skill: false,
        }),
      )
    })
    expect(await screen.findByText(/本会话用到的 Skill/)).toBeInTheDocument()
    expect((screen.getByLabelText('工作区角色宪章（AGENTS.md）') as HTMLTextAreaElement).value).toContain(
      'promo-video-production',
    )

    fireEvent.click(screen.getByRole('button', { name: /保存并创建 Bot/ }))
    await waitFor(() => {
      expect(createSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          name: '视频制作师',
          slug: 'video-studio',
          source_session_key: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7::s-promo',
          default_approval_mode: 'full_access',
          write_agents_md: true,
          write_skill: false,
        }),
      )
    })
  })

  it('defaults permission mode to full_access and saves configured model, effort, and approval mode', async () => {
    vi.spyOn(api, 'listBots').mockResolvedValue({ items: [sampleBot] })
    vi.spyOn(api, 'getBotDocs').mockResolvedValue(sampleDocs)
    const updateSpy = vi.spyOn(api, 'updateBot').mockResolvedValue({
      bot: {
        ...sampleBot,
        default_provider: 'antigravity',
        default_model: 'gemini-4-neon-xhigh',
        default_effort: 'xhigh',
        default_approval_mode: 'auto',
      },
    })

    renderBotsPage()

    expect(await screen.findByText('完全访问')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('tab', { name: /配置与协作/ }))
    const settingsPanel = await screen.findByRole('tabpanel', { name: /配置与协作/ })
    const settingsScope = within(settingsPanel)
    const envSelect = settingsScope.getByLabelText('运行环境') as HTMLSelectElement
    expect(envSelect.value).toBe('ssh-c5f75ce2e7c847daabe75ea7')
    expect(envSelect.selectedOptions[0]?.textContent).toBe('WSL')

    const approvalSelect = settingsScope.getByLabelText('默认执行权限') as HTMLSelectElement
    expect(approvalSelect.value).toBe('full_access')

    const modelSelect = settingsScope.getByLabelText('默认模型') as HTMLSelectElement
    await waitFor(() => {
      expect(settingsScope.getByRole('option', { name: 'Gemini 4 Neon' })).toBeInTheDocument()
    })
    fireEvent.change(modelSelect, { target: { value: JSON.stringify(['antigravity', 'gemini-4-neon']) } })

    const effortSelect = settingsScope.getByLabelText('默认思考强度') as HTMLSelectElement
    fireEvent.change(effortSelect, { target: { value: 'xhigh' } })
    fireEvent.change(approvalSelect, { target: { value: 'auto' } })

    fireEvent.click(settingsScope.getByRole('button', { name: /保存配置/ }))

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(
        'bot_video1',
        expect.objectContaining({
          default_provider: 'antigravity',
          default_model: 'gemini-4-neon-xhigh',
          default_effort: 'xhigh',
          default_approval_mode: 'auto',
          default_notify_caller: true,
        }),
      )
    })
  })

  it('supports presets, inbox upload, per-dispatch overrides, handoff callback, and skill metadata', async () => {
    const publisherBot: BotSummary = {
      ...sampleBot,
      id: 'bot_pub1',
      slug: 'social-publisher',
      name: '自媒体发布',
      workspace: '/home/luwei/workspace/自媒体平台',
      presets: [
        {
          id: 'preset_1',
          label: '双平台发布',
          title: '抖音+小红书发布',
          prompt: '查重后按双平台规范上传视频、封面与正文标签并验收。',
        },
      ],
    }
    vi.spyOn(api, 'listBots').mockResolvedValue({ items: [sampleBot, publisherBot] })
    vi.spyOn(api, 'getBotDocs').mockResolvedValue({
      ...sampleDocs,
      skills: [
        {
          ...sampleDocs.skills[0],
          files: ['scripts/render.py', 'references/style.md'],
        },
      ],
      referenced_skills: ['astrorder-browser'],
    })
    const uploadSpy = vi.spyOn(api, 'uploadBotInboxFiles').mockResolvedValue({
      ok: true,
      files: [
        {
          name: 'cover.png',
          path: '/home/luwei/workspace/自媒体平台/inbox/upload-abcd/cover.png',
          size: 4096,
        },
      ],
    })
    const invokeSpy = vi.spyOn(api, 'invokeBot').mockResolvedValue({
      ok: true,
      bot: publisherBot,
      job_id: 'job-handoff-1',
      staged_files: [
        {
          source_path: '/home/luwei/workspace/自媒体平台/inbox/upload-abcd/cover.png',
          target_path: '/home/luwei/workspace/自媒体平台/inbox/job-handoff-1/cover.png',
          name: 'cover.png',
          size: 4096,
        },
      ],
      session: {
        key: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7::pub-job-1',
        id: 'pub-job-1',
        agent_id: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7',
        title: '抖音+小红书发布',
      },
    })

    renderBotsPage()

    // Check Skills auxiliary files & referenced external skills on first Bot
    expect(await screen.findByRole('heading', { level: 2, name: /视频制作师/ })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', { name: /技能库 Skills/ }))
    expect(await screen.findByText('scripts/render.py')).toBeInTheDocument()
    expect(screen.getByText('astrorder-browser')).toBeInTheDocument()

    // Switch back to Dispatch and click "接力" on the task row -> switches to publisherBot with handoff banner
    fireEvent.click(screen.getByRole('tab', { name: /总览与派单/ }))
    fireEvent.click(screen.getByRole('button', { name: '接力' }))
    expect(await screen.findByRole('heading', { level: 2, name: /自媒体发布/ })).toBeInTheDocument()
    expect(screen.getByText(/接力来源：/)).toBeInTheDocument()

    // Apply preset chip
    fireEvent.click(screen.getByRole('button', { name: '双平台发布' }))
    expect((screen.getByLabelText('任务简报（Prompt）') as HTMLTextAreaElement).value).toContain('双平台规范')

    // Upload a file into inbox
    const fileInput = screen.getByLabelText('上传素材文件') as HTMLInputElement
    const mockFile = new File(['png-bytes'], 'cover.png', { type: 'image/png' })
    fireEvent.change(fileInput, { target: { files: [mockFile] } })
    await waitFor(() => {
      expect(uploadSpy).toHaveBeenCalledWith('bot_pub1', [mockFile])
    })
    expect(await screen.findByText('cover.png')).toBeInTheDocument()

    // Set per-dispatch temporary override
    fireEvent.click(screen.getByRole('button', { name: '本次策略' }))
    fireEvent.change(screen.getByLabelText('本次权限'), { target: { value: 'manual' } })

    // Dispatch and assert handoff + upload + override fields are sent
    fireEvent.click(screen.getByRole('button', { name: /派生会话执行/ }))
    await waitFor(() => {
      expect(invokeSpy).toHaveBeenCalledWith(
        'bot_pub1',
        expect.objectContaining({
          title: '抖音+小红书发布',
          attach_paths: ['/home/luwei/workspace/自媒体平台/inbox/upload-abcd/cover.png'],
          caller_session_key: 'ssh-antigravity-ssh-c5f75ce2e7c847daabe75ea7::s-promo',
          caller_workspace: '/home/luwei/workspace/video',
          notify_caller: true,
          approval_mode: 'manual',
        }),
      )
    })
  })
})
