import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { ShortcutSettingsPage } from './ShortcutSettingsPage'
import { loadShortcutBindings, shortcutFromEvent, useShortcutHandlers, useShortcutStore } from './shortcuts'

function Listener({ refresh }: { refresh: () => void }) {
  useShortcutHandlers({ refresh })
  return <input aria-label="消息" />
}
afterEach(() => { cleanup(); localStorage.clear(); useShortcutStore.getState().reset(); vi.restoreAllMocks() })

it('handles Ctrl+R in an input and switches immediately to the saved binding', () => {
  const refresh = vi.fn()
  render(<Listener refresh={refresh} />)
  const input = screen.getByRole('textbox')
  const press = (key: string, extra = {}) => fireEvent.keyDown(input, { key, ctrlKey: true, ...extra })
  expect(press('r')).toBe(false)
  expect(refresh).toHaveBeenCalledTimes(1)
  expect(useShortcutStore.getState().update('refresh', 'Ctrl+Shift+R')).toBeNull()
  press('r')
  expect(refresh).toHaveBeenCalledTimes(1)
  press('R', { shiftKey: true })
  expect(refresh).toHaveBeenCalledTimes(2)
  expect(loadShortcutBindings().refresh).toBe('Ctrl+Shift+R')
  press('R', { shiftKey: true, repeat: true })
  press('R', { shiftKey: true, isComposing: true })
  expect(refresh).toHaveBeenCalledTimes(2)
})

it('records keys without executing commands, rejects duplicates, disables and restores a shortcut', () => {
  const refresh = vi.fn()
  render(<MantineProvider><Listener refresh={refresh} /><ShortcutSettingsPage /></MantineProvider>)
  fireEvent.click(screen.getByRole('button', { name: '修改刷新页面快捷键' }))
  const recorder = screen.getByRole('textbox', { name: '录入刷新页面快捷键' })
  fireEvent.keyDown(recorder, { key: 'r', ctrlKey: true })
  expect(refresh).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: '保存快捷键' }))
  fireEvent.click(screen.getByRole('button', { name: '修改刷新页面快捷键' }))
  fireEvent.keyDown(screen.getByRole('textbox', { name: '录入刷新页面快捷键' }), { key: ',', ctrlKey: true })
  fireEvent.click(screen.getByRole('button', { name: '保存快捷键' }))
  expect(screen.getByRole('alert')).toHaveTextContent('打开设置')
  fireEvent.click(screen.getByRole('button', { name: '取消修改' }))
  fireEvent.click(screen.getByRole('button', { name: '停用刷新页面快捷键' }))
  fireEvent.keyDown(screen.getByRole('textbox', { name: '消息' }), { key: 'r', ctrlKey: true })
  expect(refresh).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button', { name: '恢复默认快捷键' }))
  fireEvent.keyDown(screen.getByRole('textbox', { name: '消息' }), { key: 'r', ctrlKey: true })
  expect(refresh).toHaveBeenCalledOnce()
})

it('protects typing and clipboard keys and accepts function keys and Cmd', () => {
  expect(shortcutFromEvent(new KeyboardEvent('keydown', { key: 'a' }))).toBeNull()
  expect(shortcutFromEvent(new KeyboardEvent('keydown', { key: 'c', ctrlKey: true }))).toBeNull()
  expect(shortcutFromEvent(new KeyboardEvent('keydown', { key: 'F5' }))).toBe('F5')
  expect(shortcutFromEvent(new KeyboardEvent('keydown', { key: 'R', metaKey: true }))).toBe('Ctrl+R')
  expect(shortcutFromEvent(new KeyboardEvent('keydown', { key: 'AltGraph', ctrlKey: true, altKey: true }))).toBeNull()
})

it('suspends session commands in dialogs and requires exact modifiers', () => {
  const terminal = vi.fn()
  function SessionListener() {
    useShortcutHandlers({ terminal })
    return <div role="dialog"><input aria-label="弹窗输入" /></div>
  }
  render(<SessionListener />)
  fireEvent.keyDown(screen.getByRole('textbox'), { key: '`', ctrlKey: true })
  expect(terminal).not.toHaveBeenCalled()
  cleanup()
  function WorkspaceListener() { useShortcutHandlers({ terminal }); return null }
  render(<WorkspaceListener />)
  fireEvent.keyDown(window, { key: '`', ctrlKey: true, shiftKey: true })
  expect(terminal).not.toHaveBeenCalled()
  fireEvent.keyDown(window, { key: '`', ctrlKey: true })
  expect(terminal).toHaveBeenCalledOnce()
})

it('reports storage errors without claiming the binding was saved', () => {
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota') })
  expect(useShortcutStore.getState().update('refresh', 'F5')).toContain('保存失败')
  expect(useShortcutStore.getState().bindings.refresh).toBe('Ctrl+R')
})

it('ignores corrupt or unsafe stored preferences', () => {
  localStorage.setItem('astrorder:shortcuts:v1', JSON.stringify({ refresh: 'Ctrl+C', settings: 123, terminal: null }))
  expect(loadShortcutBindings()).toMatchObject({ refresh: 'Ctrl+R', settings: 'Ctrl+,', terminal: null })
  localStorage.setItem('astrorder:shortcuts:v1', '{broken')
  expect(loadShortcutBindings().refresh).toBe('Ctrl+R')
})
