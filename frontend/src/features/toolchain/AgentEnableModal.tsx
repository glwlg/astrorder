import { useContext, useEffect, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Divider,
  Group,
  Loader,
  Modal,
  Paper,
  Select,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconDownload,
  IconFolderOpen,
  IconPlugConnected,
  IconRefresh,
  IconTerminal2,
} from '@tabler/icons-react'
import { QueryClientContext } from '@tanstack/react-query'
import {
  api,
  type ToolchainItemStatus,
  type ToolchainReport,
  type ToolchainTarget,
  type ToolchainValidateResult,
} from '../../api/client'
import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import { agentPluginIdForKind, type ManagedAgentKind } from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { ToolchainInstallModal } from './ToolchainInstallModal'

interface AgentEnableModalProps {
  opened: boolean
  kind: ManagedAgentKind | null
  initialEnvironmentId?: string
  configureOnly?: boolean
  onClose: () => void
  onEnabled?: () => void
}

export function AgentEnableModal({
  opened,
  kind,
  initialEnvironmentId = 'local',
  configureOnly = false,
  onClose,
  onEnabled,
}: AgentEnableModalProps) {
  const queryClient = useContext(QueryClientContext)
  const togglePlugin = usePluginSettingsStore((s) => s.togglePlugin)

  const [targets, setTargets] = useState<ToolchainTarget[]>([{ id: 'local', kind: 'local', name: '本机' }])
  const [envId, setEnvId] = useState(initialEnvironmentId)
  const [loading, setLoading] = useState(false)
  const [report, setReport] = useState<ToolchainReport | null>(null)
  const [error, setError] = useState('')

  const [manualPath, setManualPath] = useState('')
  const [validating, setValidating] = useState(false)
  const [validation, setValidation] = useState<ToolchainValidateResult | null>(null)
  const [savingPath, setSavingPath] = useState(false)
  const [autoConnect, setAutoConnect] = useState(true)
  const [configureCli, setConfigureCli] = useState(true)
  const [enabling, setEnabling] = useState(false)
  const [daemonNotice, setDaemonNotice] = useState('')

  const [installOpen, setInstallOpen] = useState(false)
  const [installAuthMode, setInstallAuthMode] = useState(false)

  const item: ToolchainItemStatus | null = report?.items.find((entry) => entry.id === kind) || null

  const refresh = async (targetEnv = envId) => {
    if (!kind) return
    setLoading(true)
    setError('')
    try {
      const [targetRes, statusRes] = await Promise.all([
        api.getToolchainTargets().catch(() => ({ items: [{ id: 'local', kind: 'local' as const, name: '本机' }] })),
        api.getToolchainStatus(targetEnv),
      ])
      if (targetRes.items?.length) setTargets(targetRes.items)
      setReport(statusRes)
      const matched = statusRes.items.find((entry) => entry.id === kind)
      if (matched) {
        setManualPath(matched.custom_executable || matched.executable || '')
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '环境检测失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!opened || !kind) return
    setEnvId(initialEnvironmentId)
    setValidation(null)
    setDaemonNotice('')
    setInstallOpen(false)
    setInstallAuthMode(false)
    void refresh(initialEnvironmentId)
  }, [opened, kind, initialEnvironmentId])

  const handleBrowseLocalFile = async () => {
    const desktop = (window as unknown as {
      astrorderDesktop?: { openFileDialog?: (defaultPath?: string) => Promise<string> }
    }).astrorderDesktop
    if (!desktop?.openFileDialog) return
    try {
      const chosen = await desktop.openFileDialog(manualPath || '')
      if (chosen) {
        setManualPath(chosen)
        setValidation(null)
        await handleValidatePath(chosen)
      }
    } catch {
      // User cancelled native file picker
    }
  }

  const handleValidatePath = async (overridePath?: string) => {
    if (!kind) return
    const targetPath = (overridePath ?? manualPath).trim()
    if (!targetPath) {
      setValidation({ valid: false, error: '请先输入或选择可执行文件路径' })
      return
    }
    setValidating(true)
    try {
      const res = await api.validateToolchainExecutable({
        environment_id: envId,
        tool_id: kind,
        path: targetPath,
      })
      setValidation(res)
      if (res.valid && res.resolved_path) {
        setManualPath(res.resolved_path)
      }
    } catch (err) {
      setValidation({
        valid: false,
        error: err instanceof Error ? err.message : '校验可执行文件失败',
      })
    } finally {
      setValidating(false)
    }
  }

  const handleSaveCustomPath = async (alsoEnable: boolean) => {
    if (!kind) return
    const trimmed = manualPath.trim()
    if (!trimmed) return
    setSavingPath(true)
    setDaemonNotice('')
    try {
      const res = await api.saveToolchainExecutable({
        environment_id: envId,
        tool_id: kind,
        path: trimmed,
      })
      notifications.show({ color: 'teal', message: res.message })
      if (res.requires_daemon_restart) {
        setDaemonNotice('已保存路径并写入 session-daemon.json。当前小内核启动时尚未加载该路径，请在空闲时通过系统托盘「小内核 → 重启小内核」使新路径生效。')
      }
      await refresh(envId)
      if (alsoEnable && !configureOnly) {
        await finalizeEnable(res.requires_daemon_restart)
      }
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存路径失败',
      })
    } finally {
      setSavingPath(false)
    }
  }

  const finalizeEnable = async (needsDaemonRestart = false) => {
    if (!kind) return
    const pluginId = agentPluginIdForKind(kind)
    if (!pluginId) return
    setEnabling(true)
    try {
      togglePlugin(pluginId, true)
      if (autoConnect && !needsDaemonRestart && item?.registered_in_daemon !== false) {
        const connTarget = envId.startsWith('ssh:') ? envId.slice(4) : envId
        if (connTarget === 'local' || envId.startsWith('ssh:')) {
          try {
            await api.changeEnvironmentAgent(connTarget, kind, true)
            if (configureCli && typeof api.getAgents === 'function' && typeof api.configureAgentSessionCLI === 'function') {
              const agentsRes = await api.getAgents()
              const matched = agentsRes.items.find(
                (ag) =>
                  ag.kind === kind &&
                  (connTarget === 'local' ? !ag.connection_id || ag.connection_id === 'local' : ag.connection_id === connTarget),
              )
              if (matched?.id) {
                await api.configureAgentSessionCLI(matched.id)
              }
            }
          } catch {
            // Non-fatal: plugin is enabled even if user still needs to login or reload daemon
          }
        }
      }
      await queryClient?.invalidateQueries({ queryKey: ['astrorder'] })
      notifications.show({
        color: 'teal',
        message: `已启用 ${agentKindLabel(kind)} 插件`,
      })
      onEnabled?.()
      if (!needsDaemonRestart) {
        onClose()
      }
    } finally {
      setEnabling(false)
    }
  }

  const hasNativePicker =
    envId === 'local' &&
    typeof window !== 'undefined' &&
    Boolean((window as unknown as { astrorderDesktop?: { openFileDialog?: unknown } }).astrorderDesktop?.openFileDialog)

  const envName = targets.find((t) => t.id === envId)?.name || envId

  if (!kind) return null

  return (
    <>
      <Modal
        opened={opened}
        onClose={onClose}
        size="lg"
        radius="md"
        zIndex={430}
        title={
          <Group gap="sm">
            <AgentBrandIcon kind={kind} size={22} />
            <div>
              <Text fw={600} size="sm">
                {configureOnly ? `配置 ${agentKindLabel(kind)}` : `启用 ${agentKindLabel(kind)} 插件`}
              </Text>
              <Text size="11px" c="dimmed">
                自动检测可执行文件，或引导安装 / 手动指定已安装路径
              </Text>
            </div>
          </Group>
        }
      >
        <Stack gap="md">
          <Group justify="space-between" align="flex-end" wrap="wrap" gap="xs">
            <Select
              size="xs"
              label="检查目标环境"
              style={{ minWidth: 220 }}
              allowDeselect={false}
              comboboxProps={{ withinPortal: true, zIndex: 500 }}
              value={envId}
              onChange={(val) => {
                if (!val) return
                setEnvId(val)
                setValidation(null)
                setDaemonNotice('')
                void refresh(val)
              }}
              data={targets.map((t) => ({ value: t.id, label: t.name }))}
            />
            <Button
              size="xs"
              variant="default"
              leftSection={<IconRefresh size={14} />}
              loading={loading}
              onClick={() => void refresh(envId)}
            >
              重新检测
            </Button>
          </Group>

          {error && <Alert color="red" variant="light">{error}</Alert>}

          {loading && !item ? (
            <Group justify="center" py="lg">
              <Loader size="sm" />
              <Text size="xs" c="dimmed">正在检测 {envName} 上的 {agentKindLabel(kind)}…</Text>
            </Group>
          ) : item ? (
            <>
              <Paper p="sm" withBorder radius="md" style={{ background: 'var(--astr-surface-muted)' }}>
                <Group justify="space-between" align="flex-start" wrap="nowrap">
                  <div style={{ minWidth: 0 }}>
                    <Group gap="xs">
                      <Text size="sm" fw={600}>{item.name}</Text>
                      <Badge
                        size="xs"
                        color={item.readiness === 'ready' ? 'teal' : item.installed ? 'yellow' : 'orange'}
                        variant="light"
                      >
                        {item.readiness === 'ready' ? '已就绪' : item.installed ? '已安装 · 待配置/登录' : '未检测到'}
                      </Badge>
                      {item.version && (
                        <Badge size="xs" color="gray" variant="outline">
                          v{item.version}
                        </Badge>
                      )}
                      {item.installed && item.in_path === false && (
                        <Badge size="xs" color="yellow" variant="outline">
                          未在 PATH 中
                        </Badge>
                      )}
                    </Group>
                    <Text size="xs" c="dimmed" mt={4} style={{ wordBreak: 'break-all' }}>
                      {item.installed ? `可执行路径：${item.executable}` : item.detail || '当前环境 PATH 与常见目录中未发现该 Agent'}
                    </Text>
                    {item.readiness_detail && item.installed && (
                      <Text size="11px" c={item.readiness === 'ready' ? 'teal' : 'yellow'} mt={3}>
                        {item.readiness_detail}
                      </Text>
                    )}
                    {item.path_hint && (
                      <Text size="11px" c="orange" mt={2}>
                        PATH 提示：{item.path_hint}
                      </Text>
                    )}
                  </div>
                  {item.installed && item.auth_command && (
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      leftSection={<IconTerminal2 size={13} />}
                      onClick={() => {
                        setInstallAuthMode(true)
                        setInstallOpen(true)
                      }}
                    >
                      终端登录
                    </Button>
                  )}
                </Group>
              </Paper>

              {(daemonNotice || item.requires_daemon_restart) && (
                <Alert color="yellow" variant="light" radius="md">
                  <Text size="xs">
                    {daemonNotice ||
                      '检测到本机新增或变更了 Agent 路径；大内核已写入 session-daemon.json，请在空闲时通过系统托盘「小内核 → 重启小内核」完成加载。'}
                  </Text>
                </Alert>
              )}

              {!item.installed && (
                <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
                  <Paper p="sm" withBorder radius="md">
                    <Stack gap="xs" h="100%" justify="space-between">
                      <div>
                        <Text size="xs" fw={700}>方式一：辅助安装</Text>
                        <Text size="xs" c="dimmed" mt={4}>
                          {item.missing_dependencies?.length
                            ? `将自动串联安装缺失的前置工具（${item.missing_dependencies.join('、')}）及 ${item.name}，支持后台日志与交互终端双模式。`
                            : `通过官方渠道引导安装 ${item.name}，支持代理/镜像配置、后台实时日志与交互终端切换。`}
                        </Text>
                      </div>
                      <Button
                        size="xs"
                        color="indigo"
                        leftSection={<IconDownload size={14} />}
                        onClick={() => {
                          setInstallAuthMode(false)
                          setInstallOpen(true)
                        }}
                      >
                        进入辅助安装
                      </Button>
                    </Stack>
                  </Paper>

                  <Paper p="sm" withBorder radius="md">
                    <Stack gap="xs" h="100%" justify="space-between">
                      <div>
                        <Text size="xs" fw={700}>方式二：指定已安装路径</Text>
                        <Text size="xs" c="dimmed" mt={4}>
                          如果已经装在自定义目录、虚拟环境或 nvm/fnm 目录中，直接选择可执行文件并实时校验即可。
                        </Text>
                      </div>
                      {hasNativePicker && (
                        <Button
                          size="xs"
                          variant="default"
                          leftSection={<IconFolderOpen size={14} />}
                          onClick={() => void handleBrowseLocalFile()}
                        >
                          选择本机可执行文件
                        </Button>
                      )}
                    </Stack>
                  </Paper>
                </SimpleGrid>
              )}

              <Divider label="自定义可执行文件路径（选填）" labelPosition="left" />

              <Stack gap="xs">
                <Group align="flex-end" gap="xs" wrap="nowrap">
                  <TextInput
                    style={{ flex: 1 }}
                    size="xs"
                    label="可执行文件绝对路径"
                    placeholder={envId === 'local' ? '例如 C:\\Users\\...\\codex.cmd 或 /usr/local/bin/codex' : '例如 /home/user/.local/bin/hermes'}
                    value={manualPath}
                    onChange={(e) => {
                      setManualPath(e.currentTarget.value)
                      setValidation(null)
                    }}
                  />
                  {hasNativePicker && (
                    <Button
                      size="xs"
                      variant="default"
                      leftSection={<IconFolderOpen size={14} />}
                      onClick={() => void handleBrowseLocalFile()}
                    >
                      浏览
                    </Button>
                  )}
                  <Button
                    size="xs"
                    variant="light"
                    loading={validating}
                    onClick={() => void handleValidatePath()}
                  >
                    校验
                  </Button>
                </Group>

                {validation && (
                  <Alert color={validation.valid ? 'teal' : 'red'} variant="light" p="xs">
                    <Text size="xs">
                      {validation.valid
                        ? `校验通过${validation.version ? `（版本 ${validation.version}）` : ''}：${validation.resolved_path}`
                        : `校验失败：${validation.error}`}
                    </Text>
                  </Alert>
                )}

                <Group justify="space-between" align="flex-start" mt={4} wrap="wrap" gap="xs">
                  {!configureOnly ? (
                    <Stack gap={4}>
                      <Checkbox
                        size="xs"
                        checked={autoConnect}
                        onChange={(e) => setAutoConnect(e.currentTarget.checked)}
                        label="启用插件后自动连接该环境 Agent"
                      />
                      <Checkbox
                        size="xs"
                        checked={configureCli}
                        disabled={!autoConnect}
                        onChange={(e) => setConfigureCli(e.currentTarget.checked)}
                        label="同时配置星序会话 CLI 与技能"
                      />
                    </Stack>
                  ) : (
                    <Button
                      size="xs"
                      variant="subtle"
                      leftSection={<IconDownload size={13} />}
                      onClick={() => {
                        setInstallAuthMode(false)
                        setInstallOpen(true)
                      }}
                    >
                      重新安装 / 终端登录
                    </Button>
                  )}

                  <Group gap="xs">
                    {manualPath.trim() && manualPath.trim() !== (item.executable || '') && (
                      <Button
                        size="xs"
                        variant="light"
                        color="indigo"
                        loading={savingPath}
                        onClick={() => void handleSaveCustomPath(!configureOnly)}
                      >
                        {configureOnly ? '保存路径' : '保存路径并启用'}
                      </Button>
                    )}
                    {!configureOnly && (
                      <Button
                        size="xs"
                        color="indigo"
                        leftSection={item.installed ? <IconCheck size={14} /> : <IconPlugConnected size={14} />}
                        loading={enabling}
                        onClick={() => void finalizeEnable(Boolean(item.requires_daemon_restart))}
                      >
                        {item.installed ? '确认启用' : '仍要启用（稍后安装）'}
                      </Button>
                    )}
                    <Button size="xs" variant="default" onClick={onClose}>
                      取消
                    </Button>
                  </Group>
                </Group>
              </Stack>
            </>
          ) : null}
        </Stack>
      </Modal>

      <ToolchainInstallModal
        opened={installOpen}
        onClose={() => setInstallOpen(false)}
        environmentId={envId}
        environmentName={envName}
        item={item}
        allItems={report?.items || []}
        initialMode={installAuthMode ? 'interactive' : undefined}
        initialCommandOverride={installAuthMode && item?.auth_command ? item.auth_command : undefined}
        onCompleted={(nextReport) => {
          if (nextReport) {
            setReport(nextReport)
          } else {
            void refresh(envId)
          }
        }}
      />
    </>
  )
}
