import { useSyncExternalStore } from 'react'

export const DEFAULT_ACCENT_COLOR = '#7540bd'
export const THEME_ACCENT_STORAGE_KEY = 'astrorder:theme-accent'
export const THEME_APPEARANCE_PLUGIN_ID = 'theme-appearance'

export interface AccentPreset {
  name: string
  hex: string
}

export const ACCENT_PRESETS: readonly AccentPreset[] = [
  { name: '星序紫', hex: '#7540bd' },
  { name: '星海蓝', hex: '#2563eb' },
  { name: '靛青蓝', hex: '#4f46e5' },
  { name: '碧波青', hex: '#0891b2' },
  { name: '翠微绿', hex: '#059669' },
  { name: '森林绿', hex: '#16a34a' },
  { name: '琥珀金', hex: '#d97706' },
  { name: '珊瑚橙', hex: '#ea580c' },
  { name: '绯红玫', hex: '#e11d48' },
  { name: '曜石岩', hex: '#475569' },
]

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

export function readStoredAccentColor(): string {
  if (typeof localStorage === 'undefined') return DEFAULT_ACCENT_COLOR
  try {
    return normalizeHexColor(localStorage.getItem(THEME_ACCENT_STORAGE_KEY)) || DEFAULT_ACCENT_COLOR
  } catch {
    return DEFAULT_ACCENT_COLOR
  }
}

let currentAccent = readStoredAccentColor()
const listeners = new Set<() => void>()

function emitChange() {
  for (const listener of listeners) listener()
}

export function getActiveAccentColor(): string {
  return currentAccent
}

export function applyAccentColor(
  rawHex: unknown,
  options: { persistLocal?: boolean } = {},
): string {
  const hex = normalizeHexColor(rawHex) || DEFAULT_ACCENT_COLOR
  const changed = currentAccent !== hex
  currentAccent = hex

  if (typeof document !== 'undefined') {
    const root = document.documentElement
    root.style.setProperty('--astr-theme-seed', hex)
    root.style.setProperty('--astr-accent-contrast-fallback', pickContrastInk(hex))
  }

  if (options.persistLocal !== false && typeof localStorage !== 'undefined') {
    try {
      if (hex === DEFAULT_ACCENT_COLOR) {
        localStorage.removeItem(THEME_ACCENT_STORAGE_KEY)
      } else {
        localStorage.setItem(THEME_ACCENT_STORAGE_KEY, hex)
      }
    } catch {
      // ignore storage quota errors
    }
  }

  if (changed) emitChange()
  return hex
}

export function syncAccentFromPluginConfigs(
  configs: Record<string, Record<string, unknown>> | undefined,
): void {
  const saved = configs?.[THEME_APPEARANCE_PLUGIN_ID]?.accentColor
  if (typeof saved === 'string') {
    const normalized = normalizeHexColor(saved)
    if (normalized) {
      applyAccentColor(normalized, { persistLocal: true })
    }
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function useAccentColor(): string {
  return useSyncExternalStore(subscribe, getActiveAccentColor, () => DEFAULT_ACCENT_COLOR)
}
