import { memo, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ActionIcon,
  Collapse,
  ColorPicker,
  Group,
  Paper,
  SegmentedControl,
  Text,
  TextInput,
  Title,
  Tooltip,
  useMantineColorScheme,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconChevronDown,
  IconChevronRight,
  IconColorPicker,
  IconRotateClockwise,
} from '@tabler/icons-react'
import {
  DEFAULT_THEME_PALETTE,
  THEME_APPEARANCE_PLUGIN_ID,
  THEME_PALETTE_PRESETS,
  THEME_ROLE_DESCRIPTORS,
  applyThemePalette,
  deriveHarmonyFromPrimary,
  isDefaultThemePalette,
  normalizeHexColor,
  pickContrastInk,
  serializePaletteForPluginConfig,
  useThemePalette,
  type ThemeColorRole,
  type ThemePalette,
  type ThemeRoleDescriptor,
} from '../../themeAccent'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { useSettingsMode } from '../settings/settingsMode'
import './appearanceSettings.css'

interface RoleColorPickerCardProps {
  descriptor: ThemeRoleDescriptor
  value: string
  defaultValue: string
  onChange: (role: ThemeColorRole, hex: string, immediate: boolean) => void
}

const RoleColorPickerCard = memo(function RoleColorPickerCard({
  descriptor,
  value,
  defaultValue,
  onChange,
}: RoleColorPickerCardProps) {
  const [hexInput, setHexInput] = useState(value)
  const [hexError, setHexError] = useState('')

  useEffect(() => {
    setHexInput(value)
    setHexError('')
  }, [value])

  const handleHexInputChange = (nextRaw: string) => {
    setHexInput(nextRaw)
    const normalized = normalizeHexColor(nextRaw)
    if (normalized) {
      setHexError('')
      onChange(descriptor.role, normalized, false)
    } else {
      setHexError('请输入 3 或 6 位 HEX 色值')
    }
  }

  const handleHexCommit = () => {
    const normalized = normalizeHexColor(hexInput)
    if (!normalized) {
      setHexInput(value)
      setHexError('')
      return
    }
    onChange(descriptor.role, normalized, true)
  }

  const isRoleDefault = value === defaultValue

  return (
    <Paper withBorder radius="md" p="md" className="appearance-role-card" data-role={descriptor.role}>
      <Group justify="space-between" align="flex-start" gap="xs" wrap="nowrap" mb={8}>
        <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
          <span
            className="appearance-swatch-dot appearance-role-dot"
            style={{ backgroundColor: value }}
            aria-hidden="true"
          />
          <div style={{ minWidth: 0 }}>
            <Group gap={6} wrap="nowrap">
              <Text fw={650} size="sm">{descriptor.label}</Text>
              <code className="appearance-role-hex">{value.toUpperCase()}</code>
            </Group>
            <Text size="11px" c="dimmed" mt={2} lh={1.4}>
              {descriptor.description}
            </Text>
          </div>
        </Group>
        <Tooltip label={`重置${descriptor.label}为默认`}>
          <ActionIcon
            size="sm"
            variant="subtle"
            color="gray"
            aria-label={`重置${descriptor.label}`}
            disabled={isRoleDefault}
            onClick={() => onChange(descriptor.role, defaultValue, true)}
          >
            <IconRotateClockwise size={14} />
          </ActionIcon>
        </Tooltip>
      </Group>

      <div className="appearance-quick-swatches" role="group" aria-label={`${descriptor.label}快捷色`}>
        {descriptor.quickSwatches.map((hex) => {
          const selected = value === hex
          const ink = pickContrastInk(hex)
          return (
            <button
              key={hex}
              type="button"
              className={`appearance-mini-swatch${selected ? ' is-selected' : ''}`}
              style={{ backgroundColor: hex, color: ink }}
              aria-pressed={selected}
              aria-label={`设置${descriptor.label}为 ${hex}`}
              onClick={() => onChange(descriptor.role, hex, true)}
            >
              {selected && <IconCheck size={12} stroke={2.8} />}
            </button>
          )
        })}
      </div>

      <ColorPicker
        format="hex"
        fullWidth
        size="sm"
        value={value}
        onChange={(val) => onChange(descriptor.role, val, false)}
        onChangeEnd={(val) => onChange(descriptor.role, val, true)}
      />

      <Group gap="xs" align="flex-end" mt="xs" wrap="nowrap">
        <TextInput
          size="xs"
          aria-label={`${descriptor.label} HEX 色值`}
          value={hexInput}
          error={hexError || undefined}
          onChange={(e) => handleHexInputChange(e.currentTarget.value)}
          onBlur={handleHexCommit}
          onKeyDown={(e) => {
            if (e.key === 'Enter') handleHexCommit()
          }}
          style={{ flex: 1 }}
          styles={{ input: { fontFamily: 'var(--astr-session-mono-font, monospace)' } }}
        />
        <label className="appearance-native-picker-btn" title={`用系统取色器选择${descriptor.label}`}>
          <IconColorPicker size={14} aria-hidden="true" />
          <span>取色器</span>
          <input
            type="color"
            aria-label={`${descriptor.label}系统取色器`}
            value={value}
            onChange={(e) => onChange(descriptor.role, e.currentTarget.value, true)}
          />
        </label>
      </Group>
    </Paper>
  )
})

