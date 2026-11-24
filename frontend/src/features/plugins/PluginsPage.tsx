import { useEffect, useMemo, useRef, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Collapse,
  Divider,
  Group,
  Modal,
  Paper,
  Progress,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAdjustments,
  IconArrowUpRight,
  IconChevronDown,
  IconChevronUp,
  IconInfoCircle,
  IconMicrophone,
  IconPuzzle,
  IconSearch,
  IconSettings,
  IconX,
} from '@tabler/icons-react'
import { api, type BrowserEngine, type LocalVoicePluginStatus } from '../../api/client'
import { artifactViewerRegistry } from '../sidecar/registry'
import type { ArtifactViewer, PluginCategory, ViewerConfigOption } from '../sidecar/types'
import {
  BROWSER_VIEWER_PLUGIN_ID,
  BUILTIN_SYSTEM_PLUGINS,
  VOICE_INPUT_PLUGIN_ID,
  type SystemPluginDescriptor,
} from './builtinPlugins'
import { PLUGIN_CATEGORIES, resolveViewerCategory, type PluginCategoryMeta, type PluginTabId } from './pluginCategories'
import { usePluginSettingsStore } from './pluginSettingsStore'
import './PluginsPage.css'

const BROWSER_ENGINE_LABELS: Record<BrowserEngine, string> = {
  browser_use: 'Browser Use',
  jev_ultrafast: 'System One',
}

function ConfigOptionControls({
  pluginId,
  options,
  pluginConfigMap,
  setPluginConfig,
}: {
  pluginId: string
  options: ViewerConfigOption[]
  pluginConfigMap?: Record<string, unknown>
  setPluginConfig: (pluginId: string, key: string, value: unknown) => void
}) {
  const readValue = (key: string, fallback: unknown) =>
    pluginConfigMap && pluginConfigMap[key] !== undefined ? pluginConfigMap[key] : fallback

  return (
    <>
      {options.map((opt) => {
        const currentVal = readValue(opt.key, opt.defaultValue)

        if (opt.type === 'boolean') {
          return (
            <Group key={opt.key} justify="space-between">
              <div>
                <Text size="xs" fw={500}>
                  {opt.label}
                </Text>
                {opt.description && (
                  <Text size="11px" c="dimmed">
                    {opt.description}
                  </Text>
                )}
              </div>
              <Switch
                size="xs"
                checked={Boolean(currentVal)}
                onChange={(e) => setPluginConfig(pluginId, opt.key, e.currentTarget.checked)}
              />
            </Group>
          )
        }

        if (opt.type === 'select' && opt.options) {
          return (
            <div key={opt.key}>
              <Text size="xs" fw={500} mb={2}>
                {opt.label}
              </Text>
              {opt.description && (
                <Text size="11px" c="dimmed" mb={4}>
                  {opt.description}
                </Text>
              )}
              <Select
                size="xs"
                allowDeselect={false}
                aria-label={opt.label}
                data={opt.options}
                value={String(currentVal)}
                onChange={(val) => {
                  if (val !== null) setPluginConfig(pluginId, opt.key, val)
                }}
              />
            </div>
          )
        }

        return (
          <TextInput
            key={opt.key}
            size="xs"
            label={opt.label}
            description={opt.description}
            value={String(currentVal ?? '')}
            onChange={(e) => setPluginConfig(pluginId, opt.key, e.currentTarget.value)}
          />
        )
      })}
    </>
  )
}

