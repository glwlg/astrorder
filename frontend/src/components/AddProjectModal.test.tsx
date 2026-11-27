import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, act } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import type { Agent } from '../domain/types'
import { OPEN_ONBOARDING_WIZARD_EVENT } from '../features/office/OnboardingWizard'
import { AddProjectModal } from './AddProjectModal'

beforeEach(() => {
 vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
 vi.spyOn(api, 'getConnections').mockResolvedValue({ ssh: { items: [{ id: 'ssh-wsl', display_name: 'WSL', settings: { host: '127.0.0.1', user: 'tester' } }] } } as never)
})
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
function renderModal() {
 return render(<QueryClientProvider client={new QueryClient()}><MantineProvider><AddProjectModal opened onClose={vi.fn()} /></MantineProvider></QueryClientProvider>)
}
const local = {root:'P:/server',drives:['P:'],items:[]}
const remote = {root:'/',drives:[],items:[{name:'home',path:'/home',is_dir:true}]}
async function changeServer(value:string) {
 fireEvent.click(screen.getByLabelText('1. 选择运行服务器'))
 const matcher = value === 'ssh-wsl' ? /WSL/ : /本机/
 fireEvent.click(await screen.findByRole('menuitem', { name: matcher }))
}

it('clears host drive buttons when switching to a remote directory', async () => {
 vi.stubGlobal('fetch', vi.fn(async (url:string) => new Response(JSON.stringify(url.includes('ssh-wsl') ? remote : local))))
 renderModal()
 await screen.findByRole('button',{name:'P:'})
 await waitFor(() => expect(screen.getByLabelText('1. 选择运行服务器')).not.toBeDisabled())
 await changeServer('ssh-wsl')
 await screen.findByText('home')
 expect(screen.queryByRole('button',{name:'P:'})).not.toBeInTheDocument()
 expect(screen.getByPlaceholderText('输入路径按回车跳转')).toHaveValue('/')
})

it('ignores a previous server response after switching connections', async () => {
 let completeLocal!: (response:Response)=>void
 vi.stubGlobal('fetch', vi.fn((url:string) => url.includes('ssh-wsl') ? Promise.resolve(new Response(JSON.stringify(remote))) : new Promise<Response>(resolve => { completeLocal=resolve })))
 renderModal()
 await waitFor(() => expect(screen.getByLabelText('1. 选择运行服务器')).not.toBeDisabled())
 await changeServer('ssh-wsl')
 await screen.findByText('home')
 await act(async () => { completeLocal(new Response(JSON.stringify(local))) })
 expect(screen.getByPlaceholderText('输入路径按回车跳转')).toHaveValue('/')
 expect(screen.getByText('home')).toBeInTheDocument()
 expect(screen.queryByRole('button',{name:'P:'})).not.toBeInTheDocument()
})

it('clears host drives even when the remote request fails and shows its reason', async () => {
 vi.stubGlobal('fetch', vi.fn(async (url:string) => url.includes('ssh-wsl')
  ? new Response(JSON.stringify({detail:'远程目录不可读取'}),{status:422})
  : new Response(JSON.stringify(local))))
 renderModal()
 await screen.findByRole('button',{name:'P:'})
 await waitFor(() => expect(screen.getByLabelText('1. 选择运行服务器')).not.toBeDisabled())
 await changeServer('ssh-wsl')
 await screen.findByText('加载目录树失败 (422)：远程目录不可读取')
 expect(screen.queryByRole('button',{name:'P:'})).not.toBeInTheDocument()
 expect(screen.getByRole('button',{name:'确定创建项目'})).toBeDisabled()
 await changeServer('local')
 expect(await screen.findByRole('button',{name:'P:'})).toBeInTheDocument()
 expect(screen.getByPlaceholderText('输入路径按回车跳转')).toHaveValue('P:/server')
})

it('shows onboarding wizard guidance and never falls back to hardcoded codex when no ready agent exists', async () => {
 vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(local))))
 const createProjectSpy = vi.spyOn(api, 'createProject')
 const onClose = vi.fn()
 const wizardSpy = vi.fn()
 window.addEventListener(OPEN_ONBOARDING_WIZARD_EVENT, wizardSpy)

 render(
  <QueryClientProvider client={new QueryClient()}>
   <MantineProvider>
    <AddProjectModal opened onClose={onClose} agents={{}} />
   </MantineProvider>
  </QueryClientProvider>,
 )

 await screen.findByRole('button', { name: 'P:' })
 expect(screen.getByText('当前尚未检测到可用的 AI 助手引擎')).toBeInTheDocument()
 expect(screen.getByRole('button', { name: '确定创建项目' })).toBeDisabled()
 expect(createProjectSpy).not.toHaveBeenCalled()

 fireEvent.click(screen.getByRole('button', { name: '打开初始化向导' }))
 expect(onClose).toHaveBeenCalledTimes(1)
 expect(wizardSpy).toHaveBeenCalledTimes(1)
 window.removeEventListener(OPEN_ONBOARDING_WIZARD_EVENT, wizardSpy)
})

it('selects the first ready agent instead of hardcoded codex when creating a project', async () => {
 vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(local))))
 const createProjectSpy = vi.spyOn(api, 'createProject').mockResolvedValue({
  project: { project_id: 'proj-1', source_id: 'local', workspace: 'P:/server', project_name: 'server' },
 } as never)
 const createSessionSpy = vi.spyOn(api, 'createSession').mockResolvedValue({
  id: 'sess-1',
  agent_id: 'hermes-local',
  title: '新会话',
  workspace: 'P:/server',
  status: 'idle',
  updated_at: '2026-11-27T00:00:00Z',
 })
 const agents: Record<string, Agent> = {
  'codex-offline': { id: 'codex-offline', name: 'Codex', kind: 'codex', status: 'disconnected', capabilities: [], limitation: null },
  'hermes-local': { id: 'hermes-local', name: 'Hermes', kind: 'hermes', status: 'ready', capabilities: ['chat'], limitation: null },
 }

 render(
  <QueryClientProvider client={new QueryClient()}>
   <MantineProvider>
    <AddProjectModal opened onClose={vi.fn()} agents={agents} />
   </MantineProvider>
  </QueryClientProvider>,
 )

 await screen.findByRole('button', { name: 'P:' })
 const submitBtn = screen.getByRole('button', { name: '确定创建项目' })
 await waitFor(() => expect(submitBtn).not.toBeDisabled())
 fireEvent.click(submitBtn)

 await waitFor(() => {
  expect(createProjectSpy).toHaveBeenCalledWith(expect.objectContaining({ agent_id: 'hermes-local' }))
  expect(createSessionSpy).toHaveBeenCalledWith(expect.objectContaining({ agent_id: 'hermes-local' }))
 })
})
