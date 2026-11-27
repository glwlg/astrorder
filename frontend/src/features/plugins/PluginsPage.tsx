import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Collapse,
  Group,
  Modal,
  NumberInput,
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
  IconChevronRight,
  IconChevronUp,
  IconInfoCircle,
  IconMicrophone,
  IconPackageImport,
  IconPuzzle,
  IconRefresh,
  IconRotateClockwise,
  IconSearch,
  IconSettings,
  IconShieldLock,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import { api, type BrowserEngine, type LocalVoicePluginStatus, type ToolchainItemStatus } from '../../api/client'
import type { PluginView } from '../../api/extensions'
import { extensionRegistry, useExtensionRegistryVersion } from '../extensions/extensionRegistry'
import { useExtensionStore } from '../extensions/extensionStore'
import { InstallPluginModal } from '../extensions/InstallPluginModal'
import { PermissionGrantModal } from '../extensions/PermissionGrantModal'
import { PluginConfigDrawer } from '../extensions/PluginConfigDrawer'
import {
  EXTERNAL_PLUGIN_PANEL_VIEWER_ID,
  PENDING_PLUGIN_CONFIG_SESSION_KEY,
  type OpenPluginConfigEventDetail,
} from '../extensions/types'
import { artifactViewerRegistry } from '../sidecar/registry'
import type { ArtifactViewer, PluginCategory, ViewerConfigOption } from '../sidecar/types'
import {
  BROWSER_VIEWER_PLUGIN_ID,
  BUILTIN_AGENT_PLUGINS,
  BUILTIN_SYSTEM_PLUGINS,
  VOICE_INPUT_PLUGIN_ID,
  type SystemPluginDescriptor,
} from './builtinPlugins'
import { PLUGIN_CATEGORIES, resolveViewerCategory, type PluginCategoryMeta, type PluginTabId } from './pluginCategories'
import { usePluginSettingsStore } from './pluginSettingsStore'
import { AgentEnginePluginCard, ToolchainRuntimeCard } from '../toolchain/ToolchainSectionCard'
import { useSettingsMode } from '../settings/settingsMode'
import './PluginsPage.css'

const BASIC_PLUGIN_CATEGORIES = new Set<PluginCategory>(['office', 'workspace', 'diagram', 'media'])
const BASIC_EXTRA_PLUGIN_IDS = new Set<string>([VOICE_INPUT_PLUGIN_ID])
const ADVANCED_ONLY_WORKSPACE_IDS = new Set<string>([
  'workspace-agent-accounts',
  'workspace-analytics-gateway',
  'workspace-monitor',
  'workspace-groups',
  'workspace-backups',
  'agent-graph-viewer',
  'blackboard-viewer',
  'memory-viewer',
  'git-worktrees-viewer',
])

const BROWSER_ENGINE_LABELS: Record<BrowserEngine, string> = {
  browser_use: 'Browser Use',
  jev_ultrafast: 'System One',
}

