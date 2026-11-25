import { beforeEach, describe, expect, it, vi } from 'vitest'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import {
  isProjectInOfficeMode,
  isSessionInOfficeMode,
  readStoredOfficeModeRegistry,
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
})
