import { useEffect, useMemo, useRef, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  ColorPicker,
  Group,
  Paper,
  SegmentedControl,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
  Tooltip,
  useComputedColorScheme,
  useMantineColorScheme,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconColorPicker,
  IconDeviceDesktop,
  IconMoon,
  IconPalette,
  IconRotateClockwise,
  IconSend,
  IconSparkles,
  IconSun,
} from '@tabler/icons-react'
import {
  DEFAULT_THEME_PALETTE,
  THEME_APPEARANCE_PLUGIN_ID,
  THEME_PALETTE_PRESETS,
  THEME_ROLE_DESCRIPTORS,
  applyThemePalette,
  contrastRatio,
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
import './appearanceSettings.css'

interface RoleColorPickerCardProps {
  descriptor: ThemeRoleDescriptor
  value: string
  defaultValue: string
  onChange: (role: ThemeColorRole, hex: string, immediate: boolean) => void
}

function RoleColorPickerCard({
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
}

export function AppearanceSettingsPage() {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const computedScheme = useComputedColorScheme('light')
  const palette = useThemePalette()
  const setPluginConfigPatch = usePluginSettingsStore((s) => s.setPluginConfigPatch)

  const [previewSwitch, setPreviewSwitch] = useState(true)
  const saveTimerRef = useRef<number | null>(null)

  useEffect(() => () => {
    if (saveTimerRef.current !== null) window.clearTimeout(saveTimerRef.current)
  }, [])

  const persistPalette = (next: ThemePalette, immediate: boolean) => {
    if (saveTimerRef.current !== null) {
      window.clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    const commit = () => {
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
  }

  const handleApplyPalette = (nextPartial: Partial<ThemePalette>, immediate: boolean) => {
    const applied = applyThemePalette(nextPartial)
    persistPalette(applied, immediate)
  }

  const handleRoleChange = (role: ThemeColorRole, rawHex: string, immediate: boolean) => {
    const normalized = normalizeHexColor(rawHex)
    if (!normalized) return
    handleApplyPalette({ [role]: normalized }, immediate)
  }

  const handleAutoHarmony = () => {
    const harmonized = deriveHarmonyFromPrimary(palette.primary)
    handleApplyPalette(harmonized, true)
    notifications.show({ color: 'teal', message: '已根据当前主色调自动搭配次级、选中与表面氛围色' })
  }

  const handleReset = () => {
    handleApplyPalette({ ...DEFAULT_THEME_PALETTE }, true)
    notifications.show({ color: 'teal', message: '已恢复默认主题配色（星序紫韵）' })
  }

  const primaryInk = useMemo(() => pickContrastInk(palette.primary), [palette.primary])
  const primaryRatio = useMemo(() => contrastRatio(palette.primary, primaryInk), [palette.primary, primaryInk])
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
    <div className="appearance-settings">
      <div className="settings-page-heading">
        <Title order={2}>外观与主题配色</Title>
        <Text c="dimmed" size="sm">
          自定义星序的明暗模式与多层级主题色，支持组合预设或分别调节主色调、次级色、选中高亮与表面氛围色，全界面实时生效并跨端同步。
        </Text>
      </div>

      <Stack gap="md">
        <Card withBorder radius="md" p="lg">
          <Group justify="space-between" align="center" wrap="wrap" gap="md">
            <div>
              <Text fw={600} size="sm">外观模式</Text>
              <Text size="xs" c="dimmed" mt={2}>
                切换浅色、深色或自动跟随系统设置（当前：{computedScheme === 'dark' ? '深色' : '浅色'}）
              </Text>
            </div>
            <SegmentedControl
              aria-label="外观模式"
              value={colorScheme}
              onChange={(value) => setColorScheme(value as 'light' | 'dark' | 'auto')}
              data={[
                {
                  value: 'light',
                  label: (
                    <Group gap={6} wrap="nowrap">
                      <IconSun size={14} aria-hidden="true" />
                      <span>浅色</span>
                    </Group>
                  ),
                },
                {
                  value: 'dark',
                  label: (
                    <Group gap={6} wrap="nowrap">
                      <IconMoon size={14} aria-hidden="true" />
                      <span>深色</span>
                    </Group>
                  ),
                },
                {
                  value: 'auto',
                  label: (
                    <Group gap={6} wrap="nowrap">
                      <IconDeviceDesktop size={14} aria-hidden="true" />
                      <span>跟随系统</span>
                    </Group>
                  ),
                },
              ]}
            />
          </Group>
        </Card>

        <Card withBorder radius="md" p="lg">
          <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm" mb="md">
            <Group gap="sm" wrap="nowrap">
              <div className="appearance-header-icon" aria-hidden="true">
                <IconPalette size={20} />
              </div>
              <div>
                <Group gap="xs" wrap="wrap">
                  <Text fw={600} size="sm">主题色板与自由搭配</Text>
                  <Badge size="xs" variant="light" color="violet">
                    {matchedPreset ? `预设 · ${matchedPreset.name}` : '自定义搭配'}
                  </Badge>
                </Group>
                <Text size="xs" c="dimmed" mt={2}>
                  选择整套配色方案，或通过下方 4 个独立取色盘自由组合主色、次级、选中高亮与表面氛围。
                </Text>
              </div>
            </Group>
            <Group gap="xs" wrap="wrap">
              <Button
                size="xs"
                variant="light"
                color="violet"
                leftSection={<IconSparkles size={14} />}
                onClick={handleAutoHarmony}
              >
                按主色一键搭配
              </Button>
              <Button
                size="xs"
                variant="subtle"
                color="gray"
                leftSection={<IconRotateClockwise size={14} />}
                disabled={isDefault}
                onClick={handleReset}
              >
                恢复默认配色
              </Button>
            </Group>
          </Group>

          <Text size="xs" fw={600} c="dimmed" mb={8}>
            精选配色方案
          </Text>
          <div className="appearance-preset-grid" role="group" aria-label="精选配色方案">
            {THEME_PALETTE_PRESETS.map((preset) => {
              const selected = matchedPreset?.id === preset.id
              const checkInk = pickContrastInk(preset.palette.primary)
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
                      style={{ backgroundColor: preset.palette.primary, color: checkInk }}
                    >
                      {selected && <IconCheck size={12} stroke={2.8} />}
                    </span>
                    <span
                      className="appearance-swatch-dot is-sub"
                      style={{ backgroundColor: preset.palette.secondary }}
                    />
                    <span
                      className="appearance-swatch-dot is-sub"
                      style={{ backgroundColor: preset.palette.highlight }}
                    />
                    <span
                      className="appearance-swatch-dot is-sub"
                      style={{ backgroundColor: preset.palette.surface }}
                    />
                  </span>
                  <span className="appearance-preset-name">{preset.name}</span>
                </button>
              )
            })}
          </div>

          <Paper withBorder radius="md" p="md" className="appearance-preview-panel" mb="md">
            <Group justify="space-between" align="center" mb="sm" wrap="wrap" gap="xs">
              <Text size="xs" fw={600} c="dimmed">全界面实时效果预览</Text>
              <Text size="xs" c="dimmed">
                主按钮对比度 {primaryRatio.toFixed(1)}:1 {primaryRatio >= 4.5 ? '· WCAG AA' : ''}
              </Text>
            </Group>
            <div className="appearance-preview-stage">
              <div className="appearance-preview-sidebar">
                <div className="appearance-preview-rail">
                  <span className="appearance-preview-rail-btn is-active">会话</span>
                  <span className="appearance-preview-rail-btn">待办</span>
                </div>
                <div className="appearance-preview-list">
                  <div className="appearance-preview-pills">
                    <span className="appearance-preview-pill is-active">全部</span>
                    <span className="appearance-preview-pill">运行中</span>
                  </div>
                  <div className="appearance-preview-row is-active">
                    <span className="appearance-preview-row-dot" aria-hidden="true" />
                    当前激活会话
                  </div>
                  <div className="appearance-preview-row">常规会话条目</div>
                </div>
              </div>

              <div className="appearance-preview-controls">
                <Group gap="xs" wrap="wrap">
                  <Button size="xs" color="violet">主要按钮</Button>
                  <span className="appearance-preview-send-btn">
                    <IconSend size={13} aria-hidden="true" />
                    发送渐变
                  </span>
                  <span className="appearance-preview-secondary-tag">次级标签</span>
                  <Switch
                    size="sm"
                    color="indigo"
                    label="开关控件"
                    checked={previewSwitch}
                    onChange={(e) => setPreviewSwitch(e.currentTarget.checked)}
                  />
                </Group>

                <div className="appearance-preview-focus-box">
                  <Text size="xs" fw={550}>选中高亮与氛围底色联动</Text>
                  <Text size="11px" c="dimmed" mt={2}>
                    主色 <code className="appearance-preview-code">{palette.primary}</code> ·
                    次级 <code className="appearance-preview-code is-secondary">{palette.secondary}</code> ·
                    选中 <code className="appearance-preview-code">{palette.highlight}</code> ·
                    氛围 <code className="appearance-preview-code">{palette.surface}</code>
                  </Text>
                </div>
              </div>
            </div>
          </Paper>

          <Text size="xs" fw={600} c="dimmed" mb={8}>
            独立取色盘（按层级自由搭配）
          </Text>
          <div className="appearance-role-grid">
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
        </Card>
      </Stack>
    </div>
  )
}
