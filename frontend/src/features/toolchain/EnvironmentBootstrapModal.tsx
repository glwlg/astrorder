import { useEffect, useMemo, useRef, useState } from 'react'
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
  SegmentedControl,
  Select,
  SimpleGrid,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconDownload,
  IconRefresh,
  IconRocket,
  IconTerminal2,
} from '@tabler/icons-react'
import {
  api,
  type ToolchainItemStatus,
  type ToolchainNetworkSettings,
  type ToolchainReport,
  type ToolchainTarget,
} from '../../api/client'
import { useBackgroundTasks } from '../../state/backgroundTasks'
import { trackToolchainInstallJob } from '../../state/toolchainTasks'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { EmbeddedTerminalPane } from './EmbeddedTerminalPane'

interface EnvironmentBootstrapModalProps {
  opened: boolean
  initialEnvironmentId?: string
  onClose: () => void
  onCompleted?: (envId: string, report?: ToolchainReport) => void
}

export function EnvironmentBootstrapModal({
  opened,
  initialEnvironmentId = 'local',
  onClose,
  onCompleted,
}: EnvironmentBootstrapModalProps) {
  const isAgentEnabled = usePluginSettingsStore((s) => s.isAgentEnabled)
  const [targets, setTargets] = useState<ToolchainTarget[]>([{ id: 'local', kind: 'local', name: '本机' }])
  const [envId, setEnvId] = useState(initialEnvironmentId)
  const [loading, setLoading] = useState(false)
  const [report, setReport] = useState<ToolchainReport | null>(null)
  const [selectedIds, setSelectedIds] = useState<Record<string, boolean>>({})
  const [mode, setMode] = useState<'background' | 'interactive'>('background')
  const [proxyUrl, setProxyUrl] = useState('')
  const [mirror, setMirror] = useState('')
  const [saveNetwork, setSaveNetwork] = useState(false)
  const [starting, setStarting] = useState(false)
  const [jobId, setJobId] = useState<string | null>(null)
  const logRef = useRef<HTMLPreElement | null>(null)

  const task = useBackgroundTasks((s) => (jobId ? s.tasks[jobId] : undefined))
  const running = starting || task?.status === 'running'
  const logOutput = task?.log || ''

  const loadEnv = async (nextEnv: string) => {
    setLoading(true)
    try {
      const [targetsRes, statusRes, netRes] = await Promise.all([
        api.getToolchainTargets().catch(() => ({ items: [{ id: 'local', kind: 'local' as const, name: '本机' }] })),
        api.getToolchainStatus(nextEnv),
        api.getToolchainNetwork().catch((): ToolchainNetworkSettings => ({ default: { proxy_url: '', mirror: '' }, by_environment: {} })),
      ])
      if (targetsRes.items?.length) setTargets(targetsRes.items)
      setReport(statusRes)

      const envNet = netRes.by_environment?.[nextEnv]
      setProxyUrl(envNet?.proxy_url || netRes.default?.proxy_url || '')
      setMirror(envNet?.mirror || netRes.default?.mirror || '')

      // Preselect missing core tools and missing enabled agents
      const defaults: Record<string, boolean> = {}
      for (const item of statusRes.items || []) {
        if (item.installed) continue
        if (item.kind === 'tool' && item.group === 'core') {
          defaults[item.id] = true
        } else if (item.kind === 'agent' && isAgentEnabled(item.id)) {
          defaults[item.id] = true
        }
      }
      setSelectedIds(defaults)
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '读取环境工具链状态失败',
      })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!opened) return
    setEnvId(initialEnvironmentId)
    setJobId(null)
    setMode('background')
    void loadEnv(initialEnvironmentId)
  }, [opened, initialEnvironmentId])

  useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [logOutput])

  const items = report?.items || []
  const coreTools = useMemo(() => items.filter((i) => i.kind === 'tool' && i.group !== 'extra'), [items])
  const extraTools = useMemo(() => items.filter((i) => i.kind === 'tool' && i.group === 'extra'), [items])
  const agentItems = useMemo(() => items.filter((i) => i.kind === 'agent'), [items])

  const chosenItems = useMemo(
    () => items.filter((item) => selectedIds[item.id]),
    [items, selectedIds],
  )

  const combinedInteractiveCommand = useMemo(() => {
    return chosenItems
      .map((item) => item.recipes?.[0]?.command)
      .filter((cmd): cmd is string => Boolean(cmd))
      .join(' && ')
  }, [chosenItems])

  const toggle = (id: string, checked: boolean) => {
    setSelectedIds((prev) => ({ ...prev, [id]: checked }))
  }

  const selectMissingCore = () => {
    const next: Record<string, boolean> = {}
    for (const item of coreTools) {
      if (!item.installed) next[item.id] = true
    }
    setSelectedIds(next)
  }

  const handleStartBatch = async () => {
    if (running || chosenItems.length === 0) return
    setStarting(true)
    try {
      const envName = targets.find((t) => t.id === envId)?.name || envId
      const job = await api.startToolchainInstallJob({
        environment_id: envId,
        tool_ids: chosenItems.map((i) => i.id),
        include_deps: true,
        proxy_url: proxyUrl.trim() || undefined,
        mirror: mirror.trim() || undefined,
        save_network: saveNetwork,
        title: `环境初始化（${envName} · ${chosenItems.length} 项）`,
      })
      setJobId(job.id)
      trackToolchainInstallJob(job, (finished) => {
        if (finished.report) {
          setReport(finished.report)
          onCompleted?.(envId, finished.report)
        } else {
          void loadEnv(envId)
          onCompleted?.(envId)
        }
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '启动批量初始化失败',
      })
    } finally {
      setStarting(false)
    }
  }

  const envName = targets.find((t) => t.id === envId)?.name || envId

  const renderCheckItem = (item: ToolchainItemStatus) => {
    const checked = Boolean(selectedIds[item.id])
    return (
      <Paper
        key={item.id}
        p="xs"
        withBorder
        radius="sm"
        style={{ background: checked ? 'color-mix(in oklab, var(--astr-accent) 6%, var(--astr-surface))' : 'var(--astr-surface-muted)' }}
      >
        <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
          <Checkbox
            size="xs"
            checked={checked}
            disabled={running}
            onChange={(e) => toggle(item.id, e.currentTarget.checked)}
            label={
              <div>
                <Group gap={6}>
                  <Text size="xs" fw={600}>{item.name}</Text>
                  <Badge
                    size="xs"
                    variant="light"
                    color={item.readiness === 'ready' ? 'teal' : item.installed ? 'yellow' : 'orange'}
                  >
                    {item.installed ? (item.version ? `v${item.version}` : '已装') : '未安装'}
                  </Badge>
                </Group>
                {item.dependencies?.length ? (
                  <Text size="10px" c="dimmed">
                    前置依赖：{item.dependencies.join('、')}
                  </Text>
                ) : null}
              </div>
            }
          />
        </Group>
      </Paper>
    )
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      size="xl"
      radius="md"
      zIndex={440}
      title={
        <Group gap="xs">
          <IconRocket size={18} style={{ color: 'var(--astr-indigo)' }} />
          <div>
            <Text fw={600} size="sm">一键环境初始化向导</Text>
            <Text size="11px" c="dimmed">勾选需要的工具与 Agent，按依赖拓扑顺序一次性完成环境准备</Text>
          </div>
        </Group>
      }
    >
      <Stack gap="sm">
        <Group justify="space-between" align="flex-end" wrap="wrap" gap="xs">
          <Group gap="xs" align="flex-end">
            <Select
              size="xs"
              label="目标环境"
              style={{ minWidth: 220 }}
              allowDeselect={false}
              comboboxProps={{ withinPortal: true, zIndex: 510 }}
              value={envId}
              disabled={running}
              onChange={(val) => {
                if (!val) return
                setEnvId(val)
                setJobId(null)
                void loadEnv(val)
              }}
              data={targets.map((t) => ({ value: t.id, label: t.name }))}
            />
            <Button
              size="xs"
              variant="default"
              leftSection={<IconRefresh size={13} />}
              loading={loading}
              onClick={() => void loadEnv(envId)}
            >
              重新体检
            </Button>
          </Group>
          <Group gap="xs">
            <Button size="compact-xs" variant="subtle" onClick={selectMissingCore} disabled={running}>
              勾选缺失核心工具
            </Button>
            <Button size="compact-xs" variant="subtle" color="gray" onClick={() => setSelectedIds({})} disabled={running}>
              清空
            </Button>
          </Group>
        </Group>

        {loading && !items.length ? (
          <Group justify="center" py="lg">
            <Loader size="sm" />
            <Text size="xs" c="dimmed">正在扫描 {envName} 环境…</Text>
          </Group>
        ) : (
          <>
            <Stack gap="xs">
              <Text size="xs" fw={700} c="dimmed">核心工具（Agent / MCP 强依赖）</Text>
              <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs">
                {coreTools.map(renderCheckItem)}
              </SimpleGrid>

              <Text size="xs" fw={700} c="dimmed" mt={4}>扩展工具（可选开发利器）</Text>
              <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="xs">
                {extraTools.map(renderCheckItem)}
              </SimpleGrid>

              <Text size="xs" fw={700} c="dimmed" mt={4}>原生 Agent 引擎</Text>
              <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="xs">
                {agentItems.map(renderCheckItem)}
              </SimpleGrid>
            </Stack>

            <Divider />

            <Group justify="space-between" align="center" wrap="wrap" gap="xs">
              <SegmentedControl
                size="xs"
                value={mode}
                onChange={(v) => setMode(v as 'background' | 'interactive')}
                data={[
                  { label: '后台批量自动安装', value: 'background' },
                  { label: '内置交互终端执行', value: 'interactive' },
                ]}
              />
              <Badge size="sm" variant="light" color="indigo">
                已选择 {chosenItems.length} 项
              </Badge>
            </Group>

            {mode === 'background' && (
              <>
                <Group grow gap="xs" align="flex-end">
                  <TextInput
                    size="xs"
                    label="网络代理（自动带入默认设置）"
                    placeholder="如 http://127.0.0.1:7890"
                    value={proxyUrl}
                    disabled={running}
                    onChange={(e) => setProxyUrl(e.currentTarget.value)}
                  />
                  <Select
                    size="xs"
                    label="包源镜像"
                    placeholder="默认官方源"
                    clearable
                    comboboxProps={{ withinPortal: true, zIndex: 510 }}
                    value={mirror || null}
                    disabled={running}
                    onChange={(v) => setMirror(v || '')}
                    data={[{ value: 'cn', label: '国内常用镜像（npmmirror / 清华 PyPI）' }]}
                  />
                </Group>
                <Checkbox
                  size="xs"
                  checked={saveNetwork}
                  disabled={running}
                  onChange={(e) => setSaveNetwork(e.currentTarget.checked)}
                  label="将以上代理与镜像保存为默认安装网络配置"
                />

                {jobId && (
                  <Paper p="sm" withBorder radius="sm" style={{ background: '#090d16', maxHeight: 260, overflowY: 'auto' }}>
                    <pre
                      ref={logRef}
                      style={{
                        margin: 0,
                        fontSize: 11.5,
                        fontFamily: 'monospace',
                        color: task?.status === 'failed' ? '#fca5a5' : '#86efac',
                        whiteSpace: 'pre-wrap',
                        wordBreak: 'break-all',
                      }}
                    >
                      {logOutput || (running ? '正在执行批量初始化…' : task?.detail || '')}
                    </pre>
                  </Paper>
                )}
              </>
            )}

            {mode === 'interactive' && (
              chosenItems.length > 0 ? (
                <EmbeddedTerminalPane
                  environmentId={envId}
                  environmentLabel={envName}
                  command={combinedInteractiveCommand}
                  height={280}
                  onVerify={() => void loadEnv(envId)}
                  verifying={loading}
                />
              ) : (
                <Alert color="gray" variant="light">请先勾选至少一项要安装的工具或 Agent。</Alert>
              )
            )}
          </>
        )}

        <Group justify="space-between" align="center" pt={4}>
          <Text size="xs" c="dimmed">
            {running ? '关闭弹窗后将在后台继续执行，完成后自动刷新检测结果。' : '批量安装会自动补齐缺失的前置依赖，已安装项如需升级也可重新勾选。'}
          </Text>
          <Group gap="xs">
            {mode === 'background' && (
              running && jobId ? (
                <Button size="xs" variant="light" color="red" onClick={() => void api.cancelToolchainInstallJob(jobId)}>
                  取消任务
                </Button>
              ) : (
                <Button
                  size="xs"
                  color="indigo"
                  leftSection={<IconDownload size={14} />}
                  disabled={chosenItems.length === 0}
                  loading={starting}
                  onClick={() => void handleStartBatch()}
                >
                  开始批量初始化（{chosenItems.length}）
                </Button>
              )
            )}
            {mode === 'interactive' && (
              <Badge size="xs" variant="light" color="teal" leftSection={<IconTerminal2 size={11} />}>
                支持 sudo / 交互确认
              </Badge>
            )}
            <Button size="xs" variant="default" onClick={onClose}>
              {running ? '放入后台' : '关闭'}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
