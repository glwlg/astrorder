import { MantineProvider } from '@mantine/core'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { ProjectAppearanceModal, projectAppearanceColor } from './projectAppearance'

afterEach(cleanup)

it('preserves the selected appearance when server preferences refresh before the first save', async () => {
  const onSave = vi.fn().mockResolvedValue(true)
  const onClose = vi.fn()
  const ui = (appearance = { project: { icon: 'folder', color: 'blue' } }) => (
    <MantineProvider>
      <ProjectAppearanceModal opened onClose={onClose} projectKey="project" projectLabel="星序" appearance={appearance} onSave={onSave} />
    </MantineProvider>
  )
  const view = render(ui())
  fireEvent.click(screen.getByRole('button', { name: '图标：火箭' }))
  fireEvent.click(screen.getByRole('button', { name: '颜色：蔷薇' }))
  view.rerender(ui({ project: { icon: 'folder', color: 'blue' } }))
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledWith('project', { icon: 'rocket', color: 'rose' }))
  expect(onSave).toHaveBeenCalledTimes(1)
})

it('waits for persistence and keeps the draft available after a failed save', async () => {
  let finish!: (saved: boolean) => void
  const onSave = vi.fn().mockImplementation(() => new Promise<boolean>(resolve => { finish = resolve }))
  const onClose = vi.fn()
  render(<MantineProvider><ProjectAppearanceModal opened onClose={onClose} projectKey="project" projectLabel="星序" appearance={{}} onSave={onSave} /></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '图标：火箭' }))
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  expect(onClose).not.toHaveBeenCalled()
  await act(async () => finish(false))
  expect(onClose).not.toHaveBeenCalled()
  onSave.mockResolvedValue(true)
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(onSave).toHaveBeenLastCalledWith('project', { icon: 'rocket', color: '' })
})

it('binds illustration colors without showing a color picker and resets on reopen', async () => {
  const onSave = vi.fn().mockResolvedValue(true)
  const onClose = vi.fn()
  const props = { opened: true, onClose, projectKey: 'project', projectLabel: '星序', appearance: { project: { icon: 'folder', color: 'orange' } }, onSave }
  const view = render(<MantineProvider><ProjectAppearanceModal {...props} /></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '图标：紫晶' }))
  expect(screen.queryByRole('region', { name: '图标颜色' })).toBeNull()
  expect(projectAppearanceColor({ icon: 'glass-prism', color: 'orange' })).toBe('#7c3aed')
  fireEvent.click(screen.getByRole('button', { name: '保存' }))
  await waitFor(() => expect(onSave).toHaveBeenCalledWith('project', { icon: 'glass-prism', color: 'violet' }))
  view.rerender(<MantineProvider><ProjectAppearanceModal {...props} opened={false} /></MantineProvider>)
  view.rerender(<MantineProvider><ProjectAppearanceModal {...props} /></MantineProvider>)
  expect(screen.getByRole('button', { name: '图标：文件夹' })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByRole('button', { name: '颜色：橙色' })).toHaveAttribute('aria-pressed', 'true')
})
