import { useEffect, useMemo, useState } from 'react'
import {
  Badge,
  Button,
  Card,
  Divider,
  Group,
  Modal,
  Paper,
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconDownload,
  IconFolderOpen,
  IconNetwork,
  IconPlugConnected,
  IconRefresh,
  IconRocket,
  IconSettings,
  IconTerminal2,
  IconTool,
} from '@tabler/icons-react'
import {
  api,
  type ToolchainItemStatus,
  type ToolchainNetworkSettings,
  type ToolchainReport,
  type ToolchainTarget,
} from '../../api/client'
import { AgentBrandIcon } from '../../components/AgentBrandIcon'
import { useAstrorderStore } from '../../state/store'
import {
  canonicalAgentKind,
  type AgentEnginePluginDescriptor,
  type ManagedAgentKind,
} from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { AgentDisableGuardModal } from './AgentDisableGuardModal'
import { AgentEnableModal } from './AgentEnableModal'
import { EnvironmentBootstrapModal } from './EnvironmentBootstrapModal'
import { ToolchainInstallModal } from './ToolchainInstallModal'

interface AgentEnginePluginCardProps {
  descriptor: AgentEnginePluginDescriptor
  localStatus?: ToolchainItemStatus
  onRefreshToolchain?: () => void
}

export function AgentEnginePluginCard({
  descriptor,
  localStatus,
  onRefreshToolchain,
}: AgentEnginePluginCardProps) {
  const isEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(descriptor.id))
  const togglePlugin = usePluginSettingsStore((s) => s.togglePlugin)
  const sessions = useAstrorderStore((s) => s.sessions)
  const agents = useAstrorderStore((s) => s.agents)

  const [enableModalOpen, setEnableModalOpen] = useState(false)
  const [configureOnly, setConfigureOnly] = useState(false)
  const [disableModalOpen, setDisableModalOpen] = useState(false)

  const runningCount = useMemo(() => {
    return Object.values(sessions).filter((sess) => {
      const ag = agents[sess.agent_id]
      return (
        canonicalAgentKind(ag?.kind) === descriptor.kind &&
        (sess.status === 'running' || sess.status === 'waiting_approval')
      )
    }).length
  }, [sessions, agents, descriptor.kind])

  const handleToggle = (nextChecked: boolean) => {
    if (nextChecked) {
      setConfigureOnly(false)
      setEnableModalOpen(true)
    } else {
      setDisableModalOpen(true)
    }
  }

  const confirmDisable = (disableRelatedPluginIds: string[]) => {
    togglePlugin(descriptor.id, false)
    for (const relId of disableRelatedPluginIds) {
      togglePlugin(relId, false)
    }
    setDisableModalOpen(false)
    notifications.show({
      color: 'gray',
      message: `已停用 ${descriptor.title} 插件，历史会话仍可只读查看`,
    })
  }

  const readinessColor = !localStatus
    ? 'gray'
    : localStatus.readiness === 'ready'
      ? 'teal'
      : localStatus.installed
        ? 'yellow'
        : 'orange'
  const readinessLabel = !localStatus
    ? ''
    : localStatus.readiness === 'ready'
      ? `本机就绪${localStatus.version ? ` v${localStatus.version}` : ''}`
      : localStatus.installed
        ? `待登录/配置${localStatus.version ? ` v${localStatus.version}` : ''}`
        : '本机未安装'

  return (
    <>
      <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)', height: '100%' }}>
        <Stack gap="sm" justify="space-between" h="100%">
          <div>
            <Group justify="space-between" align="flex-start" wrap="nowrap">
              <Group gap="sm" wrap="nowrap" align="flex-start">
                <div
                  style={{
                    padding: 8,
                    borderRadius: 8,
                    background: isEnabled
                      ? 'color-mix(in oklab, var(--astr-accent) 12%, transparent)'
                      : 'var(--astr-surface-muted)',
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    opacity: isEnabled ? 1 : 0.65,
                  }}
                >
                  <AgentBrandIcon kind={descriptor.kind} size={24} />
                </div>
                <div style={{ minWidth: 0 }}>
                  <Group gap={6} wrap="wrap">
                    <Text fw={600} size="sm">
                      {descriptor.title}
                    </Text>
                    <Badge size="xs" variant="light" color={isEnabled ? 'indigo' : 'gray'}>
                      {isEnabled ? '已启用' : '未启用'}
                    </Badge>
                    {localStatus && (
                      <Badge size="xs" variant="light" color={readinessColor}>
                        {readinessLabel}
                      </Badge>
                    )}
                  </Group>
                  <Text size="xs" c="dimmed" mt={4}>
                    {descriptor.description}
                  </Text>
                  {localStatus?.executable && (
                    <Text size="11px" c="dimmed" mt={4} ff="monospace" truncate title={localStatus.executable}>
                      路径：{localStatus.executable}
                    </Text>
                  )}
                  {localStatus?.installed && localStatus.readiness === 'needs_setup' && localStatus.readiness_detail && (
                    <Text size="11px" c="yellow" mt={3}>
                      {localStatus.readiness_detail}
                    </Text>
                  )}
                  {localStatus?.path_hint && (
                    <Text size="11px" c="orange" mt={2}>
                      {localStatus.path_hint}
                    </Text>
                  )}
                  {localStatus?.requires_daemon_restart && isEnabled && (
                    <Text size="11px" c="orange" mt={3}>
                      新路径已写入小内核配置，重启小内核后生效
                    </Text>
                  )}
                </div>
              </Group>
              <Switch
                aria-label={`启用或禁用 ${descriptor.title}`}
                size="sm"
                color="indigo"
                checked={isEnabled}
                onChange={(e) => handleToggle(e.currentTarget.checked)}
              />
            </Group>
          </div>

          <div>
            <Divider mb="xs" />
            <Group justify="space-between" align="center" wrap="wrap" gap="xs">
              <Group gap={6}>
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="gray"
                  leftSection={<IconSettings size={13} />}
                  onClick={() => {
                    setConfigureOnly(isEnabled)
                    setEnableModalOpen(true)
                  }}
                >
                  {localStatus?.installed ? '路径与检测' : '安装 / 选择路径'}
                </Button>
              </Group>
              <Text size="xs" c="dimmed">
                {isEnabled ? '生效中' : '已隐藏'}
              </Text>
            </Group>
          </div>
        </Stack>
      </Card>

      <AgentEnableModal
        opened={enableModalOpen}
        kind={descriptor.kind as ManagedAgentKind}
        configureOnly={configureOnly}
        onClose={() => setEnableModalOpen(false)}
        onEnabled={() => onRefreshToolchain?.()}
      />

      <AgentDisableGuardModal
        opened={disableModalOpen}
        kind={descriptor.kind as ManagedAgentKind}
        runningSessionCount={runningCount}
        onClose={() => setDisableModalOpen(false)}
        onConfirm={confirmDisable}
      />
    </>
  )
}

