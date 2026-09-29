import { afterEach, describe, expect, it, vi } from 'vitest'

describe('sidecar store persistence', () => {
  const values = new Map<string, string>()

  afterEach(() => {
    values.clear()
    vi.unstubAllGlobals()
    vi.resetModules()
  })

  it('restores per-session tabs, active tab, open state, and width after reload', async () => {
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    })
    const firstModule = await import('./sidecarStore')
    const first = firstModule.useSidecarStore
    first.getState().switchSession('agent-one:session-one')
    first.getState().openFileTree('session-one', 'agent-one', 'P:/workspace/project', 'Project')
    first.getState().setSidecarWidth(63)

    expect(values.size).toBeGreaterThan(0)
    vi.resetModules()

    const secondModule = await import('./sidecarStore')
    const restored = secondModule.useSidecarStore.getState()
    expect(restored.sidecarWidth).toBe(63)
    expect(restored.sessionMemories['agent-one:session-one']).toMatchObject({
      activeTabId: 'filetree:session-one',
      isOpen: true,
    })
    expect(restored.sessionMemories['agent-one:session-one'].tabs).toHaveLength(1)
  })

  it('replaces the workspace root when reopening a persisted file tab after session correction', async () => {
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    })
    const { useSidecarStore } = await import('./sidecarStore')
    useSidecarStore.getState().switchSession('agent-one:session-one')
    useSidecarStore.getState().openFileTree('session-one', 'agent-one', 'P:/DevApp/Astrorder/server', 'astrorder')
    useSidecarStore.getState().openFileTree('session-one', 'agent-one', 'P:/workspace/project', 'Project')

    const state = useSidecarStore.getState()
    expect(state.tabs).toHaveLength(1)
    expect(state.tabs[0].artifact?.path).toBe('P:/workspace/project')
    expect(state.tabs[0].title).toBe('文件 (Project)')
    expect(state.sessionMemories['agent-one:session-one'].tabs[0].artifact?.path).toBe('P:/workspace/project')
  })

  it('reconciles a restored file tab without activating it', async () => {
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    })
    const { useSidecarStore } = await import('./sidecarStore')
    const store = useSidecarStore.getState()
    store.switchSession('agent-one:session-one')
    store.openFileTree('session-one', 'agent-one', 'P:/DevApp/Astrorder/server', 'astrorder')
    store.setActiveTabId('')
    useSidecarStore.getState().syncFileTreeWorkspace('session-one', 'P:/workspace/project', 'Project')

    const state = useSidecarStore.getState()
    expect(state.activeTabId).toBe('')
    expect(state.tabs[0].artifact?.path).toBe('P:/workspace/project')
    expect(state.sessionMemories['agent-one:session-one'].tabs[0].artifact?.path).toBe('P:/workspace/project')
  })
})
