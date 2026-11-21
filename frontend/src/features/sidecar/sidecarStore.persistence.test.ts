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
    useSidecarStore.getState().openFileTree('session-one', 'agent-one', 'C:/ExampleInstall/Astrorder/server', 'astrorder')
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
    store.openFileTree('session-one', 'agent-one', 'C:/ExampleInstall/Astrorder/server', 'astrorder')
    store.setActiveTabId('')
    useSidecarStore.getState().syncFileTreeWorkspace('session-one', 'P:/workspace/project', 'Project')

    const state = useSidecarStore.getState()
    expect(state.activeTabId).toBe('')
    expect(state.tabs[0].artifact?.path).toBe('P:/workspace/project')
    expect(state.sessionMemories['agent-one:session-one'].tabs[0].artifact?.path).toBe('P:/workspace/project')
  })

  it('opens an isolated parent directory tab when revealing a file outside the session workspace', async () => {
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => values.set(key, value),
      removeItem: (key: string) => values.delete(key),
    })
    const { useSidecarStore } = await import('./sidecarStore')
    const store = useSidecarStore.getState()
    store.switchSession('agent-one:session-one')
    store.openFileTree('session-one', 'agent-one', '/home/fixture/project', 'project', 'ssh:debian')

    store.revealFileInTree('session-one', 'agent-one', '/tmp/report.xlsx', '/home/fixture/project', 'project', 'ssh:debian')
    let state = useSidecarStore.getState()
    expect(state.tabs).toHaveLength(2)
    expect(state.tabs[0].id).toBe('filetree:session-one')
    expect(state.tabs[0].artifact?.path).toBe('/home/fixture/project')
    expect(state.activeTabId).toBe('filetree:session-one:/tmp')
    expect(state.tabs[1]).toMatchObject({
      id: 'filetree:session-one:/tmp',
      title: '目录 (tmp)',
      viewerId: 'filetree-viewer',
    })
    expect(state.tabs[1].artifact?.path).toBe('/tmp')
    expect(state.tabs[1].artifact?.metadata?.revealPath).toBe('/tmp/report.xlsx')

    store.revealFileInTree('session-one', 'agent-one', '/tmp/other.xlsx', '/home/fixture/project', 'project', 'ssh:debian')
    state = useSidecarStore.getState()
    expect(state.tabs).toHaveLength(2)
    expect(state.tabs[1].artifact?.metadata?.revealPath).toBe('/tmp/other.xlsx')
  })
})
