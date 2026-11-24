import { useSyncExternalStore } from 'react'

export type ThemeColorRole = 'primary' | 'secondary' | 'highlight' | 'surface'

export interface ThemePalette {
  primary: string
  secondary: string
  highlight: string
  surface: string
}

export interface ThemePalettePreset {
  id: string
  name: string
  palette: ThemePalette
}

export interface ThemeRoleDescriptor {
  role: ThemeColorRole
  label: string
  shortLabel: string
  description: string
  cssVar: string
  configKey: 'accentColor' | 'secondaryColor' | 'highlightColor' | 'surfaceColor'
  quickSwatches: readonly string[]
}

export const DEFAULT_THEME_PALETTE: Readonly<ThemePalette> = {
  primary: '#7540bd',
  secondary: '#bf3376',
  highlight: '#8b5cf6',
  surface: '#dfd6f2',
}

export const DEFAULT_ACCENT_COLOR = DEFAULT_THEME_PALETTE.primary
export const THEME_ACCENT_STORAGE_KEY = 'astrorder:theme-accent'
export const THEME_PALETTE_STORAGE_KEY = 'astrorder:theme-palette'
export const THEME_APPEARANCE_PLUGIN_ID = 'theme-appearance'

export const THEME_ROLE_DESCRIPTORS: readonly ThemeRoleDescriptor[] = [
  {
    role: 'primary',
    label: '主色调',
    shortLabel: '主色',
    description: '控制导航高亮、按钮、开关、聚焦边框与核心交互图标',
    cssVar: '--astr-theme-primary',
    configKey: 'accentColor',
    quickSwatches: ['#7540bd', '#2563eb', '#059669', '#d97706', '#e11d48', '#334155'],
  },
  {
    role: 'secondary',
    label: '次级色',
    shortLabel: '次级',
    description: '控制发送按钮渐变尾色、侧栏氛围副光晕与运行流光点缀',
    cssVar: '--astr-theme-secondary',
    configKey: 'secondaryColor',
    quickSwatches: ['#bf3376', '#06b6d4', '#0ea5e9', '#f97316', '#84cc16', '#64748b'],
  },
  {
    role: 'highlight',
    label: '选中高亮',
    shortLabel: '选中',
    description: '控制激活会话底色、筛选标签选中态、消息微染与文本划选区',
    cssVar: '--astr-theme-highlight',
    configKey: 'highlightColor',
    quickSwatches: ['#8b5cf6', '#3b82f6', '#10b981', '#f59e0b', '#f43f5e', '#475569'],
  },
  {
    role: 'surface',
    label: '表面氛围',
    shortLabel: '氛围',
    description: '控制侧栏、导航轨、画布背景与分隔边框的冷暖基调',
    cssVar: '--astr-theme-surface',
    configKey: 'surfaceColor',
    quickSwatches: ['#dfd6f2', '#dbeafe', '#d1fae5', '#fef3c7', '#ffe4e6', '#dce3ec'],
  },
]

export const THEME_PALETTE_PRESETS: readonly ThemePalettePreset[] = [
  {
    id: 'astrorder-violet',
    name: '星序紫韵',
    palette: { primary: '#7540bd', secondary: '#bf3376', highlight: '#8b5cf6', surface: '#dfd6f2' },
  },
  {
    id: 'obsidian-slate',
    name: '曜石极简',
    palette: { primary: '#334155', secondary: '#64748b', highlight: '#475569', surface: '#dce3ec' },
  },
  {
    id: 'ocean-azure',
    name: '深海晴空',
    palette: { primary: '#2563eb', secondary: '#06b6d4', highlight: '#3b82f6', surface: '#dbeafe' },
  },
  {
    id: 'indigo-orbit',
    name: '靛蓝星轨',
    palette: { primary: '#4f46e5', secondary: '#0ea5e9', highlight: '#6366f1', surface: '#e0e7ff' },
  },
  {
    id: 'emerald-mist',
    name: '翠微山岚',
    palette: { primary: '#059669', secondary: '#0891b2', highlight: '#10b981', surface: '#d1fae5' },
  },
  {
    id: 'forest-moss',
    name: '森林苔绿',
    palette: { primary: '#15803d', secondary: '#65a30d', highlight: '#22c55e', surface: '#dcfce7' },
  },
  {
    id: 'amber-sunset',
    name: '落日琥珀',
    palette: { primary: '#d97706', secondary: '#e11d48', highlight: '#f59e0b', surface: '#fef3c7' },
  },
  {
    id: 'coral-rose',
    name: '珊瑚绯霞',
    palette: { primary: '#e11d48', secondary: '#f97316', highlight: '#f43f5e', surface: '#ffe4e6' },
  },
  {
    id: 'matcha-sand',
    name: '抹茶暖砂',
    palette: { primary: '#4d7c0f', secondary: '#ca8a04', highlight: '#65a30d', surface: '#ecfccb' },
  },
  {
    id: 'morandi-stone',
    name: '莫兰迪雾',
    palette: { primary: '#57534e', secondary: '#a16207', highlight: '#78716c', surface: '#e7e5e4' },
  },
]

