import { beforeEach, describe, expect, it } from 'vitest'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import {
  APP_PROFILE_PLUGIN_KEY,
  clearStoredAppProfileForTest,
  getAppProfile,
  hasExplicitAppProfile,
  setAppProfile,
} from './appProfile'

describe('appProfile', () => {
  beforeEach(() => {
    clearStoredAppProfileForTest()
    const state = usePluginSettingsStore.getState()
    const nextConfigs = { ...state.pluginConfigs }
    delete nextConfigs[APP_PROFILE_PLUGIN_KEY]
    usePluginSettingsStore.setState({ pluginConfigs: nextConfigs })
  })

  it('defaults to developer when unset so existing local envs are uninterrupted', () => {
    expect(hasExplicitAppProfile()).toBe(false)
    expect(getAppProfile()).toBe('developer')
  })

  it('persists office and developer profile changes across storage and plugin config', () => {
    setAppProfile('office')
    expect(hasExplicitAppProfile()).toBe(true)
    expect(getAppProfile()).toBe('office')
    expect(usePluginSettingsStore.getState().pluginConfigs[APP_PROFILE_PLUGIN_KEY]?.mode).toBe('office')

    setAppProfile('developer')
    expect(getAppProfile()).toBe('developer')
  })
})