function VoiceInputPluginCard() {
  const isEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(VOICE_INPUT_PLUGIN_ID))
  const togglePlugin = usePluginSettingsStore((s) => s.togglePlugin)
  const setPluginConfig = usePluginSettingsStore((s) => s.setPluginConfig)
  const provider = usePluginSettingsStore((s) =>
    s.getPluginConfig<string>(VOICE_INPUT_PLUGIN_ID, 'provider', 'local-r2t2'),
  )

  const [expanded, setExpanded] = useState(false)
  const [status, setStatus] = useState<LocalVoicePluginStatus | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let active = true
    const refresh = async () => {
      try {
        const next = await api.getLocalVoicePlugin()
        if (!active) return
        setStatus(next)
        setLoading(false)
      } catch {
        if (active) setLoading(false)
      }
    }
    void refresh()
    const timer = window.setInterval(() => { void refresh() }, 1500)
    return () => { active = false; window.clearInterval(timer) }
  }, [])

  const toggleLocalRuntime = async () => {
    if (!status || loading) return
    setLoading(true)
    try {
      const next = await api.setLocalVoicePlugin(!status.enabled)
      setStatus(next)
      setLoading(false)
    } catch {
      setLoading(false)
    }
  }

  const isLocalR2T2 = provider !== 'cloud-whisper'
  const running = status?.state === 'running'
  const stateLabel = status?.state === 'installing' ? '正在下载模型'
    : status?.state === 'starting' ? '正在加载模型'
      : status?.state === 'stopping' ? '正在卸载模型'
        : status?.state === 'running' ? '流式听写已启用'
          : status?.state === 'error' ? '启动失败'
            : '已停用 · 使用停止后转写'
  const providerBadge = isLocalR2T2 ? 'R2T2 本地流式' : '云端 Whisper'

  return (
    <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Group gap="sm" wrap="nowrap" align="flex-start">
          <div
            style={{
              padding: 8,
              borderRadius: 8,
              background: isEnabled && running && isLocalR2T2
                ? 'rgba(34, 197, 94, 0.12)'
                : isEnabled
                  ? 'color-mix(in oklab, var(--astr-accent) 12%, transparent)'
                  : 'var(--astr-surface-muted)',
              color: isEnabled && running && isLocalR2T2
                ? '#16a34a'
                : isEnabled
                  ? 'var(--astr-indigo)'
                  : 'var(--astr-muted)',
              display: 'flex',
            }}
          >
            <IconMicrophone size={24} />
          </div>
          <div>
            <Group gap="xs" wrap="wrap">
              <Text fw={600} size="sm">语音输入与听写</Text>
              <Badge size="xs" variant="light" color="teal">当前实现: {providerBadge}</Badge>
            </Group>
            <Text size="xs" c="dimmed" mt={4}>
              为对话与群聊输入框提供语音听写能力，支持在本地流式模型与云端 Whisper 接口间切换；停用后隐藏输入框麦克风按钮。
            </Text>
            {isEnabled && isLocalR2T2 && (
              <Group justify="space-between" align="center" gap="xs" mt={8} wrap="nowrap">
                <Text size="xs" c={status?.state === 'error' ? 'red' : 'dimmed'}>
                  R2T2 本地引擎：{status?.error || stateLabel}
                </Text>
                <Switch
                  size="xs"
                  color="green"
                  aria-label="启用 R2T2 本地流式听写"
                  checked={Boolean(status?.enabled)}
                  disabled={!status || loading}
                  onChange={() => { void toggleLocalRuntime() }}
                />
              </Group>
            )}
            {isEnabled && isLocalR2T2 && status?.state === 'installing' && (
              <Progress value={(status.progress || 0) * 100} size="xs" mt={8} />
            )}
          </div>
        </Group>
        <Switch
          aria-label="启用或禁用 语音输入与听写"
          size="sm"
          color="indigo"
          checked={isEnabled}
          onChange={(e) => togglePlugin(VOICE_INPUT_PLUGIN_ID, e.currentTarget.checked)}
        />
      </Group>

      <Divider my="sm" />
      <Group justify="space-between" align="center">
        <Button
          variant="subtle"
          size="compact-xs"
          color="gray"
          leftSection={<IconAdjustments size={13} />}
          rightSection={expanded ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
          onClick={() => setExpanded(!expanded)}
        >
          参数配置 (1)
        </Button>
        <Text size="xs" c="dimmed">
          {isEnabled ? '生效中' : '已停用'}
        </Text>
      </Group>

      <Collapse expanded={expanded}>
        <Stack gap="xs" mt="xs" p="xs" style={{ background: 'var(--astr-surface-muted)', borderRadius: 6 }}>
          <div>
            <Text size="xs" fw={500} mb={2}>工具实现</Text>
            <Text size="11px" c="dimmed" mb={4}>
              选择语音转文字引擎实现：本地 R2T2 流式模型实时回填，云端 Whisper 不占用本机显存
            </Text>
            <Select
              size="xs"
              allowDeselect={false}
              aria-label="语音听写实现"
              value={isLocalR2T2 ? 'local-r2t2' : 'cloud-whisper'}
              onChange={(val) => {
                if (val !== null) setPluginConfig(VOICE_INPUT_PLUGIN_ID, 'provider', val)
              }}
              data={[
                { value: 'local-r2t2', label: 'R2T2 本地流式模型' },
                { value: 'cloud-whisper', label: '云端 Whisper 转写' },
              ]}
            />
          </div>
          {!isLocalR2T2 && (
            <Text size="11px" c="dimmed">
              录音结束后提交至后端 /api/v1/audio/transcriptions，不启动本机 GPU 语音模型。
            </Text>
          )}
        </Stack>
      </Collapse>
    </Card>
  )
}