export function AppearanceSettingsPage({ embedded = false }: { embedded?: boolean } = {}) {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const palette = useThemePalette()
  const setPluginConfigPatch = usePluginSettingsStore((s) => s.setPluginConfigPatch)
  const { mode, managedByWorkbench } = useSettingsMode()

  const [customPickersOpen, setCustomPickersOpen] = useState(false)
  const showCustomPickers = !managedByWorkbench || mode === 'advanced' || customPickersOpen

  const saveTimerRef = useRef<number | null>(null)
  const dragRafRef = useRef<number | null>(null)
  const pendingDragPartialRef = useRef<Partial<ThemePalette> | null>(null)

  useEffect(() => () => {
    if (saveTimerRef.current !== null) window.clearTimeout(saveTimerRef.current)
    if (dragRafRef.current !== null) window.cancelAnimationFrame(dragRafRef.current)
  }, [])

  const persistPalette = useCallback((next: ThemePalette, immediate: boolean) => {
    if (saveTimerRef.current !== null) {
      window.clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    const commit = () => {
      applyThemePalette(next, { persistLocal: true })
      setPluginConfigPatch(THEME_APPEARANCE_PLUGIN_ID, serializePaletteForPluginConfig(next))
    }
    if (immediate) {
      commit()
    } else {
      saveTimerRef.current = window.setTimeout(() => {
        saveTimerRef.current = null
        commit()
      }, 280)
    }
  }, [setPluginConfigPatch])

  const handleApplyPalette = useCallback((nextPartial: Partial<ThemePalette>, immediate: boolean) => {
    if (immediate) {
      if (dragRafRef.current !== null) {
        window.cancelAnimationFrame(dragRafRef.current)
        dragRafRef.current = null
      }
      const merged = pendingDragPartialRef.current
        ? { ...pendingDragPartialRef.current, ...nextPartial }
        : nextPartial
      pendingDragPartialRef.current = null
      const applied = applyThemePalette(merged, { persistLocal: true })
      persistPalette(applied, true)
      return
    }
    pendingDragPartialRef.current = { ...pendingDragPartialRef.current, ...nextPartial }
    if (dragRafRef.current !== null) return
    const flushDrag = () => {
      dragRafRef.current = null
      const pending = pendingDragPartialRef.current
      pendingDragPartialRef.current = null
      if (!pending) return
      const applied = applyThemePalette(pending, { persistLocal: false })
      persistPalette(applied, false)
    }
    if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
      dragRafRef.current = window.requestAnimationFrame(flushDrag)
    } else {
      flushDrag()
    }
  }, [persistPalette])

  const handleRoleChange = useCallback((role: ThemeColorRole, rawHex: string, immediate: boolean) => {
    const normalized = normalizeHexColor(rawHex)
    if (!normalized) return
    handleApplyPalette({ [role]: normalized }, immediate)
  }, [handleApplyPalette])

  const handleAutoHarmony = () => {
    const harmonized = deriveHarmonyFromPrimary(palette.primary)
    handleApplyPalette(harmonized, true)
    notifications.show({ color: 'teal', message: '已根据当前主色调自动搭配次级、选中与表面氛围色' })
  }

  const handleReset = () => {
    handleApplyPalette({ ...DEFAULT_THEME_PALETTE }, true)
    notifications.show({ color: 'teal', message: '已恢复默认主题配色（星序紫韵）' })
  }

  const matchedPreset = useMemo(
    () =>
      THEME_PALETTE_PRESETS.find(
        (preset) =>
          preset.palette.primary === palette.primary &&
          preset.palette.secondary === palette.secondary &&
          preset.palette.highlight === palette.highlight &&
          preset.palette.surface === palette.surface,
      ),
    [palette],
  )
  const isDefault = isDefaultThemePalette(palette)

  return (
    <div className={embedded ? 'appearance-settings is-embedded' : 'appearance-settings'}>
      {!embedded && (
        <div className="settings-page-heading">
          <Title order={2}>外观与主题配色</Title>
          <Text c="dimmed" size="sm">
            管理明暗外观模式与界面主题配色
          </Text>
        </div>
      )}

      {/* Row: Light / Dark / Auto */}
      <div className="st-setting-row">
        <div className="st-setting-row-copy">
          <div className="st-setting-row-title">外观模式</div>
          <div className="st-setting-row-desc">切换浅色、深色或跟随系统自动适配</div>
        </div>
        <SegmentedControl
          size="xs"
          aria-label="外观模式"
          value={colorScheme}
          onChange={(value) => setColorScheme(value as 'light' | 'dark' | 'auto')}
          data={[
            { value: 'light', label: '浅色' },
            { value: 'dark', label: '深色' },
            { value: 'auto', label: '跟随系统' },
          ]}
        />
      </div>

      {/* Section: Minimalist Theme Preset Swatches */}
      <div className="appearance-theme-section">
        <div className="appearance-theme-head">
          <span className="appearance-theme-label">主题配色</span>
          <div style={{ display: 'inline-flex', alignItems: 'center', gap: 14 }}>
            {showCustomPickers && (
              <button type="button" className="st-text-link is-muted" onClick={handleAutoHarmony}>
                按主色搭配
              </button>
            )}
            <button
              type="button"
              className="st-text-link is-muted"
              disabled={isDefault}
              onClick={handleReset}
            >
              恢复默认配色
            </button>
          </div>
        </div>

        <div className="appearance-preset-grid" role="group" aria-label="精选配色方案">
          {THEME_PALETTE_PRESETS.map((preset) => {
            const selected = matchedPreset?.id === preset.id
            return (
              <button
                key={preset.id}
                type="button"
                className={`appearance-preset-btn${selected ? ' is-selected' : ''}`}
                aria-pressed={selected}
                aria-label={`应用主题配色 ${preset.name}`}
                onClick={() => handleApplyPalette(preset.palette, true)}
              >
                <span className="appearance-preset-dots" aria-hidden="true">
                  <span
                    className="appearance-swatch-dot"
                    style={{ backgroundColor: preset.palette.primary }}
                  />
                  <span
                    className="appearance-swatch-dot is-sub"
                    style={{ backgroundColor: preset.palette.secondary }}
                  />
                  <span
                    className="appearance-swatch-dot is-sub"
                    style={{ backgroundColor: preset.palette.surface }}
                  />
                </span>
                <span className="appearance-preset-name">{preset.name}</span>
                {selected && <IconCheck size={14} stroke={2.4} className="appearance-preset-check" aria-hidden="true" />}
              </button>
            )
          })}
        </div>
      </div>

      {/* Disclosure Row: 4-Role Fine-Tuning Pickers */}
      <button
        type="button"
        className="st-disclosure-row"
        aria-expanded={showCustomPickers}
        onClick={() => setCustomPickersOpen((v) => !v)}
      >
        <div className="st-setting-row-copy">
          <div className="st-setting-row-title">自定义四层级细调配色</div>
          <div className="st-setting-row-desc">按主色、次级、高亮与氛围单独微调</div>
        </div>
        {showCustomPickers ? (
          <IconChevronDown size={16} style={{ color: 'var(--st-faint)', flexShrink: 0 }} />
        ) : (
          <IconChevronRight size={16} style={{ color: 'var(--st-faint)', flexShrink: 0 }} />
        )}
      </button>

      <Collapse expanded={showCustomPickers}>
        <div className="appearance-role-grid" style={{ paddingTop: 12 }}>
          {THEME_ROLE_DESCRIPTORS.map((descriptor) => (
            <RoleColorPickerCard
              key={descriptor.role}
              descriptor={descriptor}
              value={palette[descriptor.role]}
              defaultValue={DEFAULT_THEME_PALETTE[descriptor.role]}
              onChange={handleRoleChange}
            />
          ))}
        </div>
      </Collapse>
    </div>
  )
}
