import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { CreateBotGroupDialog } from './CreateBotGroupDialog'
import { api, ApiError } from '../api/client'
import type { Agent, BotGroup, Session } from '../domain/types'

const agents: Record<string, Agent> = {
  codex: { id: 'codex', kind: 'codex', name: 'Codex', status: 'ready', capabilities: ['chat'], limitation: null },
  hermes: { id: 'hermes', kind: 'hermes', name: 'Hermes', status: 'ready', capabilities: ['chat'], limitation: null, connection_id: 'ssh-debian' },
  'local-antigravity': { id: 'local-antigravity', kind: 'antigravity', name: 'Antigravity', status: 'ready', capabilities: ['chat'], limitation: null },
  'ssh-antigravity-wsl': { id: 'ssh-antigravity-wsl', kind: 'antigravity-ssh', name: 'WSL · Antigravity', status: 'ready', capabilities: ['chat'], limitation: null, connection_id: 'wsl' },
}
const parent: Session = { id: 'parent', agent_id: 'codex', title: '已有对话', workspace: '/source', status: 'idle', updated_at: '2026-11-10T00:00:00Z' }
const child: Session = { ...parent, id: 'child', title: '分叉对话', handoff_from_agent_id: 'codex', handoff_from_session_id: 'parent' }
const saved: BotGroup = { id: 'g', name: '测试群', members: [], max_hops: 3, created_at: '', updated_at: '' }

function openDialog(group?: BotGroup) {
  const onCreated = vi.fn()
  const onClose = vi.fn()
  render(<MantineProvider env="test"><CreateBotGroupDialog agents={agents} opened onClose={onClose} onCreated={onCreated} group={group} /></MantineProvider>)
  return { onCreated, onClose }
}
async function addCodex() {
  fireEvent.click(screen.getByRole('button', { name: '添加 Codex' }))
  await waitFor(() => expect(api.getSessions).toHaveBeenCalledWith('codex'))
}
async function chooseParent() {
  const input = await screen.findByRole('combobox', { name: '从已有会话分叉（可选）' })
  await waitFor(() => expect(screen.getByRole('combobox', { name: '工作路径' })).toHaveValue('/source'))
  fireEvent.click(input)
  fireEvent.click(await screen.findByRole('option', { name: /已有对话 · \/source/ }))
}
beforeEach(() => {
  vi.spyOn(api, 'getSessions').mockResolvedValue({ items: [parent] })
  vi.spyOn(api, 'getSessionModels').mockResolvedValue({ items: [] })
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: null, model: '' })
})
afterEach(() => vi.restoreAllMocks())