function SystemPluginCard({ descriptor }: { descriptor: SystemPluginDescriptor }) {
  const isEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(descriptor.id))
  const togglePlugin = usePluginSettingsStore((s) => s.togglePlugin)
  const setPluginConfig = usePluginSettingsStore((s) => s.setPluginConfig)
  const pluginConfigMap = usePluginSettingsStore((s) => s.pluginConfigs[descriptor.id])

  const [expanded, setExpanded] = useState(false)
  const Icon = descriptor.icon

  const readOptionValue = (key: string, fallback: unknown) =>
    pluginConfigMap && pluginConfigMap[key] !== undefined ? pluginConfigMap[key] : fallback

  const defaultByKey = useMemo(() => {
    const map: Record<string, unknown> = {}
    for (const opt of descriptor.configOptions || []) map[opt.key] = opt.defaultValue
    return map
  }, [descriptor.configOptions])

  const visibleOptions = (descriptor.configOptions || []).filter((opt) => {
    if (!opt.visibleWhen) return true
    const current = readOptionValue(opt.visibleWhen.key, defaultByKey[opt.visibleWhen.key])
    return current === opt.visibleWhen.value
  })
  const hasOptions = visibleOptions.length > 0

  const activeProviderLabel = (() => {
    if (!descriptor.providerConfigKey) return descriptor.implementationName || ''
    const providerOpt = (descriptor.configOptions || []).find((opt) => opt.key === descriptor.providerConfigKey)
    if (!providerOpt) return descriptor.implementationName || ''
    const current = String(readOptionValue(providerOpt.key, providerOpt.defaultValue) ?? '')
    return providerOpt.options?.find((choice) => choice.value === current)?.label || current
  })()

  return (
    <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Group gap="sm" wrap="nowrap" align="flex-start">
          <div
            style={{
              padding: 8,
              borderRadius: 8,
              background: isEnabled ? 'color-mix(in oklab, var(--astr-accent) 12%, transparent)' : 'var(--astr-surface-muted)',
              color: isEnabled ? 'var(--astr-indigo)' : 'var(--astr-muted)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <Icon size={24} />
          </div>
          <div>
            <Group gap="xs" align="center" wrap="wrap">
              <Text fw={600} size="sm">{descriptor.title}</Text>
              {descriptor.capabilitySlot ? (
                <Badge size="xs" variant="light" color="teal">
                  {activeProviderLabel ? `当前实现: ${activeProviderLabel}` : `${descriptor.capabilityLabel || descriptor.capabilitySlot}`}
                </Badge>
              ) : descriptor.badgeLabel ? (
                <Badge size="xs" variant="light" color="indigo">{descriptor.badgeLabel}</Badge>
              ) : null}
            </Group>
            <Text size="xs" c="dimmed" mt={4}>
              {descriptor.description}
            </Text>
            {isEnabled && (descriptor.settingsAction || descriptor.routeAction) && (
              <Group gap="xs" mt={8}>
                {descriptor.settingsAction && (
                  <Button
                    size="compact-xs"
                    variant="light"
                    color="violet"
                    leftSection={<IconSettings size={13} />}
                    onClick={() => window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: descriptor.settingsAction!.tab } }))}
                  >
                    {descriptor.settingsAction.label}
                  </Button>
                )}
                {descriptor.routeAction && (
                  <Button
                    size="compact-xs"
                    variant="subtle"
                    color="indigo"
                    rightSection={<IconArrowUpRight size={13} />}
                    onClick={() => window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: descriptor.routeAction!.to } }))}
                  >
                    {descriptor.routeAction.label}
                  </Button>
                )}
              </Group>
            )}
          </div>
        </Group>
        <Switch
          aria-label={`启用 ${descriptor.title}`}
          size="sm"
          color="indigo"
          checked={isEnabled}
          onChange={(e) => togglePlugin(descriptor.id, e.currentTarget.checked)}
        />
      </Group>

      {hasOptions && (
        <>
          <Divider my="sm" />
          <Group justify="space-between" align="center">
            <Button
              variant="subtle"
              size="compact-xs"
              color="gray"
              leftSection={<IconAdjustments size={13} />}
              rightSection={expanded ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
              onClick={() => setExpanded(!expanded)}
            >
              参数配置 ({visibleOptions.length})
            </Button>
            <Text size="xs" c="dimmed">
              {isEnabled ? '生效中' : '已停用'}
            </Text>
          </Group>

          <Collapse expanded={expanded}>
            <Stack gap="xs" mt="xs" p="xs" style={{ background: 'var(--astr-surface-muted)', borderRadius: 6 }}>
              <ConfigOptionControls
                pluginId={descriptor.id}
                options={visibleOptions}
                pluginConfigMap={pluginConfigMap}
                setPluginConfig={setPluginConfig}
              />
            </Stack>
          </Collapse>
        </>
      )}
    </Card>
  )
}

