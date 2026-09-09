import { IconPlugConnected, IconRocket, IconShieldOff, IconSparkles } from '@tabler/icons-react'
import { Alert, Badge, Button, Checkbox, Drawer, Group, Paper, SimpleGrid, Stack, Text, TextInput, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useMediaQuery } from '@mantine/hooks'
import { useState } from 'react'
import { useShallow } from 'zustand/react/shallow'
import { ApiError, api } from '../../api/client'
import { EmptyState } from '../../components/EmptyState'
import { AgentStatusBadge } from '../../components/Status'
import type { Agent, LocalHermesConnection, RuntimeItem, SshConnection, SshConnectionSettings } from '../../domain/types'
import { ConnectionList, type ConnectionListEntry } from './ConnectionList'
import { ConnectionHistoryTimeline } from './ConnectionHistoryTimeline'
import { CodexConnectionCard } from './CodexConnectionCard'
import { useConnectionHistory, useConnections, useRuntime } from '../../hooks/useAstrorderData'
import { useAstrorderStore } from '../../state/store'
import { connectionNotice } from './connectionNotice'
import './agentsLayout.css'
import { EnvironmentConnections } from './EnvironmentConnections'

const capabilityLabels: Record<string, string> = {
  chat: '聊天', stop: '停止', queue: '排队', attachments: '附件', approvals: '审批', launch: '启动', history: '消息记录', events: '事件',
}

const localStateLabels: Record<LocalHermesConnection['state'], string> = {
  discovered: '已发现',
  installed: '已安装',
  connecting: '连接中',
  connected: '已连接',
  offline: '离线',
  error: '错误',
}

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

function localStateColor(state: LocalHermesConnection['state']): string {
  if (state === 'connected') return 'teal'
  if (state === 'connecting') return 'indigo'
  if (state === 'error') return 'red'
  return 'gray'
}

function remoteStateColor(state: SshConnection['state']): string {
  if (state === 'connected' || state === 'validated') return 'teal'
  if (state === 'connecting') return 'indigo'
  if (state === 'error') return 'red'
  return 'gray'
}

function AgentCard({ agent }: { agent: Agent }) {
  return (
    <Paper className="agent-card" withBorder radius="lg" p="lg">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Group gap="sm" wrap="nowrap"><span className={`agent-card-mark agent-kind-${agent.kind}`} aria-hidden="true">{agent.kind === 'codex' ? 'C' : 'H'}</span><div><Title order={3} size="h4">{agent.name || agent.id}</Title><Text size="xs" c="dimmed">{agent.kind} · {agent.id}</Text></div></Group>
          <AgentStatusBadge status={agent.status} />
        </Group>
        <div>
          <Text size="xs" fw={700} c="dimmed" mb="xs">已报告能力</Text>
          <Group gap={6}>{agent.capabilities.length > 0 ? agent.capabilities.map((capability) => <Badge key={capability} variant="outline" color="indigo">{capabilityLabels[capability] || capability}</Badge>) : <Text size="sm" c="dimmed">未报告能力</Text>}</Group>
        </div>
        {agent.limitation && <Alert color="yellow" variant="light" icon={<IconShieldOff size={17} />}>{connectionNotice(agent.limitation)}</Alert>}
      </Stack>
    </Paper>
  )
}

function RuntimeCard({ item, onLaunch }: { item: RuntimeItem; onLaunch: (item: RuntimeItem, workspace: string) => Promise<void> }) {
  const [workspace, setWorkspace] = useState('')
  const [launching, setLaunching] = useState(false)
  const launch = async () => {
    setLaunching(true)
    try {
      await onLaunch(item, workspace)
    } finally {
      setLaunching(false)
    }
  }
  const canLaunch = item.available && item.capabilities.includes('launch')
  return (
    <Paper className="runtime-card" withBorder radius="lg" p="md">
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <div><Group gap="xs"><IconRocket size={19} /><Text fw={700}>{item.kind}</Text></Group><Text size="sm" c="dimmed" mt={5}>{item.reason || (item.available ? '运行时可用，但仅允许服务端配置的工作区。' : '运行时不可用。')}</Text></div>
        <Badge color={item.available ? 'teal' : 'gray'} variant="light">{item.available ? '可用' : '不可用'}</Badge>
      </Group>
      {item.capabilities.length > 0 && <Text size="xs" c="dimmed" mt="sm">能力：{item.capabilities.join('、')}</Text>}
      <Group align="flex-end" mt="md" wrap="wrap">
        <TextInput className="workspace-input" label="允许的工作区" placeholder="由服务端白名单校验" value={workspace} onChange={(event) => setWorkspace(event.currentTarget.value)} disabled={!canLaunch || launching} />
        <Button leftSection={<IconRocket size={16} />} disabled={!canLaunch || !workspace.trim() || launching} loading={launching} onClick={() => void launch()}>请求启动</Button>
      </Group>
    </Paper>
  )
}