describe('CreateBotGroupDialog', () => {
  it('adds the same Agent twice with distinct identities and independently selected workspaces', async () => {
    const create = vi.spyOn(api, 'createBotGroup').mockResolvedValue({ ok: true, group: saved })
    const { onCreated, onClose } = openDialog()
    expect(screen.getByRole('button', { name: '添加 Antigravity' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '添加 WSL · Antigravity' })).toBeInTheDocument()
    expect(screen.getByLabelText('单次提问最大接力轮数')).toHaveValue('10')
    fireEvent.change(screen.getByLabelText(/群聊名称/), { target: { value: '测试群' } })
    await addCodex()
    fireEvent.click(screen.getByRole('button', { name: '添加 Codex' }))
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0)
    expect(screen.getAllByLabelText('群内别名').map((input) => (input as HTMLInputElement).value)).toEqual(['Codex-1', 'Codex-2'])
    const workspaces = screen.getAllByRole('combobox', { name: '工作路径' })
    fireEvent.change(workspaces[0], { target: { value: '/project-one' } })
    fireEvent.change(workspaces[1], { target: { value: '/project-two' } })
    fireEvent.click(screen.getByRole('button', { name: '创建群聊' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    const members = create.mock.calls[0][0].members
    expect(create.mock.calls[0][0].max_hops).toBe(10)
    expect(members).toHaveLength(2)
    expect(members[0]).toMatchObject({ agent_id: 'codex', alias: 'Codex-1', workspace: '/project-one' })
    expect(members[1]).toMatchObject({ agent_id: 'codex', alias: 'Codex-2', workspace: '/project-two' })
    expect(members[0].id).toBeTruthy()
    expect(members[0].id).not.toBe(members[1].id)
    expect(onCreated).toHaveBeenCalledWith(saved)
    expect(onClose).toHaveBeenCalled()
  })
  it('forks an existing session once and reuses the child after a group save failure', async () => {
    const fork = vi.spyOn(api, 'forkSession').mockResolvedValue(child)
    const create = vi.spyOn(api, 'createBotGroup').mockRejectedValueOnce(new ApiError(422, '保存失败')).mockResolvedValueOnce({ ok: true, group: saved })
    openDialog()
    fireEvent.change(screen.getByLabelText(/群聊名称/), { target: { value: '测试群' } })
    await addCodex()
    await chooseParent()
    expect(screen.getByRole('combobox', { name: '工作路径' })).toBeDisabled()
    expect(screen.getByText(/会话记录独立；文件仍在同一工作区/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '创建群聊' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('已创建的分叉会话会保留')
    expect(fork).toHaveBeenCalledWith('parent', { agent_id: 'codex', title: '测试群 · Codex-1', worktree: false })
    expect(create.mock.calls[0][0].members[0]).toMatchObject({ source_session_id: 'parent', fork_session_id: 'child', workspace: '/source' })
    fireEvent.click(screen.getByRole('button', { name: '创建群聊' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(2))
    expect(fork).toHaveBeenCalledTimes(1)
    expect(create.mock.calls[1][0].members[0].fork_session_id).toBe('child')
  })
  it('does not automatically repeat an unconfirmed fork', async () => {
    const fork = vi.spyOn(api, 'forkSession').mockRejectedValue(new Error('连接中断'))
    const create = vi.spyOn(api, 'createBotGroup')
    openDialog()
    fireEvent.change(screen.getByLabelText(/群聊名称/), { target: { value: '测试群' } })
    await addCodex()
    await chooseParent()
    fireEvent.click(screen.getByRole('button', { name: '创建群聊' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('分叉结果未确认')
    fireEvent.click(screen.getByRole('button', { name: '创建群聊' }))
    expect(fork).toHaveBeenCalledTimes(1)
    expect(create).not.toHaveBeenCalled()
  })
  it('keeps legacy member identity and its existing workspace when editing', async () => {
    const legacy: BotGroup = { ...saved, members: [{ agent_id: 'offline', machine_id: 'ssh-debian', name: '离线 Agent', alias: '旧 别名', workspace: '/old' }], member_states: { offline: { status: 'idle', session_id: 'native' } } }
    const update = vi.spyOn(api, 'updateBotGroup').mockResolvedValue({ ok: true, group: legacy })
    openDialog(legacy)
    expect(await screen.findByDisplayValue('旧 别名')).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '工作路径' })).toBeDisabled()
    fireEvent.change(screen.getByLabelText(/群聊名称/), { target: { value: '更新群聊名称' } })
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await waitFor(() => expect(update).toHaveBeenCalledTimes(1))
    expect(update.mock.calls[0][1].members?.[0]).toMatchObject({ agent_id: 'offline', machine_id: 'ssh-debian', alias: '旧 别名', workspace: '/old' })
    expect(update.mock.calls[0][1].members?.[0].id).toBeUndefined()
  })
  it('updates only the limit of a paused round and supports 20 hops without sending messages', async () => {
    const paused: BotGroup = {
      ...saved,
      members: [{ id: 'developer', agent_id: 'codex', machine_id: 'local', alias: '开发' }],
      member_states: { developer: { status: 'waiting', session_id: 'native' } },
      coordination: { status: 'error', error: '接力达到上限' },
    }
    const update = vi.spyOn(api, 'updateBotGroup').mockResolvedValue({ ok: true, group: { ...paused, max_hops: 20 } })
    const retry = vi.spyOn(api, 'retryGroup')
    openDialog(paused)
    const limit = screen.getByLabelText('单次提问最大接力轮数')
    expect(limit).toHaveValue('3')
    fireEvent.change(limit, { target: { value: '20' } })
    fireEvent.click(screen.getByRole('button', { name: '保存设置' }))
    await waitFor(() => expect(update).toHaveBeenCalledWith('g', { max_hops: 20 }))
    expect(retry).not.toHaveBeenCalled()
  })
  it('supports adding local and SSH Antigravity members with AGY-specific effort and approval options', async () => {
    const create = vi.spyOn(api, 'createBotGroup').mockResolvedValue({ ok: true, group: saved })
    vi.spyOn(api, 'getSessions').mockResolvedValue({
      items: [{ id: 's-agy', agent_id: 'ssh-antigravity-wsl', connection_id: 'wsl', title: 'WSL', workspace: '/srv/app', status: 'idle', updated_at: '' }],
    })
    vi.spyOn(api, 'getSessionModels').mockResolvedValue({
      items: [{ provider: 'antigravity', model: 'gemini-4-neon-high', label: 'Gemini 4 Neon (High)' }],
    })
    vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'antigravity', model: 'gemini-4-neon-high', effort: 'high' })

    openDialog()
    fireEvent.change(screen.getByLabelText(/群聊名称/), { target: { value: 'AGY 协作群' } })
    fireEvent.click(screen.getByRole('button', { name: '添加 WSL · Antigravity' }))
    await waitFor(() => expect(api.getSessions).toHaveBeenCalledWith('ssh-antigravity-wsl'))

    // AGY does not expose unsupported native fork selector and disables separate effort select.
    expect(screen.queryByRole('combobox', { name: '从已有会话分叉（可选）' })).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: '思考强度' })).toBeDisabled()

    fireEvent.click(screen.getByRole('button', { name: '创建群聊' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    expect(create.mock.calls[0][0].members[0]).toMatchObject({
      agent_id: 'ssh-antigravity-wsl',
      machine_id: 'wsl',
      workspace: '/srv/app',
      model_provider: 'antigravity',
      model_name: 'gemini-4-neon-high',
      approval_policy: 'manual',
    })
    expect(create.mock.calls[0][0].members[0].thinking_effort).toBeUndefined()
  })

  it('imports existing Bot Studio bots with name, persona, agent, workspace, and model while preserving manual agent entry', async () => {
    vi.spyOn(api, 'listBots').mockResolvedValue({
      items: [
        {
          id: 'bot-director',
          slug: 'video-director',
          name: '视频导演',
          description: '负责脚本文案与分镜审校',
          workspace: '/workspace/studio-video',
          connection_id: 'ssh-debian',
          default_agent_id: 'hermes',
          default_provider: 'openai',
          default_model: 'gpt-5',
          default_effort: 'high',
          default_approval_mode: 'auto',
          created_at: '2026-11-26T00:00:00Z',
          updated_at: '2026-11-26T00:00:00Z',
          has_agents_md: true,
          agents_md_excerpt: '按 2K 成片标准输出验收清单',
          skills: [],
          session_count: 1,
          running_count: 0,
        },
        {
          id: 'bot-reviewer',
          slug: 'qa-reviewer',
          name: '质量把关员',
          description: '核查事实与数据口径',
          workspace: '/workspace/qa',
          default_agent_id: 'codex',
          created_at: '2026-11-26T00:00:00Z',
          updated_at: '2026-11-26T00:00:00Z',
          has_agents_md: false,
          skills: [],
          session_count: 0,
          running_count: 0,
        },
      ],
    })
    const create = vi.spyOn(api, 'createBotGroup').mockResolvedValue({ ok: true, group: saved })

    openDialog()
    fireEvent.change(screen.getByLabelText(/群聊名称/), { target: { value: 'Bot 协作群' } })

    const importBtn = await screen.findByRole('button', { name: '导入 Bot 视频导演' })
    fireEvent.click(importBtn)
    expect(screen.getByRole('button', { name: '取消导入 Bot 视频导演' })).toHaveAttribute('aria-pressed', 'true')

    // Manual agent addition remains available alongside Bot Studio imports
    await addCodex()

    fireEvent.click(screen.getByRole('button', { name: '创建群聊' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))

    const payloadMembers = create.mock.calls[0][0].members
    expect(payloadMembers).toHaveLength(2)
    expect(payloadMembers[0]).toMatchObject({
      agent_id: 'hermes',
      machine_id: 'ssh-debian',
      name: '视频导演',
      alias: '视频导演',
      system_role_prompt: '负责脚本文案与分镜审校；按 2K 成片标准输出验收清单',
      workspace: '/workspace/studio-video',
      model_provider: 'openai',
      model_name: 'gpt-5',
      thinking_effort: 'high',
      approval_policy: 'auto',
    })
    expect(payloadMembers[1]).toMatchObject({
      agent_id: 'codex',
      alias: 'Codex-1',
    })
  })

  it('batch-imports all Bot Studio bots via 全部导入, loads agent options for each agent, and submits cleanly', async () => {
    vi.spyOn(api, 'listBots').mockResolvedValue({
      items: [
        {
          id: 'bot-a',
          slug: 'alpha',
          name: '策划师',
          description: '负责方案策划',
          workspace: '/workspace/plan',
          connection_id: 'ssh-debian',
          default_agent_id: 'hermes',
          created_at: '2026-11-26T00:00:00Z',
          updated_at: '2026-11-26T00:00:00Z',
          has_agents_md: true,
          skills: [],
          session_count: 0,
          running_count: 0,
        },
        {
          id: 'bot-b',
          slug: 'beta',
          name: '审校员',
          description: '负责成稿审校',
          workspace: '/workspace/review',
          default_agent_id: 'codex',
          created_at: '2026-11-26T00:00:00Z',
          updated_at: '2026-11-26T00:00:00Z',
          has_agents_md: false,
          skills: [],
          session_count: 0,
          running_count: 0,
        },
      ],
    })
    const create = vi.spyOn(api, 'createBotGroup').mockResolvedValue({ ok: true, group: saved })

    openDialog()
    fireEvent.change(screen.getByLabelText(/群聊名称/), { target: { value: '全员协作群' } })

    const importAllBtn = await screen.findByRole('button', { name: '全部导入' })
    fireEvent.click(importAllBtn)

    await waitFor(() => {
      expect(api.getSessions).toHaveBeenCalledWith('hermes')
      expect(api.getSessions).toHaveBeenCalledWith('codex')
    })

    expect(screen.getByRole('button', { name: '取消导入 Bot 策划师' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: '取消导入 Bot 审校员' })).toHaveAttribute('aria-pressed', 'true')

    fireEvent.click(screen.getByRole('button', { name: '创建群聊' }))
    await waitFor(() => expect(create).toHaveBeenCalledTimes(1))
    expect(create.mock.calls[0][0].members).toHaveLength(2)
    expect(create.mock.calls[0][0].members[0]).toMatchObject({ agent_id: 'hermes', name: '策划师', alias: '策划师', workspace: '/workspace/plan' })
    expect(create.mock.calls[0][0].members[1]).toMatchObject({ agent_id: 'codex', name: '审校员', alias: '审校员', workspace: '/workspace/review' })
  })
})