function PluginCard({ viewer }: { viewer: ArtifactViewer }) {
  const isEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(viewer.id))
  const togglePlugin = usePluginSettingsStore((s) => s.togglePlugin)
  const setPluginConfig = usePluginSettingsStore((s) => s.setPluginConfig)
  const pluginConfigMap = usePluginSettingsStore((s) => s.pluginConfigs[viewer.id])

  const [expanded, setExpanded] = useState(false)
  const [infoOpened, setInfoOpened] = useState(false)
  const isBrowserViewer = viewer.id === BROWSER_VIEWER_PLUGIN_ID
  const [browserEngine, setBrowserEngine] = useState<BrowserEngine>('browser_use')
  const [savingEngine, setSavingEngine] = useState(false)
  const Icon = viewer.icon

  useEffect(() => {
    if (!isBrowserViewer) return
    let active = true
    void Promise.resolve(api.getBrowserEngine?.())
      .then((res) => {
        if (active && res?.engine) setBrowserEngine(res.engine)
      })
      .catch(() => {})
    return () => { active = false }
  }, [isBrowserViewer])

  const handleBrowserEngineChange = async (next: string | null) => {
    if (!next || next === browserEngine) return
    const nextEngine = next as BrowserEngine
    const prev = browserEngine
    setBrowserEngine(nextEngine)
    setSavingEngine(true)
    try {
      const updated = await api.updateBrowserEngine(nextEngine)
      setBrowserEngine(updated.engine)
      notifications.show({ color: 'teal', message: `浏览器引擎已切换为 ${BROWSER_ENGINE_LABELS[updated.engine] || updated.engine}` })
    } catch (error) {
      setBrowserEngine(prev)
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '切换浏览器引擎失败' })
    } finally {
      setSavingEngine(false)
    }
  }

  const readOptionValue = (key: string, fallback: unknown) =>
    pluginConfigMap && pluginConfigMap[key] !== undefined ? pluginConfigMap[key] : fallback

  const defaultByKey = useMemo(() => {
    const map: Record<string, unknown> = {}
    for (const opt of viewer.configOptions || []) map[opt.key] = opt.defaultValue
    return map
  }, [viewer.configOptions])

  const visibleOptions = (viewer.configOptions || []).filter((opt) => {
    if (!opt.visibleWhen) return true
    const current = readOptionValue(opt.visibleWhen.key, defaultByKey[opt.visibleWhen.key])
    return current === opt.visibleWhen.value
  })
  const optionCount = visibleOptions.length + (isBrowserViewer ? 1 : 0)
  const hasOptions = optionCount > 0

  const activeProviderLabel = (() => {
    if (isBrowserViewer) return BROWSER_ENGINE_LABELS[browserEngine] || browserEngine
    if (!viewer.providerConfigKey) return viewer.implementationName || ''
    const providerOpt = (viewer.configOptions || []).find((opt) => opt.key === viewer.providerConfigKey)
    if (!providerOpt) return viewer.implementationName || ''
    const current = String(readOptionValue(providerOpt.key, providerOpt.defaultValue) ?? '')
    return providerOpt.options?.find((choice) => choice.value === current)?.label || current
  })()

  return (
    <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Group gap="sm" wrap="nowrap" align="flex-start">
          <div
            style={{
              padding: 8,
              borderRadius: 8,
              background: isEnabled ? 'color-mix(in oklab, var(--astr-accent) 12%, transparent)' : 'var(--astr-surface-muted)',
              color: isEnabled ? 'var(--astr-indigo)' : 'var(--astr-muted)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <Icon size={24} />
          </div>
          <div>
            <Group gap="xs" align="center" wrap="wrap">
              <Text fw={600} size="sm">
                {viewer.title}
              </Text>
              {viewer.version && (
                <Badge size="xs" variant="outline" color="gray">
                  v{viewer.version}
                </Badge>
              )}
              {viewer.capabilitySlot && (
                <Badge size="xs" variant="light" color="teal">
                  {activeProviderLabel ? `当前实现: ${activeProviderLabel}` : `${viewer.capabilityLabel || viewer.capabilitySlot} · 可替换实现`}
                </Badge>
              )}
              {viewer.capabilities.canEdit && (
                <Badge size="xs" variant="light" color="blue">
                  支持编辑写回
                </Badge>
              )}
            </Group>
            <Text size="xs" c="dimmed" mt={4}>
              {viewer.description || '为工作区提供专有媒体与格式的交互渲染与处理支持。'}
            </Text>
            {isEnabled && viewer.settingsAction && (
              <Button
                size="compact-xs"
                variant="light"
                color="violet"
                mt={8}
                leftSection={<IconSettings size={13} />}
                onClick={() => window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: viewer.settingsAction!.tab } }))}
              >
                {viewer.settingsAction.label}
              </Button>
            )}
            {viewer.id === 'blackboard-viewer' && (
              <Group gap={6} mt={6}>
                <Badge size="xs" color="indigo" variant="light">
                  星序多 Agent 核心总线
                </Badge>
                <Badge size="xs" color="cyan" variant="light">
                  json-render 视觉引擎
                </Badge>
                <Badge size="xs" color="teal" variant="light">
                  星系自动物理隔离
                </Badge>
              </Group>
            )}
          </div>
        </Group>

        <Group gap="xs" wrap="nowrap" align="center">
          <Tooltip label="查看插件说明与支持能力" withArrow>
            <ActionIcon
              size="sm"
              variant="subtle"
              color="gray"
              onClick={() => setInfoOpened(true)}
              aria-label={`查看 ${viewer.title} 说明`}
            >
              <IconInfoCircle size={17} />
            </ActionIcon>
          </Tooltip>

          <Switch
            checked={isEnabled}
            onChange={(e) => togglePlugin(viewer.id, e.currentTarget.checked)}
            size="sm"
            color="indigo"
            aria-label={`启用或禁用 ${viewer.title}`}
          />
        </Group>
      </Group>

      {hasOptions && (
        <>
          <Divider my="sm" />
          <Group justify="space-between" align="center">
            <Button
              variant="subtle"
              size="compact-xs"
              color="gray"
              leftSection={<IconAdjustments size={13} />}
              rightSection={expanded ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
              onClick={() => setExpanded(!expanded)}
            >
              参数配置 ({optionCount})
            </Button>
            <Text size="xs" c="dimmed">
              {isEnabled ? '生效中' : '已停用'}
            </Text>
          </Group>

          <Collapse expanded={expanded}>
            <Stack gap="xs" mt="xs" p="xs" style={{ background: 'var(--astr-surface-muted)', borderRadius: 6 }}>
              {isBrowserViewer && (
                <div>
                  <Text size="xs" fw={500} mb={2}>工具实现</Text>
                  <Text size="11px" c="dimmed" mb={4}>
                    选择自动执行网页任务的底层引擎（与「模型与网关」同步生效）
                  </Text>
                  <Select
                    size="xs"
                    allowDeselect={false}
                    aria-label="浏览器自动化引擎"
                    disabled={savingEngine}
                    value={browserEngine}
                    onChange={(val) => { void handleBrowserEngineChange(val) }}
                    data={[
                      { value: 'browser_use', label: 'Browser Use' },
                      { value: 'jev_ultrafast', label: 'System One' },
                    ]}
                  />
                </div>
              )}
              <ConfigOptionControls
                pluginId={viewer.id}
                options={visibleOptions}
                pluginConfigMap={pluginConfigMap}
                setPluginConfig={setPluginConfig}
              />
            </Stack>
          </Collapse>
        </>
      )}

      <Modal
        opened={infoOpened}
        onClose={() => setInfoOpened(false)}
        size="lg"
        radius="lg"
        centered
        title={
          <Group gap="xs">
            <div
              style={{
                width: 28,
                height: 28,
                borderRadius: 6,
                background: 'color-mix(in oklab, var(--astr-accent) 12%, transparent)',
                color: 'var(--astr-indigo, #5B5BD6)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <Icon size={18} />
            </div>
            <Text fw={700} size="md">
              {viewer.title} · 能力说明
            </Text>
            {viewer.version && (
              <Badge size="xs" variant="light" color="indigo">
                v{viewer.version}
              </Badge>
            )}
          </Group>
        }
      >
        <Stack gap="md">
          <Text size="sm" c="dimmed" style={{ lineHeight: 1.6 }}>
            {viewer.documentation?.summary || viewer.description || '为工作区提供专有交互呈现与处理能力。'}
          </Text>

          {viewer.documentation?.sections?.map((sec, idx) => (
            <div
              key={idx}
              style={{
                padding: '10px 12px',
                borderRadius: 8,
                background: 'var(--astr-surface-muted, #F8FAFC)',
                border: '1px solid var(--astr-border, #E5E7EB)',
              }}
            >
              <Text size="xs" fw={700} c="var(--astr-text, #1E293B)" mb={4}>
                {sec.title}
              </Text>
              <Text size="11.5px" c="dimmed" style={{ whiteSpace: 'pre-wrap', lineHeight: 1.55 }}>
                {sec.content}
              </Text>
            </div>
          ))}

          {viewer.documentation?.supportedComponents && viewer.documentation.supportedComponents.length > 0 && (
            <div>
              <Group justify="space-between" mb={8}>
                <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
                  当前支持的可视化组件清单 (json-render 组件库)
                </Text>
                <Badge size="xs" variant="light" color="indigo">
                  共 {viewer.documentation.supportedComponents.length} 项组件
                </Badge>
              </Group>

              <div
                style={{
                  maxHeight: 280,
                  overflowY: 'auto',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 6,
                  paddingRight: 4,
                }}
              >
                {viewer.documentation.supportedComponents.map((comp) => (
                  <div
                    key={comp.name}
                    style={{
                      padding: '8px 10px',
                      borderRadius: 6,
                      background: 'var(--astr-surface-muted, #F8FAFC)',
                      border: '1px solid var(--astr-border, #E5E7EB)',
                      fontSize: 11.5,
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 2 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <code style={{ fontFamily: 'monospace', fontWeight: 700, color: 'var(--astr-accent)' }}>
                          {comp.name}
                        </code>
                        <span style={{ fontWeight: 600, color: '#1E293B' }}>- {comp.label}</span>
                      </div>
                      {comp.triggerKeys && comp.triggerKeys.length > 0 && (
                        <div style={{ display: 'flex', gap: 4 }}>
                          {comp.triggerKeys.map((tk, tIdx) => (
                            <Badge key={tIdx} size="xs" variant="outline" color="gray" style={{ fontSize: 9.5 }}>
                              {tk}
                            </Badge>
                          ))}
                        </div>
                      )}
                    </div>
                    <Text size="11px" c="dimmed" style={{ lineHeight: 1.45 }}>
                      {comp.description}
                    </Text>
                  </div>
                ))}
              </div>
            </div>
          )}

          {viewer.extensions && viewer.extensions.length > 0 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11 }}>
              <span style={{ color: '#64748B' }}>关联文件类型:</span>
              <Group gap={4}>
                {viewer.extensions.map((ext) => (
                  <Badge key={ext} size="xs" variant="light" color="gray">
                    {ext}
                  </Badge>
                ))}
              </Group>
            </div>
          )}

          <Group justify="flex-end" mt="xs">
            <Button size="xs" variant="default" onClick={() => setInfoOpened(false)}>
              关闭
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Card>
  )
}

