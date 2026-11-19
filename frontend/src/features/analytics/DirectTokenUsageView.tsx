import { useEffect, useId, useState } from 'react'
import { Alert, Badge, Button, Group, Paper, Progress, ScrollArea, SegmentedControl, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import { IconRefresh } from '@tabler/icons-react'
import { useInRouterContext, useNavigate } from 'react-router-dom'
import { api, type DirectUsageSessionRow, type OcxUsageResponse } from '../../api/client'
import { formatTokens, getModelColor } from './usageFormat'
import './directTokenUsage.css'

type DirectRange = 'today' | '7d' | '30d' | 'all'
type DirectSurface = 'all' | 'antigravity' | 'codex' | 'grok' | 'hermes' | 'claude'

const rangeOptions: Array<{ label: string; value: DirectRange }> = [
  { label: '今日', value: 'today' },
  { label: '近 7 天', value: '7d' },
  { label: '近 30 天', value: '30d' },
  { label: '全部', value: 'all' },
]

const surfaceOptions: Array<{ label: string; value: DirectSurface }> = [
  { label: '全部直连', value: 'all' },
  { label: 'Antigravity', value: 'antigravity' },
  { label: 'Codex', value: 'codex' },
  { label: 'Grok', value: 'grok' },
  { label: 'Hermes', value: 'hermes' },
]

const zhInt = new Intl.NumberFormat('zh-CN')

function MetricCard({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <Paper withBorder radius="md" p="md" className="direct-usage-metric">
      <Text size="xs" c="dimmed">{label}</Text>
      <Text size="xl" fw={700} mt={4}>{value}</Text>
      {sub && <Text size="xs" c="dimmed" mt={4}>{sub}</Text>}
    </Paper>
  )
}

function SessionNavTrigger({ row }: { row: DirectUsageSessionRow }) {
  const navigate = useNavigate()
  return (
    <button
      type="button"
      className="direct-usage-session-link"
      onClick={() => navigate(`/chat/${encodeURIComponent(row.sessionId)}?agent_id=${encodeURIComponent(row.agentId)}`)}
      title={`${row.title}${row.workspace ? ` · ${row.workspace}` : ''}`}
    >
      <span className="direct-usage-session-title">{row.title || row.sessionId}</span>
      {row.workspace && <span className="direct-usage-session-sub">{row.workspace}</span>}
    </button>
  )
}

export function DirectTokenUsageView() {
  const [range, setRange] = useState<DirectRange>('7d')
  const [surface, setSurface] = useState<DirectSurface>('all')
  const [revision, setRevision] = useState(0)
  const [data, setData] = useState<OcxUsageResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const inRouter = useInRouterContext()
  const sessionsHeadingId = useId()

  useEffect(() => {
    let active = true
    api.getAnalyticsUsage({ range, surface, source: 'direct' })
      .then((result) => {
        if (active) setData(result)
      })
      .catch((cause: unknown) => {
        if (active) setError(cause instanceof Error ? cause.message : '读取直连 Token 统计失败')
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [range, surface, revision])

  const summary = data?.summary || {}
  const totalTokens = Number(summary.totalTokens || 0)
  const inputTokens = Number(summary.inputTokens || 0)
  const outputTokens = Number(summary.outputTokens || 0)
  const cacheReadTokens = Number(summary.cacheReadInputTokens || summary.cachedInputTokens || 0)
  const reasoningTokens = Number(summary.reasoningOutputTokens || 0)
  const requests = Number(summary.requests || 0)
  const cacheHitRate = Number(summary.cacheHitRate || 0) * 100
  const activeSessions = Number(summary.activeSessions || 0)
  const activeAgents = Number(summary.activeAgents || 0)
  const days = data?.days || []
  const models = data?.models || []
  const directAgents = data?.directAgents || []
  const directSessions = data?.directSessions || []
  const maxDayTokens = Math.max(1, ...days.map((d) => Number(d.totalTokens || 0)))

  return (
    <ScrollArea className="direct-usage-scroll" type="auto">
      <div className="direct-usage-content">
        <Group justify="space-between" align="flex-start" gap="md" wrap="wrap">
          <div>
            <Title order={2} size="h3">直连 Agent Token 统计</Title>
            <Text size="sm" c="dimmed" mt={4}>
              自动汇总未经过模型网关的直连 Agent Token 消耗（含本机与 SSH 远端原生会话步骤统计），已接入网关的流量不会重复计入。
            </Text>
          </div>
          <Button
            size="xs"
            variant="subtle"
            leftSection={<IconRefresh size={14} />}
            onClick={() => {
              setLoading(true)
              setError('')
              setRevision((v) => v + 1)
            }}
            loading={loading}
          >
            刷新
          </Button>
        </Group>

        <Group gap="sm" wrap="wrap" className="direct-usage-filters">
          <SegmentedControl
            size="xs"
            value={range}
            onChange={(val) => {
              setLoading(true)
              setError('')
              setRange(val as DirectRange)
            }}
            data={rangeOptions}
            aria-label="统计时间范围"
          />
          <SegmentedControl
            size="xs"
            value={surface}
            onChange={(val) => {
              setLoading(true)
              setError('')
              setSurface(val as DirectSurface)
            }}
            data={surfaceOptions}
            aria-label="直连 Agent 类型"
          />
        </Group>

        {data?.daemonUpgradeRequired && (
          <Alert color="blue" mb="md">
            当前运行的小内核尚未支持原生会话聚合查询，请在系统托盘点击“小内核 → 更新小内核”以加载完整的 Antigravity 步骤级历史统计。
          </Alert>
        )}

        {loading ? (
          <Text role="status" size="sm" c="dimmed">正在读取直连 Token 统计…</Text>
        ) : error ? (
          <Alert color="red">{error}</Alert>
        ) : data && (
          <Stack gap="md">
            <SimpleGrid cols={{ base: 2, md: 4 }} spacing="sm">
              <MetricCard
                label="总消耗 Token"
                value={formatTokens(totalTokens)}
                sub={`未缓存输入 ${formatTokens(inputTokens)} · 输出 ${formatTokens(outputTokens)}`}
              />
              <MetricCard
                label="推理步数 / 请求"
                value={zhInt.format(requests)}
                sub={reasoningTokens > 0 ? `含思考 ${formatTokens(reasoningTokens)}` : '原生步骤精确计量'}
              />
              <MetricCard
                label="缓存命中率"
                value={totalTokens > 0 ? `${cacheHitRate.toFixed(1)}%` : '—'}
                sub={`缓存读取 ${formatTokens(cacheReadTokens)}`}
              />
              <MetricCard
                label="活跃直连会话"
                value={zhInt.format(activeSessions)}
                sub={`${zhInt.format(activeAgents)} 个直连 Agent 节点`}
              />
            </SimpleGrid>

            {totalTokens === 0 && requests === 0 ? (
              <Paper withBorder radius="md" p="xl">
                <Text size="sm" c="dimmed" ta="center">所选时间与筛选范围内暂无未走网关的直连 Token 消耗</Text>
              </Paper>
            ) : (
              <>
                {days.length > 0 && (
                  <Paper withBorder radius="md" p="md" className="direct-usage-panel">
                    <Group justify="space-between" mb="sm">
                      <Title order={3} size="h5">每日消耗趋势</Title>
                      <Text size="xs" c="dimmed">共 {days.length} 天有活跃记录</Text>
                    </Group>
                    <div className="direct-usage-days">
                      {days.map((day) => {
                        const pct = Math.max(2, (Number(day.totalTokens || 0) / maxDayTokens) * 100)
                        return (
                          <div className="direct-usage-day-row" key={day.date}>
                            <span className="direct-usage-mono">{day.date}</span>
                            <div className="direct-usage-day-bar-track" aria-hidden="true">
                              <div className="direct-usage-day-bar-fill" style={{ inlineSize: `${pct}%` }} />
                            </div>
                            <span className="direct-usage-mono">
                              {formatTokens(day.totalTokens)} · {zhInt.format(day.requests)} 步
                            </span>
                          </div>
                        )
                      })}
                    </div>
                  </Paper>
                )}

                <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
                  <Paper withBorder radius="md" p="md" className="direct-usage-panel">
                    <Title order={3} size="h5" mb="sm">模型消耗分布</Title>
                    <Stack gap="sm">
                      {models.map((m) => {
                        const share = Math.max(0, Math.min(100, Number(m.shareRatio || 0) * 100))
                        return (
                          <div key={`${m.provider}:${m.model}`}>
                            <Group justify="space-between" gap="xs" wrap="nowrap">
                              <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
                                <span
                                  className="direct-usage-model-dot"
                                  style={{ background: getModelColor(m.model, m.provider) }}
                                  aria-hidden="true"
                                />
                                <Text size="sm" fw={500} truncate>{m.model}</Text>
                                {m.provider && <Badge size="xs" variant="light" color="gray">{m.provider}</Badge>}
                              </Group>
                              <Text size="sm" fw={600} className="direct-usage-mono" style={{ flexShrink: 0 }}>
                                {formatTokens(m.totalTokens)} ({share.toFixed(1)}%)
                              </Text>
                            </Group>
                            <Progress value={share} size={5} radius="xl" mt={6} color="blue" />
                            <Text size="xs" c="dimmed" mt={4}>
                              {zhInt.format(m.requests)} 步 · 输入 {formatTokens(m.inputTokens)} · 缓存 {formatTokens(m.cacheReadInputTokens)} · 输出 {formatTokens(m.outputTokens)}
                            </Text>
                          </div>
                        )
                      })}
                    </Stack>
                  </Paper>

                  <Paper withBorder radius="md" p="md" className="direct-usage-panel">
                    <Title order={3} size="h5" mb="sm">直连 Agent / 节点分布</Title>
                    <Stack gap="sm">
                      {directAgents.map((ag) => {
                        const share = Math.max(0, Math.min(100, Number(ag.shareRatio || 0) * 100))
                        return (
                          <div key={ag.agentId}>
                            <Group justify="space-between" gap="xs" wrap="nowrap">
                              <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
                                <Text size="sm" fw={500} truncate>{ag.name}</Text>
                                <Badge size="xs" variant="light">{ag.connectionName}</Badge>
                              </Group>
                              <Text size="sm" fw={600} className="direct-usage-mono" style={{ flexShrink: 0 }}>
                                {formatTokens(ag.totalTokens)} ({share.toFixed(1)}%)
                              </Text>
                            </Group>
                            <Progress value={share} size={5} radius="xl" mt={6} color="teal" />
                            <Text size="xs" c="dimmed" mt={4}>
                              {zhInt.format(ag.sessions)} 个会话 · {zhInt.format(ag.requests)} 步 · 缓存命中 {(Number(ag.cacheHitRate || 0) * 100).toFixed(1)}%
                            </Text>
                          </div>
                        )
                      })}
                    </Stack>
                  </Paper>
                </SimpleGrid>

                {directSessions.length > 0 && (
                  <Paper withBorder radius="md" p="md" className="direct-usage-panel">
                    <Group justify="space-between" mb="sm">
                      <Title order={3} size="h5" id={sessionsHeadingId}>直连会话消耗排行</Title>
                      <Text size="xs" c="dimmed">前 {directSessions.length} 个高消耗会话</Text>
                    </Group>
                    <div
                      tabIndex={0}
                      role="region"
                      aria-labelledby={sessionsHeadingId}
                      className="direct-usage-table-wrapper"
                    >
                      <table className="direct-usage-table">
                        <thead>
                          <tr>
                            <th scope="col">会话</th>
                            <th scope="col">Agent / 节点</th>
                            <th scope="col">模型</th>
                            <th scope="col" className="direct-usage-num">步数</th>
                            <th scope="col" className="direct-usage-num">输入 / 缓存 / 输出</th>
                            <th scope="col" className="direct-usage-num">总消耗</th>
                          </tr>
                        </thead>
                        <tbody>
                          {directSessions.map((row) => (
                            <tr key={`${row.agentId}:${row.sessionId}`}>
                              <th scope="row">
                                {inRouter ? (
                                  <SessionNavTrigger row={row} />
                                ) : (
                                  <div>
                                    <span className="direct-usage-session-title">{row.title || row.sessionId}</span>
                                    {row.workspace && <span className="direct-usage-session-sub">{row.workspace}</span>}
                                  </div>
                                )}
                              </th>
                              <td>
                                <Group gap={6} wrap="nowrap">
                                  <Text size="xs" fw={500}>{row.agentName}</Text>
                                  <Badge size="xs" variant="outline" color="gray">{row.connectionName}</Badge>
                                </Group>
                              </td>
                              <td><Text size="xs" className="direct-usage-mono">{row.model || '—'}</Text></td>
                              <td className="direct-usage-num">{zhInt.format(row.requests)}</td>
                              <td className="direct-usage-num">
                                <Text size="xs" c="dimmed" component="span">
                                  {formatTokens(row.inputTokens)} / {formatTokens(row.cacheReadInputTokens)} / {formatTokens(row.outputTokens)}
                                </Text>
                              </td>
                              <td className="direct-usage-num">
                                <Text size="sm" fw={600} component="span">{formatTokens(row.totalTokens)}</Text>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </Paper>
                )}
              </>
            )}
          </Stack>
        )}
      </div>
    </ScrollArea>
  )
}
