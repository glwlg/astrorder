import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor, act } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { api } from '../api/client'
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
function changeServer(value:string) { fireEvent.change(screen.getByLabelText('1. 选择运行服务器'), {target:{value}}) }

it('clears host drive buttons when switching to a remote directory', async () => {
 vi.stubGlobal('fetch', vi.fn(async (url:string) => new Response(JSON.stringify(url.includes('ssh-wsl') ? remote : local))))
 renderModal()
 await screen.findByRole('button',{name:'P:'})
 await waitFor(() => expect(screen.getByLabelText('1. 选择运行服务器')).not.toBeDisabled())
 changeServer('ssh-wsl')
 await screen.findByText('home')
 expect(screen.queryByRole('button',{name:'P:'})).not.toBeInTheDocument()
 expect(screen.getByPlaceholderText('输入路径按回车跳转')).toHaveValue('/')
})

it('ignores a previous server response after switching connections', async () => {
 let completeLocal!: (response:Response)=>void
 vi.stubGlobal('fetch', vi.fn((url:string) => url.includes('ssh-wsl') ? Promise.resolve(new Response(JSON.stringify(remote))) : new Promise<Response>(resolve => { completeLocal=resolve })))
 renderModal()
 await waitFor(() => expect(screen.getByLabelText('1. 选择运行服务器')).not.toBeDisabled())
 changeServer('ssh-wsl')
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
 changeServer('ssh-wsl')
 await screen.findByText('加载目录树失败 (422)：远程目录不可读取')
 expect(screen.queryByRole('button',{name:'P:'})).not.toBeInTheDocument()
 expect(screen.getByRole('button',{name:'确定创建项目'})).toBeDisabled()
 changeServer('local')
 expect(await screen.findByRole('button',{name:'P:'})).toBeInTheDocument()
 expect(screen.getByPlaceholderText('输入路径按回车跳转')).toHaveValue('P:/server')
})
