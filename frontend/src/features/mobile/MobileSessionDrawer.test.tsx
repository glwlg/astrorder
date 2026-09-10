import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MobileSessionDrawer } from './MobileSessionDrawer'
import type { ProjectGroup } from '../../components/sessionRailModel'

describe('MobileSessionDrawer', () => {
  afterEach(() => {
    cleanup()
  })

  const groups: ProjectGroup[] = [
    {
      key: 'project:local\0proj-1',
      label: '移动项目A',
      sessions: [
        {
          id: 'sess-m-1',
          agent_id: 'local',
          title: '会话 1',
          workspace: null,
          status: 'idle',
          updated_at: '2026-09-09T10:00:00Z',
        },
      ],
      sessionCount: 1,
      latestUpdatedAt: '2026-09-09T10:00:00Z',
    },
    {
      key: 'unmarked:local',
      label: '未标记项目',
      sessions: [],
      sessionCount: 0,
      latestUpdatedAt: '',
    },
  ]

  it('renders project list and allows deleting named project on disclosure click', () => {
    const onDeleteProject = vi.fn()
    const onSelect = vi.fn()
    const onPin = vi.fn()

    render(
      <MobileSessionDrawer
        groups={groups}
        pins={{}}
        selectedKey=""
        appearance={{}}
        onSelect={onSelect}
        onPin={onPin}
        onDeleteProject={onDeleteProject}
      />,
    )

    // 默认是展开状态，应该能直接看到删除项目按钮
    const deleteBtn = screen.getByRole('button', { name: '删除项目 移动项目A' })
    expect(deleteBtn).toBeInTheDocument()

    fireEvent.click(deleteBtn)
    expect(onDeleteProject).toHaveBeenCalledWith(groups[0])
  })

  it('does not render delete button for unmarked group', () => {
    const onDeleteProject = vi.fn()
    render(
      <MobileSessionDrawer
        groups={groups}
        pins={{}}
        selectedKey=""
        appearance={{}}
        onSelect={vi.fn()}
        onPin={vi.fn()}
        onDeleteProject={onDeleteProject}
      />,
    )

    expect(screen.queryByRole('button', { name: '删除项目 未标记项目' })).not.toBeInTheDocument()
  })
})
