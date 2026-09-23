import { useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Card,
  Grid,
  Group,
  Progress,
  SegmentedControl,
  SimpleGrid,
  Skeleton,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { IconAlertTriangle, IconBolt, IconCalendarStats, IconChartHistogram, IconCircleCheck, IconClock, IconCoin, IconDatabase } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api, type OcxUsageBreakdown, type OcxUsageResponse } from '../../api/client'

const zhNumber = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })
const usd = new Intl.NumberFormat('zh-CN', { style: 'currency', currency: 'USD', maximumFractionDigits: 2 })

export function formatTokens(tokens: number = 0): string {
  if (tokens >= 100_000_000) return `${zhNumber.format(tokens / 100_000_000)} 亿`
  if (tokens >= 10_000) return `${zhNumber.format(tokens / 10_000)} 万`
  return zhNumber.format(tokens)
}

const percent = (ratio: number = 0) => `${(ratio * 100).toFixed(1)}%`
const localInput = (date: Date) => {
  const offset = date.getTimezoneOffset() * 60_000
  return new Date(date.getTime() - offset).toISOString().slice(0, 16)
}

export function getModelColor(model: string = '', provider: string = ''): string {
  const m = model.toLowerCase()
  if (m.includes('gpt-5.6-sol') || m === 'sol') return '#ef4444'
  if (m.includes('gpt-5.6-luna') || m === 'luna') return '#10b981'
  if (m.includes('deepseek')) return '#3b82f6'
  if (m.includes('gemini')) return '#2563eb'
  if (m.includes('guolian')) return '#8b5cf6'
  if (m.includes('terra')) return '#38bdf8'
  if (m.includes('grok')) return '#06b6d4'
  if (m.includes('claude')) return '#f97316'
  if (m.includes('astra')) return '#a855f7'
  if (m === 'unknown') return '#14b8a6'
  const key = `${provider}/${model}`
  let hash = 0
  for (let i = 0; i < key.length; i++) hash = ((hash * 31) + key.charCodeAt(i)) >>> 0
  return `hsl(${hash % 360} 60% 50%)`
}

function DayBarsChart({ days, loading }: { days: Array<OcxUsageResponse['days'][0]>; loading?: boolean }) {
  const sorted = useMemo(() => [...days].sort((a, b) => a.date.localeCompare(b.date)).slice(-7), [days])
  const maxTokens = Math.max(1, ...sorted.map((d) => d.totalTokens))

  if (loading) return <Skeleton height={180} />

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7, 1fr)', gap: 10, alignItems: 'end', height: 210, paddingTop: 8 }}>
      {sorted.map((day) => {
        const heightPercent = Math.min(100, Math.round((day.totalTokens / maxTokens) * 100))
        const weekday = new Intl.DateTimeFormat('zh-CN', { weekday: 'short' }).format(new Date(`${day.date}T12:00:00`))
        const fullDate = new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' }).format(new Date(`${day.date}T12:00:00`))
        const models = day.models || []

        const tooltipContent = (
          <div style={{ padding: '4px 6px', minWidth: 190, maxWidth: 260 }}>
            <Text fw={700} size="xs" mb={4}>{fullDate}</Text>
            <Group justify="space-between" mb={6} style={{ fontSize: 11 }}>
              <Text size="xs" c="dimmed">{zhNumber.format(day.requests)} 请求</Text>
              <Text size="xs" fw={650}>{formatTokens(day.totalTokens)} Token</Text>
            </Group>
            {models.length > 0 && (
              <div style={{ borderTop: '1px solid var(--astr-border, rgba(0,0,0,0.08))', paddingTop: 6, display: 'flex', flexDirection: 'column', gap: 4 }}>
                {models.slice(0, 8).map((m) => (
                  <Group key={`${m.provider}:${m.model}`} justify="space-between" gap="xs" style={{ fontSize: 11 }}>
                    <Group gap={6} wrap="nowrap" style={{ overflow: 'hidden' }}>
                      <span style={{ width: 8, height: 8, borderRadius: '50%', background: getModelColor(m.model, m.provider), flexShrink: 0 }} />
                      <Text size="xs" truncate style={{ maxWidth: 130 }}>{m.model}</Text>
                    </Group>
                    <Text size="xs" fw={600} style={{ fontFamily: 'ui-monospace, monospace' }}>{formatTokens(m.totalTokens)}</Text>
                  </Group>
                ))}
              </div>
            )}
          </div>
        )

        return (
          <Tooltip key={day.date} label={tooltipContent} withArrow position="top" radius="sm" multiline>
            <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', height: '100%', cursor: 'pointer' }}>
              <div
                style={{
                  flex: 1,
                  width: '100%',
                  maxWidth: 52,
                  background: 'color-mix(in srgb, var(--astr-text, #000) 5%, transparent)',
                  borderRadius: 6,
                  display: 'flex',
                  alignItems: 'flex-end',
                  overflow: 'hidden',
                  padding: 0,
                }}
              >
                <div
                  style={{
                    width: '100%',
                    height: `${heightPercent}%`,
                    minHeight: day.totalTokens > 0 ? 3 : 0,
                    borderRadius: '6px 6px 0 0',
                    display: 'flex',
                    flexDirection: 'column-reverse',
                    overflow: 'hidden',
                    transition: 'height 0.3s ease',
                  }}
                >
                  {models.length > 0 ? (
                    models.map((m) => {
                      const share = day.totalTokens > 0 ? (m.totalTokens / day.totalTokens) * 100 : 0
                      return (
                        <div
                          key={`${m.provider}:${m.model}`}
                          style={{
                            width: '100%',
                            height: `${share}%`,
                            minHeight: m.totalTokens > 0 ? 1 : 0,
                            background: getModelColor(m.model, m.provider),
                          }}
                        />
                      )
                    })
                  ) : (
                    <div style={{ width: '100%', height: '100%', background: 'var(--astr-indigo, #6366f1)' }} />
                  )}
                </div>
              </div>

              <Text fw={700} size="xs" mt={6} style={{ fontFamily: 'ui-monospace, monospace', fontSize: 11.5 }}>
                {formatTokens(day.totalTokens)}
              </Text>
              <Text size="xs" c="dimmed" mt={1} style={{ fontSize: 11 }}>
                {weekday}
              </Text>
            </div>
          </Tooltip>
        )
      })}
    </div>
  )
}