interface CatalogEntry {
  id: string
  category: PluginCategory
  searchText: string
  isToggleable: boolean
  render: () => React.ReactNode
}

function CategorySection({
  meta,
  entries,
  enabledPlugins,
}: {
  meta: PluginCategoryMeta
  entries: CatalogEntry[]
  enabledPlugins: Record<string, boolean>
}) {
  if (entries.length === 0) return null
  const Icon = meta.icon
  const toggleable = entries.filter((entry) => entry.isToggleable)
  const enabledCount = toggleable.filter((entry) => enabledPlugins[entry.id] !== false).length

  return (
    <section className="plugins-category-section" aria-label={meta.label}>
      <div className="plugins-category-header">
        <div className="plugins-category-title-wrap">
          <span className="plugins-category-icon" aria-hidden="true">
            <Icon size={16} />
          </span>
          <div style={{ minWidth: 0 }}>
            <Group gap="xs" wrap="nowrap">
              <Text fw={600} size="sm">{meta.label}</Text>
              <Badge size="xs" variant="light" color="gray">
                {toggleable.length > 0 ? `${enabledCount} / ${entries.length}` : `${entries.length} 项`}
              </Badge>
            </Group>
            <Text size="xs" c="dimmed" truncate>
              {meta.description}
            </Text>
          </div>
        </div>
      </div>

      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        {entries.map((entry) => (
          <div key={entry.id}>{entry.render()}</div>
        ))}
      </SimpleGrid>
    </section>
  )
}