function NumberOptionControl({
  pluginId,
  option,
  value,
  setPluginConfig,
}: {
  pluginId: string
  option: ViewerConfigOption
  value: unknown
  setPluginConfig: (pluginId: string, key: string, value: unknown) => void
}) {
  const min = option.min ?? 1
  const max = option.max ?? 9999
  const fallback = typeof option.defaultValue === 'number' ? option.defaultValue : min
  const resolved = (() => {
    const num = typeof value === 'number' ? value : Number(value)
    if (!Number.isFinite(num)) return fallback
    return Math.min(max, Math.max(min, Math.round(num)))
  })()
  const [draft, setDraft] = useState<string | number>(resolved)

  useEffect(() => {
    setDraft(resolved)
  }, [resolved])

  const commit = (raw: string | number) => {
    const parsed = typeof raw === 'number' ? raw : Number(String(raw).trim())
    if (!Number.isFinite(parsed)) {
      setDraft(resolved)
      return
    }
    const clamped = Math.min(max, Math.max(min, Math.round(parsed)))
    setDraft(clamped)
    if (clamped !== resolved) {
      setPluginConfig(pluginId, option.key, clamped)
    }
  }

  return (
    <NumberInput
      size="xs"
      label={option.label}
      description={option.description}
      aria-label={option.label}
      min={min}
      max={max}
      step={option.step ?? 1}
      allowDecimal={false}
      clampBehavior="blur"
      value={draft}
      onChange={(next) => {
        setDraft(next)
        if (typeof next === 'number' && Number.isFinite(next) && next >= min && next <= max) {
          const rounded = Math.round(next)
          if (rounded !== resolved) {
            setPluginConfig(pluginId, option.key, rounded)
          }
        }
      }}
      onBlur={() => commit(draft)}
    />
  )
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

        if (opt.type === 'number') {
          return (
            <NumberOptionControl
              key={opt.key}
              pluginId={pluginId}
              option={opt}
              value={currentVal}
              setPluginConfig={setPluginConfig}
            />
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
    <Card withBorder className="plugins-tile">
      <div>
        <div className="plugins-tile-head">
          <div className="plugins-tile-title-row">
            <span
              className={`plugins-tile-icon${isEnabled ? '' : ' is-muted'}`}
              style={isEnabled && running && isLocalR2T2 ? { color: '#10b981' } : undefined}
              aria-hidden="true"
            >
              <IconMicrophone size={17} />
            </span>
            <Text fw={600} size="sm">语音输入与听写</Text>
            <Badge size="xs" variant="light" color="gray">当前实现: {providerBadge}</Badge>
          </div>
          <Switch
            aria-label="启用或禁用 语音输入与听写"
            size="sm"
            checked={isEnabled}
            onChange={(e) => togglePlugin(VOICE_INPUT_PLUGIN_ID, e.currentTarget.checked)}
          />
        </div>

        <div className="plugins-tile-desc">
          为对话与会议提供语音转文字能力，支持本机离线实时转写或云端语音接口。
        </div>

        {isEnabled && isLocalR2T2 && (
          <Group justify="space-between" align="center" gap="xs" mt={10} wrap="nowrap">
            <Text size="xs" c={status?.state === 'error' ? 'red' : 'dimmed'}>
              本机离线语音引擎：{status?.error || stateLabel}
            </Text>
            <Switch
              size="xs"
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

      <div>
        <div className="plugins-tile-footer is-split">
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
        </div>

        <Collapse expanded={expanded}>
          <Stack gap="xs" mt="xs" p="xs" style={{ background: 'var(--st-rail, var(--astr-surface-muted))', borderRadius: 8 }}>
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
                录音结束后将使用已配置的云端语音接口转写，不占用本机硬件资源。
              </Text>
            )}
          </Stack>
        </Collapse>
      </div>
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
    <Card withBorder className="plugins-tile">
      <div>
        <div className="plugins-tile-head">
          <div className="plugins-tile-title-row">
            <span className={`plugins-tile-icon${isEnabled ? '' : ' is-muted'}`} aria-hidden="true">
              <Icon size={17} />
            </span>
            <Text fw={600} size="sm">{descriptor.title}</Text>
            {descriptor.capabilitySlot && (
              <Badge size="xs" variant="light" color="gray">
                {activeProviderLabel ? `当前实现: ${activeProviderLabel}` : `${descriptor.capabilityLabel || descriptor.capabilitySlot}`}
              </Badge>
            )}
          </div>
          <Switch
            aria-label={`启用 ${descriptor.title}`}
            size="sm"
            checked={isEnabled}
            onChange={(e) => togglePlugin(descriptor.id, e.currentTarget.checked)}
          />
        </div>
        <div className="plugins-tile-desc">{descriptor.description}</div>
      </div>

      {(isEnabled && (descriptor.settingsAction || descriptor.routeAction)) || hasOptions ? (
        <>
          <div className={`plugins-tile-footer${hasOptions ? ' is-split' : ''}`}>
            {hasOptions && (
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
            )}
            <Group gap={12}>
              {isEnabled && descriptor.settingsAction && (
                <button
                  type="button"
                  className="st-text-link is-muted"
                  onClick={() => window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: descriptor.settingsAction!.tab } }))}
                >
                  {descriptor.settingsAction.label}
                </button>
              )}
              {isEnabled && descriptor.routeAction && (
                <button
                  type="button"
                  className="st-text-link is-muted"
                  onClick={() => window.dispatchEvent(new CustomEvent('astrorder:navigate', { detail: { to: descriptor.routeAction!.to } }))}
                >
                  <span>{descriptor.routeAction.label}</span>
                  <IconArrowUpRight size={13} />
                </button>
              )}
            </Group>
          </div>
          {hasOptions && (
            <Collapse expanded={expanded}>
              <Stack gap="xs" mt="xs" p="xs" style={{ background: 'var(--st-rail, var(--astr-surface-muted))', borderRadius: 8 }}>
                <ConfigOptionControls
                  pluginId={descriptor.id}
                  options={visibleOptions}
                  pluginConfigMap={pluginConfigMap}
                  setPluginConfig={setPluginConfig}
                />
              </Stack>
            </Collapse>
          )}
        </>
      ) : null}
    </Card>
  )
}

