import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MobileSessionDrawer } from './MobileSessionDrawer'
import type { ProjectGroup } from '../../components/sessionRailModel'
import { scopeKey } from '../../domain/semantics'
import type { Session } from '../../domain/types'

function session(partial: Partial<Session> & Pick<Session, 'id' | 'title'>): Session {
  return {
    agent_id: 'local',
    workspace: '/workspace/mobile',
    status: 'idle',
    updated_at: '2026-09-09T10:00:00Z',
    ...partial,
  }
}

const groups: ProjectGroup[] = [
  {
    key: 'project:local\0proj-1',
    label: '移动项目A',
    sessions: [
      session({ id: 'sess-m-1', title: '会话 1' }),
      session({ id: 'sess-m-2', title: '会话 2' }),
    ],
    sessionCount: 2,
    latestUpdatedAt: '2026-09-09T10:00:00Z',
  },
  {
    key: 'project:local\0proj-2',
    label: '移动项目B',
    sessions: [session({ id: 'sess-b-1', title: '另一项目会话' })],
    sessionCount: 1,
    latestUpdatedAt: '2026-09-09T09:00:00Z',
  },
  {
    key: 'unmarked:local',
    label: '未标记项目',
    sessions: [session({ id: 'sess-u-1', title: 'Astrorder 远程会话', workspace: '/workspace/ops' })],
    sessionCount: 1,
    latestUpdatedAt: '',
  },
]

function mount(overrides: Partial<Parameters<typeof MobileSessionDrawer>[0]> = {}) {
  return render(
    <MobileSessionDrawer
      groups={groups}
      pins={{}}
      selectedKey={scopeKey('local', 'sess-m-1')}
      appearance={{}}
      onSelect={vi.fn()}
      onPin={vi.fn()}
      onDeleteProject={vi.fn()}
      onDeleteSession={vi.fn()}
      onCreate={vi.fn()}
      {...overrides}
    />,
  )
}

describe('MobileSessionDrawer', () => {
  afterEach(() => {
    cleanup()
  })

  it('expands only the project that contains the selected session', () => {
    mount()
    expect(screen.getByRole('button', { name: '会话 1' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '另一项目会话' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'ops' })).not.toBeInTheDocument()
  })

  it('keeps pin and delete off the session row and exposes them from a long-press menu', () => {
    const onPin = vi.fn()
    const onDeleteSession = vi.fn()
    mount({ onPin, onDeleteSession })

    expect(screen.queryByRole('button', { name: '置顶 会话 1' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '删除会话 会话 1' })).not.toBeInTheDocument()

    fireEvent.contextMenu(screen.getByRole('button', { name: '会话 1' }), { clientX: 140, clientY: 220 })
    const menu = screen.getByRole('menu', { name: '会话操作' })
    fireEvent.click(within(menu).getByRole('menuitem', { name: '置顶' }))
    expect(onPin).toHaveBeenCalledWith(groups[0].sessions[0])

    fireEvent.contextMenu(screen.getByRole('button', { name: '会话 1' }), { clientX: 140, clientY: 220 })
    fireEvent.click(within(screen.getByRole('menu', { name: '会话操作' })).getByRole('menuitem', { name: '删除' }))
    expect(onDeleteSession).toHaveBeenCalledWith(groups[0].sessions[0], expect.objectContaining({ clientX: 140, clientY: 220 }))
  })

  it('hides project create/delete as always-visible actions and offers them from the project menu', () => {
    const onCreate = vi.fn()
    const onDeleteProject = vi.fn()
    mount({ onCreate, onDeleteProject })

    expect(screen.queryByRole('button', { name: '新建会话' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '删除项目 移动项目A' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '项目操作 移动项目A' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '新建会话' }))
    expect(onCreate).toHaveBeenCalledWith(groups[0])

    fireEvent.click(screen.getByRole('button', { name: '项目操作 移动项目A' }))
    fireEvent.click(screen.getByRole('menuitem', { name: '删除项目' }), { clientX: 120, clientY: 240 })
    expect(onDeleteProject).toHaveBeenCalledWith(groups[0], expect.objectContaining({ clientX: 120, clientY: 240 }))
  })

  it('does not offer delete project for unmarked groups', () => {
    mount()
    fireEvent.click(screen.getByRole('button', { name: '未标记项目' }))
    fireEvent.click(screen.getByRole('button', { name: '项目操作 未标记项目' }))
    expect(screen.queryByRole('menuitem', { name: '删除项目' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'ops' })).toBeInTheDocument()
  })
})