function LocalHermesCard({ connection, busy, onConnect, onDisconnect }: {
  connection: LocalHermesConnection
  busy: boolean
  onConnect: () => Promise<void>
  onDisconnect: () => Promise<void>
}) {
  const isConnected = connection.state === 'connected'
  return (
    <Paper withBorder radius="lg" p="lg">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <div>
            <Title order={3} size="h4">本机 Hermes</Title>
            <Text size="sm" c="dimmed" mt={4}>通过 Hermes 原生 TUI gateway 在受控新会话中加载项目插件；浏览器不会持有连接器凭据。</Text>
          </div>
          <Badge color={localStateColor(connection.state)} variant="light">{localStateLabels[connection.state]}</Badge>
        </Group>
        <Text size="sm">{connection.detail}</Text>
        <Group gap="xs"><Text size="xs" c="dimmed">运行时元数据</Text><Badge variant="outline" color="indigo">{connection.version || '未报告版本'}</Badge></Group>
        <Text size="xs" c="dimmed">连接本机 Hermes 将启动 Astrorder 本地运行时，自动同步项目工作区与会话列表。</Text>
        <Group>
          <Button leftSection={<IconPlugConnected size={16} />} loading={busy && !isConnected} disabled={!connection.available || busy || isConnected} onClick={() => void onConnect()}>连接本机 Hermes</Button>
          <Button variant="default" loading={busy && isConnected} disabled={busy || !connection.agent_id} onClick={() => void onDisconnect()}>断开本机 Hermes</Button>
        </Group>
      </Stack>
    </Paper>
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
          {isMobile && <Checkbox checked={hasCurrentHostKeyConfirmation} onChange={(event) => setConfirmedIdentityKey(event.currentTarget.checked ? identityKey : null)} label="我已在系统 known_hosts 中审核主机指纹（不会绕过校验）" disabled={busy} />}
        </Stack>}
        {(!isMobile || phase === 'deploy') && <Stack gap="sm">
          <TextInput label="远端 Hermes 路径" placeholder="/opt/hermes/bin/hermes" value={form.hermes_path} onChange={(event) => update('hermes_path', event.currentTarget.value)} disabled={busy} />
          <TextInput label="远端工作区" placeholder="/srv/astrorder-workspace" value={form.workspace} onChange={(event) => update('workspace', event.currentTarget.value)} disabled={busy} />
          <Text size="xs" c="dimmed">保存后可执行不联网的 OpenSSH 配置验证。远端原生插件尚未部署或未提供主机时，连接操作会明确失败，绝不会伪造已连接状态。</Text>
        </Stack>}
        {!isMobile && <Group wrap="wrap">
          <Button loading={busy} onClick={() => void onSave(settings)}>保存 SSH 配置</Button>
          {onTest && <Button variant="default" loading={busy} disabled={busy || !hasSavedConfiguration} onClick={() => void onTest()}>测试 SSH 配置</Button>}
          <Button variant="default" loading={busy} disabled={busy || !hasSavedConfiguration} onClick={() => void onConnect()}>连接远程 Hermes</Button>
          <Button variant="subtle" loading={busy} disabled={busy || !hasSavedConfiguration} onClick={() => void onDisconnect()}>断开远程连接</Button>
          {onDelete && <Button color="red" variant="subtle" loading={busy} disabled={busy || !hasSavedConfiguration} onClick={() => void onDelete()}>删除连接</Button>}
        </Group>}
        {isMobile && <Group justify="space-between" mt="sm">
          <Button variant="default" onClick={goBack} disabled={phase === 'basic' || busy}>返回</Button>
          {phase !== 'deploy' ? <Button onClick={goNext} disabled={phase === 'identity' && !hasCurrentHostKeyConfirmation || busy}>下一步</Button> : <Group gap="xs">
            <Button loading={busy} onClick={() => void onSave(settings)}>保存配置</Button>
            {onTest && <Button variant="default" loading={busy} disabled={busy || !hasSavedConfiguration || !hasCurrentHostKeyConfirmation} onClick={handleTest}>测试</Button>}
            <Button variant="default" loading={busy} disabled={busy || !hasSavedConfiguration || !hasCurrentHostKeyConfirmation} onClick={handleDeploy}>部署并连接</Button>
          </Group>}
        </Group>}
      </Stack>
    </Paper>
  )
}