function PluginCard({
  viewer,
  onOpenConfig,
}: {
  viewer: ArtifactViewer
  onOpenConfig: (pluginId: string) => void
}) {
  const registryVersion = useExtensionRegistryVersion()
  const isEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(viewer.id))
  const togglePlugin = usePluginSettingsStore((s) => s.togglePlugin)
  const setPluginConfig = usePluginSettingsStore((s) => s.setPluginConfig)
  const pluginConfigMap = usePluginSettingsStore((s) => s.pluginConfigs[viewer.id])
  const extManifest = useExtensionStore((s) => s.items.find((item) => item.id === viewer.id))

  const [expanded, setExpanded] = useState(false)
  const [infoOpened, setInfoOpened] = useState(false)
  const isBrowserViewer = viewer.id === BROWSER_VIEWER_PLUGIN_ID
  const [browserEngine, setBrowserEngine] = useState<BrowserEngine>('browser_use')
  const [savingEngine, setSavingEngine] = useState(false)
  const Icon = viewer.icon

  const builtinReg = useMemo(() => {
    void registryVersion
    return extensionRegistry.getBuiltin(viewer.id)
  }, [registryVersion, viewer.id])
  const hasDrawerConfig = extensionRegistry.hasSettingsDrawer(viewer.id, extManifest)

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
    <Card withBorder className="plugins-tile">
      <div>
        <div className="plugins-tile-head">
          <div className="plugins-tile-title-row">
            <span className={`plugins-tile-icon${isEnabled ? '' : ' is-muted'}`} aria-hidden="true">
              <Icon size={17} />
            </span>
            <Text fw={600} size="sm">
              {viewer.title}
            </Text>
            {viewer.capabilitySlot && (
              <Badge size="xs" variant="light" color="gray">
                {activeProviderLabel ? `当前实现: ${activeProviderLabel}` : `${viewer.capabilityLabel || viewer.capabilitySlot}`}
              </Badge>
            )}
          </div>

          <Group gap={6} wrap="nowrap" align="center">
            <Tooltip label="查看说明" withArrow>
              <ActionIcon
                size="sm"
                variant="subtle"
                color="gray"
                onClick={() => setInfoOpened(true)}
                aria-label={`查看 ${viewer.title} 说明`}
              >
                <IconInfoCircle size={15} />
              </ActionIcon>
            </Tooltip>

            <Switch
              checked={isEnabled}
              onChange={(e) => togglePlugin(viewer.id, e.currentTarget.checked)}
              size="sm"
              aria-label={`启用或禁用 ${viewer.title}`}
            />
          </Group>
        </div>

        <div className="plugins-tile-desc">
          {viewer.description || '为工作区提供专有媒体与格式的交互渲染与处理支持。'}
        </div>
      </div>

      {(hasOptions || hasDrawerConfig || (isEnabled && viewer.settingsAction)) && (
        <>
          <div className={`plugins-tile-footer${hasOptions ? ' is-split' : ''}`}>
            {hasOptions && (
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
            )}
            {hasDrawerConfig ? (
              <Button
                variant="light"
                size="compact-xs"
                color="indigo"
                leftSection={<IconSettings size={13} />}
                onClick={() => onOpenConfig(viewer.id)}
              >
                {builtinReg?.settingsButtonLabel || '配置'}
              </Button>
            ) : isEnabled && viewer.settingsAction ? (
              <button
                type="button"
                className="st-text-link is-muted"
                onClick={() => window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: viewer.settingsAction!.tab } }))}
              >
                {viewer.settingsAction.label}
              </button>
            ) : null}
          </div>

          {hasOptions && (
            <Collapse expanded={expanded}>
              <Stack gap="xs" mt="xs" p="xs" style={{ background: 'var(--st-rail, var(--astr-surface-muted))', borderRadius: 8 }}>
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
          )}
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

