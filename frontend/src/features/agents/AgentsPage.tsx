import { IconPlugConnected, IconRocket, IconShieldOff, IconSparkles } from '@tabler/icons-react'
import { Alert, Badge, Button, Group, Paper, SimpleGrid, Stack, Text, TextInput, Title } from '@mantine/core'
import { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { useShallow } from 'zustand/react/shallow'
import { ApiError, api } from '../../api/client'
import type { Agent, RuntimeItem } from '../../domain/types'
import { EmptyState } from '../../components/EmptyState'
import { AgentStatusBadge } from '../../components/Status'
import { useRuntime } from '../../hooks/useAstrorderData'
import { useAstrorderStore } from '../../state/store'

const capabilityLabels: Record<string, string> = {
  chat: '聊天', stop: '停止', queue: '排队', attachments: '附件', approvals: '审批', launch: '启动', history: '历史', events: '事件',
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
        {agent.limitation && <Alert color="yellow" variant="light" icon={<IconShieldOff size={17} />}>{agent.limitation}</Alert>}
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

export function AgentsPage() {
  const queryClient = useQueryClient()
  const agents = useAstrorderStore(useShallow((state) => Object.values(state.agents)))
  const runtime = useRuntime(true)
  const [error, setError] = useState<string | null>(null)
  const launch = async (item: RuntimeItem, workspace: string) => {
    setError(null)
    try {
      await api.launchRuntime(item.kind, workspace)
      useAstrorderStore.getState().setConnection('connected')
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'bootstrap'] })
      await runtime.refetch()
    } catch (nextError) {
      setError(nextError instanceof ApiError ? nextError.detail : nextError instanceof Error ? nextError.message : '启动请求失败')
    }
  }
  return (
    <div className="route-page agents-page">
      <div className="route-heading" >
        <Text className="eyebrow" size="xs" fw={700}>CONNECTIONS</Text>
        <Title order={2} size="h2" mt={4}>Agent 设置</Title>
        <Text c="dimmed" mt={5}>只呈现服务端和连接器报告的真实能力；未报告的操作保持禁用。</Text>
      </div>
      {error && <Alert className="route-alert" color="red" mt="lg">{error}</Alert>}
      <section className="agent-section" aria-labelledby="connected-agents-heading">
        <Group justify="space-between" mb="sm" mt="xl"><div><Title id="connected-agents-heading" order={3} size="h4">连接状态</Title><Text size="sm" c="dimmed">来自 /api/v1/bootstrap 的 Agent 快照。</Text></div><Badge variant="light" color={agents.length ? 'indigo' : 'gray'} leftSection={<IconPlugConnected size={14} />}>{agents.length} 个 Agent</Badge></Group>
        {agents.length === 0 ? <EmptyState icon={<IconSparkles />} title="尚无已连接 Agent" description="这里不会显示模拟连接。请由真实 Hermes/Codex 连接器向服务端报告 Agent 后再操作。" /> : <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">{agents.map((agent) => <AgentCard key={agent.id} agent={agent} />)}</SimpleGrid>}
      </section>
      <section className="runtime-section" aria-labelledby="runtime-heading">
        <Group justify="space-between" mb="sm" mt="xl"><div><Title id="runtime-heading" order={3} size="h4">受控运行时</Title><Text size="sm" c="dimmed">启动只通过契约 HTTP 请求；浏览器不执行 shell，也不伪装成原生连接。</Text></div></Group>
        {runtime.isLoading && <Text c="dimmed">正在读取运行时能力…</Text>}
        {runtime.error && <Alert color="yellow">无法读取运行时能力：{runtime.error instanceof Error ? runtime.error.message : '服务未提供该接口'}</Alert>}
        {!runtime.isLoading && !runtime.error && runtime.data?.items.length === 0 && <Text c="dimmed">服务端没有报告可用运行时。</Text>}
        <Stack gap="md">{runtime.data?.items.map((item) => <RuntimeCard item={item} onLaunch={launch} key={item.kind} />)}</Stack>
      </section>
    </div>
  )
}
