import { MantineProvider } from '@mantine/core'
import { fireEvent, render } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import type { Project } from '../domain/types'
import { SessionRail } from './SessionRail'

it('keeps dragged order across activity updates, filtering and remount', () => {
  const saved = new Map<string, string>()
  vi.stubGlobal('localStorage', { getItem: (key: string) => saved.get(key) ?? null, setItem: (key: string, value: string) => saved.set(key, value) })
  const projects: Project[] = ['Zulu', 'alpha', 'Beta'].map(name => ({ id: name, project_id: name, project_name: name, source_id: 'source', agent_id: 'agent', session_count: 0, updated_at: '2026-09-08T00:00:00Z' }))
  const ui = (items: Project[]) => <MantineProvider><SessionRail sessions={[]} agents={{}} projects={items} onSelect={() => {}} /></MantineProvider>
  const view = render(ui(projects))
  const labels = () => [...view.container.querySelectorAll('.session-project-label')].map(node => node.textContent)
  try {
    expect(labels()).toEqual(['alpha', 'Beta', 'Zulu'])
    const transfer = { setData: vi.fn(), effectAllowed: '', dropEffect: '' }
    fireEvent.dragStart(view.getByRole('button', { name: '调整 Zulu 顺序' }), { dataTransfer: transfer })
    fireEvent.drop(view.getByRole('button', { name: '调整 alpha 顺序' }).closest('.session-project-header')!, { dataTransfer: transfer })
    expect(labels()).toEqual(['Zulu', 'alpha', 'Beta'])
    view.rerender(ui([...projects].reverse().map(p => ({ ...p, updated_at: '2030-01-01T00:00:00Z' }))))
    expect(labels()).toEqual(['Zulu', 'alpha', 'Beta'])
    fireEvent.change(view.getByPlaceholderText('搜索项目或会话'), { target: { value: 'alpha' } })
    expect(labels()).toEqual(['alpha'])
    fireEvent.change(view.getByPlaceholderText('搜索项目或会话'), { target: { value: '' } })
    expect(labels()).toEqual(['Zulu', 'alpha', 'Beta'])
    view.unmount()
    const reopened = render(ui(projects))
    expect([...reopened.container.querySelectorAll('.session-project-label')].map(n => n.textContent)).toEqual(['Zulu', 'alpha', 'Beta'])
    reopened.unmount()
  } finally { view.unmount(); vi.unstubAllGlobals() }
})
