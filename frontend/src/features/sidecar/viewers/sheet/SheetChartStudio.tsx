import { useMemo, useRef, useState } from 'react'
import { Button, Group, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconChartBar,
  IconChartDonut,
  IconChartLine,
  IconDownload,
  IconFileSpreadsheet,
  IconLayoutList,
  IconMessageForward,
} from '@tabler/icons-react'
import { exportRowsToCsv, exportSheetsToXlsx } from '../../../office/export/exportService'
import './sheetChartStudio.css'

export type SheetChartType = 'bar' | 'hbar' | 'line' | 'donut'
export type SheetAggregation = 'sum' | 'avg' | 'count' | 'max'
export type SheetSortOrder = 'desc' | 'asc' | 'original'

const CHART_PALETTE = [
  '#18181b',
  '#2563eb',
  '#059669',
  '#d97706',
  '#7c3aed',
  '#0891b2',
  '#e11d48',
  '#475569',
  '#0d9488',
  '#ea580c',
]

function cellText(val: unknown): string {
  if (val === null || val === undefined) return ''
  if (typeof val === 'string') return val.trim()
  if (typeof val === 'number') return Number.isFinite(val) ? String(val) : ''
  if (typeof val === 'boolean') return val ? 'TRUE' : 'FALSE'
  try {
    return JSON.stringify(val)
  } catch {
    return String(val)
  }
}

function parseNum(val: unknown): number | null {
  if (typeof val === 'number') return Number.isFinite(val) ? val : null
  if (typeof val === 'string') {
    const cleaned = val.trim().replace(/[,¥$￥%]/g, '')
    if (!cleaned || /^0\d+/.test(cleaned)) return null
    const n = Number(cleaned)
    return Number.isFinite(n) ? n : null
  }
  return null
}

function formatCompact(n: number): string {
  if (!Number.isFinite(n)) return '0'
  const abs = Math.abs(n)
  if (abs >= 1e8) return `${(n / 1e8).toFixed(2).replace(/\.?0+$/, '')}亿`
  if (abs >= 1e4) return `${(n / 1e4).toFixed(2).replace(/\.?0+$/, '')}万`
  if (abs >= 1000) return n.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
  return Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/\.?0+$/, '')
}

function formatExact(n: number): string {
  if (!Number.isFinite(n)) return '0'
  return n.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}

function truncateLabel(label: string, max = 10): string {
  return label.length > max ? `${label.slice(0, max - 1)}…` : label
}

export interface PivotGroupRow {
  label: string
  count: number
  sum: number
  avg: number
  min: number
  max: number
  value: number
  share: number
  color: string
}

export interface SheetChartStudioProps {
  mode: 'chart' | 'pivot'
  fileName: string
  filePath: string
  sheetName: string
  columns: string[]
  rows: unknown[][]
  onSendToChat: (promptText: string) => void
}