interface ToolchainRuntimeCardProps {
  onReportUpdated?: (envId: string, report: ToolchainReport) => void
}

export function ToolchainRuntimeCard({ onReportUpdated }: ToolchainRuntimeCardProps) {
  const [targets, setTargets] = useState<ToolchainTarget[]>([{ id: 'local', kind: 'local', name: '本机' }])
  const [envId, setEnvId] = useState('local')
  const [groupFilter, setGroupFilter] = useState<'all' | 'core' | 'extra'>('core')
  const [loading, setLoading] = useState(false)
  const [report, setReport] = useState<ToolchainReport | null>(null)
  const [error, setError] = useState('')

  const [installTarget, setInstallTarget] = useState<ToolchainItemStatus | null>(null)
  const [bootstrapOpen, setBootstrapOpen] = useState(false)
  const [pathTarget, setPathTarget] = useState<ToolchainItemStatus | null>(null)
  const [pathDraft, setPathDraft] = useState('')
  const [savingPath, setSavingPath] = useState(false)

  // Persistent install network settings (proxy / mirror)
  const [networkOpen, setNetworkOpen] = useState(false)
  const [networkSettings, setNetworkSettings] = useState<ToolchainNetworkSettings>({
    default: { proxy_url: '', mirror: '' },
  })
  const [netProxyDraft, setNetProxyDraft] = useState('')
  const [netMirrorDraft, setNetMirrorDraft] = useState('')
  const [savingNetwork, setSavingNetwork] = useState(false)

  const inspect = async (nextEnv = envId) => {
    setLoading(true)
    setError('')
    try {
      const [targetsRes, statusRes, netRes] = await Promise.all([
        api.getToolchainTargets().catch(() => ({ items: [{ id: 'local', kind: 'local' as const, name: '本机' }] })),
        api.getToolchainStatus(nextEnv),
        typeof api.getToolchainNetwork === 'function'
          ? api.getToolchainNetwork().catch(() => ({ default: { proxy_url: '', mirror: '' } }))
          : Promise.resolve({ default: { proxy_url: '', mirror: '' } }),
      ])
      if (targetsRes.items?.length) setTargets(targetsRes.items)
      setReport(statusRes)
      setNetworkSettings(netRes)
      onReportUpdated?.(nextEnv, statusRes)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取工具链状态失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void inspect('local')
  }, [])

  const allToolItems = useMemo(
    () => (report?.items || []).filter((item) => item.kind === 'tool'),
    [report],
  )
  const coreTools = useMemo(
    () => allToolItems.filter((item) => item.group !== 'extra'),
    [allToolItems],
  )
  const extraTools = useMemo(
    () => allToolItems.filter((item) => item.group === 'extra'),
    [allToolItems],
  )
  const displayedTools = useMemo(() => {
    if (groupFilter === 'core') return coreTools
    if (groupFilter === 'extra') return extraTools
    return allToolItems
  }, [groupFilter, coreTools, extraTools, allToolItems])

  const installedCount = allToolItems.filter((item) => item.installed).length
  const envName = targets.find((t) => t.id === envId)?.name || envId
  const hasSavedNetwork = Boolean(networkSettings.default?.proxy_url || networkSettings.default?.mirror)

  const hasNativePicker =
    envId === 'local' &&
    typeof window !== 'undefined' &&
    Boolean((window as unknown as { astrorderDesktop?: { openFileDialog?: unknown } }).astrorderDesktop?.openFileDialog)

  const handleBrowsePath = async () => {
    const desktop = (window as unknown as {
      astrorderDesktop?: { openFileDialog?: (defaultPath?: string) => Promise<string> }
    }).astrorderDesktop
    if (!desktop?.openFileDialog) return
    try {
      const picked = await desktop.openFileDialog(pathDraft)
      if (picked) setPathDraft(picked)
    } catch {}
  }

  const handleSaveToolPath = async () => {
    if (!pathTarget) return
    setSavingPath(true)
    try {
      const res = await api.saveToolchainExecutable({
        environment_id: envId,
        tool_id: pathTarget.id,
        path: pathDraft.trim(),
      })
      notifications.show({ color: 'teal', message: res.message })
      setPathTarget(null)
      await inspect(envId)
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存路径失败',
      })
    } finally {
      setSavingPath(false)
    }
  }

  const openNetworkModal = () => {
    setNetProxyDraft(networkSettings.default?.proxy_url || '')
    setNetMirrorDraft(networkSettings.default?.mirror || '')
    setNetworkOpen(true)
  }

  const handleSaveNetwork = async () => {
    setSavingNetwork(true)
    try {
      const updated = await api.saveToolchainNetwork({
        environment_id: 'default',
        proxy_url: netProxyDraft.trim(),
        mirror: netMirrorDraft.trim(),
      })
      setNetworkSettings(updated)
      setNetworkOpen(false)
      notifications.show({ color: 'teal', message: '已保存默认安装网络代理与镜像设置' })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存网络配置失败',
      })
    } finally {
      setSavingNetwork(false)
    }
  }

  return (
    <>
      <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)', gridColumn: '1 / -1' }}>
        <Stack gap="sm">
          <Group justify="space-between" align="flex-start" wrap="wrap" gap="sm">
            <Group gap="sm" wrap="nowrap" align="flex-start">
              <div
                style={{
                  padding: 8,
                  borderRadius: 8,
                  background: 'color-mix(in oklab, var(--astr-accent) 12%, transparent)',
                  color: 'var(--astr-indigo)',
                  display: 'flex',
                }}
              >
                <IconTool size={24} />
              </div>
              <div>
                <Group gap="xs" wrap="wrap">
                  <Text fw={600} size="sm">
                    常用开发工具链辅助安装
                  </Text>
                  <Badge size="xs" variant="light" color="indigo">
                    已就绪 {installedCount} / {allToolItems.length || 9}
                  </Badge>
                  {report?.os && (
                    <Badge size="xs" variant="outline" color="gray">
                      {report.os}
                      {report.arch ? ` · ${report.arch}` : ''}
                    </Badge>
                  )}
                  {hasSavedNetwork && (
                    <Badge size="xs" variant="light" color="teal">
                      已配默认代理/镜像
                    </Badge>
                  )}
                </Group>
                <Text size="xs" c="dimmed" mt={4}>
                  统一检测与引导安装核心工具（uv、Node.js/npm、Git、rg）与扩展工具（gh、jq、fd、pnpm、uv Python）；支持本机、WSL 与 SSH 批量初始化。
                </Text>
              </div>
            </Group>

            <Group gap="xs" wrap="wrap">
              <Select
                size="xs"
                aria-label="选择工具链目标环境"
                style={{ width: 170 }}
                allowDeselect={false}
                comboboxProps={{ withinPortal: true, zIndex: 500 }}
                value={envId}
                onChange={(val) => {
                  if (!val) return
                  setEnvId(val)
                  void inspect(val)
                }}
                data={targets.map((t) => ({ value: t.id, label: t.name }))}
              />
              <Button
                size="xs"
                variant="light"
                color="indigo"
                leftSection={<IconRocket size={13} />}
                onClick={() => setBootstrapOpen(true)}
              >
                一键初始化
              </Button>
              <Button
                size="xs"
                variant="default"
                leftSection={<IconNetwork size={13} />}
                onClick={openNetworkModal}
              >
                代理/镜像
              </Button>
              <Button
                size="xs"
                variant="default"
                leftSection={<IconRefresh size={13} />}
                loading={loading}
                onClick={() => void inspect(envId)}
              >
                重新体检
              </Button>
            </Group>
          </Group>

          <Group justify="space-between" align="center" wrap="wrap" gap="xs">
            <SegmentedControl
              size="xs"
              value={groupFilter}
              onChange={(val) => setGroupFilter(val as 'all' | 'core' | 'extra')}
              data={[
                { value: 'core', label: `核心工具 (${coreTools.length || 4})` },
                { value: 'extra', label: `扩展工具 (${extraTools.length || 5})` },
                { value: 'all', label: `全部 (${allToolItems.length || 9})` },
              ]}
            />
          </Group>

          {error && (
            <Text size="xs" c="red">
              {error}
            </Text>
          )}

          <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs">
            {displayedTools.map((tool) => (
              <Paper
                key={tool.id}
                p="sm"
                withBorder
                radius="md"
                style={{ background: 'var(--astr-surface-muted)' }}
              >
                <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
                  <div style={{ minWidth: 0 }}>
                    <Group gap={6} wrap="wrap">
                      <Text size="xs" fw={700}>
                        {tool.name}
                      </Text>
                      <Badge
                        size="xs"
                        variant="light"
                        color={tool.readiness === 'ready' ? 'teal' : tool.installed ? 'yellow' : 'orange'}
                        leftSection={tool.installed ? <IconCheck size={10} /> : undefined}
                      >
                        {tool.installed ? (tool.version ? `v${tool.version}` : '已安装') : '未安装'}
                      </Badge>
                      {tool.installed && tool.in_path === false && (
                        <Badge size="xs" variant="outline" color="yellow">
                          未在 PATH
                        </Badge>
                      )}
                    </Group>
                    <Text size="11px" c="dimmed" mt={3}>
                      {tool.description}
                    </Text>
                    {tool.executable && (
                      <Text size="10px" c="dimmed" ff="monospace" mt={3} truncate title={tool.executable}>
                        {tool.executable}
                      </Text>
                    )}
                    {tool.path_hint && (
                      <Text size="10px" c="orange" mt={2}>
                        {tool.path_hint}
                      </Text>
                    )}
                  </div>
                  <Stack gap={4} style={{ flexShrink: 0 }}>
                    <Button
                      size="compact-xs"
                      variant={tool.installed ? 'default' : 'light'}
                      color={tool.installed ? 'gray' : 'indigo'}
                      leftSection={
                        tool.recipes?.[0]?.mode === 'interactive' ? (
                          <IconTerminal2 size={12} />
                        ) : (
                          <IconDownload size={12} />
                        )
                      }
                      onClick={() => setInstallTarget(tool)}
                    >
                      {tool.installed ? '重装 / 升级' : '辅助安装'}
                    </Button>
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      leftSection={<IconPlugConnected size={12} />}
                      onClick={() => {
                        setPathTarget(tool)
                        setPathDraft(tool.custom_executable || tool.executable || '')
                      }}
                    >
                      指定路径
                    </Button>
                  </Stack>
                </Group>
              </Paper>
            ))}
          </SimpleGrid>
        </Stack>
      </Card>

      <ToolchainInstallModal
        opened={Boolean(installTarget)}
        onClose={() => setInstallTarget(null)}
        environmentId={envId}
        environmentName={envName}
        item={installTarget}
        allItems={report?.items || []}
        onCompleted={(nextReport) => {
          if (nextReport) {
            setReport(nextReport)
            onReportUpdated?.(envId, nextReport)
          } else {
            void inspect(envId)
          }
        }}
      />

      <EnvironmentBootstrapModal
        opened={bootstrapOpen}
        initialEnvironmentId={envId}
        onClose={() => setBootstrapOpen(false)}
        onCompleted={(targetEnv, nextReport) => {
          if (targetEnv === envId && nextReport) {
            setReport(nextReport)
            onReportUpdated?.(envId, nextReport)
          } else if (targetEnv === envId) {
            void inspect(envId)
          }
        }}
      />

      <Modal
        opened={networkOpen}
        onClose={() => setNetworkOpen(false)}
        centered
        size="md"
        zIndex={450}
        title={<Text fw={600} size="sm">工具与 Agent 安装网络代理 / 镜像设置</Text>}
      >
        <Stack gap="sm">
          <Text size="xs" c="dimmed">
            持久化保存后，所有后台安装任务（含本机、WSL 与 SSH 批量初始化）都将自动带入以下网络环境变量。
          </Text>
          <TextInput
            size="xs"
            label="默认 HTTP / HTTPS 代理"
            placeholder="例如 http://127.0.0.1:7890（留空则不使用代理）"
            value={netProxyDraft}
            onChange={(e) => setNetProxyDraft(e.currentTarget.value)}
          />
          <Select
            size="xs"
            label="默认包源镜像（npm / uv）"
            placeholder="默认官方源"
            clearable
            comboboxProps={{ withinPortal: true, zIndex: 520 }}
            value={netMirrorDraft || null}
            onChange={(v) => setNetMirrorDraft(v || '')}
            data={[{ value: 'cn', label: '国内常用镜像（npmmirror / 清华 PyPI）' }]}
          />
          <Group justify="flex-end" gap="xs" mt="xs">
            <Button size="xs" variant="default" onClick={() => setNetworkOpen(false)}>
              取消
            </Button>
            <Button size="xs" color="indigo" loading={savingNetwork} onClick={() => void handleSaveNetwork()}>
              保存默认设置
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal
        opened={Boolean(pathTarget)}
        onClose={() => setPathTarget(null)}
        centered
        size="md"
        zIndex={450}
        title={<Text fw={600} size="sm">指定 {pathTarget?.name} 可执行文件路径</Text>}
      >
        <Stack gap="sm">
          <Text size="xs" c="dimmed">
            留空并保存可恢复默认 PATH 自动探测。保存前将自动执行版本校验。
          </Text>
          <Group align="flex-end" gap="xs" wrap="nowrap">
            <TextInput
              style={{ flex: 1 }}
              size="xs"
              label={`路径（${envName}）`}
              placeholder="输入可执行文件绝对路径，或留空自动探测"
              value={pathDraft}
              onChange={(e) => setPathDraft(e.currentTarget.value)}
            />
            {hasNativePicker && (
              <Button
                size="xs"
                variant="default"
                leftSection={<IconFolderOpen size={14} />}
                onClick={() => void handleBrowsePath()}
              >
                浏览
              </Button>
            )}
          </Group>
          <Group justify="flex-end" gap="xs">
            <Button size="xs" variant="default" onClick={() => setPathTarget(null)}>
              取消
            </Button>
            <Button size="xs" color="indigo" loading={savingPath} onClick={() => void handleSaveToolPath()}>
              校验并保存
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  )
}
