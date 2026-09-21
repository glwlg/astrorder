import { useEffect, useState, useMemo } from 'react'
import {
  ActionIcon,
  Badge,
  Card,
  Group,
  Progress,
  SegmentedControl,
  SimpleGrid,
  Skeleton,
  Stack,
  Table,
  Text,
  Title,
  Tooltip,
  Grid,
} from '@mantine/core'
import {
  IconBolt,
  IconBrain,
  IconCalendarStats,
  IconChartBar,
  IconClock,
  IconCpu,
  IconDatabase,
  IconFileText,
  IconRefresh,
} from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api } from '../../api/client'

const zhNumber = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 })

export function formatTokens(tokens: number = 0): string {
  if (tokens >= 100_000_000) return `${zhNumber.format(tokens / 100_000_000)} 亿`
  if (tokens >= 10_000) return `${zhNumber.format(tokens / 10_000)} 万`
  return zhNumber.format(tokens)
}

export function AnalyticsPage() {
  const [overview, setOverview] = useState<any>(null)
  const [calendarData, setCalendarData] = useState<any>(null)
  const [topSessions, setTopSessions] = useState<any[]>([])
  const [loading, setLoading] = useState(true)
  const [syncing, setSyncing] = useState(false)
  const [selectedRange, setSelectedRange] = useState<'30' | '90' | '180' | '365'>('180')

  const loadAllData = async () => {
    setLoading(true)
    try {
      const [resOverview, resCalendar, resTop] = await Promise.all([
        api.getAnalyticsOverview(),
        api.getAnalyticsCalendar(parseInt(selectedRange, 10)),
        api.getAnalyticsTopSessions(8),
      ])
      setOverview(resOverview)
      setCalendarData(resCalendar)
      setTopSessions(resTop.items || [])
    } catch (e: any) {
      notifications.show({ color: 'red', message: e.message || '加载统计数据失败' })
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void loadAllData()
  }, [selectedRange])

  const handleManualSync = async () => {
    setSyncing(true)
    try {
      const res = await api.syncAnalytics()
      notifications.show({
        color: 'teal',
        message: `已同步回溯 ${res.result.total_records || 0} 条历史会话 Token 记录`,
      })
      void loadAllData()
    } catch (e: any) {
      notifications.show({ color: 'red', message: e.message || '同步失败' })
    } finally {
      setSyncing(false)
    }
  }

  const totals = overview?.totals || {}
  const today = overview?.today || {}
  const models = overview?.by_model || []
  const rawAgents = overview?.by_agent || []

  // 聚合智能体算力引擎
  const agentBreakdown = useMemo(() => {
    const totalTok = totals.total_tokens || 1
    const engineMap: Record<string, { label: string; desc: string; color: string; tokens: number; sessions: number; inTok: number; cacheTok: number }> = {
      codex: { label: 'Codex-Pro', desc: '全局架构与主线开发', color: '#4f46e5', tokens: 0, sessions: 0, inTok: 0, cacheTok: 0 },
      hermes: { label: 'Hermes', desc: '伴随执行与深度对弈', color: '#06b6d4', tokens: 0, sessions: 0, inTok: 0, cacheTok: 0 },
      grok: { label: 'Grok-3', desc: '实时对弈与验证探索', color: '#f59e0b', tokens: 0, sessions: 0, inTok: 0, cacheTok: 0 },
      other: { label: '其他辅助代理', desc: 'Reviewer / Sync / CI', color: '#9ca3af', tokens: 0, sessions: 0, inTok: 0, cacheTok: 0 },
    }

    rawAgents.forEach((a: any) => {
      const prov = (a.provider || '').toLowerCase()
      const aid = (a.agent_id || '').toLowerCase()
      let targetKey = 'other'
      if (prov === 'codex' || aid.includes('codex')) targetKey = 'codex'
      else if (prov === 'hermes' || aid.includes('hermes') || prov === 'custom') targetKey = 'hermes'
      else if (prov === 'grok' || aid.includes('grok')) targetKey = 'grok'

      const e = engineMap[targetKey]
      e.tokens += a.tokens || 0
      e.sessions += a.session_count || 0
      e.inTok += a.input_tokens || 0
      e.cacheTok += a.cached_tokens || 0
    })

    return Object.entries(engineMap).map(([k, item]) => {
      const share = roundNum((item.tokens / totalTok) * 100, 1)
      const hitRate = item.inTok > 0 ? Math.min(100, Math.round((item.cacheTok / item.inTok) * 100)) : 0
      return { key: k, ...item, share, hitRate }
    }).filter(i => i.tokens > 0 || i.key !== 'other')
  }, [rawAgents, totals.total_tokens])

  const formatRate = (rate: number = 0) => {
    return Math.min(100, Math.max(0, Math.round(rate)))
  }

  function roundNum(val: number, precision: number = 1) {
    const p = Math.pow(10, precision)
    return Math.round(val * p) / p
  }

  // 构造真实日历：按周对齐矩阵
  const { calendarWeeks, monthMarkers, totalActiveDays, avgDailyTokens, peakDay } = useMemo(() => {
    const rawItems = calendarData?.items || []
    const map = new Map<string, any>()
    rawItems.forEach((it: any) => map.set(it.date, it))

    const daysCount = parseInt(selectedRange, 10)
    const today = new Date()
    today.setHours(0, 0, 0, 0)

    const startDate = new Date(today)
    startDate.setDate(startDate.getDate() - (daysCount - 1))

    // 对齐到所在周的周日（0 = 周日，1 = 周一）
    const dayOfWeek = startDate.getDay()
    startDate.setDate(startDate.getDate() - dayOfWeek)

    const weeks: any[][] = []
    let currentWeek: any[] = []
    const cur = new Date(startDate)

    let activeCount = 0
    let totalTokensInRange = 0
    let peakTok = 0
    let peakDate = ''

    const months: { label: string; weekIdx: number }[] = []
    let lastMonth = -1

    while (cur <= today || currentWeek.length > 0) {
      const dateStr = cur.toISOString().slice(0, 10)
      const isFuture = cur > today
      const it = map.get(dateStr)
      const tok = isFuture ? 0 : it?.tokens || 0

      if (tok > 0) {
        activeCount++
        totalTokensInRange += tok
        if (tok > peakTok) {
          peakTok = tok
          peakDate = dateStr
        }
      }

      currentWeek.push({
        date: dateStr,
        dayOfMonth: cur.getDate(),
        month: cur.getMonth() + 1,
        tokens: tok,
        cached_tokens: it?.cached_tokens || 0,
        cache_hit_rate: it?.cache_hit_rate || 0,
        session_count: it?.session_count || 0,
        isFuture,
      })

      if (currentWeek.length === 7) {
        const m = currentWeek[0].month
        if (m !== lastMonth) {
          months.push({ label: `${m}月`, weekIdx: weeks.length })
          lastMonth = m
        }
        weeks.push(currentWeek)
        currentWeek = []
        if (cur >= today) break
      }
      cur.setDate(cur.getDate() + 1)
    }

    const avg = activeCount > 0 ? Math.round(totalTokensInRange / activeCount) : 0

    return {
      calendarWeeks: weeks,
      monthMarkers: months,
      totalActiveDays: activeCount,
      avgDailyTokens: avg,
      peakDay: { date: peakDate, tokens: peakTok },
    }
  }, [calendarData, selectedRange])

  const maxDailyTokens = useMemo(() => {
    let m = 1
    calendarWeeks.forEach((week) => {
      week.forEach((d) => {
        if (d.tokens > m) m = d.tokens
      })
    })
    return m
  }, [calendarWeeks])

  return (
    <div
      style={{
        height: '100%',
        overflowY: 'auto',
        padding: '16px 20px 36px',
        background: 'var(--astr-canvas-bg, #f8f9fb)',
      }}
    >
      {/* 顶部紧凑标题栏 */}
      <Group justify="space-between" align="center" mb={12}>
        <Group gap="xs">
          <Title order={3} style={{ fontSize: 17, fontWeight: 700, letterSpacing: '-0.02em' }}>
            用量与 Token 统计
          </Title>
          <Badge size="xs" variant="light" color="indigo" radius="sm">
            实时聚合
          </Badge>
          <Text size="xs" c="dimmed" style={{ marginLeft: 4 }}>
            跨会话、跨智能体（Codex / Hermes / Grok）分布式算力及上下文消耗全景
          </Text>
        </Group>
        <Group gap={8}>
          <SegmentedControl
            size="xs"
            value={selectedRange}
            onChange={(val: any) => setSelectedRange(val)}
            data={[
              { label: '近 30 天', value: '30' },
              { label: '近 90 天', value: '90' },
              { label: '近半年', value: '180' },
              { label: '近 1 年', value: '365' },
            ]}
          />
          <Tooltip label="从原生引擎日志回溯同步历史数据" position="bottom" withArrow>
            <ActionIcon
              variant="default"
              size="sm"
              radius="md"
              loading={syncing}
              onClick={handleManualSync}
              aria-label="同步数据"
            >
              <IconRefresh size={14} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>

      {/* 4 项核心数据指标 */}
      <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} spacing="sm" mb={12}>
        <Card p="sm" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)' }}>
          <Group justify="space-between" align="center">
            <Text size="xs" c="dimmed" fw={600}>累计 TOKEN 消耗</Text>
            <ActionIcon size="sm" radius="md" color="indigo" variant="subtle">
              <IconBolt size={15} />
            </ActionIcon>
          </Group>
          <Group align="baseline" gap={6} mt={2}>
            <Text fw={750} style={{ fontFamily: 'ui-monospace, monospace', fontSize: 21, letterSpacing: '-0.02em' }}>
              {loading ? <Skeleton height={24} width={80} /> : formatTokens(totals.total_tokens)}
            </Text>
            <Badge size="xs" color="teal" variant="light" radius="sm">
              已结算
            </Badge>
          </Group>
          <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid var(--astr-border, #f1f5f9)', fontSize: 11, color: 'var(--astr-muted, #64748b)' }}>
            输入 <strong style={{ color: 'var(--astr-text, #111827)' }}>{formatTokens(totals.input_tokens)}</strong> · 输出 <strong style={{ color: 'var(--astr-text, #111827)' }}>{formatTokens(totals.output_tokens)}</strong>
          </div>
        </Card>

        <Card p="sm" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)' }}>
          <Group justify="space-between" align="center">
            <Text size="xs" c="dimmed" fw={600}>全域缓存命中率</Text>
            <ActionIcon size="sm" radius="md" color="teal" variant="subtle">
              <IconDatabase size={15} />
            </ActionIcon>
          </Group>
          <Group align="baseline" gap={6} mt={2}>
            <Text fw={750} style={{ fontFamily: 'ui-monospace, monospace', fontSize: 21, color: '#10b981', letterSpacing: '-0.02em' }}>
              {loading ? <Skeleton height={24} width={70} /> : `${formatRate(totals.cache_hit_rate)}%`}
            </Text>
            <Badge size="xs" color="teal" variant="outline" radius="sm">
              极高效复用
            </Badge>
          </Group>
          <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid var(--astr-border, #f1f5f9)', fontSize: 11, color: 'var(--astr-muted, #64748b)' }}>
            累计复用 <strong style={{ color: 'var(--astr-text, #111827)' }}>{formatTokens(totals.cached_tokens)}</strong> 缓存 Tokens
          </div>
        </Card>

        <Card p="sm" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)' }}>
          <Group justify="space-between" align="center">
            <Text size="xs" c="dimmed" fw={600}>今日活跃消耗</Text>
            <ActionIcon size="sm" radius="md" color="blue" variant="subtle">
              <IconClock size={15} />
            </ActionIcon>
          </Group>
          <Group align="baseline" gap={6} mt={2}>
            <Text fw={750} style={{ fontFamily: 'ui-monospace, monospace', fontSize: 21, letterSpacing: '-0.02em' }}>
              {loading ? <Skeleton height={24} width={80} /> : formatTokens(today.total_tokens)}
            </Text>
            <Text size="xs" c="dimmed" style={{ fontSize: 11 }}>
              缓存率 {formatRate(today.cache_hit_rate)}%
            </Text>
          </Group>
          <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid var(--astr-border, #f1f5f9)', fontSize: 11, color: 'var(--astr-muted, #64748b)' }}>
            输入 {formatTokens(today.input_tokens)} · 输出 {formatTokens(today.output_tokens)}
          </div>
        </Card>

        <Card p="sm" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)' }}>
          <Group justify="space-between" align="center">
            <Text size="xs" c="dimmed" fw={600}>已覆盖会话</Text>
            <ActionIcon size="sm" radius="md" color="violet" variant="subtle">
              <IconFileText size={15} />
            </ActionIcon>
          </Group>
          <Group align="baseline" gap={6} mt={2}>
            <Text fw={750} style={{ fontFamily: 'ui-monospace, monospace', fontSize: 21, letterSpacing: '-0.02em' }}>
              {loading ? <Skeleton height={24} width={60} /> : `${totals.session_count || 0} 个`}
            </Text>
            <Badge size="xs" color="violet" variant="light" radius="sm">
              跨多智能体
            </Badge>
          </Group>
          <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid var(--astr-border, #f1f5f9)', fontSize: 11, color: 'var(--astr-muted, #64748b)' }}>
            跨 {agentBreakdown.length || 3} 组环境智能体协同产出
          </div>
        </Card>
      </SimpleGrid>

      {/* 中层：非对称双栏结构 (60% 热力日历 + 40% 算力引擎分布) */}
      <Grid  mb={12}>
        {/* 左侧 60%：热力图，空间饱满无大片留白 */}
        <Grid.Col span={{ base: 12, lg: 7 }}>
          <Card p="md" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)', height: '100%' }}>
            <Group justify="space-between" align="center" mb="xs">
              <Group gap={6}>
                <IconCalendarStats size={15} color="var(--astr-indigo, #6366f1)" />
                <Text fw={650} size="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.02em' }}>
                  每日 Token 产出热力日历 (Activity Heatmap)
                </Text>
                <Badge size="xs" variant="subtle" color="gray">
                  共 {calendarWeeks.length} 周
                </Badge>
              </Group>
              <Text size="xs" c="dimmed" style={{ fontSize: 11 }}>
                活跃天数: <strong style={{ color: 'var(--astr-text, #111827)' }}>{totalActiveDays}</strong> 天
              </Text>
            </Group>

            {loading ? (
              <Skeleton height={110} radius="sm" />
            ) : (
              <div style={{ overflowX: 'auto', paddingBottom: 4 }}>
                <div style={{ minWidth: 540 }}>
                  {/* 月份刻度 */}
                  <div style={{ display: 'flex', fontSize: 10, fontFamily: 'ui-monospace, monospace', color: 'var(--astr-muted, #94a3b8)', marginBottom: 4, paddingLeft: 18 }}>
                    {monthMarkers.map((m, idx) => (
                      <div key={idx} style={{ width: `${(100 / Math.max(monthMarkers.length, 1))}%` }}>
                        {m.label}
                      </div>
                    ))}
                  </div>

                  <div style={{ display: 'flex', gap: 3.5, alignItems: 'flex-start' }}>
                    {/* 星期标签列 */}
                    <div style={{ display: 'grid', gridTemplateRows: 'repeat(7, 11px)', gap: 3, paddingRight: 4 }}>
                      <span style={{ fontSize: 9, color: 'var(--astr-muted, #94a3b8)', lineHeight: '11px' }}>日</span>
                      <span style={{ fontSize: 9, color: 'transparent', lineHeight: '11px' }}>一</span>
                      <span style={{ fontSize: 9, color: 'var(--astr-muted, #94a3b8)', lineHeight: '11px' }}>二</span>
                      <span style={{ fontSize: 9, color: 'transparent', lineHeight: '11px' }}>三</span>
                      <span style={{ fontSize: 9, color: 'var(--astr-muted, #94a3b8)', lineHeight: '11px' }}>四</span>
                      <span style={{ fontSize: 9, color: 'transparent', lineHeight: '11px' }}>五</span>
                      <span style={{ fontSize: 9, color: 'var(--astr-muted, #94a3b8)', lineHeight: '11px' }}>六</span>
                    </div>

                    {/* 按周排列的矩阵网格 */}
                    <div style={{ display: 'flex', gap: 3, flex: 1 }}>
                      {calendarWeeks.map((week, wIdx) => (
                        <div key={wIdx} style={{ display: 'grid', gridTemplateRows: 'repeat(7, 11px)', gap: 3 }}>
                          {week.map((day) => {
                            if (day.isFuture) {
                              return <div key={day.date} style={{ width: 11, height: 11 }} />
                            }
                            const tok = day.tokens
                            const ratio = tok / maxDailyTokens
                            let bg = 'color-mix(in srgb, var(--astr-text, #000) 5%, transparent)'
                            if (tok > 0) {
                              if (ratio < 0.05) bg = 'rgba(79, 70, 229, 0.25)'
                              else if (ratio < 0.25) bg = 'rgba(79, 70, 229, 0.5)'
                              else if (ratio < 0.65) bg = '#6366f1'
                              else bg = '#4338ca'
                            }
                            return (
                              <Tooltip
                                key={day.date}
                                label={
                                  <div style={{ fontSize: 11, lineHeight: 1.4 }}>
                                    <div style={{ fontWeight: 700 }}>{day.date}</div>
                                    <div>消耗: {formatTokens(tok)} tokens</div>
                                    {tok > 0 && <div>缓存率: {formatRate(day.cache_hit_rate)}%</div>}
                                    {tok > 0 && <div>涉及会话: {day.session_count} 个</div>}
                                  </div>
                                }
                                position="top"
                                withArrow
                              >
                                <div
                                  style={{
                                    width: 11,
                                    height: 11,
                                    borderRadius: 2,
                                    background: bg,
                                    cursor: 'pointer',
                                  }}
                                />
                              </Tooltip>
                            )
                          })}
                        </div>
                      ))}
                    </div>
                  </div>

                  {/* 底部指标与图例 */}
                  <Group justify="space-between" align="center" mt={10} style={{ fontSize: 11, color: 'var(--astr-muted, #94a3b8)' }}>
                    <div>
                      活跃日均: <strong style={{ color: 'var(--astr-text, #111827)' }}>{formatTokens(avgDailyTokens)}</strong>
                      {peakDay.date && (
                        <span style={{ marginLeft: 12 }}>
                          峰值日 ({peakDay.date}): <strong style={{ color: 'var(--astr-text, #111827)' }}>{formatTokens(peakDay.tokens)}</strong>
                        </span>
                      )}
                    </div>
                    <Group gap={4}>
                      <span>较少</span>
                      <span style={{ width: 9, height: 9, borderRadius: 2, background: 'color-mix(in srgb, var(--astr-text, #000) 5%, transparent)' }} />
                      <span style={{ width: 9, height: 9, borderRadius: 2, background: 'rgba(79, 70, 229, 0.25)' }} />
                      <span style={{ width: 9, height: 9, borderRadius: 2, background: 'rgba(79, 70, 229, 0.5)' }} />
                      <span style={{ width: 9, height: 9, borderRadius: 2, background: '#6366f1' }} />
                      <span style={{ width: 9, height: 9, borderRadius: 2, background: '#4338ca' }} />
                      <span>极多</span>
                    </Group>
                  </Group>
                </div>
              </div>
            )}
          </Card>
        </Grid.Col>

        {/* 右侧 40%：智能体引擎与算力分布 */}
        <Grid.Col span={{ base: 12, lg: 5 }}>
          <Card p="md" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)', height: '100%' }}>
            <Group justify="space-between" align="center" mb="xs">
              <Group gap={6}>
                <IconCpu size={15} color="var(--astr-indigo, #6366f1)" />
                <Text fw={650} size="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.02em' }}>
                  智能体引擎与算力分布 (Compute Breakdown)
                </Text>
              </Group>
              <Badge size="xs" variant="subtle" color="gray">
                按引擎聚合
              </Badge>
            </Group>

            {/* 堆叠占比条 */}
            <div style={{ marginTop: 8, marginBottom: 12 }}>
              <div style={{ height: 10, width: '100%', borderRadius: 5, overflow: 'hidden', display: 'flex', background: 'var(--astr-border, #f1f5f9)' }}>
                {agentBreakdown.map((e) => (
                  <div
                    key={e.key}
                    style={{
                      height: '100%',
                      width: `${e.share}%`,
                      background: e.color,
                      transition: 'width 0.3s ease',
                    }}
                    title={`${e.label}: ${e.share}%`}
                  />
                ))}
              </div>
            </div>

            {/* 引擎详情列表 */}
            <Stack gap={6}>
              {agentBreakdown.map((e) => (
                <div
                  key={e.key}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'space-between',
                    padding: '6px 10px',
                    borderRadius: 6,
                    background: 'color-mix(in srgb, var(--astr-text, #000) 2.5%, transparent)',
                    border: '1px solid var(--astr-border, #f1f5f9)',
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ width: 8, height: 8, borderRadius: '50%', background: e.color, flexShrink: 0 }} />
                    <div>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <Text size="xs" fw={600}>{e.label}</Text>
                        <span style={{ fontSize: 10, color: 'var(--astr-muted, #94a3b8)' }}>{e.desc}</span>
                      </div>
                      <div style={{ fontSize: 11, fontFamily: 'ui-monospace, monospace', color: 'var(--astr-muted, #64748b)', marginTop: 1 }}>
                        {e.sessions} 会话 · {formatTokens(e.tokens)}
                      </div>
                    </div>
                  </div>
                  <div style={{ textAlign: 'right' }}>
                    <Text size="xs" fw={700} style={{ fontFamily: 'ui-monospace, monospace' }}>
                      {e.share}%
                    </Text>
                    <span style={{ fontSize: 10, color: '#10b981', fontFamily: 'ui-monospace, monospace' }}>
                      缓存率 {e.hitRate}%
                    </span>
                  </div>
                </div>
              ))}
            </Stack>
          </Card>
        </Grid.Col>
      </Grid>

      {/* 底部双列：模型消耗分布 & 会话 Top 榜 */}
      <Grid >
        {/* 模型消耗占比 */}
        <Grid.Col span={{ base: 12, md: 6 }}>
          <Card p="md" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)', height: '100%' }}>
            <Group justify="space-between" align="center" mb="xs">
              <Group gap={6}>
                <IconBrain size={15} color="var(--astr-purple, #a855f7)" />
                <Text fw={650} size="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.02em' }}>
                  模型消耗占比与缓存效率
                </Text>
              </Group>
              <Text size="xs" c="dimmed">共 {models.length} 个活跃模型</Text>
            </Group>

            <div style={{ maxHeight: 340, overflowY: 'auto', paddingRight: 4 }}>
              <Stack gap={8}>
                {models.slice(0, 8).map((m: any) => (
                  <div key={m.model} style={{ padding: '2px 0' }}>
                    <Group justify="space-between" mb={2} style={{ fontSize: 12 }}>
                      <Group gap={6}>
                        <Text fw={600} size="xs" style={{ maxWidth: 220 }}  title={m.model}>
                          {m.model}
                        </Text>
                        <Badge size="xs" variant="subtle" color="gray">
                          {m.session_count} 个
                        </Badge>
                      </Group>
                      <Text fw={700} style={{ fontFamily: 'ui-monospace, monospace', fontSize: 12 }}>
                        {formatTokens(m.total_tokens)} <span style={{ color: 'var(--astr-muted, #94a3b8)', fontWeight: 400 }}>({m.share}%)</span>
                      </Text>
                    </Group>
                    <Progress value={m.share || 0} size={5} color="indigo" radius="xl" />
                    <Group justify="space-between" mt={2} style={{ fontSize: 10.5, color: 'var(--astr-muted, #94a3b8)' }}>
                      <span>缓存命中: {formatRate(m.cache_hit_rate)}%</span>
                      <span>复用量: {formatTokens(m.cached_tokens)}</span>
                    </Group>
                  </div>
                ))}
              </Stack>
            </div>
          </Card>
        </Grid.Col>

        {/* 会话 Token Top 榜 */}
        <Grid.Col span={{ base: 12, md: 6 }}>
          <Card p="md" radius="md" withBorder style={{ background: 'var(--astr-surface, #fff)', height: '100%' }}>
            <Group justify="space-between" align="center" mb="xs">
              <Group gap={6}>
                <IconChartBar size={15} color="var(--astr-indigo, #6366f1)" />
                <Text fw={650} size="xs" style={{ textTransform: 'uppercase', letterSpacing: '0.02em' }}>
                  会话 Token 消耗 Top 榜
                </Text>
              </Group>
              <Text size="xs" c="dimmed">按总产出排序</Text>
            </Group>

            <div style={{ maxHeight: 340, overflowY: 'auto' }}>
              <Table highlightOnHover verticalSpacing={6} style={{ fontSize: 12 }}>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th style={{ fontSize: 11, color: '#94a3b8', fontWeight: 600 }}>会话名称与标识</Table.Th>
                    <Table.Th style={{ fontSize: 11, color: '#94a3b8', fontWeight: 600 }}>主要模型</Table.Th>
                    <Table.Th style={{ fontSize: 11, color: '#94a3b8', fontWeight: 600, textAlign: 'right' }}>总 Token</Table.Th>
                    <Table.Th style={{ fontSize: 11, color: '#94a3b8', fontWeight: 600, textAlign: 'right' }}>缓存率</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {topSessions.map((s) => (
                    <Table.Tr key={`${s.agent_id}:${s.session_id}`}>
                      <Table.Td style={{ maxWidth: 170 }}>
                        <Text size="xs" fw={500}  title={s.title}>
                          {s.title}
                        </Text>
                        <div style={{ fontSize: 10, color: 'var(--astr-muted, #94a3b8)', fontFamily: 'ui-monospace, monospace' }}>
                          {s.agent_id}
                        </div>
                      </Table.Td>
                      <Table.Td style={{ maxWidth: 130 }}>
                        <Badge size="xs" variant="light" color="indigo" style={{ maxWidth: 125 }}  title={s.model}>
                          {s.model}
                        </Badge>
                      </Table.Td>
                      <Table.Td style={{ textAlign: 'right', fontFamily: 'ui-monospace, monospace', fontWeight: 600, fontSize: 12 }}>
                        {formatTokens(s.total_tokens)}
                      </Table.Td>
                      <Table.Td style={{ textAlign: 'right', color: '#10b981', fontWeight: 600, fontFamily: 'ui-monospace, monospace', fontSize: 12 }}>
                        {formatRate(s.cache_hit_rate)}%
                      </Table.Td>
                    </Table.Tr>
                  ))}
                </Table.Tbody>
              </Table>
            </div>
          </Card>
        </Grid.Col>
      </Grid>
    </div>
  )
}