function BreakdownTable({ title, rows, showModel }: { title: string; rows: OcxUsageBreakdown[]; showModel?: boolean }) {
  return (
    <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface, #fff)' }}>
      <Text fw={650} size="sm" mb="sm">{title}</Text>
      <Table.ScrollContainer minWidth={850}>
        <Table striped highlightOnHover verticalSpacing="xs" horizontalSpacing="sm">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{showModel ? '模型' : '提供方'}</Table.Th>
              {showModel && <Table.Th>提供方</Table.Th>}
              <Table.Th ta="right">请求</Table.Th>
              <Table.Th ta="right">Token</Table.Th>
              <Table.Th ta="right">输入 / 输出</Table.Th>
              <Table.Th ta="right">缓存读取</Table.Th>
              <Table.Th ta="right">缓存命中率</Table.Th>
              <Table.Th ta="right">计价覆盖率</Table.Th>
              <Table.Th ta="right">占比</Table.Th>
              <Table.Th ta="right">标价折算</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map((row, index) => (
              <Table.Tr key={`${row.provider}:${row.model || ''}:${index}`}>
                <Table.Td fw={600}>{showModel ? row.model : row.provider}</Table.Td>
                {showModel && <Table.Td>{row.provider}</Table.Td>}
                <Table.Td ta="right">{zhNumber.format(row.requests)}</Table.Td>
                <Table.Td ta="right">{formatTokens(row.totalTokens)}</Table.Td>
                <Table.Td ta="right">{formatTokens(row.inputTokens)} / {formatTokens(row.outputTokens)}</Table.Td>
                <Table.Td ta="right">{formatTokens(row.cacheReadInputTokens)}</Table.Td>
                <Table.Td ta="right">{row.cacheHitRate == null ? '—' : percent(row.cacheHitRate)}</Table.Td>
                <Table.Td ta="right">{percent(row.priceCoverageRatio)}</Table.Td>
                <Table.Td ta="right">{percent(row.shareRatio)}</Table.Td>
                <Table.Td ta="right">{usd.format(row.estimatedCostUsd || 0)}</Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </Card>
  )
}

export function AnalyticsPage() {
  const [data, setData] = useState<OcxUsageResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [range, setRange] = useState<'all' | '30d' | '7d' | 'custom'>('7d')
  const [surface, setSurface] = useState<'all' | 'codex' | 'claude' | 'grok'>('all')
  const [since, setSince] = useState(() => localInput(new Date(Date.now() - 7 * 86_400_000)))
  const [until, setUntil] = useState(() => localInput(new Date()))

  useEffect(() => {
    const sinceMs = range === 'custom' ? new Date(since).getTime() : undefined
    const untilMs = range === 'custom' ? new Date(until).getTime() : undefined
    if (range === 'custom' && (!Number.isFinite(sinceMs) || !Number.isFinite(untilMs) || sinceMs! >= untilMs!)) return
    let active = true
    setLoading(true)
    api.getAnalyticsUsage({ range: range === 'custom' ? 'all' : range, surface, since: sinceMs, until: untilMs })
      .then((result) => { if (active) setData(result) })
      .catch((error: any) => { if (active) notifications.show({ color: 'red', message: error.message || '加载网关用量失败' }) })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [range, surface, since, until])

  const summary = data?.summary || {}
  const days = data?.days || []
  const maxDayTokens = Math.max(1, ...days.map((day) => day.totalTokens))
  const activeDays = days.filter((day) => day.requests > 0).length
  const calendarDays = useMemo(() => [...days].sort((a, b) => a.date.localeCompare(b.date)), [days])
  const cacheHitRate = summary.cacheObservedInputTokens ? summary.cacheReadInputTokens / summary.cacheObservedInputTokens : 0
  const metrics = [
    { label: '请求数', value: zhNumber.format(summary.requests || 0), detail: `共 ${zhNumber.format(summary.attemptCount || 0)} 次尝试`, icon: IconBolt, color: 'indigo' },
    { label: '已计量', value: zhNumber.format(summary.measuredRequests || 0), detail: `${zhNumber.format(summary.reportedRequests || 0)} 次由提供方上报`, icon: IconCircleCheck, color: 'teal' },
    { label: 'Token 总数', value: formatTokens(summary.totalTokens || 0), detail: `输入 ${formatTokens(summary.inputTokens || 0)} · 输出 ${formatTokens(summary.outputTokens || 0)}`, icon: IconChartHistogram, color: 'violet' },
    { label: '缓存命中 Token', value: formatTokens(summary.cacheReadInputTokens || 0), detail: `缓存命中率 ${percent(cacheHitRate)}`, icon: IconDatabase, color: 'cyan' },
    { label: '覆盖率', value: percent(summary.coverageRatio || 0), detail: `${zhNumber.format(summary.unreportedRequests || 0)} 个请求未上报用量`, icon: IconCircleCheck, color: 'blue' },
    { label: '活跃天数', value: zhNumber.format(activeDays), detail: `当前范围共 ${days.length} 天`, icon: IconClock, color: 'grape' },
    { label: 'API 标价折算', value: usd.format(summary.estimatedCostUsd || 0), detail: `${zhNumber.format(summary.pricedRequests || 0)} 个请求已计价`, icon: IconCoin, color: 'yellow' },
    { label: '无法计费请求', value: zhNumber.format(summary.unpricedRequests || 0), detail: `${zhNumber.format(summary.unmeteredRequests || 0)} 个请求未计量`, icon: IconAlertTriangle, color: 'orange' },
  ]

  return (
    <div style={{ height: '100%', overflowY: 'auto', padding: '16px 20px 36px', background: 'var(--astr-canvas-bg, #f8f9fb)' }}>
      <Stack gap={12}>
        <Group justify="space-between" align="center" wrap="wrap">
          <Group gap="xs">
            <Title order={3} style={{ fontSize: 17, fontWeight: 700, letterSpacing: '-0.02em' }}>用量与 Token 统计</Title>
            <Badge size="xs" variant="light" color="indigo" radius="sm">网关实时数据</Badge>
            <Text size="xs" c="dimmed">
              {data?.generatedAt ? `更新于 ${new Date(data.generatedAt).toLocaleString('zh-CN')}` : '正在读取网关数据'}
            </Text>
          </Group>
          <Group gap={8} wrap="wrap">
            <SegmentedControl size="xs" value={surface} onChange={(value) => setSurface(value as typeof surface)} data={[
              { label: '全部', value: 'all' }, { label: 'Codex', value: 'codex' }, { label: 'Claude', value: 'claude' }, { label: 'Grok', value: 'grok' },
            ]} />
            <SegmentedControl size="xs" value={range} onChange={(value) => setRange(value as typeof range)} data={[
              { label: '可用历史', value: 'all' }, { label: '30 天', value: '30d' }, { label: '7 天', value: '7d' }, { label: '自定义', value: 'custom' },
            ]} />
          </Group>
        </Group>

        {range === 'custom' && (
          <Group grow>
            <TextInput type="datetime-local" label="开始时间" value={since} onChange={(event) => setSince(event.currentTarget.value)} />
            <TextInput type="datetime-local" label="结束时间" value={until} onChange={(event) => setUntil(event.currentTarget.value)} />
          </Group>
        )}

        {(data?.historyTruncated || data?.entriesTruncated) && (
          <Alert color="yellow" icon={<IconAlertTriangle size={16} />} title="部分历史数据已截断">
            已丢弃 {zhNumber.format(data.entriesDropped || 0)} 条记录，截断前缀 {formatTokens(data.truncatedPrefixBytes || 0)} 字节。
          </Alert>
        )}

        <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} spacing="sm">
          {metrics.map((metric) => {
            const Icon = metric.icon
            return <Card key={metric.label} p="sm" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)' }}>
              <Group justify="space-between" align="center">
                <Text size="xs" c="dimmed" fw={600}>{metric.label}</Text>
                <ActionIcon size="sm" radius="md" color={metric.color} variant="subtle"><Icon size={15} /></ActionIcon>
              </Group>
              <Text fw={750} mt={2} style={{ fontFamily: 'ui-monospace, monospace', fontSize: 21, letterSpacing: '-0.02em' }}>
                {loading ? <Skeleton height={24} width={80} /> : metric.value}
              </Text>
              <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid var(--astr-border, #f1f5f9)' }}>
                <Text size="xs" c="dimmed" style={{ fontSize: 11 }}>{metric.detail}</Text>
              </div>
            </Card>
          })}
        </SimpleGrid>

        <Grid>
          <Grid.Col span={{ base: 12, lg: range === '7d' ? 7 : 4 }}>
            <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface, #fff)', height: '100%' }}>
              <Group justify="space-between" mb="sm">
                <Group gap={6}><IconCalendarStats size={15} color="var(--astr-indigo, #6366f1)" /><Text fw={650} size="xs">{range === '7d' ? '每日活动' : '每日 Token 活动'}</Text></Group>
                <Text size="xs" c="dimmed">活跃 {activeDays} 天</Text>
              </Group>
              {range === '7d' ? (
                <DayBarsChart days={days} loading={loading} />
              ) : loading ? <Skeleton height={112} /> : (
                <div style={{ overflowX: 'auto', paddingBottom: 4 }}>
                  <div style={{ display: 'flex', gap: 4, width: 'max-content', minWidth: '100%' }}>
                    <div style={{ display: 'grid', gridTemplateRows: 'repeat(7, 11px)', gap: 3, paddingRight: 2 }}>
                      {['日', '', '二', '', '四', '', '六'].map((label, index) => <span key={index} style={{ width: 12, fontSize: 9, lineHeight: '11px', color: label ? 'var(--astr-muted, #94a3b8)' : 'transparent' }}>{label || '·'}</span>)}
                    </div>
                    <div style={{ display: 'grid', gridAutoFlow: 'column', gridTemplateRows: 'repeat(7, 11px)', gridAutoColumns: '11px', gap: 3, width: 'max-content' }}>
                      {calendarDays.map((day) => {
                        const intensity = day.totalTokens / maxDayTokens
                        const alpha = day.totalTokens ? 0.22 + intensity * 0.78 : 0.06
                        return <Tooltip key={day.date} label={`${day.date} · ${formatTokens(day.totalTokens)} Token · ${day.requests} 次请求 · ${usd.format(day.estimatedCostUsd || 0)}`}>
                          <span style={{ width: 11, height: 11, borderRadius: 2, background: `rgba(79, 70, 229, ${alpha})` }} />
                        </Tooltip>
                      })}
                    </div>
                  </div>
                </div>
              )}
              {range !== '7d' && (
                <Group justify="space-between" mt="sm">
                  <Text size="xs" c="dimmed">共 {days.length} 天 · {zhNumber.format(summary.requests || 0)} 次请求</Text>
                  <Group gap={4}><Text size="xs" c="dimmed">较少</Text>{[0.08, 0.25, 0.5, 0.75, 1].map(value => <span key={value} style={{ width: 9, height: 9, borderRadius: 2, background: `rgba(79, 70, 229, ${value})` }} />)}<Text size="xs" c="dimmed">较多</Text></Group>
                </Group>
              )}
            </Card>
          </Grid.Col>
          <Grid.Col span={{ base: 12, lg: range === '7d' ? 5 : 8 }}>
            <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface, #fff)', height: '100%' }}>
              <Group justify="space-between" mb={6}><Group gap={6}><IconChartHistogram size={15} color="var(--astr-indigo, #6366f1)" /><Text fw={650} size="xs">计量覆盖与缓存</Text></Group><Badge size="xs" variant="light">{percent(summary.coverageRatio || 0)}</Badge></Group>
              <Progress value={(summary.coverageRatio || 0) * 100} size={5} color="indigo" mb={6} />
              <SimpleGrid cols={2} spacing={0}>
                {[
                  ['已计量', summary.measuredRequests], ['提供方上报', summary.reportedRequests], ['估算', summary.estimatedRequests], ['未上报', summary.unreportedRequests],
                  ['发送 / 已结算', `${zhNumber.format(summary.sends || 0)} / ${zhNumber.format(summary.settledSends || 0)}`], ['缓存合成请求', summary.cacheSynthesizedRequests],
                  ['缓存未知请求', summary.cacheUnknownRequests], ['推理输出 Token', formatTokens(summary.reasoningOutputTokens || 0)],
                ].map(([label, value]) => <Group key={label as string} justify="space-between" gap="xs" py={6} px={8} style={{ borderBottom: '1px solid var(--astr-border, #f1f5f9)' }}><Text size="xs" c="dimmed">{label}</Text><Text size="xs" fw={650}>{typeof value === 'number' ? zhNumber.format(value) : value}</Text></Group>)}
              </SimpleGrid>
            </Card>
          </Grid.Col>
        </Grid>

        <BreakdownTable title="模型用量" rows={data?.models || []} showModel />
        <BreakdownTable title="提供方用量" rows={data?.providers || []} />

        <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface, #fff)' }}>
          <Text fw={650} size="sm" mb="sm">账号用量</Text>
          <Table.ScrollContainer minWidth={760}>
            <Table striped highlightOnHover verticalSpacing="xs">
              <Table.Thead><Table.Tr><Table.Th>账号</Table.Th><Table.Th ta="right">请求</Table.Th><Table.Th ta="right">已计量 / 未计量</Table.Th><Table.Th ta="right">Token</Table.Th><Table.Th ta="right">缓存读取</Table.Th><Table.Th ta="right">用量覆盖率</Table.Th><Table.Th ta="right">计价覆盖率</Table.Th><Table.Th ta="right">标价折算</Table.Th></Table.Tr></Table.Thead>
              <Table.Tbody>
                {(data?.accounts || []).map((account) => <Table.Tr key={account.accountLogLabel}>
                  <Table.Td><Group gap={6}><Text size="sm" fw={600}>{account.accountLogLabel}</Text>{account.ambiguous && <Badge size="xs" color="yellow">不明确</Badge>}</Group></Table.Td>
                  <Table.Td ta="right">{zhNumber.format(account.requests)}</Table.Td>
                  <Table.Td ta="right">{zhNumber.format(account.measuredAttempts)} / {zhNumber.format(account.unmeteredAttempts)}</Table.Td>
                  <Table.Td ta="right">{formatTokens(account.totalTokens)}</Table.Td>
                  <Table.Td ta="right">{formatTokens(account.cacheReadInputTokens)}</Table.Td>
                  <Table.Td ta="right">{percent(account.usageCoverageRatio)}</Table.Td>
                  <Table.Td ta="right">{percent(account.priceCoverageRatio)}</Table.Td>
                  <Table.Td ta="right">{usd.format(account.estimatedCostUsd || 0)}</Table.Td>
                </Table.Tr>)}
              </Table.Tbody>
            </Table>
          </Table.ScrollContainer>
        </Card>
      </Stack>
    </div>
  )
}