export function SheetChartStudio({
  mode,
  fileName,
  filePath,
  sheetName,
  columns,
  rows,
  onSendToChat,
}: SheetChartStudioProps) {
  const svgRef = useRef<SVGSVGElement | null>(null)
  const [hoveredIdx, setHoveredIdx] = useState<number | null>(null)
  const [exporting, setExporting] = useState(false)

  // Detect numeric columns (at least 1 finite number and >= 40% numeric among non-empty samples)
  const numericColIndices = useMemo(() => {
    const out: number[] = []
    const sampleSize = Math.min(rows.length, 200)
    for (let c = 0; c < columns.length; c++) {
      let nonEmpty = 0
      let numCount = 0
      for (let r = 0; r < sampleSize; r++) {
        const raw = rows[r]?.[c]
        if (cellText(raw) !== '') {
          nonEmpty++
          if (parseNum(raw) !== null) numCount++
        }
      }
      if (numCount > 0 && nonEmpty > 0 && numCount / nonEmpty >= 0.4) {
        out.push(c)
      }
    }
    return out
  }, [columns, rows])

  const defaultDimCol = useMemo(() => {
    const nonNum = columns.findIndex((_c, idx) => !numericColIndices.includes(idx))
    return nonNum >= 0 ? nonNum : 0
  }, [columns, numericColIndices])

  const defaultMetricCol = numericColIndices.length > 0 ? numericColIndices[0] : -1

  const [dimColOverride, setDimColOverride] = useState<number | null>(null)
  const [metricColOverride, setMetricColOverride] = useState<number | null>(null)
  const [agg, setAgg] = useState<SheetAggregation>(() => (defaultMetricCol >= 0 ? 'sum' : 'count'))
  const [chartType, setChartType] = useState<SheetChartType>('bar')
  const [topN, setTopN] = useState<'8' | '15' | '30' | 'all'>('15')
  const [sortOrder, setSortOrder] = useState<SheetSortOrder>('desc')

  const dimCol =
    dimColOverride !== null && dimColOverride < columns.length ? dimColOverride : defaultDimCol
  const metricCol =
    metricColOverride !== null && metricColOverride < columns.length
      ? metricColOverride
      : defaultMetricCol
  const effectiveAgg: SheetAggregation = metricCol < 0 ? 'count' : agg

  const { groups, totalValue, grandRowCount, topItem } = useMemo(() => {
    const bucketMap = new Map<
      string,
      { label: string; order: number; count: number; numCount: number; sum: number; min: number; max: number }
    >()
    let orderSeq = 0
    for (const row of rows) {
      const label = cellText(row[dimCol]) || '(空值)'
      let bucket = bucketMap.get(label)
      if (!bucket) {
        bucket = {
          label,
          order: orderSeq++,
          count: 0,
          numCount: 0,
          sum: 0,
          min: Infinity,
          max: -Infinity,
        }
        bucketMap.set(label, bucket)
      }
      bucket.count += 1
      if (metricCol >= 0) {
        const n = parseNum(row[metricCol])
        if (n !== null) {
          bucket.numCount += 1
          bucket.sum += n
          if (n < bucket.min) bucket.min = n
          if (n > bucket.max) bucket.max = n
        }
      }
    }

    const rawItems = Array.from(bucketMap.values()).map((b) => {
      const avg = b.numCount > 0 ? b.sum / b.numCount : 0
      const min = b.numCount > 0 ? b.min : 0
      const max = b.numCount > 0 ? b.max : 0
      let value = b.count
      if (effectiveAgg === 'sum') value = b.sum
      else if (effectiveAgg === 'avg') value = avg
      else if (effectiveAgg === 'max') value = max
      return {
        label: b.label,
        order: b.order,
        count: b.count,
        sum: b.sum,
        avg,
        min,
        max,
        value,
      }
    })

    if (sortOrder === 'desc') {
      rawItems.sort((a, b) => b.value - a.value || b.count - a.count)
    } else if (sortOrder === 'asc') {
      rawItems.sort((a, b) => a.value - b.value || a.count - b.count)
    } else {
      rawItems.sort((a, b) => a.order - b.order)
    }

    const limit = topN === 'all' ? rawItems.length : Number(topN)
    let sliced = rawItems
    if (rawItems.length > limit) {
      const head = rawItems.slice(0, limit)
      const tail = rawItems.slice(limit)
      if (effectiveAgg === 'sum' || effectiveAgg === 'count') {
        const otherSum = tail.reduce((acc, item) => acc + item.sum, 0)
        const otherCount = tail.reduce((acc, item) => acc + item.count, 0)
        const otherVal = tail.reduce((acc, item) => acc + item.value, 0)
        head.push({
          label: `其他 (${tail.length} 项)`,
          order: 999999,
          count: otherCount,
          sum: otherSum,
          avg: otherCount > 0 ? otherSum / otherCount : 0,
          min: Math.min(...tail.map((t) => t.min)),
          max: Math.max(...tail.map((t) => t.max)),
          value: otherVal,
        })
      }
      sliced = head
    }

    const positiveTotal = sliced.reduce((acc, item) => acc + Math.max(0, item.value), 0)
    const sumValue = sliced.reduce((acc, item) => acc + item.value, 0)
    const finalRows: PivotGroupRow[] = sliced.map((item, idx) => ({
      label: item.label,
      count: item.count,
      sum: item.sum,
      avg: item.avg,
      min: item.min,
      max: item.max,
      value: item.value,
      share: positiveTotal > 0 ? (Math.max(0, item.value) / positiveTotal) * 100 : 0,
      color: CHART_PALETTE[idx % CHART_PALETTE.length],
    }))

    const sortedByVal = [...finalRows].sort((a, b) => b.value - a.value)
    return {
      groups: finalRows,
      totalValue: sumValue,
      grandRowCount: rows.length,
      topItem: sortedByVal[0] || null,
    }
  }, [dimCol, effectiveAgg, metricCol, rows, sortOrder, topN])

  const dimName = columns[dimCol] || '分组'
  const metricName = metricCol >= 0 ? columns[metricCol] || '数值' : '记录数'
  const aggLabel: Record<SheetAggregation, string> = {
    sum: '合计',
    avg: '均值',
    count: '计数',
    max: '最大值',
  }
  const chartTitle = `${dimName} · ${effectiveAgg === 'count' ? '记录数分布' : `${metricName} (${aggLabel[effectiveAgg]})`}`

  const handleExportPng = () => {
    const svg = svgRef.current
    if (!svg) return
    try {
      const clone = svg.cloneNode(true) as SVGSVGElement
      const vb = svg.getAttribute('viewBox')?.split(' ').map(Number) || [0, 0, 760, 360]
      const width = vb[2] || 760
      const height = vb[3] || 360
      clone.setAttribute('xmlns', 'http://www.w3.org/2000/svg')
      clone.setAttribute('width', String(width))
      clone.setAttribute('height', String(height))

      // Add clean white background rect so exported PNG looks sharp on slides & documents
      const bg = document.createElementNS('http://www.w3.org/2000/svg', 'rect')
      bg.setAttribute('x', '0')
      bg.setAttribute('y', '0')
      bg.setAttribute('width', String(width))
      bg.setAttribute('height', String(height))
      bg.setAttribute('fill', '#ffffff')
      clone.insertBefore(bg, clone.firstChild)

      const xml = new XMLSerializer().serializeToString(clone)
      const blob = new Blob([xml], { type: 'image/svg+xml;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const img = new Image()
      img.onload = () => {
        const scale = 2
        const canvas = document.createElement('canvas')
        canvas.width = width * scale
        canvas.height = height * scale
        const ctx = canvas.getContext('2d')
        if (ctx) {
          ctx.scale(scale, scale)
          ctx.fillStyle = '#ffffff'
          ctx.fillRect(0, 0, width, height)
          ctx.drawImage(img, 0, 0, width, height)
          canvas.toBlob((pngBlob) => {
            if (!pngBlob) return
            const pngUrl = URL.createObjectURL(pngBlob)
            const a = document.createElement('a')
            const safeBase = `${fileName.replace(/\.[^.]+$/, '')}_${sheetName}_${chartType}`
            a.href = pngUrl
            a.download = `${safeBase}.png`
            document.body.appendChild(a)
            a.click()
            a.remove()
            setTimeout(() => URL.revokeObjectURL(pngUrl), 500)
          }, 'image/png')
        }
        URL.revokeObjectURL(url)
        notifications.show({ color: 'teal', message: '已导出高清图表 PNG' })
      }
      img.onerror = () => {
        URL.revokeObjectURL(url)
        notifications.show({ color: 'red', message: '导出 PNG 失败，请重试' })
      }
      img.src = url
    } catch {
      notifications.show({ color: 'red', message: '导出 PNG 失败' })
    }
  }

  const handleExportPivot = async (fmt: 'xlsx' | 'csv') => {
    const pivotCols =
      metricCol >= 0
        ? [dimName, '记录数', `${metricName}(合计)`, `${metricName}(均值)`, '最小值', '最大值', '占比(%)']
        : [dimName, '记录数', '占比(%)']
    const pivotRows = groups.map((g) =>
      metricCol >= 0
        ? [g.label, g.count, Number(g.sum.toFixed(2)), Number(g.avg.toFixed(2)), g.min, g.max, Number(g.share.toFixed(1))]
        : [g.label, g.count, Number(g.share.toFixed(1))],
    )
    const base = `${fileName.replace(/\.[^.]+$/, '')}_${sheetName}_透视汇总`
    if (fmt === 'csv') {
      exportRowsToCsv({ columns: pivotCols, rows: pivotRows, fileName: `${base}.csv` })
      notifications.show({ color: 'teal', message: '已导出透视汇总 CSV' })
      return
    }
    setExporting(true)
    try {
      await exportSheetsToXlsx({
        fileName: `${base}.xlsx`,
        sheets: [{ name: '透视汇总', columns: pivotCols, rows: pivotRows }],
      })
      notifications.show({ color: 'teal', message: '已导出透视汇总 Excel (.xlsx)' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '导出失败' })
    } finally {
      setExporting(false)
    }
  }

  const handleSendInsightToChat = () => {
    const header = `| ${dimName} | 记录数 | ${effectiveAgg === 'count' ? '占比' : `${metricName}(${aggLabel[effectiveAgg]}) | 占比`} |`
    const sep = effectiveAgg === 'count' ? '| --- | --- | --- |' : '| --- | --- | --- | --- |'
    const lines = groups.slice(0, 20).map((g) =>
      effectiveAgg === 'count'
        ? `| ${g.label.replace(/\|/g, '\\|')} | ${g.count} | ${g.share.toFixed(1)}% |`
        : `| ${g.label.replace(/\|/g, '\\|')} | ${g.count} | ${formatExact(g.value)} | ${g.share.toFixed(1)}% |`,
    )
    const prompt = [
      `请基于以下表格透视汇总数据（来源：\`${filePath}\` · 工作表「${sheetName}」），为管理层提炼 3-5 条核心发现、异常点与可执行建议：`,
      '',
      `- 分析维度：**${dimName}**`,
      `- 统计指标：**${effectiveAgg === 'count' ? '记录数' : `${metricName} (${aggLabel[effectiveAgg]})`}**`,
      `- 样本总行数：**${grandRowCount}**，分组数：**${groups.length}**，汇总值：**${formatExact(totalValue)}**`,
      topItem ? `- 最高分组：**${topItem.label}**（${formatExact(topItem.value)}，占比 ${topItem.share.toFixed(1)}%）` : '',
      '',
      header,
      sep,
      ...lines,
      '',
    ]
      .filter(Boolean)
      .join('\n')
    onSendToChat(prompt)
  }

  if (columns.length === 0 || rows.length === 0) {
    return (
      <div className="sheet-studio-wrap">
        <Text size="sm" c="dimmed" ta="center" py="xl">
          当前筛选结果为空，暂无可分析的数据行
        </Text>
      </div>
    )
  }

  return (
    <div className="sheet-studio-wrap" data-testid={`sheet-studio-${mode}`}>
      {/* Controls bar */}
      <div className="sheet-studio-controls">
        {mode === 'chart' && (
          <div className="sheet-studio-type-group" role="group" aria-label="图表类型">
            {(
              [
                { id: 'bar', label: '柱状图', icon: IconChartBar },
                { id: 'hbar', label: '条形图', icon: IconLayoutList },
                { id: 'line', label: '折线图', icon: IconChartLine },
                { id: 'donut', label: '环形图', icon: IconChartDonut },
              ] as const
            ).map((item) => {
              const Icon = item.icon
              return (
                <button
                  key={item.id}
                  type="button"
                  aria-pressed={chartType === item.id}
                  className={`sheet-studio-type-btn${chartType === item.id ? ' is-active' : ''}`}
                  onClick={() => setChartType(item.id)}
                >
                  <Icon size={13} />
                  <span>{item.label}</span>
                </button>
              )
            })}
          </div>
        )}

        <label className="sheet-studio-field">
          <span>分组维度</span>
          <select
            className="sheet-studio-select"
            aria-label="分组维度"
            value={dimCol}
            onChange={(e) => setDimColOverride(Number(e.target.value))}
          >
            {columns.map((col, idx) => (
              <option key={`${col}-${idx}`} value={idx}>
                {col}
              </option>
            ))}
          </select>
        </label>

        <label className="sheet-studio-field">
          <span>数值指标</span>
          <select
            className="sheet-studio-select"
            aria-label="数值指标"
            value={metricCol}
            onChange={(e) => {
              const next = Number(e.target.value)
              setMetricColOverride(next)
              if (next < 0) setAgg('count')
              else if (agg === 'count') setAgg('sum')
            }}
          >
            <option value={-1}>（按行数计数）</option>
            {columns.map((col, idx) => (
              <option key={`${col}-${idx}`} value={idx}>
                {col}
                {numericColIndices.includes(idx) ? '' : '（文本）'}
              </option>
            ))}
          </select>
        </label>

        <label className="sheet-studio-field">
          <span>聚合</span>
          <select
            className="sheet-studio-select"
            aria-label="聚合方式"
            value={effectiveAgg}
            disabled={metricCol < 0}
            onChange={(e) => setAgg(e.target.value as SheetAggregation)}
          >
            <option value="sum">求和 (SUM)</option>
            <option value="avg">均值 (AVG)</option>
            <option value="count">计数 (COUNT)</option>
            <option value="max">最大 (MAX)</option>
          </select>
        </label>

        <label className="sheet-studio-field">
          <span>显示</span>
          <select
            className="sheet-studio-select"
            aria-label="显示条数"
            value={topN}
            onChange={(e) => setTopN(e.target.value as '8' | '15' | '30' | 'all')}
          >
            <option value="8">Top 8</option>
            <option value="15">Top 15</option>
            <option value="30">Top 30</option>
            <option value="all">全部</option>
          </select>
        </label>

        <label className="sheet-studio-field">
          <span>排序</span>
          <select
            className="sheet-studio-select"
            aria-label="排序方式"
            value={sortOrder}
            onChange={(e) => setSortOrder(e.target.value as SheetSortOrder)}
          >
            <option value="desc">从高到低</option>
            <option value="asc">从低到高</option>
            <option value="original">原顺序</option>
          </select>
        </label>

        <Group gap={6} ml="auto">
          {mode === 'chart' ? (
            <Button
              size="compact-xs"
              variant="default"
              leftSection={<IconDownload size={13} />}
              onClick={handleExportPng}
            >
              导出图表 PNG
            </Button>
          ) : (
            <>
              <Button
                size="compact-xs"
                variant="default"
                loading={exporting}
                leftSection={<IconFileSpreadsheet size={13} />}
                onClick={() => void handleExportPivot('xlsx')}
              >
                导出透视 Excel
              </Button>
              <Button
                size="compact-xs"
                variant="subtle"
                onClick={() => void handleExportPivot('csv')}
              >
                CSV
              </Button>
            </>
          )}
          <Button
            size="compact-xs"
            variant="light"
            color="indigo"
            leftSection={<IconMessageForward size={13} />}
            onClick={handleSendInsightToChat}
          >
            带结论发给对话
          </Button>
        </Group>
      </div>

      {/* KPI Strip */}
      <div className="sheet-studio-kpis">
        <div className="sheet-studio-kpi">
          <div className="sheet-studio-kpi-label">
            {effectiveAgg === 'avg' ? '各组均值之和' : `${metricName} (${aggLabel[effectiveAgg]})`}
          </div>
          <div className="sheet-studio-kpi-value">{formatExact(totalValue)}</div>
          <div className="sheet-studio-kpi-sub">覆盖 {grandRowCount} 条记录</div>
        </div>
        <div className="sheet-studio-kpi">
          <div className="sheet-studio-kpi-label">分组数量</div>
          <div className="sheet-studio-kpi-value">{groups.length}</div>
          <div className="sheet-studio-kpi-sub">按「{dimName}」聚合</div>
        </div>
        <div className="sheet-studio-kpi">
          <div className="sheet-studio-kpi-label">最高分组</div>
          <div className="sheet-studio-kpi-value">{topItem ? topItem.label : '-'}</div>
          <div className="sheet-studio-kpi-sub">
            {topItem ? `${formatExact(topItem.value)} · 占 ${topItem.share.toFixed(1)}%` : '-'}
          </div>
        </div>
        <div className="sheet-studio-kpi">
          <div className="sheet-studio-kpi-label">单组均值</div>
          <div className="sheet-studio-kpi-value">
            {groups.length > 0 ? formatExact(totalValue / groups.length) : '0'}
          </div>
          <div className="sheet-studio-kpi-sub">
            {hoveredIdx !== null && groups[hoveredIdx]
              ? `当前高亮：${groups[hoveredIdx].label} (${formatExact(groups[hoveredIdx].value)})`
              : '悬停图表可查看精准明细'}
          </div>
        </div>
      </div>

      {/* Main visualization or pivot table */}
      {mode === 'chart' ? (
        <div className="sheet-studio-canvas-card">
          <Group justify="space-between" align="center">
            <Text size="sm" fw={600}>
              {chartTitle}
            </Text>
            {hoveredIdx !== null && groups[hoveredIdx] && (
              <Text size="xs" fw={600} c="indigo">
                {groups[hoveredIdx].label}：{formatExact(groups[hoveredIdx].value)}（占比{' '}
                {groups[hoveredIdx].share.toFixed(1)}%，{groups[hoveredIdx].count} 行）
              </Text>
            )}
          </Group>

          {chartType === 'bar' && (
            <VerticalBarSvg
              refObj={svgRef}
              groups={groups}
              hoveredIdx={hoveredIdx}
              onHover={setHoveredIdx}
            />
          )}
          {chartType === 'hbar' && (
            <HorizontalBarSvg
              refObj={svgRef}
              groups={groups}
              hoveredIdx={hoveredIdx}
              onHover={setHoveredIdx}
            />
          )}
          {chartType === 'line' && (
            <LineSvg
              refObj={svgRef}
              groups={groups}
              hoveredIdx={hoveredIdx}
              onHover={setHoveredIdx}
            />
          )}
          {chartType === 'donut' && (
            <DonutSvg
              refObj={svgRef}
              groups={groups}
              totalValue={totalValue}
              hoveredIdx={hoveredIdx}
              onHover={setHoveredIdx}
            />
          )}

          <div className="sheet-studio-legend">
            {groups.slice(0, 12).map((g, idx) => (
              <span
                key={`${g.label}-${idx}`}
                className="sheet-studio-legend-item"
                onMouseEnter={() => setHoveredIdx(idx)}
                onMouseLeave={() => setHoveredIdx(null)}
              >
                <span className="sheet-studio-legend-dot" style={{ background: g.color }} />
                <span>{g.label}</span>
                <Text span size="xs" c="dimmed">
                  {formatCompact(g.value)} ({g.share.toFixed(1)}%)
                </Text>
              </span>
            ))}
          </div>
        </div>
      ) : (
        <div className="sheet-pivot-table-wrap">
          <table className="sheet-pivot-table" aria-label={`透视汇总：${dimName}`}>
            <thead>
              <tr>
                <th>{dimName}</th>
                <th className="sheet-pivot-num">记录数</th>
                {metricCol >= 0 && (
                  <>
                    <th className="sheet-pivot-num">{metricName} · 合计</th>
                    <th className="sheet-pivot-num">均值</th>
                    <th className="sheet-pivot-num">最小</th>
                    <th className="sheet-pivot-num">最大</th>
                  </>
                )}
                <th>占比分布</th>
              </tr>
            </thead>
            <tbody>
              {groups.map((g, idx) => (
                <tr key={`${g.label}-${idx}`}>
                  <td style={{ fontWeight: 500 }}>{g.label}</td>
                  <td className="sheet-pivot-num">{g.count}</td>
                  {metricCol >= 0 && (
                    <>
                      <td className="sheet-pivot-num">{formatExact(g.sum)}</td>
                      <td className="sheet-pivot-num">{formatExact(g.avg)}</td>
                      <td className="sheet-pivot-num">{formatExact(g.min)}</td>
                      <td className="sheet-pivot-num">{formatExact(g.max)}</td>
                    </>
                  )}
                  <td style={{ whiteSpace: 'nowrap' }}>
                    <span className="sheet-pivot-bar-track">
                      <span
                        className="sheet-pivot-bar-fill"
                        style={{ width: `${Math.min(100, Math.max(0, g.share))}%`, background: g.color }}
                      />
                    </span>
                    <span style={{ fontVariantNumeric: 'tabular-nums', fontSize: 12 }}>
                      {g.share.toFixed(1)}%
                    </span>
                  </td>
                </tr>
              ))}
              <tr className="is-total-row">
                <td>总计（{groups.length} 组）</td>
                <td className="sheet-pivot-num">{grandRowCount}</td>
                {metricCol >= 0 && (
                  <>
                    <td className="sheet-pivot-num">
                      {formatExact(groups.reduce((acc, g) => acc + g.sum, 0))}
                    </td>
                    <td className="sheet-pivot-num">
                      {grandRowCount > 0
                        ? formatExact(groups.reduce((acc, g) => acc + g.sum, 0) / grandRowCount)
                        : '0'}
                    </td>
                    <td className="sheet-pivot-num">
                      {groups.length > 0 ? formatExact(Math.min(...groups.map((g) => g.min))) : '0'}
                    </td>
                    <td className="sheet-pivot-num">
                      {groups.length > 0 ? formatExact(Math.max(...groups.map((g) => g.max))) : '0'}
                    </td>
                  </>
                )}
                <td>100.0%</td>
              </tr>
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function VerticalBarSvg({
  refObj,
  groups,
  hoveredIdx,
  onHover,
}: {
  refObj: React.RefObject<SVGSVGElement | null>
  groups: PivotGroupRow[]
  hoveredIdx: number | null
  onHover: (idx: number | null) => void
}) {
  const width = 760
  const height = 340
  const pad = { top: 28, right: 20, bottom: 56, left: 66 }
  const plotW = width - pad.left - pad.right
  const plotH = height - pad.top - pad.bottom
  const maxVal = Math.max(1, ...groups.map((g) => Math.max(0, g.value)))
  const step = plotW / Math.max(1, groups.length)
  const barW = Math.min(44, Math.max(10, step * 0.58))
  const ticks = [0, 0.25, 0.5, 0.75, 1]

  return (
    <svg
      ref={refObj}
      className="sheet-studio-svg"
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label="柱状图"
    >
      {ticks.map((t, i) => {
        const y = pad.top + plotH * (1 - t)
        return (
          <g key={i}>
            <line
              x1={pad.left}
              x2={width - pad.right}
              y1={y}
              y2={y}
              stroke="#e4e4e7"
              strokeDasharray={t === 0 ? undefined : '3 3'}
            />
            <text x={pad.left - 8} y={y + 4} textAnchor="end" fontSize="11" fill="#71717a">
              {formatCompact(maxVal * t)}
            </text>
          </g>
        )
      })}
      {groups.map((g, idx) => {
        const clamped = Math.max(0, g.value)
        const h = (clamped / maxVal) * plotH
        const x = pad.left + idx * step + (step - barW) / 2
        const y = pad.top + plotH - h
        const isHovered = hoveredIdx === idx
        return (
          <g
            key={`${g.label}-${idx}`}
            onMouseEnter={() => onHover(idx)}
            onMouseLeave={() => onHover(null)}
            style={{ cursor: 'pointer' }}
          >
            <rect
              x={x}
              y={y}
              width={barW}
              height={Math.max(1, h)}
              rx={4}
              fill={g.color}
              opacity={hoveredIdx === null || isHovered ? 0.92 : 0.35}
            />
            <text
              x={x + barW / 2}
              y={Math.max(14, y - 6)}
              textAnchor="middle"
              fontSize="10.5"
              fontWeight={isHovered ? '700' : '500'}
              fill="#3f3f46"
            >
              {formatCompact(g.value)}
            </text>
            <text
              x={x + barW / 2}
              y={height - 20}
              textAnchor="middle"
              fontSize="11"
              fill="#52525b"
            >
              {truncateLabel(g.label, groups.length > 10 ? 5 : 9)}
            </text>
          </g>
        )
      })}
    </svg>
  )
}

function HorizontalBarSvg({
  refObj,
  groups,
  hoveredIdx,
  onHover,
}: {
  refObj: React.RefObject<SVGSVGElement | null>
  groups: PivotGroupRow[]
  hoveredIdx: number | null
  onHover: (idx: number | null) => void
}) {
  const rowH = 30
  const width = 760
  const height = Math.max(220, groups.length * rowH + 40)
  const pad = { top: 16, right: 86, bottom: 20, left: 136 }
  const plotW = width - pad.left - pad.right
  const maxVal = Math.max(1, ...groups.map((g) => Math.max(0, g.value)))

  return (
    <svg
      ref={refObj}
      className="sheet-studio-svg"
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label="条形图"
    >
      {groups.map((g, idx) => {
        const y = pad.top + idx * rowH
        const w = (Math.max(0, g.value) / maxVal) * plotW
        const isHovered = hoveredIdx === idx
        return (
          <g
            key={`${g.label}-${idx}`}
            onMouseEnter={() => onHover(idx)}
            onMouseLeave={() => onHover(null)}
            style={{ cursor: 'pointer' }}
          >
            <text x={pad.left - 10} y={y + 15} textAnchor="end" fontSize="11.5" fill="#3f3f46">
              {truncateLabel(g.label, 12)}
            </text>
            <rect
              x={pad.left}
              y={y + 3}
              width={plotW}
              height={17}
              rx={4}
              fill="#f4f4f5"
            />
            <rect
              x={pad.left}
              y={y + 3}
              width={Math.max(2, w)}
              height={17}
              rx={4}
              fill={g.color}
              opacity={hoveredIdx === null || isHovered ? 0.92 : 0.35}
            />
            <text
              x={pad.left + w + 8}
              y={y + 15}
              textAnchor="start"
              fontSize="11"
              fontWeight={isHovered ? '700' : '500'}
              fill="#52525b"
            >
              {formatCompact(g.value)} ({g.share.toFixed(1)}%)
            </text>
          </g>
        )
      })}
    </svg>
  )
}

function LineSvg({
  refObj,
  groups,
  hoveredIdx,
  onHover,
}: {
  refObj: React.RefObject<SVGSVGElement | null>
  groups: PivotGroupRow[]
  hoveredIdx: number | null
  onHover: (idx: number | null) => void
}) {
  const width = 760
  const height = 340
  const pad = { top: 28, right: 24, bottom: 54, left: 66 }
  const plotW = width - pad.left - pad.right
  const plotH = height - pad.top - pad.bottom
  const maxVal = Math.max(1, ...groups.map((g) => Math.max(0, g.value)))
  const ticks = [0, 0.25, 0.5, 0.75, 1]

  const points = groups.map((g, idx) => {
    const x =
      groups.length === 1
        ? pad.left + plotW / 2
        : pad.left + (idx / (groups.length - 1)) * plotW
    const y = pad.top + plotH - (Math.max(0, g.value) / maxVal) * plotH
    return { x, y, g, idx }
  })

  const pathD = points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${p.x} ${p.y}`).join(' ')
  const areaD =
    points.length > 0
      ? `${pathD} L ${points[points.length - 1].x} ${pad.top + plotH} L ${points[0].x} ${pad.top + plotH} Z`
      : ''

  return (
    <svg
      ref={refObj}
      className="sheet-studio-svg"
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label="折线图"
    >
      {ticks.map((t, i) => {
        const y = pad.top + plotH * (1 - t)
        return (
          <g key={i}>
            <line
              x1={pad.left}
              x2={width - pad.right}
              y1={y}
              y2={y}
              stroke="#e4e4e7"
              strokeDasharray={t === 0 ? undefined : '3 3'}
            />
            <text x={pad.left - 8} y={y + 4} textAnchor="end" fontSize="11" fill="#71717a">
              {formatCompact(maxVal * t)}
            </text>
          </g>
        )
      })}
      {areaD && <path d={areaD} fill="#2563eb" opacity={0.08} />}
      {pathD && <path d={pathD} fill="none" stroke="#18181b" strokeWidth={2.5} strokeLinejoin="round" />}
      {points.map(({ x, y, g, idx }) => {
        const isHovered = hoveredIdx === idx
        return (
          <g
            key={`${g.label}-${idx}`}
            onMouseEnter={() => onHover(idx)}
            onMouseLeave={() => onHover(null)}
            style={{ cursor: 'pointer' }}
          >
            <circle
              cx={x}
              cy={y}
              r={isHovered ? 6 : 4}
              fill="#ffffff"
              stroke={g.color}
              strokeWidth={2.5}
            />
            <text
              x={x}
              y={Math.max(14, y - 10)}
              textAnchor="middle"
              fontSize="10.5"
              fontWeight={isHovered ? '700' : '500'}
              fill="#3f3f46"
            >
              {formatCompact(g.value)}
            </text>
            <text
              x={x}
              y={height - 20}
              textAnchor="middle"
              fontSize="11"
              fill="#52525b"
            >
              {truncateLabel(g.label, groups.length > 10 ? 5 : 9)}
            </text>
          </g>
        )
      })}
    </svg>
  )
}

function DonutSvg({
  refObj,
  groups,
  totalValue,
  hoveredIdx,
  onHover,
}: {
  refObj: React.RefObject<SVGSVGElement | null>
  groups: PivotGroupRow[]
  totalValue: number
  hoveredIdx: number | null
  onHover: (idx: number | null) => void
}) {
  const width = 760
  const height = 320
  const cx = 230
  const cy = 160
  const r = 104
  const strokeW = 36
  const circ = 2 * Math.PI * r
  const positiveSum = groups.reduce((acc, g) => acc + Math.max(0, g.value), 0) || 1

  let cumulative = 0
  const slices = groups.map((g, idx) => {
    const frac = Math.max(0, g.value) / positiveSum
    const len = frac * circ
    const offset = -cumulative * circ
    cumulative += frac
    return { g, idx, len, offset }
  })

  const focused = hoveredIdx !== null ? groups[hoveredIdx] : null

  return (
    <svg
      ref={refObj}
      className="sheet-studio-svg"
      viewBox={`0 0 ${width} ${height}`}
      role="img"
      aria-label="环形图"
    >
      <circle cx={cx} cy={cy} r={r} fill="none" stroke="#f4f4f5" strokeWidth={strokeW} />
      <g transform={`rotate(-90 ${cx} ${cy})`}>
        {slices.map(({ g, idx, len, offset }) => {
          const isHovered = hoveredIdx === idx
          return (
            <circle
              key={`${g.label}-${idx}`}
              cx={cx}
              cy={cy}
              r={r}
              fill="none"
              stroke={g.color}
              strokeWidth={isHovered ? strokeW + 5 : strokeW}
              strokeDasharray={`${len} ${circ - len}`}
              strokeDashoffset={offset}
              opacity={hoveredIdx === null || isHovered ? 0.94 : 0.35}
              style={{ cursor: 'pointer', transition: 'stroke-width 120ms ease, opacity 120ms ease' }}
              onMouseEnter={() => onHover(idx)}
              onMouseLeave={() => onHover(null)}
            />
          )
        })}
      </g>
      <text x={cx} y={cy - 6} textAnchor="middle" fontSize="12" fill="#71717a">
        {focused ? truncateLabel(focused.label, 12) : '汇总总计'}
      </text>
      <text x={cx} y={cy + 16} textAnchor="middle" fontSize="18" fontWeight="700" fill="#18181b">
        {focused ? formatCompact(focused.value) : formatCompact(totalValue)}
      </text>
      {focused && (
        <text x={cx} y={cy + 34} textAnchor="middle" fontSize="11" fill="#52525b">
          占比 {focused.share.toFixed(1)}%
        </text>
      )}

      {/* Right-side ranking breakdown inside SVG so PNG export includes labels cleanly */}
      {groups.slice(0, 9).map((g, idx) => {
        const y = 44 + idx * 28
        return (
          <g
            key={`rank-${idx}`}
            onMouseEnter={() => onHover(idx)}
            onMouseLeave={() => onHover(null)}
            style={{ cursor: 'pointer' }}
          >
            <rect x={410} y={y - 10} width={10} height={10} rx={3} fill={g.color} />
            <text x={428} y={y - 1} fontSize="12" fill="#27272a" fontWeight={hoveredIdx === idx ? '700' : '400'}>
              {truncateLabel(g.label, 14)}
            </text>
            <text x={730} y={y - 1} textAnchor="end" fontSize="12" fill="#52525b">
              {formatCompact(g.value)} · {g.share.toFixed(1)}%
            </text>
          </g>
        )
      })}
    </svg>
  )
}
