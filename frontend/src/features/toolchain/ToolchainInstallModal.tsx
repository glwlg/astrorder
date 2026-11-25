import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Checkbox,
  Collapse,
  Group,
  Modal,
  Paper,
  SegmentedControl,
  Select,
  Stack,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconCopy,
  IconDownload,
  IconExternalLink,
  IconRefresh,
  IconTerminal2,
} from '@tabler/icons-react'
import { api, type ToolchainItemStatus, type ToolchainReport } from '../../api/client'
import { useBackgroundTasks } from '../../state/backgroundTasks'
import { trackToolchainInstallJob } from '../../state/toolchainTasks'
import { EmbeddedTerminalPane } from './EmbeddedTerminalPane'

interface ToolchainInstallModalProps {
  opened: boolean
  onClose: () => void
  environmentId: string
  environmentName?: string
  item: ToolchainItemStatus | null
  allItems?: ToolchainItemStatus[]
  initialMode?: 'background' | 'interactive'
  initialCommandOverride?: string
  onCompleted?: (report?: ToolchainReport) => void
}

export function ToolchainInstallModal({
  opened,
  onClose,
  environmentId,
  environmentName,
  item,
  allItems = [],
  initialMode,
  initialCommandOverride,
  onCompleted,
}: ToolchainInstallModalProps) {
  const recipes = item?.recipes ?? []
  const [recipeId, setRecipeId] = useState<string>(recipes[0]?.id || '')
  const activeRecipe = useMemo(
    () => recipes.find((r) => r.id === recipeId) || recipes[0] || null,
    [recipes, recipeId],
  )

  const [mode, setMode] = useState<'background' | 'interactive'>(
    initialMode || activeRecipe?.mode || 'background',
  )
  const [includeDeps, setIncludeDeps] = useState(true)
  const [commandDraft, setCommandDraft] = useState(initialCommandOverride || activeRecipe?.command || '')
  const [showNetwork, setShowNetwork] = useState(false)
  const [proxyUrl, setProxyUrl] = useState('')
  const [mirror, setMirror] = useState('')
  const [saveNetwork, setSaveNetwork] = useState(false)
  const [starting, setStarting] = useState(false)
  const [verifying, setVerifying] = useState(false)
  const [jobId, setJobId] = useState<string | null>(null)
  const [latestReport, setLatestReport] = useState<ToolchainReport | null>(null)
  const [terminalAuthMode, setTerminalAuthMode] = useState(false)
  const logPreRef = useRef<HTMLPreElement | null>(null)

  const task = useBackgroundTasks((s) => (jobId ? s.tasks[jobId] : undefined))
  const running = starting || task?.status === 'running'
  const logOutput = task?.log || ''

  useEffect(() => {
    if (!opened || !item) return
    const first = item.recipes?.[0]
    setRecipeId(first?.id || '')
    setMode(initialMode || first?.mode || 'background')
    setCommandDraft(initialCommandOverride || first?.command || '')
    setIncludeDeps(true)
    setSaveNetwork(false)
    setJobId(null)
    setLatestReport(null)
    setTerminalAuthMode(false)
    if (typeof api.getToolchainNetwork === 'function') {
      void api.getToolchainNetwork()
        .then((net) => {
          const envNet = net.by_environment?.[environmentId]
          setProxyUrl(envNet?.proxy_url || net.default?.proxy_url || '')
          setMirror(envNet?.mirror || net.default?.mirror || '')
        })
        .catch(() => {})
    }
  }, [opened, item?.id, environmentId, initialMode, initialCommandOverride])

  useEffect(() => {
    if (!activeRecipe || initialCommandOverride) return
    setCommandDraft(activeRecipe.command)
    if (!initialMode) {
      setMode(activeRecipe.mode)
    }
  }, [activeRecipe?.id])

  useEffect(() => {
    if (logPreRef.current) {
      logPreRef.current.scrollTop = logPreRef.current.scrollHeight
    }
  }, [logOutput])

  const missingDepItems = useMemo(() => {
    if (!item?.missing_dependencies?.length) return []
    const map = new Map(allItems.map((entry) => [entry.id, entry]))
    return item.missing_dependencies.map((id) => map.get(id)).filter((x): x is ToolchainItemStatus => Boolean(x))
  }, [item, allItems])

  const latestItem = useMemo(() => {
    if (!item) return null
    if (!latestReport) return item
    return latestReport.items.find((entry) => entry.id === item.id) || item
  }, [item, latestReport])

  const effectiveCommand = useMemo(() => {
    if (terminalAuthMode && item?.auth_command) {
      return item.auth_command
    }
    if (mode === 'interactive' && includeDeps && missingDepItems.length > 0) {
      const depCmds = missingDepItems
        .map((dep) => dep.recipes?.[0]?.command)
        .filter((cmd): cmd is string => Boolean(cmd))
      if (depCmds.length > 0) {
        return [...depCmds, commandDraft.trim()].filter(Boolean).join(' && ')
      }
    }
    return commandDraft.trim()
  }, [terminalAuthMode, item?.auth_command, mode, includeDeps, missingDepItems, commandDraft])

  const handleCopyCommand = async () => {
    if (!effectiveCommand) return
    try {
      await navigator.clipboard.writeText(effectiveCommand)
      notifications.show({ color: 'teal', message: '已复制安装命令到剪贴板' })
    } catch {
      notifications.show({ color: 'yellow', message: '复制失败，请手动选中命令复制' })
    }
  }

  const handleStartBackground = async () => {
    if (!item || running) return
    setStarting(true)
    try {
      const customCmd =
        activeRecipe && commandDraft.trim() === activeRecipe.command.trim() ? undefined : commandDraft.trim()
      const job = await api.startToolchainInstallJob({
        environment_id: environmentId,
        tool_ids: [item.id],
        recipe_id: activeRecipe?.id,
        custom_command: customCmd,
        include_deps: includeDeps,
        proxy_url: proxyUrl.trim() || undefined,
        mirror: mirror.trim() || undefined,
        save_network: saveNetwork,
      })
      setJobId(job.id)
      trackToolchainInstallJob(job, (finished) => {
        if (finished.report) {
          setLatestReport(finished.report)
          onCompleted?.(finished.report)
        } else {
          void handleVerify()
        }
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '启动安装任务失败',
      })
    } finally {
      setStarting(false)
    }
  }

  const handleVerify = async () => {
    if (!item) return
    setVerifying(true)
    try {
      const report = await api.getToolchainStatus(environmentId)
      setLatestReport(report)
      onCompleted?.(report)
      const matched = report.items.find((entry) => entry.id === item.id)
      if (matched?.installed) {
        notifications.show({
          color: 'teal',
          message: `已检测到 ${item.name}${matched.version ? ` (${matched.version})` : ''}`,
        })
      } else {
        notifications.show({
          color: 'yellow',
          message: `暂未检测到 ${item.name}，如刚完成安装请确认 PATH 或选择已安装路径`,
        })
      }
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '复检失败',
      })
    } finally {
      setVerifying(false)
    }
  }

  if (!item) return null

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      size="lg"
      radius="md"
      zIndex={460}
      title={
        <Group gap={8}>
          <IconDownload size={18} style={{ color: 'var(--astr-indigo)' }} />
          <Text fw={600} size="sm">
            {terminalAuthMode ? `${item.name} · 交互终端登录/初始化` : `辅助安装 ${item.name}`}
          </Text>
          <Badge size="xs" variant="light" color="gray">
            {environmentName || environmentId}
          </Badge>
          {latestItem?.installed && (
            <Badge size="xs" variant="light" color="teal" leftSection={<IconCheck size={11} />}>
              已安装{latestItem.version ? ` ${latestItem.version}` : ''}
            </Badge>
          )}
        </Group>
      }
    >
      <Stack gap="sm">
        {missingDepItems.length > 0 && !terminalAuthMode && (
          <Alert color="indigo" variant="light" radius="md">
            <Stack gap={6}>
              <Text size="xs" fw={600}>
                检测到缺失前置依赖：{missingDepItems.map((dep) => dep.name).join('、')}
              </Text>
              <Checkbox
                size="xs"
                checked={includeDeps}
                onChange={(e) => setIncludeDeps(e.currentTarget.checked)}
                label={`按顺序自动一并安装（${[...missingDepItems.map((d) => d.name), item.name].join(' → ')}）`}
              />
            </Stack>
          </Alert>
        )}

        {!terminalAuthMode && (
          <Group justify="space-between" align="center" wrap="wrap" gap="xs">
            <SegmentedControl
              size="xs"
              value={mode}
              onChange={(val) => setMode(val as 'background' | 'interactive')}
              data={[
                { label: '后台自动安装（日志）', value: 'background' },
                { label: '内置交互终端（PTY）', value: 'interactive' },
              ]}
            />
            {recipes.length > 1 && (
              <Select
                size="xs"
                style={{ minWidth: 220 }}
                aria-label="选择安装方案"
                allowDeselect={false}
                comboboxProps={{ withinPortal: true, zIndex: 520 }}
                value={activeRecipe?.id || null}
                onChange={(val) => val && setRecipeId(val)}
                data={recipes.map((r) => ({ value: r.id, label: r.label }))}
              />
            )}
          </Group>
        )}

        {activeRecipe?.note && !terminalAuthMode && (
          <Text size="xs" c="dimmed">
            {activeRecipe.note}
          </Text>
        )}

        <Paper p="xs" withBorder radius="sm" style={{ background: 'var(--astr-surface-muted)' }}>
          <Group justify="space-between" mb={4}>
            <Text size="xs" fw={600} c="dimmed">
              {terminalAuthMode ? '登录 / 初始化命令' : '将执行的官方安装命令（可预览或微调）'}
            </Text>
            <Group gap={6}>
              <Button
                size="compact-xs"
                variant="subtle"
                leftSection={<IconCopy size={12} />}
                onClick={() => void handleCopyCommand()}
              >
                复制命令
              </Button>
              {item.docs_url && (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  component="a"
                  href={item.docs_url}
                  target="_blank"
                  rel="noreferrer"
                  rightSection={<IconExternalLink size={12} />}
                >
                  官方文档
                </Button>
              )}
            </Group>
          </Group>
          <Textarea
            size="xs"
            autosize
            minRows={1}
            maxRows={4}
            styles={{ input: { fontFamily: 'monospace', fontSize: 12 } }}
            value={effectiveCommand}
            readOnly={terminalAuthMode || (mode === 'interactive' && includeDeps && missingDepItems.length > 0)}
            onChange={(e) => setCommandDraft(e.currentTarget.value)}
          />
        </Paper>

        {mode === 'background' && !terminalAuthMode && (
          <>
            <div>
              <Button
                size="compact-xs"
                variant="subtle"
                color="gray"
                rightSection={showNetwork ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
                onClick={() => setShowNetwork((v) => !v)}
              >
                网络代理与镜像设置{(proxyUrl || mirror) ? '（已加载默认配置）' : '（可选）'}
              </Button>
              <Collapse expanded={showNetwork}>
                <Stack gap="xs" mt="xs">
                  <Group grow gap="xs">
                    <TextInput
                      size="xs"
                      label="HTTP / HTTPS 代理"
                      placeholder="如 http://127.0.0.1:7890"
                      value={proxyUrl}
                      onChange={(e) => setProxyUrl(e.currentTarget.value)}
                    />
                    <Select
                      size="xs"
                      label="包源镜像（npm / uv）"
                      placeholder="默认官方源"
                      clearable
                      comboboxProps={{ withinPortal: true, zIndex: 520 }}
                      value={mirror || null}
                      onChange={(v) => setMirror(v || '')}
                      data={[
                        { value: 'cn', label: '国内常用镜像（npmmirror / 清华 PyPI）' },
                      ]}
                    />
                  </Group>
                  <Checkbox
                    size="xs"
                    checked={saveNetwork}
                    onChange={(e) => setSaveNetwork(e.currentTarget.checked)}
                    label="保存为默认网络代理与镜像（后续安装自动生效）"
                  />
                </Stack>
              </Collapse>
            </div>

            {jobId && (
              <Paper
                p="sm"
                withBorder
                radius="sm"
                style={{ background: '#090d16', maxHeight: 300, overflowY: 'auto' }}
              >
                <pre
                  ref={logPreRef}
                  style={{
                    margin: 0,
                    fontSize: 11.5,
                    fontFamily: 'monospace',
                    color: task?.status === 'failed' ? '#fca5a5' : '#86efac',
                    whiteSpace: 'pre-wrap',
                    wordBreak: 'break-all',
                  }}
                >
                  {logOutput || (running ? '正在启动安装进程…' : task?.detail || '等待日志输出…')}
                </pre>
              </Paper>
            )}
          </>
        )}

        {(mode === 'interactive' || terminalAuthMode) && (
          <EmbeddedTerminalPane
            environmentId={environmentId}
            environmentLabel={environmentName}
            command={effectiveCommand}
            onVerify={() => void handleVerify()}
            verifying={verifying}
          />
        )}

        {latestItem?.installed && item.auth_command && !terminalAuthMode && (
          <Alert color="teal" variant="light" radius="md">
            <Group justify="space-between" align="center" wrap="wrap" gap="xs">
              <div>
                <Text size="xs" fw={600}>
                  安装已就绪：{latestItem.executable}
                </Text>
                <Text size="xs" c="dimmed">
                  {item.auth_hint || `如需登录授权，可在交互终端执行 ${item.auth_command}`}
                </Text>
              </div>
              <Button
                size="xs"
                variant="light"
                color="teal"
                leftSection={<IconTerminal2 size={14} />}
                onClick={() => setTerminalAuthMode(true)}
              >
                打开终端执行 {item.auth_command}
              </Button>
            </Group>
          </Alert>
        )}

        <Group justify="space-between" align="center" pt={4}>
          <Text size="xs" c="dimmed">
            {running
              ? '关闭此弹窗后任务会继续在后台执行，可在右上角后台任务查看进度。'
              : mode === 'interactive'
                ? '如遇 sudo 密码、权限确认或登录提示，请直接在上方终端中交互输入。'
                : '如遇权限弹窗或交互提示，可随时切换至「内置交互终端」执行。'}
          </Text>
          <Group gap="xs">
            {mode === 'background' && !terminalAuthMode && (
              <>
                {running && jobId ? (
                  <Button
                    size="xs"
                    variant="light"
                    color="red"
                    onClick={() => void api.cancelToolchainInstallJob(jobId)}
                  >
                    取消安装
                  </Button>
                ) : (
                  <Button
                    size="xs"
                    color="indigo"
                    leftSection={<IconDownload size={14} />}
                    loading={starting}
                    onClick={() => void handleStartBackground()}
                  >
                    {jobId ? '重新安装' : '开始安装'}
                  </Button>
                )}
              </>
            )}
            <Button
              size="xs"
              variant="default"
              leftSection={<IconRefresh size={13} />}
              loading={verifying}
              onClick={() => void handleVerify()}
            >
              重新检测
            </Button>
            <Button size="xs" variant="default" onClick={onClose}>
              {running ? '放入后台' : '关闭'}
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
