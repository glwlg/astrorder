import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import {
  DEFAULT_THEME_PALETTE,
  THEME_ACCENT_STORAGE_KEY,
  THEME_APPEARANCE_PLUGIN_ID,
  THEME_PALETTE_STORAGE_KEY,
  applyThemePalette,
  deriveHarmonyFromPrimary,
  normalizeHexColor,
  pickContrastInk,
} from '../../themeAccent'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { AppearanceSettingsPage } from './AppearanceSettingsPage'

describe('themeAccent multi-role utilities and AppearanceSettingsPage', () => {
  beforeEach(() => {
    localStorage.clear()
    usePluginSettingsStore.getState().reset()
    applyThemePalette({ ...DEFAULT_THEME_PALETTE }, { persistLocal: false })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    localStorage.clear()
    applyThemePalette({ ...DEFAULT_THEME_PALETTE }, { persistLocal: false })
  })

  it('normalizes 3-digit and 6-digit hex codes, picks contrast ink, and derives harmony', () => {
    expect(normalizeHexColor('#2563EB')).toBe('#2563eb')
    expect(normalizeHexColor('0af')).toBe('#00aaff')
    expect(normalizeHexColor('not-a-color')).toBeNull()
    expect(pickContrastInk('#7540bd')).toBe('#ffffff')
    expect(pickContrastInk('#fde047')).toBe('#18181b')

    const slateHarmony = deriveHarmonyFromPrimary('#475569')
    expect(slateHarmony.primary).toBe('#475569')
    expect(slateHarmony.surface).not.toBe(DEFAULT_THEME_PALETTE.surface)
  })

  it('applies presets, independently mixes all 4 role pickers, persists atomically, and resets', async () => {
    const updateSpy = vi.spyOn(api, 'updatePluginSettings').mockImplementation(async (req) => ({
      enabled: {},
      configs: req.configs ?? {},
    }))

    render(
      <MantineProvider>
        <AppearanceSettingsPage />
      </MantineProvider>,
    )

    // 1) Apply curated 4-color preset
    fireEvent.click(screen.getByRole('button', { name: '应用主题配色 深海晴空' }))
    expect(document.documentElement.style.getPropertyValue('--astr-theme-primary')).toBe('#2563eb')
    expect(document.documentElement.style.getPropertyValue('--astr-theme-secondary')).toBe('#06b6d4')
    expect(document.documentElement.style.getPropertyValue('--astr-theme-highlight')).toBe('#3b82f6')
    expect(document.documentElement.style.getPropertyValue('--astr-theme-surface')).toBe('#dbeafe')
    expect(document.documentElement.getAttribute('data-astr-theme-custom')).toBe('true')
    expect(localStorage.getItem(THEME_ACCENT_STORAGE_KEY)).toBe('#2563eb')
    expect(JSON.parse(localStorage.getItem(THEME_PALETTE_STORAGE_KEY) || '{}')).toEqual({
      primary: '#2563eb',
      secondary: '#06b6d4',
      highlight: '#3b82f6',
      surface: '#dbeafe',
    })

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith({
        configs: {
          [THEME_APPEARANCE_PLUGIN_ID]: {
            accentColor: '#2563eb',
            secondaryColor: '#06b6d4',
            highlightColor: '#3b82f6',
            surfaceColor: '#dbeafe',
          },
        },
      })
    })

    // 2) Independently customize Secondary via native picker & Surface via HEX input
    const secondaryPicker = screen.getByLabelText('次级色系统取色器')
    fireEvent.change(secondaryPicker, { target: { value: '#ea580c' } })
    expect(document.documentElement.style.getPropertyValue('--astr-theme-secondary')).toBe('#ea580c')
    expect(document.documentElement.style.getPropertyValue('--astr-theme-primary')).toBe('#2563eb')

    const surfaceHex = screen.getByLabelText('表面氛围 HEX 色值')
    fireEvent.change(surfaceHex, { target: { value: '#fef3c7' } })
    fireEvent.blur(surfaceHex)
    expect(document.documentElement.style.getPropertyValue('--astr-theme-surface')).toBe('#fef3c7')

    // 3) Reset restores all 4 defaults and clears custom attribute
    fireEvent.click(screen.getByRole('button', { name: '恢复默认配色' }))
    expect(document.documentElement.style.getPropertyValue('--astr-theme-primary')).toBe(DEFAULT_THEME_PALETTE.primary)
    expect(document.documentElement.style.getPropertyValue('--astr-theme-secondary')).toBe(DEFAULT_THEME_PALETTE.secondary)
    expect(document.documentElement.style.getPropertyValue('--astr-theme-highlight')).toBe(DEFAULT_THEME_PALETTE.highlight)
    expect(document.documentElement.style.getPropertyValue('--astr-theme-surface')).toBe(DEFAULT_THEME_PALETTE.surface)
    expect(document.documentElement.hasAttribute('data-astr-theme-custom')).toBe(false)
    expect(localStorage.getItem(THEME_PALETTE_STORAGE_KEY)).toBeNull()
  })

  it('hydrates both legacy single accentColor and full 4-role palette from plugin settings', async () => {
    vi.spyOn(api, 'getPluginSettings').mockResolvedValueOnce({
      enabled: {},
      configs: {
        [THEME_APPEARANCE_PLUGIN_ID]: {
          accentColor: '#475569',
        },
      },
    })

    await usePluginSettingsStore.getState().hydrate(true)
    expect(document.documentElement.style.getPropertyValue('--astr-theme-primary')).toBe('#475569')
    expect(document.documentElement.style.getPropertyValue('--astr-theme-surface')).not.toBe(DEFAULT_THEME_PALETTE.surface)
    expect(document.documentElement.getAttribute('data-astr-theme-custom')).toBe('true')

    vi.spyOn(api, 'getPluginSettings').mockResolvedValueOnce({
      enabled: {},
      configs: {
        [THEME_APPEARANCE_PLUGIN_ID]: {
          accentColor: '#059669',
          secondaryColor: '#f59e0b',
          highlightColor: '#10b981',
          surfaceColor: '#ecfccb',
        },
      },
    })

    await usePluginSettingsStore.getState().hydrate(true)
    expect(document.documentElement.style.getPropertyValue('--astr-theme-primary')).toBe('#059669')
    expect(document.documentElement.style.getPropertyValue('--astr-theme-secondary')).toBe('#f59e0b')
    expect(document.documentElement.style.getPropertyValue('--astr-theme-highlight')).toBe('#10b981')
    expect(document.documentElement.style.getPropertyValue('--astr-theme-surface')).toBe('#ecfccb')
  })
})
