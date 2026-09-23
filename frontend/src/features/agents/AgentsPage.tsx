import { Alert, Badge, Button, Checkbox, Group, Modal, Paper, SimpleGrid, Stack, Switch, Text, TextInput, Title } from '@mantine/core'
import { IconRefresh, IconTerminal } from '@tabler/icons-react'
import { useEffect, useRef, useState } from 'react'
import { useMediaQuery } from '@mantine/hooks'
import { useShallow } from 'zustand/react/shallow'
import { AgentStatusBadge } from '../../components/Status'
import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import type { Agent, SshConnection, SshConnectionSettings } from '../../domain/types'
import { SpotlightCard } from '../../components/animations/SpotlightCard'
import { useAstrorderStore } from '../../state/store'
import './agentsLayout.css'
import { EnvironmentConnections } from './EnvironmentConnections'
import { NativeObservationPanel } from '../../components/NativeObservationPanel'
import { api } from '../../api/client'
import { notifications } from '@mantine/notifications'
import { useBackgroundTasks } from '../../state/backgroundTasks'

const allCapabilities: { key: string; label: string }[] = [
  { key: 'chat', label: '对话交互' },
  { key: 'stop', label: '任务终止' },
  { key: 'queue', label: '原生排队' },
  { key: 'attachments', label: '多模态附件' },
  { key: 'approvals', label: '安全审批' },
  { key: 'launch', label: '会话拉起' },
  { key: 'history', label: '历史追溯' },
  { key: 'events', label: '实时事件' },
  { key: 'task_events', label: '子任务追踪' },
  { key: 'delete', label: '原生会话删除' },
]

const remoteStateLabels: Record<SshConnection['state'], string> = {
  unconfigured: '未配置',
  configured: '已配置',
  validated: '已验证',
  connecting: '连接中',
  connected: '已连接',
  disconnected: '已断开',
  error: '错误',
}

type SshForm = {
  display_name: string
  profile_name: string
  host: string
  port: string
  user: string
  ssh_config_alias: string
  identity_file: string
  hermes_path: string
  workspace: string
}

type SshPhase = 'basic' | 'identity' | 'deploy'

function sshIdentityKey(form: SshForm): string {
  return [
    form.profile_name,
    form.host,
    form.port,
    form.user,
    form.ssh_config_alias,
    form.identity_file,
  ].map((value) => value.trim()).join('\u001f')
}

function sshForm(connection: SshConnection): SshForm {
  const settings = connection.settings
  return {
    display_name: connection.display_name || '',
    profile_name: connection.profile_name || 'default',
    host: settings?.host || '',
    port: String(settings?.port || 22),
    user: settings?.user || '',
    ssh_config_alias: settings?.ssh_config_alias || '',
    identity_file: settings?.identity_file || '',
    hermes_path: settings?.hermes_path || '',
    workspace: settings?.workspace || '',
  }
}

function remoteStateColor(state: SshConnection['state']): string {
  if (state === 'connected' || state === 'validated') return 'teal'
  if (state === 'connecting') return 'indigo'
  if (state === 'error') return 'red'
  return 'gray'
}

