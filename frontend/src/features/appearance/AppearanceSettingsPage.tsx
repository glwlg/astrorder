import { useEffect, useMemo, useRef, useState } from 'react'
import {
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
  IconSun,
} from '@tabler/icons-react'
import {
  ACCENT_PRESETS,
  DEFAULT_ACCENT_COLOR,
  THEME_APPEARANCE_PLUGIN_ID,
  applyAccentColor,
  contrastRatio,
  normalizeHexColor,
  pickContrastInk,
  useAccentColor,
} from '../../themeAccent'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import './appearanceSettings.css'

export function AppearanceSettingsPage() {
  const { colorScheme, setColorScheme } = useMantineColorScheme()
  const computedScheme = useComputedColorScheme('light')
  const activeAccent = useAccentColor()
  const setPluginConfig = usePluginSettingsStore((s) => s.setPluginConfig)

  const [hexInput, setHexInput] = useState(activeAccent)
  const [hexError, setHexError] = useState('')
  const [previewSwitch, setPreviewSwitch] = useState(true)
  const saveTimerRef = useRef<number | null>(null)

  useEffect(() => {
    setHexInput(activeAccent)
    setHexError('')
  }, [activeAccent])

  useEffect(() => () => {
    if (saveTimerRef.current !== null) window.clearTimeout(saveTimerRef.current)
  }, [])

  const persistAccent = (hex: string, immediate: boolean) => {
    if (saveTimerRef.current !== null) {
      window.clearTimeout(saveTimerRef.current)
      saveTimerRef.current = null
    }
    const commit = () => {
      setPluginConfig(THEME_APPEARANCE_PLUGIN_ID, 'accentColor', hex)
    }
    if (immediate) {
      commit()
    } else {
      saveTimerRef.current = window.setTimeout(() => {
        saveTimerRef.current = null
        commit()
      }, 300)
    }
  }

  const handleSelectAccent = (rawHex: string, immediate: boolean) => {
    const normalized = normalizeHexColor(rawHex)
    if (!normalized) return
    applyAccentColor(normalized)
    setHexInput(normalized)
    setHexError('')
    persistAccent(normalized, immediate)
  }

  const handleHexInputChange = (value: string) => {
    setHexInput(value)
    const normalized = normalizeHexColor(value)
    if (normalized) {
      setHexError('')
      applyAccentColor(normalized)
      persistAccent(normalized, false)
    } else {
      setHexError('请输入 3 位或 6 位 HEX 色值，例如 #2563eb')
    }
  }

  const handleHexInputCommit = () => {
    const normalized = normalizeHexColor(hexInput)
    if (!normalized) {
      setHexInput(activeAccent)
      setHexError('')
      return
    }
    handleSelectAccent(normalized, true)
  }

  const handleReset = () => {
    handleSelectAccent(DEFAULT_ACCENT_COLOR, true)
    notifications.show({ color: 'teal', message: '已恢复默认主题色（星序紫）' })
  }

  const ink = useMemo(() => pickContrastInk(activeAccent), [activeAccent])
  const ratio = useMemo(() => contrastRatio(activeAccent, ink), [activeAccent, ink])
  const matchedPreset = useMemo(
    () => ACCENT_PRESETS.find((preset) => preset.hex === activeAccent),
    [activeAccent],
  )
  const isDefault = activeAccent === DEFAULT_ACCENT_COLOR

  return (
    <div className="appearance-settings">
      <div className="settings-page-heading">
        <Title order={2}>外观与主题色</Title>
        <Text c="dimmed" size="sm">
          自定义星序的明暗模式与全局主题强调色，更改会实时生效并自动同步保存。
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
                <Group gap="xs">
                  <Text fw={600} size="sm">主题强调色</Text>
                  <Badge size="xs" variant="light" color="violet">
                    {matchedPreset ? `${matchedPreset.name} · ${activeAccent.toUpperCase()}` : `自定义 · ${activeAccent.toUpperCase()}`}
                  </Badge>
                </Group>
                <Text size="xs" c="dimmed" mt={2}>
                  控制按钮、开关、侧栏选中项、输入框聚焦边框、文本选区与原生控件强调色。
                </Text>
              </div>
            </Group>
            <Button
              size="xs"
              variant="subtle"
              color="gray"
              leftSection={<IconRotateClockwise size={14} />}
              disabled={isDefault}
              onClick={handleReset}
            >
              恢复默认紫色
            </Button>
          </Group>

          <Text size="xs" fw={600} c="dimmed" mb={8}>
            精选预设
          </Text>
          <div className="appearance-preset-grid" role="group" aria-label="预设主题色">
            {ACCENT_PRESETS.map((preset) => {
              const selected = activeAccent === preset.hex
              const checkInk = pickContrastInk(preset.hex)
              return (
                <button
                  key={preset.hex}
                  type="button"
                  className={`appearance-preset-btn${selected ? ' is-selected' : ''}`}
                  aria-pressed={selected}
                  aria-label={`选择主题色 ${preset.name} ${preset.hex}`}
                  onClick={() => handleSelectAccent(preset.hex, true)}
                >
                  <span
                    className="appearance-swatch-dot"
                    style={{ backgroundColor: preset.hex, color: checkInk }}
                    aria-hidden="true"
                  >
                    {selected && <IconCheck size={14} stroke={2.6} />}
                  </span>
                  <span className="appearance-preset-name">{preset.name}</span>
                </button>
              )
            })}
          </div>

          <div className="appearance-picker-grid">
            <div className="appearance-wheel-panel">
              <Text size="xs" fw={600} c="dimmed" mb={8}>
                自由调色盘
              </Text>
              <ColorPicker
                format="hex"
                fullWidth
                size="md"
                value={activeAccent}
                onChange={(val) => handleSelectAccent(val, false)}
                onChangeEnd={(val) => handleSelectAccent(val, true)}
              />
              <Group gap="sm" align="flex-end" mt="sm" wrap="nowrap">
                <TextInput
                  size="xs"
                  label="HEX 色值"
                  aria-label="HEX 色值"
                  value={hexInput}
                  error={hexError || undefined}
                  onChange={(e) => handleHexInputChange(e.currentTarget.value)}
                  onBlur={handleHexInputCommit}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') handleHexInputCommit()
                  }}
                  style={{ flex: 1 }}
                  styles={{ input: { fontFamily: 'var(--astr-session-mono-font, monospace)' } }}
                />
                <label className="appearance-native-picker-btn" title="打开系统取色盘">
                  <IconColorPicker size={15} aria-hidden="true" />
                  <span>系统取色器</span>
                  <input
                    type="color"
                    aria-label="系统取色器"
                    value={activeAccent}
                    onChange={(e) => handleSelectAccent(e.currentTarget.value, true)}
                  />
                </label>
              </Group>
            </div>

            <Paper withBorder radius="md" p="md" className="appearance-preview-panel">
              <Group justify="space-between" align="center" mb="sm">
                <Text size="xs" fw={600} c="dimmed">实时预览</Text>
                <Text size="xs" c="dimmed">
                  按钮文字对比度 {ratio.toFixed(1)}:1 {ratio >= 4.5 ? '· WCAG AA' : ''}
                </Text>
              </Group>
              <Stack gap="sm">
                <div className="appearance-preview-nav-row">
                  <span className="appearance-preview-nav-item is-active">
                    <span className="appearance-preview-nav-dot" aria-hidden="true" />
                    当前激活导航
                  </span>
                  <span className="appearance-preview-nav-item">普通导航</span>
                </div>

                <Group gap="xs" wrap="wrap">
                  <Button size="xs" color="violet">主要按钮</Button>
                  <Button size="xs" variant="light" color="violet">次级按钮</Button>
                  <Switch
                    size="sm"
                    color="indigo"
                    label="开关控件"
                    checked={previewSwitch}
                    onChange={(e) => setPreviewSwitch(e.currentTarget.checked)}
                  />
                </Group>

                <div className="appearance-preview-focus-box">
                  <Text size="xs" fw={500}>聚焦边框与选中背景预览</Text>
                  <Text size="11px" c="dimmed" mt={2}>
                    当前使用 <code className="appearance-preview-code">--astr-theme-seed: {activeAccent}</code>，深浅模式自动衍生层次。
                  </Text>
                </div>

                <div className="appearance-token-strip" aria-label="衍生色阶预览">
                  <span className="appearance-token-chip">
                    <i className="appearance-swatch-dot" style={{ background: 'var(--astr-accent)' }} aria-hidden="true" />
                    主色
                  </span>
                  <span className="appearance-token-chip">
                    <i className="appearance-swatch-dot" style={{ background: 'var(--astr-accent-strong)' }} aria-hidden="true" />
                    悬停加深
                  </span>
                  <span className="appearance-token-chip">
                    <i className="appearance-swatch-dot" style={{ background: 'var(--astr-accent-soft)' }} aria-hidden="true" />
                    浅色背景
                  </span>
                  <span className="appearance-token-chip">
                    <i className="appearance-swatch-dot" style={{ background: 'var(--astr-selection-bg)' }} aria-hidden="true" />
                    文本选区
                  </span>
                </div>
              </Stack>
            </Paper>
          </div>
        </Card>
      </Stack>
    </div>
  )
}
