import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import {
  OFFICE_WORKSPACE_MODE_CONFIG_KEY,
  isProjectInOfficeMode,
  isSessionInOfficeMode,
  readStoredOfficeModeRegistry,
  resolveActiveRegistry,
  setProjectOfficeMode,
} from './officeWorkspaceMode'

describe('officeWorkspaceMode', () => {
  beforeEach(() => {
    localStorage.clear()
    usePluginSettingsStore.getState().reset()
    vi.spyOn(usePluginSettingsStore.getState(), 'setPluginConfig').mockImplementation((pluginId, key, value) => {
      usePluginSettingsStore.setState((state) => ({
        pluginConfigs: {
          ...state.pluginConfigs,
          [pluginId]: { ...(state.pluginConfigs[pluginId] || {}), [key]: value },
        },
      }))
    })
  })

  it('persists and resolves office mode by project key and normalized workspace path', () => {
    const enabled = setProjectOfficeMode(
      { projectKey: 'project:local\u0000p-office', workspace: 'D:\\Work\\Docs\\' },
      true,
    )
    expect(isProjectInOfficeMode(enabled, 'project:local\u0000p-office', null)).toBe(true)
    expect(isProjectInOfficeMode(enabled, 'other-key', 'd:/work/docs')).toBe(true)
    expect(
      isSessionInOfficeMode(enabled, {
        workspace: 'D:/Work/Docs',
        agent_id: 'codex',
      }),
    ).toBe(true)
    expect(
      isSessionInOfficeMode(enabled, {
        workspace: 'D:/Code/Repo',
        agent_id: 'codex',
      }),
    ).toBe(false)
    expect(readStoredOfficeModeRegistry().workspaces).toContain('d:/work/docs')

    const disabled = setProjectOfficeMode(
      { projectKey: 'project:local\u0000p-office', workspace: 'D:\\Work\\Docs' },
      false,
    )
    expect(isProjectInOfficeMode(disabled, 'project:local\u0000p-office', 'D:/Work/Docs')).toBe(false)
  })

  it('does not resurrect stale localStorage entries when server returns an empty office registry', () => {
    localStorage.setItem(
      'astrorder:office-workspace-mode',
      JSON.stringify({ keys: ['project:local\u0000stale'], workspaces: ['d:/work/stale'] }),
    )

    // Explicit empty registry from server/plugin config must win over stale localStorage
    const fromEmptyServer = resolveActiveRegistry({ keys: [], workspaces: [] }, true)
    expect(fromEmptyServer).toEqual({ keys: [], workspaces: [] })
    expect(isProjectInOfficeMode(fromEmptyServer, 'project:local\u0000stale', 'D:/Work/Stale')).toBe(false)

    // Hydrated store with no office-workspace-mode entry must also stay empty
    const fromHydratedUndefined = resolveActiveRegistry(undefined, true)
    expect(fromHydratedUndefined).toEqual({ keys: [], workspaces: [] })

    // Pre-hydration fallback still reads cached localStorage to avoid flicker before server responds
    const preHydrate = resolveActiveRegistry(undefined, false)
    expect(preHydrate.keys).toContain('project:local\u0000stale')

    // Turning off the last office project keeps it off even if localStorage previously had it
    usePluginSettingsStore.setState({
      hydrated: true,
      pluginConfigs: {
        [OFFICE_WORKSPACE_MODE_CONFIG_KEY]: {
          projects: { keys: ['project:local\u0000stale'], workspaces: ['d:/work/stale'] },
        },
      },
    })
    const turnedOff = setProjectOfficeMode(
      { projectKey: 'project:local\u0000stale', workspace: 'D:/Work/Stale' },
      false,
    )
    expect(turnedOff).toEqual({ keys: [], workspaces: [] })
    expect(readStoredOfficeModeRegistry()).toEqual({ keys: [], workspaces: [] })
    const reResolved = resolveActiveRegistry(
      usePluginSettingsStore.getState().pluginConfigs[OFFICE_WORKSPACE_MODE_CONFIG_KEY]?.projects,
      true,
    )
    expect(reResolved).toEqual({ keys: [], workspaces: [] })
  })
})