function AgentCard({ agent }: { agent: Agent }) {
  const brand = agentKindLabel(agent.kind)
  const name = agent.name.includes(brand) ? agent.name : `${agent.name} · ${brand}`
  const [mcpEnabled, setMcpEnabled] = useState(true)
  const [loadingMcp, setLoadingMcp] = useState(false)

  useEffect(() => {
    let active = true
    void api.getAgentMcpStatus(agent.id).then((res) => {
      if (active) setMcpEnabled(res.enabled)
    }).catch(() => {})
    return () => { active = false }
  }, [agent.id])

  const handleToggleMcp = async (val: boolean) => {
    setLoadingMcp(true)
    try {
      await api.toggleAgentMcpStatus(agent.id, val)
      setMcpEnabled(val)
      notifications.show({
        color: val ? 'teal' : 'gray',
        message: `已${val ? '允许' : '禁止'} ${agent.name} 调度星序平台 (MCP)`,
      })
    } catch (e: any) {
      notifications.show({ color: 'red', message: e.message || '修改 MCP 权限失败' })
    } finally {
      setLoadingMcp(false)
    }
  }

  return (
    <SpotlightCard spotlightColor={agent.kind === 'codex' ? 'rgba(16, 185, 129, 0.22)' : 'rgba(59, 130, 246, 0.22)'} radius="var(--mantine-radius-lg, 16px)">
      <Paper className="agent-card" withBorder={false} p="lg" style={{ background: 'transparent' }}>
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Group gap="sm" wrap="nowrap"><span className={`agent-card-mark agent-kind-${agent.kind}`} aria-hidden="true"><AgentBrandIcon kind={agent.kind} size={24} /></span><div><Title order={3} size="h4">{name}</Title><Text size="xs" c="dimmed">{brand} · {agent.id}</Text></div></Group>
          <AgentStatusBadge status={agent.status} />
        </Group>
        <Paper withBorder radius="md" p="xs" style={{ background: 'var(--astr-surface-muted)' }}>
          <Group justify="space-between" align="center">
            <div>
              <Text size="xs" fw={700}>
                星序 MCP 调度授权
              </Text>
              <Text size="11px" c="dimmed">
                {mcpEnabled ? '允许此 Agent 发现并调用星序平台能力与跨机协同' : '已禁止此 Agent 调度星序 MCP 工具链'}
              </Text>
            </div>
            <Switch
              checked={mcpEnabled}
              onChange={(e) => void handleToggleMcp(e.currentTarget.checked)}
              disabled={loadingMcp}
              color="indigo"
              size="xs"
              aria-label="切换星序 MCP 权限"
            />
          </Group>
        </Paper>
        <div>
          <Text size="xs" fw={600} c="dimmed" mb={8} style={{ letterSpacing: '0.02em' }}>
            支持能力与通道规格
          </Text>
          <Group gap={6} wrap="wrap">
            {allCapabilities.map(({ key, label }) => {
              const supported = agent.capabilities.includes(key)
              return (
                <span
                  key={key}
                  className={`capability-tag ${supported ? 'is-supported' : 'is-unsupported'}`}
                  title={supported ? `当前 Agent 支持 ${label}` : `当前环境暂未开放 ${label}`}
                >
                  <span>{label}</span>
                </span>
              )
            })}
          </Group>
        </div>
        {agent.kind==='codex' && <NativeObservationPanel agentId={agent.id} />}
        <AgentUpgradeAction agent={agent} />
      </Stack>
    </Paper>
    </SpotlightCard>
  )
}