const VALID_PLUGIN_CATEGORIES = new Set<PluginCategory>([
  'agent',
  'development',
  'office',
  'workspace',
  'diagram',
  'media',
  'runtime',
])

function resolveExtensionCategory(raw?: string): PluginCategory {
  if (raw && VALID_PLUGIN_CATEGORIES.has(raw as PluginCategory)) {
    return raw as PluginCategory
  }
  return 'workspace'
}

function getRuntimeStatusBadge(plugin: PluginView): { label: string; color: string } {
  if (!plugin.enabled) {
    return { label: '已停用', color: 'gray' }
  }
  switch (plugin.runtime_state) {
    case 'ready':
    case 'builtin':
      return { label: '运行中', color: 'teal' }
    case 'starting':
      return { label: '启动中', color: 'blue' }
    case 'error':
      return { label: '异常', color: 'red' }
    default:
      return { label: '已停用', color: 'gray' }
  }
}

function ExternalPluginCard({
  plugin,
  onToggle,
  onOpenConfig,
  onViewPermissions,
  onRestart,
  onRequestUninstall,
}: {
  plugin: PluginView
  onToggle: (plugin: PluginView, nextEnabled: boolean) => Promise<void>
  onOpenConfig: (pluginId: string) => void
  onViewPermissions: (plugin: PluginView) => void
  onRestart: (plugin: PluginView) => Promise<void>
  onRequestUninstall: (plugin: PluginView) => void
}) {
  const [busy, setBusy] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const runtimeBadge = getRuntimeStatusBadge(plugin)
  const hasSettings = extensionRegistry.hasSettingsDrawer(plugin.id, plugin)
  const canRestart = plugin.backend_type === 'process' && plugin.enabled

  return (
    <Card withBorder className="plugins-tile">
      <div>
        <div className="plugins-tile-head">
          <div className="plugins-tile-title-row">
            <span className={`plugins-tile-icon${plugin.enabled ? '' : ' is-muted'}`} aria-hidden="true">
              <IconPuzzle size={17} />
            </span>
            <Text fw={600} size="sm">
              {plugin.name}
            </Text>
            <Badge size="xs" variant="light" color="indigo">
              第三方
            </Badge>
            {plugin.version && (
              <Badge size="xs" variant="outline" color="gray">
                v{plugin.version}
              </Badge>
            )}
            <Tooltip label={plugin.runtime_error || runtimeBadge.label} disabled={!plugin.runtime_error} withArrow>
              <Badge size="xs" variant="light" color={runtimeBadge.color}>
                {runtimeBadge.label}
              </Badge>
            </Tooltip>
          </div>

          <Switch
            checked={plugin.enabled}
            disabled={busy}
            size="sm"
            aria-label={`启用或禁用 ${plugin.name}`}
            onChange={(e) => {
              const nextChecked = e.currentTarget.checked
              setBusy(true)
              void onToggle(plugin, nextChecked).finally(() => setBusy(false))
            }}
          />
        </div>

        <div className="plugins-tile-desc">
          {plugin.description || '通过 Astrorder 扩展平台安装的第三方插件。'}
        </div>

        {plugin.runtime_error && (
          <div className="plugins-tile-error" role="alert">
            {plugin.runtime_error}
          </div>
        )}
      </div>

      <div className="plugins-tile-footer is-split">
        <div className="plugins-tile-actions">
          {hasSettings && (
            <Button
              size="compact-xs"
              variant="light"
              color="indigo"
              leftSection={<IconSettings size={13} />}
              onClick={() => onOpenConfig(plugin.id)}
            >
              配置
            </Button>
          )}
          <Button
            size="compact-xs"
            variant="subtle"
            color="gray"
            leftSection={<IconShieldLock size={13} />}
            onClick={() => onViewPermissions(plugin)}
          >
            权限{plugin.permissions && plugin.permissions.length > 0 ? ` (${plugin.permissions.length})` : ''}
          </Button>
          {canRestart && (
            <Button
              size="compact-xs"
              variant="subtle"
              color="gray"
              loading={restarting}
              leftSection={<IconRotateClockwise size={13} />}
              onClick={() => {
                setRestarting(true)
                void onRestart(plugin).finally(() => setRestarting(false))
              }}
            >
              重启
            </Button>
          )}
        </div>

        <Button
          size="compact-xs"
          variant="subtle"
          color="red"
          leftSection={<IconTrash size={13} />}
          onClick={() => onRequestUninstall(plugin)}
        >
          卸载
        </Button>
      </div>
    </Card>
  )
}

