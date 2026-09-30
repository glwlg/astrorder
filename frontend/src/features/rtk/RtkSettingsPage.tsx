import { useState } from 'react'
import { Alert, Badge, Button, Group, NativeSelect, Paper, SegmentedControl, Stack, Switch, Text, Title } from '@mantine/core'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import './rtk.css'

const number = (value: number) => new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
export function RtkSettingsPage() {
  const client = useQueryClient()
  const [environment, setEnvironment] = useState('local')
  const [days, setDays] = useState('30')
  const [busy, setBusy] = useState<string | null>(null)
  const [actionError, setActionError] = useState('')
  const environments = useQuery({ queryKey: ['astrorder', 'environments'], queryFn: api.getEnvironments })
  const query = useQuery({ queryKey: ['astrorder', 'rtk', environment, days], queryFn: () => api.getRtk(environment, days), retry: false })
  const data = query.data
  const run = async (key: string, action: () => Promise<unknown>) => {
    setBusy(key); setActionError('')
    try { await action(); await client.invalidateQueries({ queryKey: ['astrorder', 'rtk', environment] }) }
    catch (error) { setActionError(error instanceof Error ? error.message : '操作未确认，请重新检测') }
    finally { setBusy(null) }
  }
  const max = Math.max(1, ...(data?.daily.map(day => day.saved_tokens + day.output_tokens) ?? []))
  return <Stack className="rtk-settings" gap="lg">
    <Group justify="space-between" align="end">
      <div><Title order={3}>RTK 命令行压缩</Title><Text c="dimmed" size="sm" mt={6}>压缩 Agent 读取的命令输出，减少上下文中的冗余内容。</Text></div>
      <NativeSelect label="运行环境" value={environment} disabled={!!busy} onChange={event => { setEnvironment(event.currentTarget.value); setActionError('') }} data={environments.data?.items.map(item => ({ value: item.id, label: `${item.name}${item.method === 'ssh' ? ' · SSH' : ''}` })) ?? [{ value: 'local', label: '本机' }]} />
    </Group>
    {environments.isError && <Alert color="red">环境列表读取失败，请在连接设置检查服务器。</Alert>}
    {(query.isError || actionError) && <Alert color="red" title="未能确认 RTK 状态">{actionError || (query.error instanceof Error ? query.error.message : '读取失败')}<Button mt="sm" size="xs" variant="light" onClick={() => void query.refetch()}>重新检测</Button></Alert>}
    {query.isPending && <Text role="status">正在读取 RTK 状态…</Text>}
    {data && !query.isError && <>
      <Paper withBorder radius="lg" p="lg">
        <Group justify="space-between"><Group><Text fw={700}>RTK</Text><Badge color={data.installed ? 'teal' : 'gray'}>{data.installed ? `已安装 ${data.version || ''}` : '未安装'}</Badge></Group><Group gap="xs"><Button variant="default" size="xs" disabled={!!busy} loading={query.isFetching} onClick={() => void query.refetch()}>刷新</Button>{!data.installed && <Button size="xs" loading={busy === 'install'} disabled={!!busy} onClick={() => void run('install', () => api.installRtk(environment))}>安装 RTK</Button>}</Group></Group>
        {data.executable && <Text size="xs" c="dimmed" mt="xs" className="rtk-path">{data.executable}</Text>}
        {data.detail && <Alert mt="sm" color="blue">{data.detail}</Alert>}
        {data.installed && <>
          <Group mt="lg" gap="xl"><div><Text size="xs" c="dimmed">估算节省</Text><Text size="xl" fw={700}>{number(data.summary.total_saved)} token</Text></div><div><Text size="xs" c="dimmed">处理命令</Text><Text size="xl" fw={700}>{number(data.summary.total_commands)}</Text></div><div><Text size="xs" c="dimmed">输出减少</Text><Text size="xl" fw={700}>{data.summary.avg_savings_pct.toFixed(1)}%</Text></div></Group>
          <Paper withBorder radius="md" p="sm" mt="lg"><Group justify="space-between"><Text size="sm" fw={600}>每日节省</Text><SegmentedControl size="xs" value={days} onChange={setDays} disabled={!!busy} data={[{ value: '30', label: '30 天' }, { value: '90', label: '90 天' }, { value: 'all', label: '全部' }]} /></Group>
            {data.daily.length ? <div className="rtk-chart" role="img" aria-label="每日已节省与保留输出 token"><div className="rtk-chart-bars">{data.daily.map(day => <div className="rtk-day" key={day.date} title={`${day.date}：节省 ${day.saved_tokens.toLocaleString()}，保留 ${day.output_tokens.toLocaleString()} token；${day.commands} 条命令`}><div className="rtk-bar" style={{ height: `${100 * (day.saved_tokens + day.output_tokens) / max}%` }}><div className="rtk-saved" style={{ flex: day.saved_tokens }} /><div className="rtk-output" style={{ flex: day.output_tokens }} /></div></div>)}</div><div className="rtk-chart-dates"><span>{data.daily[0].date}</span><span>{data.daily.at(-1)?.date}</span></div></div> : <Text ta="center" c="dimmed" py="xl">这个时间范围还没有 RTK 命令记录</Text>}
            <Group gap="md" mt="xs"><Text size="xs"><span className="rtk-dot rtk-saved" />已节省</Text><Text size="xs"><span className="rtk-dot rtk-output" />保留输出</Text></Group>
          </Paper>
        </>}
      </Paper>
      <div><Group justify="space-between" mb="sm"><Text size="sm" fw={600}>AGENT</Text><Text size="xs" c="dimmed">切换后需新开对应 Agent 进程；当前会话不会重启</Text></Group><Paper withBorder radius="lg" className="rtk-agents">{data.agents.map(agent => <Group className="rtk-agent" key={agent.kind} justify="space-between" wrap="nowrap"><div><Text fw={600} size="sm">{agent.name}</Text>{agent.detail && <Text size="xs" c="dimmed">{agent.detail}</Text>}</div><Switch aria-label={`${agent.name} RTK`} checked={agent.enabled} disabled={!data.installed || !agent.supported || !!busy} onChange={event => { const enabled = event.currentTarget.checked; void run(agent.kind, () => api.setRtkAgent(environment, agent.kind, enabled)) }} /></Group>)}</Paper></div>
      <Text size="xs" c="dimmed">统计来自所选环境当前用户的 RTK 记录，是命令输出 token 的估算减少量，不代表模型账单节省，也不区分 Agent。</Text>
    </>}
  </Stack>
}