function AgentUpgradeAction({ agent }: { agent: Agent }) {
  const [upgrading, setUpgrading] = useState(false)
  const [resultModalOpen, setResultModalOpen] = useState(false)
  const [logOutput, setLogOutput] = useState('')
  const [cmdName, setCmdName] = useState('')
  const [isSuccess, setIsSuccess] = useState(true)
  const abortCtrlRef = useRef<AbortController | null>(null)
  const logPreRef = useRef<HTMLPreElement | null>(null)

  const addBackgroundTask = useBackgroundTasks((state) => state.add)
  const completeBackgroundTask = useBackgroundTasks((state) => state.complete)
  const failBackgroundTask = useBackgroundTasks((state) => state.fail)

  const handleUpgrade = async () => {
    setUpgrading(true)
    setLogOutput('')
    setCmdName('')
    setIsSuccess(true)
    setResultModalOpen(true)

    const abortCtrl = new AbortController()
    abortCtrlRef.current = abortCtrl

    // 登记到全局统一后台任务
    const taskId = addBackgroundTask({
      title: `升级 ${agent.name}`,
      detail: '正在执行版本升级指令…',
      actionLabel: '查看日志',
      action: () => {
        setResultModalOpen(true)
      },
      cancel: () => {
        if (abortCtrlRef.current) {
          abortCtrlRef.current.abort()
        }
      },
    })

    try {
      const res = await api.upgradeAgentStream(
        agent.id,
        (chunk) => {
          setLogOutput((prev) => {
            const next = prev + chunk
            if (logPreRef.current) {
              logPreRef.current.scrollTop = logPreRef.current.scrollHeight
            }
            return next
          })
        },
        (command) => {
          setCmdName(command)
        },
        abortCtrl.signal,
      )

      setIsSuccess(res.ok)
      if (res.ok) {
        completeBackgroundTask(taskId, '升级已顺利完成')
        notifications.show({
          color: 'teal',
          title: '升级成功',
          message: `${agent.name} 升级指令已执行完成。`,
        })
      } else {
        failBackgroundTask(taskId, `升级退出码: ${res.exit_code}`)
        notifications.show({
          color: 'red',
          title: '升级异常',
          message: `${agent.name} 升级命令返回非零退出码: ${res.exit_code}`,
        })
      }
    } catch (err: any) {
      if (err.name === 'AbortError') {
        failBackgroundTask(taskId, '用户已取消任务')
        notifications.show({
          color: 'gray',
          title: '升级已取消',
          message: `${agent.name} 升级已被手动取消。`,
        })
      } else {
        failBackgroundTask(taskId, err.message || '升级失败')
        notifications.show({
          color: 'red',
          title: '升级失败',
          message: err.message || '执行升级命令失败',
        })
      }
    } finally {
      setUpgrading(false)
      abortCtrlRef.current = null
    }
  }

  const isCodex = agent.kind === 'codex'
  const isHermes = agent.kind === 'hermes'
  const isGrok = agent.kind === 'grok'
  const commandHint = isCodex ? 'vp install -g @openai/codex@latest' : isHermes ? 'hermes update' : isGrok ? 'x.ai/cli/install' : '原生升级'

  return (
    <>
      <Group justify="space-between" align="center" pt={4} style={{ borderTop: '1px solid var(--astr-border-subtle, rgba(255,255,255,0.06))' }}>
        <div>
          <Text size="xs" fw={600} c="dimmed">一键原生版本升级</Text>
          <Text size="10px" c="dimmed" ff="monospace">{commandHint}</Text>
        </div>
        <Button
          size="xs"
          variant="light"
          color="indigo"
          loading={upgrading}
          leftSection={<IconRefresh size={13} />}
          onClick={() => void handleUpgrade()}
        >
          检查并升级
        </Button>
      </Group>

      <Modal
        opened={resultModalOpen}
        onClose={() => setResultModalOpen(false)}
        title={
          <Group gap={8}>
            <IconTerminal size={18} color={isSuccess ? 'var(--astr-teal)' : 'var(--astr-red)'} />
            <Text fw={600} size="sm">{agent.name} 升级执行日志</Text>
            {upgrading && <Badge size="xs" color="indigo" variant="light">实时流式输出中…</Badge>}
          </Group>
        }
        size="lg"
        radius="md"
      >
        <Stack gap="sm">
          <Paper p="xs" withBorder radius="sm" style={{ background: 'var(--astr-surface-muted)' }}>
            <Text size="xs" c="dimmed">执行指令：</Text>
            <Text size="xs" ff="monospace" fw={600}>{cmdName || '准备就绪…'}</Text>
          </Paper>
          <Paper p="sm" withBorder radius="sm" style={{ background: '#090d16', maxHeight: '380px', overflowY: 'auto' }}>
            <pre ref={logPreRef} style={{ margin: 0, fontSize: '11px', fontFamily: 'monospace', color: isSuccess ? '#86efac' : '#fca5a5', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
              {logOutput || (upgrading ? '正在建立连接并拉取执行流…' : '升级完成，无额外日志输出。')}
            </pre>
          </Paper>
          <Group justify="space-between" align="center">
            <Text size="xs" c="dimmed">
              {upgrading ? '提示：关闭此弹窗将自动放入后台任务继续执行，不会中止升级。' : ''}
            </Text>
            <Group gap="xs">
              <Button size="xs" variant="default" onClick={() => setResultModalOpen(false)}>
                {upgrading ? '放入后台运行' : '关闭'}
              </Button>
            </Group>
          </Group>
        </Stack>
      </Modal>
    </>
  )
}

export function SshSettingsCard({ connection, busy, draft, onDraftChange, onSave, onTest, onConnect, onDisconnect, onDelete }: {
  connection: SshConnection
  busy: boolean
  draft?: SshForm
  onDraftChange?: (draft: SshForm) => void
  onSave: (settings: SshConnectionSettings) => Promise<void>
  onTest?: () => Promise<void>
  onConnect: () => Promise<void>
  onDisconnect: () => Promise<void>
  onDelete?: () => Promise<void>
}) {
  const isMobile = useMediaQuery('(max-width: 767px)')
  const [form, setForm] = useState<SshForm>(() => draft || sshForm(connection))
  const [phase, setPhase] = useState<SshPhase>('basic')
  const [confirmedIdentityKey, setConfirmedIdentityKey] = useState<string | null>(null)
  const identityKey = sshIdentityKey(form)
  const hasCurrentHostKeyConfirmation = confirmedIdentityKey === identityKey
  const update = (field: keyof SshForm, value: string) => {
    setForm((current) => {
      const next = { ...current, [field]: value }
      onDraftChange?.(next)
      return next
    })
    if (['profile_name', 'host', 'port', 'user', 'ssh_config_alias', 'identity_file'].includes(field)) {
      setConfirmedIdentityKey(null)
    }
  }
  const settings: SshConnectionSettings = {
    connection_id: connection.id === 'new' ? null : connection.id,
    display_name: form.display_name.trim() || null,
    profile_name: form.profile_name.trim() || 'default',
    host: form.host.trim() || null,
    port: Number(form.port || 22),
    user: form.user.trim() || null,
    ssh_config_alias: form.ssh_config_alias.trim() || null,
    identity_file: form.identity_file.trim() || null,
    hermes_path: form.hermes_path.trim() || null,
    workspace: form.workspace.trim() || null,
  }
  const hasSavedConfiguration = connection.settings !== null
  const navigateTo = (next: SshPhase) => {
    if (next === 'basic') {
      setPhase('basic')
    } else if (next === 'identity' && (phase === 'basic' || phase === 'deploy')) {
      setPhase('identity')
    } else if (next === 'deploy' && phase === 'identity' && hasCurrentHostKeyConfirmation) {
      setPhase('deploy')
    }
  }
  const goNext = () => {
    if (phase === 'basic') navigateTo('identity')
    else navigateTo('deploy')
  }
  const goBack = () => setPhase((current) => current === 'deploy' ? 'identity' : 'basic')
  const handleDeploy = () => {
    if (!isMobile || (phase === 'deploy' && hasCurrentHostKeyConfirmation && hasSavedConfiguration && !busy)) {
      void onConnect()
    }
  }
  const handleTest = () => {
    if (onTest && (!isMobile || (phase === 'deploy' && hasCurrentHostKeyConfirmation && hasSavedConfiguration && !busy))) {
      void onTest()
    }
  }
  return (
    <Paper withBorder radius="lg" p="lg">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <div><Title order={3} size="h4">{connection.display_name || '远程 Hermes（SSH v1）'}</Title><Text size="sm" c="dimmed" mt={4}>connection {connection.id === 'new' ? 'new' : connection.id} · 仅通过系统 OpenSSH 和同一原生连接器传输。</Text></div>
          <Badge color={remoteStateColor(connection.state)} variant="light">{remoteStateLabels[connection.state]}</Badge>
        </Group>
        <Text size="sm">{connection.detail}</Text>
        {isMobile && <Group role="group" aria-label="SSH 设置阶段" grow wrap="nowrap">
          <Button variant={phase === 'basic' ? 'filled' : 'light'} onClick={() => navigateTo('basic')}>1 基本信息</Button>
          <Button variant={phase === 'identity' ? 'filled' : 'light'} onClick={() => navigateTo('identity')}>2 验证身份</Button>
          <Button variant={phase === 'deploy' ? 'filled' : 'light'} onClick={() => navigateTo('deploy')}>3 自动部署</Button>
        </Group>}
        {(!isMobile || phase === 'basic') && <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
          <TextInput label="连接显示名" placeholder="例如 WSL Hermes" value={form.display_name} onChange={(event) => update('display_name', event.currentTarget.value)} disabled={busy} />
          <TextInput label="Hermes profile" placeholder="default" value={form.profile_name} onChange={(event) => update('profile_name', event.currentTarget.value)} disabled={busy} />
          <TextInput label="SSH 配置别名" placeholder="例如 prod-hermes" value={form.ssh_config_alias} onChange={(event) => update('ssh_config_alias', event.currentTarget.value)} disabled={busy} />
          <TextInput label="SSH 主机" placeholder="host.example.com 或 192.0.2.10" value={form.host} onChange={(event) => update('host', event.currentTarget.value)} disabled={busy} />
          <TextInput label="SSH 端口" inputMode="numeric" value={form.port} onChange={(event) => update('port', event.currentTarget.value)} disabled={busy} />
          <TextInput label="SSH 用户" placeholder="deploy" value={form.user} onChange={(event) => update('user', event.currentTarget.value)} disabled={busy} />
        </SimpleGrid>}
        {(!isMobile || phase === 'identity') && <Stack gap="sm">
          <TextInput label="私钥文件引用（不上传私钥）" placeholder="C:\\Users\\you\\.ssh\\id_ed25519" value={form.identity_file} onChange={(event) => update('identity_file', event.currentTarget.value)} disabled={busy} />
          {isMobile && <Alert color="yellow" variant="light">不会读取或上传私钥。连接只使用系统 OpenSSH/agent，并拒绝未知或变化的 host key。</Alert>}
          {isMobile && <Checkbox checked={hasCurrentHostKeyConfirmation
} onChange={(event) => setConfirmedIdentityKey(event.currentTarget.checked ? identityKey : null)} label="我已在系统 known_hosts 中审核主机指纹（不会绕过校验）" disabled={busy} />}
        </Stack>}
        {(!isMobile || phase === 'deploy') && <Stack gap="sm">
          <TextInput label="远端 Hermes 路径" placeholder="/opt/hermes/bin/hermes" value={form.hermes_path} onChange={(event) => update('hermes_path', event.currentTarget.value)} disabled={busy} />
          <TextInput label="远端工作区" placeholder="/srv/astrorder-workspace" value={form.workspace} onChange={(event) => update('workspace', event.currentTarget.value)} disabled={busy} />
          <Text size="xs" c="dimmed">保存后可执行不联网的 OpenSSH 配置验证。远端原生插件尚未部署或未提供主机时，连接操作会明确失败，绝不会伪造已连接状态。</Text>
        </Stack>}
        {!isMobile && <Group wrap="wrap">
          <Button loading={busy} onClick={() => void onSave(settings)}>保存 SSH 配置</Button>
          {onTest && <Button variant="default" loading={busy} disabled={busy || !hasSavedConfiguration
} onClick={() => void onTest()}>测试 SSH 配置</Button>}
          <Button variant="default" loading={busy} disabled={busy || !hasSavedConfiguration
} onClick={() => void onConnect()}>连接远程 Hermes</Button>
          <Button variant="subtle" loading={busy} disabled={busy || !hasSavedConfiguration
} onClick={() => void onDisconnect()}>断开远程连接</Button>
          {onDelete && <Button color="red" variant="subtle" loading={busy} disabled={busy || !hasSavedConfiguration
} onClick={() => void onDelete()}>删除连接</Button>}
        </Group>}
        {isMobile && <Group justify="space-between" mt="sm">
          <Button variant="default" onClick={goBack} disabled={phase === 'basic' || busy}>返回</Button>
          {phase !== 'deploy' ? <Button onClick={goNext} disabled={phase === 'identity' && !hasCurrentHostKeyConfirmation || busy}>下一步</Button> : <Group gap="xs">
            <Button loading={busy} onClick={() => void onSave(settings)}>保存配置</Button>
            {onTest && <Button variant="default" loading={busy} disabled={busy || !hasSavedConfiguration || !hasCurrentHostKeyConfirmation
} onClick={handleTest}>测试</Button>}
            <Button variant="default" loading={busy} disabled={busy || !hasSavedConfiguration || !hasCurrentHostKeyConfirmation
} onClick={handleDeploy}>部署并连接</Button>
          </Group>}
        </Group>}
      </Stack>
    </Paper>
  )
}

export function AgentsPage() {
  const agents = useAstrorderStore(useShallow((state) => Object.values(state.agents)))
  return <div className="route-page agents-page"><EnvironmentConnections /><section className="agent-section"><Title order={3} size="h4" mt="xl" mb="sm">Agent 状态</Title><Stack gap="sm">{agents.map(agent => <AgentCard key={agent.id} agent={agent} />)}</Stack></section></div>
}
