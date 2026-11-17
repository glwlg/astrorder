import { useEffect, useState } from 'react'
import { Alert, Badge, Button, Group, Paper, ScrollArea, SegmentedControl, SimpleGrid, Stack, Table, Text, Title } from '@mantine/core'
import { IconRefresh } from '@tabler/icons-react'
import { api, type AgentUsageRange, type AgentUsageReport } from '../../api/client'
import './agentUsage.css'

const ranges: Array<{ label: string; value: AgentUsageRange }> = [
  { label: '近 24 小时', value: '24h' },
  { label: '近 7 天', value: '7d' },
  { label: '近 30 天', value: '30d' },
  { label: '全部', value: 'all' },
]
const number = new Intl.NumberFormat('zh-CN')
const dateTime = new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

function agentLabel(agent: { agent_id: string; name: string }): string {
  if (!agent.agent_id) return '未识别'
  return agent.name || agent.agent_id
}

function Metric({ label, value }: { label: string; value: string }) {
  return <Paper withBorder radius="md" p="md" className="agent-usage-metric">
    <Text size="xs" c="dimmed">{label}</Text>
    <Text size="xl" fw={700} mt={4}>{value}</Text>
  </Paper>
}

export function AgentUsageView() {
  const [range, setRange] = useState<AgentUsageRange>('7d')
  const [revision, setRevision] = useState(0)
  const [data, setData] = useState<AgentUsageReport | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let active = true
    setLoading(true)
    setError('')
    api.getAgentUsage(range).then(result => {
      if (active) setData(result)
    }).catch((cause: unknown) => {
      if (active) setError(cause instanceof Error ? cause.message : '读取使用统计失败')
    }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [range, revision])

  return <ScrollArea className="agent-usage-scroll" type="auto">
    <div className="agent-usage-content">
      <Group justify="space-between" align="flex-start" gap="md" wrap="wrap">
        <div>
          <Title order={2} size="h3">星序 CLI 使用统计</Title>
          <Text size="sm" c="dimmed" mt={4}>统计 CLI 和其他非浏览器 Agent API 调用，启用后开始记录。未识别身份的调用单独列出。</Text>
        </div>
        <Button size="xs" variant="subtle" leftSection={<IconRefresh size={14} />} onClick={() => setRevision(value => value + 1)} loading={loading}>刷新</Button>
      </Group>
      <SegmentedControl className="agent-usage-range" size="xs" value={range} onChange={value => setRange(value as AgentUsageRange)} data={ranges} aria-label="统计时间范围" />
      {loading ? <Text role="status" size="sm" c="dimmed">正在读取使用统计…</Text> : error ? <Alert color="red">{error}</Alert> : data && <Stack gap="md">
        <SimpleGrid cols={{ base: 2, md: 4 }} spacing="sm">
          <Metric label="调用次数" value={number.format(data.summary.calls)} />
          <Metric label="请求成功率" value={data.summary.calls ? `${(data.summary.succeeded * 100 / data.summary.calls).toFixed(1)}%` : '—'} />
          <Metric label="活跃 Agent" value={number.format(data.summary.active_agents)} />
          <Metric label="失败次数" value={number.format(data.summary.failed)} />
        </SimpleGrid>
        {data.summary.calls === 0 ? <Paper withBorder radius="md" p="xl"><Text size="sm" c="dimmed" ta="center">此时间范围内暂无调用记录</Text></Paper> : <>
          <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
            <Paper withBorder radius="md" p="md" className="agent-usage-panel">
              <Title order={3} size="h5" mb="sm">Agent 调用</Title>
              <Table.ScrollContainer minWidth={340}>
                <Table verticalSpacing="xs" highlightOnHover>
                  <Table.Thead><Table.Tr><Table.Th>Agent</Table.Th><Table.Th ta="right">调用</Table.Th><Table.Th ta="right">失败</Table.Th></Table.Tr></Table.Thead>
                  <Table.Tbody>{data.agents.map(agent => <Table.Tr key={agent.agent_id}>
                    <Table.Td><Text size="sm" fw={500}>{agentLabel(agent)}</Text>{agent.agent_id && agent.name && agent.name !== agent.agent_id && <Text size="xs" c="dimmed">{agent.agent_id}</Text>}</Table.Td>
                    <Table.Td ta="right">{number.format(agent.calls)}</Table.Td><Table.Td ta="right">{number.format(agent.failed)}</Table.Td>
                  </Table.Tr>)}</Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            </Paper>
            <Paper withBorder radius="md" p="md" className="agent-usage-panel">
              <Title order={3} size="h5" mb="sm">能力调用</Title>
              <Table.ScrollContainer minWidth={340}>
                <Table verticalSpacing="xs" highlightOnHover>
                  <Table.Thead><Table.Tr><Table.Th>能力</Table.Th><Table.Th ta="right">调用</Table.Th><Table.Th ta="right">失败</Table.Th></Table.Tr></Table.Thead>
                  <Table.Tbody>{data.capabilities.map(item => <Table.Tr key={item.capability}>
                    <Table.Td><Text size="sm" className="agent-usage-capability">{item.capability === 'unknown' ? '未知能力' : item.capability}</Text></Table.Td>
                    <Table.Td ta="right">{number.format(item.calls)}</Table.Td><Table.Td ta="right">{number.format(item.failed)}</Table.Td>
                  </Table.Tr>)}</Table.Tbody>
                </Table>
              </Table.ScrollContainer>
            </Paper>
          </SimpleGrid>
          <Paper withBorder radius="md" p="md">
            <Group justify="space-between" mb="sm"><Title order={3} size="h5">最近调用</Title><Text size="xs" c="dimmed">最近 30 条</Text></Group>
            <Table.ScrollContainer minWidth={620}>
              <Table verticalSpacing="xs" highlightOnHover>
                <Table.Thead><Table.Tr><Table.Th>时间</Table.Th><Table.Th>Agent</Table.Th><Table.Th>能力</Table.Th><Table.Th>结果</Table.Th><Table.Th ta="right">耗时</Table.Th></Table.Tr></Table.Thead>
                <Table.Tbody>{data.recent.map((item, index) => <Table.Tr key={`${item.occurred_at}-${index}`}>
                  <Table.Td><Text size="sm">{dateTime.format(new Date(item.occurred_at))}</Text></Table.Td>
                  <Table.Td><Text size="sm">{agentLabel(item)}</Text></Table.Td>
                  <Table.Td><Text size="sm" className="agent-usage-capability">{item.capability === 'unknown' ? '未知能力' : item.capability}</Text></Table.Td>
                  <Table.Td><Badge size="sm" variant="light" color={item.success ? 'teal' : 'red'}>{item.success ? '成功' : '失败'}</Badge></Table.Td>
                  <Table.Td ta="right">{item.duration_ms < 1000 ? `${number.format(item.duration_ms)} ms` : `${(item.duration_ms / 1000).toFixed(1)} s`}</Table.Td>
                </Table.Tr>)}</Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </Paper>
        </>}
      </Stack>}
    </div>
  </ScrollArea>
}