export interface AccentPreset {
  name: string
  hex: string
}

export const ACCENT_PRESETS: readonly AccentPreset[] = THEME_PALETTE_PRESETS.map((preset) => ({
  name: preset.name,
  hex: preset.palette.primary,
}))

export function normalizeHexColor(raw: unknown): string | null {
  if (typeof raw !== 'string') return null
  const trimmed = raw.trim().replace(/^#/, '')
  if (/^[0-9a-fA-F]{3}$/.test(trimmed)) {
    const [r, g, b] = trimmed.split('')
    return `#${r}${r}${g}${g}${b}${b}`.toLowerCase()
  }
  if (/^[0-9a-fA-F]{6}$/.test(trimmed)) {
    return `#${trimmed}`.toLowerCase()
  }
  return null
}

function hexToRgb(hex: string): [number, number, number] {
  const norm = normalizeHexColor(hex) || DEFAULT_ACCENT_COLOR
  return [
    parseInt(norm.slice(1, 3), 16),
    parseInt(norm.slice(3, 5), 16),
    parseInt(norm.slice(5, 7), 16),
  ]
}

function rgbToHex(r: number, g: number, b: number): string {
  const clamp = (v: number) => Math.max(0, Math.min(255, Math.round(v)))
  return `#${[clamp(r), clamp(g), clamp(b)].map((v) => v.toString(16).padStart(2, '0')).join('')}`
}

function rgbToHsl(r: number, g: number, b: number): [number, number, number] {
  const rn = r / 255
  const gn = g / 255
  const bn = b / 255
  const max = Math.max(rn, gn, bn)
  const min = Math.min(rn, gn, bn)
  const l = (max + min) / 2
  if (max === min) return [0, 0, l]
  const d = max - min
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
  let h = 0
  if (max === rn) h = ((gn - bn) / d + (gn < bn ? 6 : 0)) * 60
  else if (max === gn) h = ((bn - rn) / d + 2) * 60
  else h = ((rn - gn) / d + 4) * 60
  return [h, s, l]
}

function hslToHex(h: number, s: number, l: number): string {
  const hn = ((h % 360) + 360) % 360
  const sn = Math.max(0, Math.min(1, s))
  const ln = Math.max(0, Math.min(1, l))
  const c = (1 - Math.abs(2 * ln - 1)) * sn
  const x = c * (1 - Math.abs(((hn / 60) % 2) - 1))
  const m = ln - c / 2
  let r = 0
  let g = 0
  let b = 0
  if (hn < 60) [r, g, b] = [c, x, 0]
  else if (hn < 120) [r, g, b] = [x, c, 0]
  else if (hn < 180) [r, g, b] = [0, c, x]
  else if (hn < 240) [r, g, b] = [0, x, c]
  else if (hn < 300) [r, g, b] = [x, 0, c]
  else [r, g, b] = [c, 0, x]
  return rgbToHex((r + m) * 255, (g + m) * 255, (b + m) * 255)
}

export function deriveHarmonyFromPrimary(rawPrimary: string): ThemePalette {
  const primary = normalizeHexColor(rawPrimary) || DEFAULT_THEME_PALETTE.primary
  if (primary === DEFAULT_THEME_PALETTE.primary) {
    return { ...DEFAULT_THEME_PALETTE }
  }
  const exactPreset = THEME_PALETTE_PRESETS.find((preset) => preset.palette.primary === primary)
  if (exactPreset) {
    return { ...exactPreset.palette }
  }
  const [r, g, b] = hexToRgb(primary)
  const [h, s, l] = rgbToHsl(r, g, b)
  if (s < 0.16) {
    return {
      primary,
      secondary: hslToHex(h, Math.min(0.22, s + 0.04), Math.min(0.62, Math.max(0.38, l + 0.14))),
      highlight: hslToHex(h, Math.min(0.2, s + 0.02), Math.min(0.56, Math.max(0.34, l + 0.07))),
      surface: hslToHex(h, Math.min(0.18, s + 0.02), 0.89),
    }
  }
  return {
    primary,
    secondary: hslToHex(h + 36, Math.min(0.84, Math.max(0.52, s)), Math.min(0.58, Math.max(0.42, l + 0.02))),
    highlight: hslToHex(h, Math.min(0.9, Math.max(0.55, s + 0.06)), Math.min(0.64, Math.max(0.48, l + 0.1))),
    surface: hslToHex(h, Math.min(0.48, s * 0.62), 0.9),
  }
}

export function relativeLuminance(hex: string): number {
  const [r, g, b] = hexToRgb(hex).map((v) => {
    const s = v / 255
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

export function contrastRatio(hexA: string, hexB: string): number {
  const l1 = relativeLuminance(hexA)
  const l2 = relativeLuminance(hexB)
  const lighter = Math.max(l1, l2)
  const darker = Math.min(l1, l2)
  return (lighter + 0.05) / (darker + 0.05)
}

export function pickContrastInk(hex: string): '#ffffff' | '#18181b' {
  const whiteRatio = contrastRatio(hex, '#ffffff')
  const darkRatio = contrastRatio(hex, '#18181b')
  return whiteRatio >= darkRatio ? '#ffffff' : '#18181b'
}

export function isDefaultThemePalette(palette: ThemePalette): boolean {
  return (
    palette.primary === DEFAULT_THEME_PALETTE.primary &&
    palette.secondary === DEFAULT_THEME_PALETTE.secondary &&
    palette.highlight === DEFAULT_THEME_PALETTE.highlight &&
    palette.surface === DEFAULT_THEME_PALETTE.surface
  )
}

function palettesEqual(a: ThemePalette, b: ThemePalette): boolean {
  return (
    a.primary === b.primary &&
    a.secondary === b.secondary &&
    a.highlight === b.highlight &&
    a.surface === b.surface
  )
}

export function normalizeThemePalette(raw: Partial<Record<ThemeColorRole, unknown>> | null | undefined): ThemePalette {
  const primary = normalizeHexColor(raw?.primary) || DEFAULT_THEME_PALETTE.primary
  const hasExplicitExtras =
    normalizeHexColor(raw?.secondary) !== null ||
    normalizeHexColor(raw?.highlight) !== null ||
    normalizeHexColor(raw?.surface) !== null
  const fallback = !hasExplicitExtras && primary !== DEFAULT_THEME_PALETTE.primary
    ? deriveHarmonyFromPrimary(primary)
    : DEFAULT_THEME_PALETTE
  return {
    primary,
    secondary: normalizeHexColor(raw?.secondary) || fallback.secondary,
    highlight: normalizeHexColor(raw?.highlight) || fallback.highlight,
    surface: normalizeHexColor(raw?.surface) || fallback.surface,
  }
}

export function readStoredThemePalette(): ThemePalette {
  if (typeof localStorage === 'undefined') return { ...DEFAULT_THEME_PALETTE }
  try {
    const rawPalette = localStorage.getItem(THEME_PALETTE_STORAGE_KEY)
    if (rawPalette) {
      const parsed = JSON.parse(rawPalette) as Partial<Record<ThemeColorRole, unknown>>
      if (parsed && typeof parsed === 'object') {
        return normalizeThemePalette(parsed)
      }
    }
  } catch {
    // fallback to legacy single-accent key
  }
  try {
    const legacy = normalizeHexColor(localStorage.getItem(THEME_ACCENT_STORAGE_KEY))
    if (legacy) {
      return deriveHarmonyFromPrimary(legacy)
    }
  } catch {
    // ignore storage errors
  }
  return { ...DEFAULT_THEME_PALETTE }
}

export function readStoredAccentColor(): string {
  return readStoredThemePalette().primary
}

let currentPalette: ThemePalette = readStoredThemePalette()
const listeners = new Set<() => void>()

function emitChange() {
  for (const listener of listeners) listener()
}

export function getActiveThemePalette(): ThemePalette {
  return currentPalette
}

export function getActiveAccentColor(): string {
  return currentPalette.primary
}

export function serializePaletteForPluginConfig(palette: ThemePalette): Record<string, string> {
  return {
    accentColor: palette.primary,
    secondaryColor: palette.secondary,
    highlightColor: palette.highlight,
    surfaceColor: palette.surface,
  }
}

export function applyThemePalette(
  raw: Partial<ThemePalette>,
  options: { persistLocal?: boolean } = {},
): ThemePalette {
  const next: ThemePalette = {
    primary: normalizeHexColor(raw.primary) || currentPalette.primary || DEFAULT_THEME_PALETTE.primary,
    secondary: normalizeHexColor(raw.secondary) || currentPalette.secondary || DEFAULT_THEME_PALETTE.secondary,
    highlight: normalizeHexColor(raw.highlight) || currentPalette.highlight || DEFAULT_THEME_PALETTE.highlight,
    surface: normalizeHexColor(raw.surface) || currentPalette.surface || DEFAULT_THEME_PALETTE.surface,
  }
  const changed = !palettesEqual(currentPalette, next)
  currentPalette = next

  if (typeof document !== 'undefined') {
    const root = document.documentElement
    const setProp = (name: string, value: string) => {
      if (root.style.getPropertyValue(name) !== value) {
        root.style.setProperty(name, value)
      }
    }
    setProp('--astr-theme-seed', next.primary)
    setProp('--astr-theme-primary', next.primary)
    setProp('--astr-theme-secondary', next.secondary)
    setProp('--astr-theme-highlight', next.highlight)
    setProp('--astr-theme-surface', next.surface)
    setProp('--astr-accent-contrast-fallback', pickContrastInk(next.primary))
    setProp('--astr-secondary-contrast-fallback', pickContrastInk(next.secondary))
    if (isDefaultThemePalette(next)) {
      if (root.hasAttribute('data-astr-theme-custom')) {
        root.removeAttribute('data-astr-theme-custom')
      }
    } else if (root.getAttribute('data-astr-theme-custom') !== 'true') {
      root.setAttribute('data-astr-theme-custom', 'true')
    }
  }

  if (options.persistLocal !== false && typeof localStorage !== 'undefined') {
    try {
      if (isDefaultThemePalette(next)) {
        localStorage.removeItem(THEME_PALETTE_STORAGE_KEY)
        localStorage.removeItem(THEME_ACCENT_STORAGE_KEY)
      } else {
        localStorage.setItem(THEME_PALETTE_STORAGE_KEY, JSON.stringify(next))
        localStorage.setItem(THEME_ACCENT_STORAGE_KEY, next.primary)
      }
    } catch {
      // ignore storage quota errors
    }
  }

  if (changed) emitChange()
  return next
}

export function applyAccentColor(
  rawHex: unknown,
  options: { persistLocal?: boolean } = {},
): string {
  const hex = normalizeHexColor(rawHex) || DEFAULT_ACCENT_COLOR
  const palette = applyThemePalette({ primary: hex }, options)
  return palette.primary
}

export function syncThemeFromPluginConfigs(
  configs: Record<string, Record<string, unknown>> | undefined,
): void {
  const entry = configs?.[THEME_APPEARANCE_PLUGIN_ID]
  if (!entry || typeof entry !== 'object') return
  const primary = normalizeHexColor(entry.accentColor)
  const secondary = normalizeHexColor(entry.secondaryColor)
  const highlight = normalizeHexColor(entry.highlightColor)
  const surface = normalizeHexColor(entry.surfaceColor)
  if (!primary && !secondary && !highlight && !surface) return

  const resolved = normalizeThemePalette({ primary, secondary, highlight, surface })
  applyThemePalette(resolved, { persistLocal: true })
}

export const syncAccentFromPluginConfigs = syncThemeFromPluginConfigs

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function useThemePalette(): ThemePalette {
  return useSyncExternalStore(subscribe, getActiveThemePalette, () => DEFAULT_THEME_PALETTE)
}

export function useAccentColor(): string {
  return useSyncExternalStore(subscribe, getActiveAccentColor, () => DEFAULT_ACCENT_COLOR)
}