export function PluginsPage() {
  const viewers = useMemo(() => artifactViewerRegistry.getAllViewers(), [])
  const hydrate = usePluginSettingsStore((s) => s.hydrate)
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)

  const [activeTab, setActiveTab] = useState<PluginTabId>('all')
  const [query, setQuery] = useState('')
  const tablistRef = useRef<HTMLDivElement | null>(null)
  const panelRefs = useRef<Record<string, HTMLDivElement | null>>({})

  useEffect(() => {
    void hydrate()
  }, [hydrate])

  const catalogEntries = useMemo<CatalogEntry[]>(() => {
    const entries: CatalogEntry[] = [
      ...BUILTIN_SYSTEM_PLUGINS.map((descriptor) => ({
        id: descriptor.id,
        category: descriptor.category,
        searchText: [
          descriptor.id,
          descriptor.title,
          descriptor.description,
          descriptor.capabilityLabel || '',
          descriptor.implementationName || '',
          ...(descriptor.configOptions || []).flatMap((opt) => (opt.options || []).map((choice) => choice.label)),
          ...descriptor.searchKeywords,
        ].join(' ').toLowerCase(),
        isToggleable: true,
        render: () => <SystemPluginCard descriptor={descriptor} />,
      })),
      {
        id: VOICE_INPUT_PLUGIN_ID,
        category: 'runtime',
        searchText: '语音输入与听写 r2t2 本地流式 whisper 云端 语音 模型 录音 voice',
        isToggleable: true,
        render: () => <VoiceInputPluginCard />,
      },
      ...viewers.map((viewer) => ({
        id: viewer.id,
        category: resolveViewerCategory(viewer),
        searchText: [
          viewer.id,
          viewer.title,
          viewer.description || '',
          viewer.capabilityLabel || '',
          viewer.implementationName || '',
          ...(viewer.id === BROWSER_VIEWER_PLUGIN_ID ? ['browser use', 'system one', '浏览器', '镜像'] : []),
          ...(viewer.configOptions || []).flatMap((opt) => (opt.options || []).map((choice) => choice.label)),
          ...(viewer.extensions || []),
        ].join(' ').toLowerCase(),
        isToggleable: true,
        render: () => <PluginCard viewer={viewer} />,
      })),
    ]
    return entries
  }, [viewers])

  const normalizedQuery = query.trim().toLowerCase()
  const filteredEntries = useMemo(() => {
    if (!normalizedQuery) return catalogEntries
    return catalogEntries.filter((entry) => entry.searchText.includes(normalizedQuery))
  }, [catalogEntries, normalizedQuery])

  const entriesByCategory = useMemo(() => {
    const grouped: Record<PluginCategory, CatalogEntry[]> = {
      development: [],
      workspace: [],
      diagram: [],
      media: [],
      runtime: [],
    }
    for (const entry of filteredEntries) {
      grouped[entry.category].push(entry)
    }
    return grouped
  }, [filteredEntries])

  const tabs = useMemo(
    () => [
      { id: 'all' as PluginTabId, label: '全部', count: filteredEntries.length },
      ...PLUGIN_CATEGORIES.map((cat) => ({
        id: cat.id as PluginTabId,
        label: cat.label,
        count: entriesByCategory[cat.id].length,
      })),
    ],
    [filteredEntries.length, entriesByCategory],
  )

  const selectTab = (nextId: PluginTabId, scrollTab = true) => {
    setActiveTab(nextId)
    if (scrollTab && tablistRef.current) {
      const btn = tablistRef.current.querySelector<HTMLButtonElement>(`[data-tab-id="${nextId}"]`)
      btn?.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
    }
  }

  // Sync tab selection when native find-in-page reveals a hidden="until-found" panel.
  useEffect(() => {
    const cleanups: Array<() => void> = []
    for (const tab of tabs) {
      const panel = panelRefs.current[tab.id]
      if (!panel) continue
      const onBeforeMatch = () => selectTab(tab.id, true)
      panel.addEventListener('beforematch', onBeforeMatch)
      cleanups.push(() => panel.removeEventListener('beforematch', onBeforeMatch))
    }
    return () => { cleanups.forEach((fn) => fn()) }
  }, [tabs])

  const handleTabKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if ('focusGroup' in HTMLElement.prototype) return
    const list = tablistRef.current
    if (!list) return
    const buttons = Array.from(list.querySelectorAll<HTMLButtonElement>('[role="tab"]'))
    const idx = buttons.indexOf(document.activeElement as HTMLButtonElement)
    if (idx < 0 || buttons.length === 0) return

    let nextIdx = -1
    if (event.key === 'ArrowRight') nextIdx = (idx + 1) % buttons.length
    else if (event.key === 'ArrowLeft') nextIdx = (idx - 1 + buttons.length) % buttons.length
    else if (event.key === 'Home') nextIdx = 0
    else if (event.key === 'End') nextIdx = buttons.length - 1
    else return

    event.preventDefault()
    const target = buttons[nextIdx]
    const targetId = target?.dataset.tabId as PluginTabId | undefined
    target?.focus()
    if (targetId) selectTab(targetId, true)
  }

  const allToggleableIds = [
    ...BUILTIN_SYSTEM_PLUGINS.map((p) => p.id),
    VOICE_INPUT_PLUGIN_ID,
    ...viewers.map((v) => v.id),
  ]
  const enabledCount = allToggleableIds.filter((id) => enabledPlugins[id] !== false).length

  return (
    <div className="route-page plugins-page">
      <Paper p="md" withBorder radius="lg" mb="md" style={{ background: 'var(--astr-surface)' }}>
        <Group justify="space-between" align="center" wrap="wrap" gap="sm">
          <Group gap="sm" wrap="nowrap">
            <div
              style={{
                width: 38,
                height: 38,
                borderRadius: 10,
                background: 'color-mix(in srgb, var(--astr-indigo) 12%, transparent)',
                color: 'var(--astr-indigo)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
                flexShrink: 0,
              }}
            >
              <IconPuzzle size={22} />
            </div>
            <div>
              <Title order={2} size="h3">
                插件管理
              </Title>
              <Text size="xs" c="dimmed" mt={2}>
                按能力分类管理开发工具、工作台扩展、富媒介渲染器与本地运行时，能力槽位支持按需启停与替换实现。
              </Text>
            </div>
          </Group>

          <Group gap="xs" wrap="nowrap">
            <TextInput
              size="xs"
              placeholder="搜索插件、实现或文件类型…"
              aria-label="搜索插件"
              value={query}
              onChange={(e) => setQuery(e.currentTarget.value)}
              leftSection={<IconSearch size={14} />}
              rightSection={
                query ? (
                  <ActionIcon size="xs" variant="subtle" color="gray" aria-label="清空搜索" onClick={() => setQuery('')}>
                    <IconX size={12} />
                  </ActionIcon>
                ) : null
              }
              style={{ width: 220 }}
            />
            <Badge size="md" variant="light" color="indigo">
              已启用 {enabledCount} / {allToggleableIds.length}
            </Badge>
          </Group>
        </Group>
      </Paper>

      <div className="plugins-tabs-shell">
        <div
          ref={tablistRef}
          className="plugins-tablist"
          role="tablist"
          aria-label="插件分类"
          onKeyDown={handleTabKeyDown}
          {...({ focusgroup: 'tablist nomemory' } as Record<string, string>)}
        >
          {tabs.map((tab) => {
            const selected = activeTab === tab.id
            return (
              <button
                key={tab.id}
                type="button"
                id={`plugins-tab-${tab.id}`}
                data-tab-id={tab.id}
                role="tab"
                className="plugins-tab"
                aria-selected={selected}
                tabIndex={selected ? 0 : -1}
                {...(selected ? ({ focusgroupstart: '' } as Record<string, string>) : {})}
                onClick={() => selectTab(tab.id, true)}
              >
                <span>{tab.label}</span>
                <span className="plugins-tab-count">{tab.count}</span>
              </button>
            )
          })}
        </div>
      </div>

      <div className="plugins-panels">
        {tabs.map((tab) => {
          const isSelected = activeTab === tab.id
          const sectionsToRender =
            tab.id === 'all'
              ? PLUGIN_CATEGORIES.filter((cat) => entriesByCategory[cat.id].length > 0)
              : PLUGIN_CATEGORIES.filter((cat) => cat.id === tab.id && entriesByCategory[cat.id].length > 0)

          return (
            <div
              key={tab.id}
              ref={(el) => { panelRefs.current[tab.id] = el }}
              role="tabpanel"
              className="plugins-tabpanel"
              aria-labelledby={`plugins-tab-${tab.id}`}
              hidden={isSelected ? undefined : ('until-found' as unknown as boolean)}
            >
              {isSelected && (
                <div className="plugins-panel-inner">
                  {sectionsToRender.length > 0 ? (
                    sectionsToRender.map((cat) => (
                      <CategorySection
                        key={cat.id}
                        meta={cat}
                        entries={entriesByCategory[cat.id]}
                        enabledPlugins={enabledPlugins}
                      />
                    ))
                  ) : (
                    <Paper withBorder radius="md" p="xl" ta="center" style={{ background: 'var(--astr-surface)' }}>
                      <Text size="sm" c="dimmed">
                        当前分类下没有匹配“{query.trim()}”的插件
                      </Text>
                    </Paper>
                  )}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}
