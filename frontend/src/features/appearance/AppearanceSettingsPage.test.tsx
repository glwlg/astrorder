import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import {
  DEFAULT_ACCENT_COLOR,
  THEME_ACCENT_STORAGE_KEY,
  THEME_APPEARANCE_PLUGIN_ID,
  applyAccentColor,
  normalizeHexColor,
  pickContrastInk,
} from '../../themeAccent'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { api } from '../../api/client'
import { AppearanceSettingsPage } from './AppearanceSettingsPage'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
  applyAccentColor(DEFAULT_ACCENT_COLOR, { persistLocal: false })
  usePluginSettingsStore.getState().reset()
})

describe('themeAccent utilities and AppearanceSettingsPage', () => {
  it('normalizes 3/6-digit hex strings and chooses WCAG-contrasting ink for light and dark accents', () => {
    expect(normalizeHexColor('#2563EB')).toBe('#2563eb')
    expect(normalizeHexColor('0af')).toBe('#00aaff')
    expect(normalizeHexColor('url(javascript:alert(1))')).toBeNull()
    expect(pickContrastInk('#7540bd')).toBe('#ffffff')
    expect(pickContrastInk('#facc15')).toBe('#18181b')
  })

  it('applies preset and custom colors to CSS variables, persists to backend settings, and resets to default purple', async () => {
    const updateSpy = vi.spyOn(api, 'updatePluginSettings').mockImplementation(async (patch) => ({
      enabled: {},
      configs: patch.configs || {},
    }))

    render(
      <MantineProvider>
        <AppearanceSettingsPage />
      </MantineProvider>,
    )

    expect(screen.getByText('外观与主题色')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '恢复默认紫色' })).toBeDisabled()

    // Select a preset swatch ("星海蓝" #2563eb)
    fireEvent.click(screen.getByRole('button', { name: /星海蓝/ }))
    expect(document.documentElement.style.getPropertyValue('--astr-theme-seed')).toBe('#2563eb')
    expect(localStorage.getItem(THEME_ACCENT_STORAGE_KEY)).toBe('#2563eb')
    await waitFor(() =>
      expect(updateSpy).toHaveBeenCalledWith({
        configs: { [THEME_APPEARANCE_PLUGIN_ID]: { accentColor: '#2563eb' } },
      }),
    )

    // Pick a bright custom color via native color picker (#facc15) — contrast ink switches to dark
    const nativeInput = screen.getByLabelText('系统取色器')
    fireEvent.change(nativeInput, { target: { value: '#facc15' } })
    expect(document.documentElement.style.getPropertyValue('--astr-theme-seed')).toBe('#facc15')
    expect(document.documentElement.style.getPropertyValue('--astr-accent-contrast-fallback')).toBe('#18181b')

    // Type a custom HEX value and press Enter
    const hexInput = screen.getByLabelText('HEX 色值')
    fireEvent.change(hexInput, { target: { value: '#059669' } })
    fireEvent.keyDown(hexInput, { key: 'Enter' })
    expect(document.documentElement.style.getPropertyValue('--astr-theme-seed')).toBe('#059669')

    // Reset back to default Astrorder purple
    fireEvent.click(screen.getByRole('button', { name: '恢复默认紫色' }))
    expect(document.documentElement.style.getPropertyValue('--astr-theme-seed')).toBe(DEFAULT_ACCENT_COLOR)
    expect(localStorage.getItem(THEME_ACCENT_STORAGE_KEY)).toBeNull()
  })

  it('hydrates accent color from persisted server plugin configs', async () => {
    vi.spyOn(api, 'getPluginSettings').mockResolvedValue({
      enabled: {},
      configs: { [THEME_APPEARANCE_PLUGIN_ID]: { accentColor: '#0891b2' } },
    })

    await usePluginSettingsStore.getState().hydrate(true)
    expect(document.documentElement.style.getPropertyValue('--astr-theme-seed')).toBe('#0891b2')
    expect(localStorage.getItem(THEME_ACCENT_STORAGE_KEY)).toBe('#0891b2')
  })
})
