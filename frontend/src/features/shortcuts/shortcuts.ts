import { useEffect, useLayoutEffect, useRef } from 'react'
import { create } from 'zustand'

export const shortcutActions = [
  { id: 'refresh', label: '刷新页面', group: '通用', defaultKey: 'Ctrl+R', description: '重新加载当前页面。' },
  { id: 'settings', label: '打开设置', group: '通用', defaultKey: 'Ctrl+,', description: '打开设置窗口。' },
  { id: 'chat', label: '前往会话', group: '导航', defaultKey: 'Ctrl+Alt+1', description: '打开会话工作台。' },
  { id: 'monitor', label: '前往监控室', group: '导航', defaultKey: 'Ctrl+Alt+2', description: '打开监控室。' },
  { id: 'swarm', label: '前往星图', group: '导航', defaultKey: 'Ctrl+Alt+3', description: '打开星图。' },
  { id: 'quickOpen', label: '快速打开文件', group: '当前会话', defaultKey: 'Ctrl+P', description: '搜索当前工作区中的文件。' },
  { id: 'fileTree', label: '打开文件树', group: '当前会话', defaultKey: 'Ctrl+Shift+E', description: '显示当前工作区的文件树。' },
  { id: 'terminal', label: '打开终端', group: '当前会话', defaultKey: 'Ctrl+`', description: '打开当前会话的内置终端。' },
  { id: 'browser', label: '打开浏览器', group: '当前会话', defaultKey: 'Ctrl+T', description: '打开内置浏览器。' },
  { id: 'sideChat', label: '打开侧边聊天', group: '当前会话', defaultKey: 'Ctrl+Alt+S', description: '在侧边栏打开当前会话。' },
  { id: 'blackboard', label: '打开黑板', group: '当前会话', defaultKey: 'Ctrl+Alt+B', description: '显示当前会话黑板。' },
  { id: 'memory', label: '打开知识记忆', group: '当前会话', defaultKey: 'Ctrl+Alt+M', description: '显示当前会话的知识记忆。' },
  { id: 'agentGraph', label: '打开决策状态机', group: '当前会话', defaultKey: 'Ctrl+Alt+G', description: '显示当前会话的决策状态机。' },
  { id: 'sidecar', label: '展开或收起侧边栏', group: '当前会话', defaultKey: 'Ctrl+Shift+B', description: '切换右侧工作区的显示状态。' },
] as const
export type ShortcutAction = typeof shortcutActions[number]['id']
export type ShortcutBindings = Record<ShortcutAction, string | null>
const storageKey = 'astrorder:shortcuts:v1'
export const defaultShortcutBindings = Object.fromEntries(shortcutActions.map(action => [action.id, action.defaultKey])) as ShortcutBindings
const modifiers = new Set(['Control', 'Meta', 'Alt', 'Shift', 'AltGraph'])
const reserved = new Set(['Ctrl+A', 'Ctrl+C', 'Ctrl+V', 'Ctrl+X', 'Ctrl+Z', 'Ctrl+Y', 'Ctrl+S', 'Ctrl+F', 'Ctrl+W', 'Ctrl+Tab', 'Ctrl+Shift+Tab', 'Ctrl+Shift+Z', 'Alt+F4', 'Alt+Tab', 'Ctrl+Alt+Delete'])

export function shortcutFromEvent(event: Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey' | 'shiftKey'>): string | null {
  if (modifiers.has(event.key) || event.key === 'Dead' || event.key === 'Unidentified') return null
  const key = event.key.length === 1 ? event.key.toUpperCase() : event.key
  if (!/^(?:[A-Z0-9`,.\-=/;[\]\\']|F(?:[1-9]|1[0-2])|Enter|Tab|ArrowUp|ArrowDown|ArrowLeft|ArrowRight|Home|End|PageUp|PageDown)$/.test(key)) return null
  if (!event.ctrlKey && !event.metaKey && !event.altKey && !/^F(?:[1-9]|1[0-2])$/.test(key)) return null
  const binding = [event.ctrlKey || event.metaKey ? 'Ctrl' : '', event.altKey ? 'Alt' : '', event.shiftKey ? 'Shift' : '', key].filter(Boolean).join('+')
  return reserved.has(binding) ? null : binding
}

function validBinding(value: unknown): value is string {
  if (typeof value !== 'string') return false
  const parts = value.split('+')
  const key = parts.pop() || ''
  return shortcutFromEvent({ key, ctrlKey: parts.includes('Ctrl'), metaKey: false, altKey: parts.includes('Alt'), shiftKey: parts.includes('Shift') }) === value
}

export function loadShortcutBindings(): ShortcutBindings {
  const bindings = { ...defaultShortcutBindings }
  try {
    const stored: unknown = JSON.parse(localStorage.getItem(storageKey) || '{}')
    if (!stored || typeof stored !== 'object' || Array.isArray(stored)) return bindings
    for (const { id } of shortcutActions) {
      const value = (stored as Record<string, unknown>)[id]
      if (value === null || validBinding(value)) bindings[id] = value
    }
    // Retain the first action if preferences from outside the UI contain duplicates.
    const seen = new Set<string>()
    for (const { id } of shortcutActions) {
      const key = bindings[id]
      if (key && seen.has(key)) bindings[id] = null
      else if (key) seen.add(key)
    }
  } catch { /* Unavailable storage or old malformed preferences use defaults. */ }
  return bindings
}

type ShortcutState = {
  bindings: ShortcutBindings
  update: (id: ShortcutAction, binding: string | null) => string | null
  reset: () => string | null
}
export const useShortcutStore = create<ShortcutState>((set, get) => {
  const save = (bindings: ShortcutBindings): string | null => {
    try { localStorage.setItem(storageKey, JSON.stringify(bindings)) }
    catch { return '快捷键保存失败，请检查本机存储是否可用。' }
    set({ bindings })
    return null
  }
  return {
    bindings: loadShortcutBindings(),
    update: (id, binding) => {
      if (binding !== null && !validBinding(binding)) return '请使用 Ctrl、Alt 与按键的组合，或 F1–F12；编辑和系统快捷键不可占用。'
      const conflict = shortcutActions.find(action => action.id !== id && binding !== null && get().bindings[action.id] === binding)
      if (conflict) return `已被「${conflict.label}」使用，请选择其他组合或先停用该快捷键。`
      return save({ ...get().bindings, [id]: binding })
    },
    reset: () => save({ ...defaultShortcutBindings }),
  }
})

export function useShortcutHandlers(handlers: Partial<Record<ShortcutAction, () => void>>, allowInDialog = false) {
  const latest = useRef(handlers)
  useLayoutEffect(() => { latest.current = handlers })
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.repeat || event.isComposing || event.getModifierState('AltGraph')) return
      if (document.querySelector('[data-shortcut-recording="true"]')) return
      if (!allowInDialog && document.querySelector('[role="dialog"], [role="alertdialog"]')) return
      const key = shortcutFromEvent(event)
      if (!key) return
      const bindings = useShortcutStore.getState().bindings
      const action = shortcutActions.find(action => bindings[action.id] === key && latest.current[action.id])
      if (!action) return
      event.preventDefault()
      event.stopPropagation()
      latest.current[action.id]?.()
    }
    window.addEventListener('keydown', onKeyDown, { capture: true })
    return () => window.removeEventListener('keydown', onKeyDown, { capture: true })
  }, [allowInDialog])
}