interface CatalogEntry {
  id: string
  category: PluginCategory
  searchText: string
  isToggleable: boolean
  isEnabled?: boolean
  fullWidth?: boolean
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
  const toggleable = entries.filter((entry) => entry.isToggleable)
  const enabledCount = toggleable.filter((entry) =>
    entry.isEnabled !== undefined ? entry.isEnabled : enabledPlugins[entry.id] !== false,
  ).length

  return (
    <section className="plugins-category-section" aria-label={meta.label}>
      <div className="plugins-category-header">
        <span className="plugins-category-title">{meta.label}</span>
        <span className="plugins-category-count">
          {toggleable.length > 0 ? `${enabledCount} / ${toggleable.length} 已启用` : `${entries.length} 项`}
        </span>
      </div>

      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing={14}>
        {entries.map((entry) => (
          <div key={entry.id} style={entry.fullWidth ? { gridColumn: '1 / -1' } : undefined}>
            {entry.render()}
          </div>
        ))}
      </SimpleGrid>
    </section>
  )
}

export function PluginsPage() {
  const viewers = useMemo(
    () =>
      artifactViewerRegistry
        .getAllViewers()
        .filter(
          (viewer) =>
            viewer.id !== EXTERNAL_PLUGIN_PANEL_VIEWER_ID &&
            !(viewer as ArtifactViewer & { hiddenFromCatalog?: boolean }).hiddenFromCatalog,
        ),
    [],
  )
  const hydrate = usePluginSettingsStore((s) => s.hydrate)
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const extensionItems = useExtensionStore((s) => s.items)
  const extensionsLoading = useExtensionStore((s) => s.loading)
  const hydrateExtensions = useExtensionStore((s) => s.hydrate)
  const refreshExtensions = useExtensionStore((s) => s.refresh)
  const setExtensionEnabled = useExtensionStore((s) => s.setEnabled)
  const restartExtension = useExtensionStore((s) => s.restart)
  const uninstallExtension = useExtensionStore((s) => s.uninstall)
  const { mode, setMode, managedByWorkbench } = useSettingsMode()
  const isBasicMode = managedByWorkbench && mode === 'basic'

  const [activeTab, setActiveTab] = useState<PluginTabId>('all')
  const [query, setQuery] = useState('')
  const [localToolchain, setLocalToolchain] = useState<Record<string, ToolchainItemStatus>>({})
  const [configPluginId, setConfigPluginId] = useState<string | null>(() => {
    try {
      const pending = sessionStorage.getItem(PENDING_PLUGIN_CONFIG_SESSION_KEY)
      if (pending) {
        sessionStorage.removeItem(PENDING_PLUGIN_CONFIG_SESSION_KEY)
        return pending
      }
    } catch {
      // ignore storage errors
    }
    return null
  })
  const [installModalOpened, setInstallModalOpened] = useState(false)
  const [permissionModal, setPermissionModal] = useState<{
    plugin: PluginView
    mode: 'grant' | 'view'
    pendingPermissions?: string[]
  } | null>(null)
  const [permissionGranting, setPermissionGranting] = useState(false)
  const [uninstallTarget, setUninstallTarget] = useState<PluginView | null>(null)
  const [uninstalling, setUninstalling] = useState(false)
  const tablistRef = useRef<HTMLDivElement | null>(null)
  const panelRefs = useRef<Record<string, HTMLDivElement | null>>({})

  const refreshLocalToolchain = useCallback(() => {
    if (typeof api.getToolchainStatus !== 'function') return
    void api.getToolchainStatus('local')
      .then((report) => {
        const mapped: Record<string, ToolchainItemStatus> = {}
        for (const item of report.items || []) {
          mapped[item.id] = item
        }
        setLocalToolchain(mapped)
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    void hydrate()
    void hydrateExtensions()
    refreshLocalToolchain()
  }, [hydrate, hydrateExtensions, refreshLocalToolchain])

  useEffect(() => {
    const onOpenConfig = (event: Event) => {
      const detail = (event as CustomEvent<OpenPluginConfigEventDetail>).detail
      if (detail?.pluginId) {
        try {
          sessionStorage.removeItem(PENDING_PLUGIN_CONFIG_SESSION_KEY)
        } catch {
          // ignore
        }
        setConfigPluginId(detail.pluginId)
      }
    }
    window.addEventListener('astrorder:open-plugin-config', onOpenConfig)
    return () => window.removeEventListener('astrorder:open-plugin-config', onOpenConfig)
  }, [])

  const handleToggleExternal = useCallback(async (plugin: PluginView, nextEnabled: boolean) => {
    try {
      const res = await setExtensionEnabled(plugin.id, nextEnabled)
      if (res.requires_permission_grant) {
        setPermissionModal({
          plugin: res.item || plugin,
          mode: 'grant',
          pendingPermissions:
            res.pending_permissions && res.pending_permissions.length > 0
              ? res.pending_permissions
              : plugin.pending_permissions && plugin.pending_permissions.length > 0
                ? plugin.pending_permissions
                : plugin.permissions || [],
        })
        return
      }
      notifications.show({
        color: 'teal',
        message: nextEnabled ? `已启用扩展「${plugin.name}」` : `已停用扩展「${plugin.name}」`,
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : `更新扩展「${plugin.name}」状态失败`,
      })
    }
  }, [setExtensionEnabled])

  const handleConfirmPermissionGrant = async (grantedPermissions: string[]) => {
    if (!permissionModal || permissionGranting) return
    const target = permissionModal.plugin
    setPermissionGranting(true)
    try {
      const res = await setExtensionEnabled(target.id, true, grantedPermissions)
      if (res.requires_permission_grant) {
        setPermissionModal({
          plugin: res.item || target,
          mode: 'grant',
          pendingPermissions: res.pending_permissions || grantedPermissions,
        })
        return
      }
      setPermissionModal(null)
      notifications.show({
        color: 'teal',
        message: `已授权并启用扩展「${target.name}」`,
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : `启用扩展「${target.name}」失败`,
      })
    } finally {
      setPermissionGranting(false)
    }
  }

  const handleRestartExternal = useCallback(async (plugin: PluginView) => {
    try {
      await restartExtension(plugin.id)
      notifications.show({
        color: 'teal',
        message: `已重启扩展「${plugin.name}」`,
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : `重启扩展「${plugin.name}」失败`,
      })
    }
  }, [restartExtension])

  const handleConfirmUninstall = async () => {
    if (!uninstallTarget || uninstalling) return
    const target = uninstallTarget
    setUninstalling(true)
    try {
      await uninstallExtension(target.id)
      if (configPluginId === target.id) {
        setConfigPluginId(null)
      }
      setUninstallTarget(null)
      notifications.show({
        color: 'teal',
        message: `已卸载扩展「${target.name}」`,
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : `卸载扩展「${target.name}」失败`,
      })
    } finally {
      setUninstalling(false)
    }
  }

  const externalPlugins = useMemo(
    () => extensionItems.filter((item) => item.source === 'external'),
    [extensionItems],
  )

  const catalogEntries = useMemo<CatalogEntry[]>(() => {
    const entries: CatalogEntry[] = [
      ...BUILTIN_AGENT_PLUGINS.map((descriptor) => ({
        id: descriptor.id,
        category: 'agent' as PluginCategory,
        searchText: [
          descriptor.id,
          descriptor.kind,
          descriptor.title,
          descriptor.description,
          ...descriptor.searchKeywords,
        ].join(' ').toLowerCase(),
        isToggleable: true,
        render: () => (
          <AgentEnginePluginCard
            descriptor={descriptor}
            localStatus={localToolchain[descriptor.kind]}
            onRefreshToolchain={refreshLocalToolchain}
          />
        ),
      })),
      {
        id: 'toolchain-runtime-panel',
        category: 'runtime' as PluginCategory,
        searchText: '常用开发工具链 uv uvx node npm npx git ripgrep rg 安装 体检 工具 路径 runtime toolchain',
        isToggleable: false,
        fullWidth: true,
        render: () => (
          <ToolchainRuntimeCard
            onReportUpdated={(envId, report) => {
              if (envId !== 'local') return
              const mapped: Record<string, ToolchainItemStatus> = {}
              for (const item of report.items || []) mapped[item.id] = item
              setLocalToolchain(mapped)
            }}
          />
        ),
      },
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
        category: 'office',
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
        render: () => <PluginCard viewer={viewer} onOpenConfig={(id) => setConfigPluginId(id)} />,
      })),
      ...externalPlugins.map((plugin) => ({
        id: plugin.id,
        category: resolveExtensionCategory(plugin.category),
        searchText: [
          plugin.id,
          plugin.name,
          plugin.description || '',
          plugin.author || '',
          plugin.category || '',
          '第三方 外部扩展 external',
        ].join(' ').toLowerCase(),
        isToggleable: true,
        isEnabled: plugin.enabled,
        render: () => (
          <ExternalPluginCard
            plugin={plugin}
            onToggle={handleToggleExternal}
            onOpenConfig={(id) => setConfigPluginId(id)}
            onViewPermissions={(target) => setPermissionModal({ plugin: target, mode: 'view' })}
            onRestart={handleRestartExternal}
            onRequestUninstall={(target) => setUninstallTarget(target)}
          />
        ),
      })),
    ]
    return entries
  }, [viewers, localToolchain, externalPlugins, refreshLocalToolchain, handleToggleExternal, handleRestartExternal])

  const normalizedQuery = query.trim().toLowerCase()
  const visibleCatalogEntries = useMemo(() => {
    if (!isBasicMode || normalizedQuery) return catalogEntries
    return catalogEntries.filter((entry) => {
      if (ADVANCED_ONLY_WORKSPACE_IDS.has(entry.id)) return false
      return BASIC_PLUGIN_CATEGORIES.has(entry.category) || BASIC_EXTRA_PLUGIN_IDS.has(entry.id)
    })
  }, [catalogEntries, isBasicMode, normalizedQuery])

  const hiddenAdvancedCount = catalogEntries.length - visibleCatalogEntries.length

  const filteredEntries = useMemo(() => {
    if (!normalizedQuery) return visibleCatalogEntries
    return visibleCatalogEntries.filter((entry) => entry.searchText.includes(normalizedQuery))
  }, [visibleCatalogEntries, normalizedQuery])

  const entriesByCategory = useMemo(() => {
    const grouped: Record<PluginCategory, CatalogEntry[]> = {
      agent: [],
      development: [],
      office: [],
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

  const visibleCategories = useMemo(
    () =>
      isBasicMode && !normalizedQuery
        ? PLUGIN_CATEGORIES.filter((cat) => entriesByCategory[cat.id].length > 0)
        : PLUGIN_CATEGORIES,
    [entriesByCategory, isBasicMode, normalizedQuery],
  )

  const tabs = useMemo(
    () => [
      { id: 'all' as PluginTabId, label: isBasicMode && !normalizedQuery ? '常用功能' : '全部', count: filteredEntries.length },
      ...visibleCategories.map((cat) => ({
        id: cat.id as PluginTabId,
        label: cat.label,
        count: entriesByCategory[cat.id].length,
      })),
    ],
    [filteredEntries.length, entriesByCategory, visibleCategories, isBasicMode, normalizedQuery],
  )

  useEffect(() => {
    if (!tabs.some((t) => t.id === activeTab)) {
      setActiveTab('all')
    }
  }, [activeTab, tabs])

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

  const builtinToggleableIds = [
    ...BUILTIN_AGENT_PLUGINS.map((p) => p.id),
    ...BUILTIN_SYSTEM_PLUGINS.map((p) => p.id),
    VOICE_INPUT_PLUGIN_ID,
    ...viewers.map((v) => v.id),
  ]
  const totalToggleableCount = builtinToggleableIds.length + externalPlugins.length
  const enabledCount =
    builtinToggleableIds.filter((id) => enabledPlugins[id] !== false).length +
    externalPlugins.filter((plugin) => plugin.enabled).length

  const handleRefreshAll = () => {
    void hydrate(true)
    void refreshExtensions()
    refreshLocalToolchain()
  }

  return (
    <div className="route-page plugins-page">
      <div className="plugins-page-top">
        <div>
          <Title order={2} style={{ fontSize: 22, fontWeight: 650, letterSpacing: '-0.025em', color: 'var(--st-ink, var(--astr-text))' }}>
            插件管理
          </Title>
          <Text size="sm" c="dimmed" mt={4}>
            {isBasicMode ? '按需开关办公与协作功能，保持工作区简洁' : '按能力分类管理全部工作台扩展、开发工具与本地运行时'}
          </Text>
        </div>

        <div className="plugins-page-search">
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
          {!isBasicMode && (
            <>
              <Button
                size="xs"
                variant="light"
                color="indigo"
                leftSection={<IconPackageImport size={14} />}
                onClick={() => setInstallModalOpened(true)}
              >
                安装外部插件
              </Button>
              <Tooltip label="刷新插件列表" withArrow>
                <ActionIcon
                  size="sm"
                  variant="subtle"
                  color="gray"
                  aria-label="刷新插件列表"
                  loading={extensionsLoading}
                  onClick={handleRefreshAll}
                >
                  <IconRefresh size={15} />
                </ActionIcon>
              </Tooltip>
              <Text size="xs" c="dimmed" style={{ whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>
                已启用 {enabledCount} / {totalToggleableCount}
              </Text>
            </>
          )}
        </div>
      </div>

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
        {isBasicMode && hiddenAdvancedCount > 0 && !normalizedQuery && (
          <div className="st-inline-notice">
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
              <IconInfoCircle size={14} aria-hidden="true" />
              <span>已隐藏 {hiddenAdvancedCount} 项开发引擎、数据库与命令行底层插件</span>
            </span>
            <button type="button" className="st-text-link" onClick={() => setMode('advanced')}>
              <span>在高级配置中查看全部</span>
              <IconChevronRight size={14} aria-hidden="true" />
            </button>
          </div>
        )}
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

      <PluginConfigDrawer
        opened={Boolean(configPluginId)}
        pluginId={configPluginId}
        onClose={() => setConfigPluginId(null)}
      />

      <InstallPluginModal
        opened={installModalOpened}
        onClose={() => setInstallModalOpened(false)}
        onInstalled={(plugin, requestEnable) => {
          if (requestEnable) {
            void handleToggleExternal(plugin, true)
          }
        }}
      />

      <PermissionGrantModal
        opened={Boolean(permissionModal)}
        plugin={permissionModal?.plugin || null}
        mode={permissionModal?.mode || 'grant'}
        pendingPermissions={permissionModal?.pendingPermissions}
        loading={permissionGranting}
        onConfirm={(perms) => {
          void handleConfirmPermissionGrant(perms)
        }}
        onClose={() => setPermissionModal(null)}
      />

      <Modal
        opened={Boolean(uninstallTarget)}
        onClose={() => setUninstallTarget(null)}
        size="sm"
        radius="lg"
        centered
        title="确认卸载外部插件"
      >
        <Stack gap="md">
          <Text size="sm" c="dimmed" style={{ lineHeight: 1.6 }}>
            确定要卸载第三方扩展「{uninstallTarget?.name}」吗？卸载后会停止其后台进程，并移除该扩展的安装目录、权限记录及保存的凭据。
          </Text>
          <Group justify="flex-end" gap="xs">
            <Button size="xs" variant="default" onClick={() => setUninstallTarget(null)}>
              取消
            </Button>
            <Button
              size="xs"
              color="red"
              loading={uninstalling}
              onClick={() => {
                void handleConfirmUninstall()
              }}
            >
              确认卸载
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