function newSshConnection(): SshConnection {
  return {
    id: 'new',
    display_name: '',
    profile_name: 'default',
    state: 'configured',
    settings: null,
    detail: '填写并保存新的 SSH 连接。',
    remote_os: null,
    agent_id: null,
    runtime_id: null,
  }
}

export function AgentsPage() {
  const agents = useAstrorderStore(useShallow((state) => Object.values(state.agents)))
  return <div className="route-page agents-page"><EnvironmentConnections /><section className="agent-section"><Title order={3} size="h4" mt="xl" mb="sm">Agent 状态</Title><Stack gap="sm">{agents.map(agent => <AgentCard key={agent.id} agent={agent} />)}</Stack></section></div>
}

export function LegacyAgentsPage() {
  const codex = useQuery({ queryKey: ['astrorder', 'codex-connection'], queryFn: api.getCodexConnection, retry: false, refetchInterval: 10000 })
  const queryClient = useQueryClient()
  const agents = useAstrorderStore(useShallow((state) => Object.values(state.agents)))
  const runtime = useRuntime(true)
  const connections = useConnections(true)
  const [busyConnections, setBusyConnections] = useState<Record<string, true>>({})
  const [selectedConnection, setSelectedConnection] = useState<ConnectionListEntry | null>(null)
  const [sshDrafts, setSshDrafts] = useState<Record<string, SshForm>>({})
  const mobileDrawer = useMediaQuery('(max-width: 767px)')
  const refreshConnections = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ['astrorder', 'bootstrap'] }),
      queryClient.invalidateQueries({ queryKey: ['astrorder', 'connections'] }),
    ])
    await connections.refetch()
  }
  const runConnectionAction = async (connectionKey: string, action: () => Promise<unknown>) => {
    setBusyConnections((current) => ({ ...current, [connectionKey]: true }))
    try {
      await action()
      await refreshConnections()
    } catch (nextError) {
      const msg = nextError instanceof ApiError ? nextError.detail : nextError instanceof Error ? nextError.message : '连接操作失败'
      notifications.show({
        title: '操作失败',
        message: msg,
        color: 'red',
        autoClose: 5000,
      })
    } finally {
      setBusyConnections((current) => {
        const next = { ...current }
        delete next[connectionKey]
        return next
      })
    }
  }
  const launch = async (item: RuntimeItem, workspace: string) => {
    try {
      await api.launchRuntime(item.kind, workspace)
      useAstrorderStore.getState().setConnection('connected')
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'bootstrap'] })
      await runtime.refetch()
    } catch (nextError) {
      const msg = nextError instanceof ApiError ? nextError.detail : nextError instanceof Error ? nextError.message : '启动请求失败'
      notifications.show({
        title: '启动失败',
        message: msg,
        color: 'red',
        autoClose: 5000,
      })
    }
  }
  const sshItems = connections.data?.ssh.items || []
  const selectedEntry = selectedConnection?.kind === 'ssh'
    ? selectedConnection.id === 'new'
      ? selectedConnection
      : sshItems.find((item) => item.id === selectedConnection.id)
        ? { ...selectedConnection, connection: sshItems.find((item) => item.id === selectedConnection.id)! }
        : null
    : selectedConnection
  const historyConnectionId = selectedEntry?.kind === 'ssh' && selectedEntry.id !== 'new' ? selectedEntry.id : null
  const history = useConnectionHistory(historyConnectionId, true)
  const historyItems = history.data?.pages.flatMap((page) => page.items) || []
  return (
    <div className="route-page agents-page">

      <section className="agent-section" aria-labelledby="connections-list-heading">
        {connections.isLoading && <Text c="dimmed">正在读取连接器状态…</Text>}
        {connections.error && <Alert color="yellow">无法读取连接器状态：{connections.error instanceof Error ? connections.error.message : '服务未提供该接口'}</Alert>}
        {connections.data && <ConnectionList
          local={connections.data.local}
          codex={codex.data}
          sshItems={sshItems}
          onAdd={() => setSelectedConnection({ kind: 'ssh', id: 'new', label: '添加 SSH 连接', connection: newSshConnection() })}
          onSelect={setSelectedConnection}
        />}
      </section>
      {selectedEntry && <Drawer
        opened
        onClose={() => setSelectedConnection(null)}
        closeButtonProps={{ 'aria-label': '关闭详情' }}
        position={mobileDrawer ? 'bottom' : 'right'}
        size={mobileDrawer ? 'min(90vh, 760px)' : 'min(92vw, 620px)'}
        title={selectedEntry.kind === 'local' ? '本机 Hermes' : selectedEntry.id === 'new' ? '添加 SSH 连接' : selectedEntry.label}
      >
        {selectedEntry.kind === 'codex' ? <CodexConnectionCard /> : selectedEntry.kind === 'local' ? <LocalHermesCard
          connection={selectedEntry.connection}
          busy={Boolean(busyConnections.local)}
          onConnect={() => runConnectionAction('local', api.connectLocalHermes)}
          onDisconnect={() => runConnectionAction('local', api.disconnectLocalHermes)}
        /> : <Stack key={selectedEntry.id} gap="lg"><SshSettingsCard
          connection={selectedEntry.connection}
          busy={Boolean(busyConnections[selectedEntry.id])}
          draft={sshDrafts[selectedEntry.id]}
          onDraftChange={(draft) => setSshDrafts((current) => ({ ...current, [selectedEntry.id]: draft }))}
          onSave={(settings) => runConnectionAction(selectedEntry.id, async () => {
            if (selectedEntry.id === 'new') {
              await api.saveSshConnection(settings)
              setSshDrafts((current) => { const next = { ...current }; delete next[selectedEntry.id]; return next })
              setSelectedConnection(null)
            } else {
              await api.updateSshConnection(selectedEntry.id, settings)
              setSshDrafts((current) => { const next = { ...current }; delete next[selectedEntry.id]; return next })
            }
          })}
          onTest={selectedEntry.id === 'new' ? undefined : () => runConnectionAction(selectedEntry.id, () => api.testSshConnection(selectedEntry.id))}
          onConnect={() => selectedEntry.id === 'new' ? Promise.resolve() : runConnectionAction(selectedEntry.id, () => api.connectSshConnection(selectedEntry.id))}
          onDisconnect={() => selectedEntry.id === 'new' ? Promise.resolve() : runConnectionAction(selectedEntry.id, () => api.disconnectSshConnection(selectedEntry.id))}
          onDelete={selectedEntry.id === 'new' ? undefined : async () => {
            await runConnectionAction(selectedEntry.id, () => api.deleteSshConnection(selectedEntry.id))
            setSelectedConnection(null)
          }}
        />{selectedEntry.id !== 'new' && <ConnectionHistoryTimeline items={historyItems} isLoading={history.isLoading} error={history.error} hasMore={Boolean(history.hasNextPage)} onLoadMore={() => void history.fetchNextPage()} />}</Stack>}
      </Drawer>}

      <section className="agent-section" aria-labelledby="connected-agents-heading">
        <Group justify="space-between" mb="sm" mt="xl"><div><Title id="connected-agents-heading" order={3} size="h4">连接状态</Title><Text size="sm" c="dimmed">来自 /api/v1/bootstrap 的 Agent 快照。</Text></div><Badge variant="light" color={agents.length ? 'indigo' : 'gray'} leftSection={<IconPlugConnected size={14} />}>{agents.length} 个 Agent</Badge></Group>
        {agents.length === 0 ? <EmptyState icon={<IconSparkles />} title="尚无已连接 Agent" description="这里不会显示模拟连接。请由真实 Hermes/Codex 连接器向服务端报告 Agent 后再操作。" /> : <Stack className="agent-status-list" gap="sm">{agents.map((agent) => <AgentCard key={agent.id} agent={agent} />)}</Stack>}
      </section>
      <section className="runtime-section" aria-labelledby="runtime-heading">
        <Group justify="space-between" mb="sm" mt="xl"><div><Title id="runtime-heading" order={3} size="h4">受控运行时</Title><Text size="sm" c="dimmed">启动只通过契约 HTTP 请求；浏览器不执行 shell，也不伪装成原生连接。</Text></div></Group>
        {runtime.isLoading && <Text c="dimmed">正在读取运行时能力…</Text>}
        {runtime.error && <Alert color="yellow">无法读取运行时能力：{runtime.error instanceof Error ? runtime.error.message : '服务未提供该接口'}</Alert>}
        {!runtime.isLoading && !runtime.error && runtime.data?.items.length === 0 && <Text c="dimmed">服务端没有报告可用运行时。</Text>}
        <Stack gap="md">{runtime.data?.items.filter(item => item.kind !== 'codex').map((item) => <RuntimeCard item={item} onLaunch={launch} key={item.kind} />)}</Stack>
      </section>
    </div>
  )
}