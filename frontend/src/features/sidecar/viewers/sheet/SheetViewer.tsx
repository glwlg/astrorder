import { useCallback, useEffect, useMemo, useState } from 'react'
import { ActionIcon, Badge, Button, Checkbox, Group, LoadingOverlay, Menu, Paper, Text, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowDown,
  IconArrowUp,
  IconChartBar,
  IconDownload,
  IconExternalLink,
  IconFileSpreadsheet,
  IconMessageForward,
  IconRefresh,
  IconSearch,
  IconSparkles,
  IconTable,
  IconTableOptions,
} from '@tabler/icons-react'
import { api, type SheetPreviewResponse } from '../../../../api/client'
import { exportRowsToCsv, exportSheetsToXlsx } from '../../../office/export/exportService'
import { downloadFile } from '../../fileTransfer'
import type { ViewerContext } from '../../types'
import { SheetChartStudio } from './SheetChartStudio'
import './sheetViewer.css'

function cellToString(val: unknown): string {
  if (val === null || val === undefined) return ''
  if (typeof val === 'string') return val
  if (typeof val === 'number') return Number.isFinite(val) ? String(val) : ''
  if (typeof val === 'boolean') return val ? 'TRUE' : 'FALSE'
  try {
    return JSON.stringify(val)
  } catch {
    return String(val)
  }
}

function toFiniteNumber(val: unknown): number | null {
  if (typeof val === 'number') return Number.isFinite(val) ? val : null
  if (typeof val === 'string') {
    const s = val.trim().replace(/,/g, '')
    if (!s || /^0\d+/.test(s)) return null
    const n = Number(s)
    return Number.isFinite(n) ? n : null
  }
  return null
}

function formatStatNumber(n: number): string {
  if (Math.abs(n) >= 1000) {
    return n.toLocaleString('zh-CN', { maximumFractionDigits: 2 })
  }
  return Number.isInteger(n) ? String(n) : n.toFixed(2).replace(/\.?0+$/, '')
}

function buildMarkdownTableSnippet(sheetName: string, columns: string[], rows: unknown[][]): string {
  const esc = (v: unknown) => cellToString(v).replace(/\|/g, '\\|').replace(/\r?\n/g, ' ')
  const header = `| ${columns.map(esc).join(' | ')} |`
  const sep = `| ${columns.map(() => '---').join(' | ')} |`
  const body = rows.map((r) => `| ${columns.map((_c, idx) => esc(r[idx])).join(' | ')} |`)
  return [`表格 \`${sheetName}\`（共 ${rows.length} 行）：`, '', header, sep, ...body].join('\n')
}

export function SheetViewer({ artifact, session, toolbarAction }: ViewerContext) {
  const [data, setData] = useState<SheetPreviewResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)

  const [activeSheetIdx, setActiveSheetIdx] = useState(0)
  const [searchText, setSearchText] = useState('')
  const [sortCol, setSortCol] = useState<number | null>(null)
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc')
  const [selectedCol, setSelectedCol] = useState<number | null>(null)
  const [checkedRowIds, setCheckedRowIds] = useState<Set<number>>(new Set())
  const [exporting, setExporting] = useState(false)
  const [viewMode, setViewMode] = useState<'table' | 'chart' | 'pivot'>('table')

  const filePath = artifact.path || artifact.name

  const loadSheet = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const resp = await api.previewSheet({
        path: filePath,
        sessionId: session?.id || artifact.sessionId,
        connectionId: session?.connection_id || artifact.connectionId,
      })
      setData(resp)
      setActiveSheetIdx(0)
      setSortCol(null)
      setSelectedCol(null)
      setCheckedRowIds(new Set())
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '表格解析失败')
    } finally {
      setLoading(false)
    }
  }, [artifact.connectionId, artifact.sessionId, filePath, session?.connection_id, session?.id])

  useEffect(() => {
    void loadSheet()
  }, [loadSheet, revision])

  const sheets = data?.sheets || []
  const currentSheet = sheets[Math.min(activeSheetIdx, Math.max(0, sheets.length - 1))]

  const handleSwitchSheet = (idx: number) => {
    setActiveSheetIdx(idx)
    setSearchText('')
    setSortCol(null)
    setSelectedCol(null)
    setCheckedRowIds(new Set())
  }

  const processedRows = useMemo(() => {
    if (!currentSheet) return []
    const indexed = currentSheet.rows.map((cells, originalIndex) => ({ originalIndex, cells }))
    const q = searchText.trim().toLowerCase()
    const filtered = q
      ? indexed.filter((row) =>
          row.cells.some((c) => cellToString(c).toLowerCase().includes(q)),
        )
      : indexed

    if (sortCol === null) return filtered
    const col = sortCol
    const factor = sortDir === 'asc' ? 1 : -1
    return [...filtered].sort((a, b) => {
      const av = a.cells[col]
      const bv = b.cells[col]
      const an = toFiniteNumber(av)
      const bn = toFiniteNumber(bv)
      if (an !== null && bn !== null) {
        return (an - bn) * factor
      }
      return cellToString(av).localeCompare(cellToString(bv), 'zh-CN') * factor
    })
  }, [currentSheet, searchText, sortCol, sortDir])

  const columnStats = useMemo(() => {
    if (!currentSheet || selectedCol === null || selectedCol >= currentSheet.columns.length) {
      return null
    }
    const col = selectedCol
    const total = processedRows.length
    let nonEmpty = 0
    const uniqueSet = new Set<string>()
    let numCount = 0
    let sum = 0
    let minVal = Infinity
    let maxVal = -Infinity

    for (const row of processedRows) {
      const raw = row.cells[col]
      const str = cellToString(raw).trim()
      if (str !== '') {
        nonEmpty++
        uniqueSet.add(str)
      }
      const num = toFiniteNumber(raw)
      if (num !== null) {
        numCount++
        sum += num
        if (num < minVal) minVal = num
        if (num > maxVal) maxVal = num
      }
    }

    return {
      columnName: currentSheet.columns[col],
      total,
      nonEmpty,
      unique: uniqueSet.size,
      numCount,
      sum,
      avg: numCount > 0 ? sum / numCount : 0,
      min: numCount > 0 ? minVal : 0,
      max: numCount > 0 ? maxVal : 0,
    }
  }, [currentSheet, processedRows, selectedCol])

  const handleHeaderClick = (colIdx: number) => {
    setSelectedCol(colIdx)
    if (sortCol === colIdx) {
      if (sortDir === 'asc') {
        setSortDir('desc')
      } else {
        setSortCol(null)
      }
    } else {
      setSortCol(colIdx)
      setSortDir('asc')
    }
  }

  const toggleRow = (origIdx: number) => {
    setCheckedRowIds((prev) => {
      const next = new Set(prev)
      if (next.has(origIdx)) next.delete(origIdx)
      else next.add(origIdx)
      return next
    })
  }

  const allVisibleChecked =
    processedRows.length > 0 && processedRows.every((r) => checkedRowIds.has(r.originalIndex))

  const toggleAllVisible = () => {
    if (allVisibleChecked) {
      setCheckedRowIds(new Set())
    } else {
      setCheckedRowIds(new Set(processedRows.slice(0, 100).map((r) => r.originalIndex)))
    }
  }

  const dispatchSheetPrompt = useCallback(
    (prompt: string, toastMessage: string) => {
      window.dispatchEvent(
        new CustomEvent('astrorder:quote-to-composer', {
          detail: {
            agentId: session?.agent_id || artifact.agentId,
            sessionId: session?.id || artifact.sessionId,
            text: prompt,
          },
        }),
      )
      notifications.show({ color: 'teal', message: toastMessage })
    },
    [artifact.agentId, artifact.sessionId, session?.agent_id, session?.id],
  )

  const handleSendToChat = () => {
    if (!currentSheet) return
    const selectedRows =
      checkedRowIds.size > 0
        ? processedRows.filter((r) => checkedRowIds.has(r.originalIndex)).map((r) => r.cells)
        : processedRows.slice(0, 30).map((r) => r.cells)
    if (selectedRows.length === 0) {
      notifications.show({ color: 'yellow', message: '当前没有可发送的数据行' })
      return
    }
    const md = buildMarkdownTableSnippet(
      `${artifact.name} · ${currentSheet.name}`,
      currentSheet.columns,
      selectedRows,
    )
    const prompt = `请分析以下表格数据（来源：\`${filePath}\`）：\n\n${md}\n`
    dispatchSheetPrompt(prompt, `已将 ${selectedRows.length} 行表格数据填入对话输入框`)
  }

  const handleAiSheetAnalysis = (scenario: 'summary' | 'anomalies' | 'compare' | 'executive') => {
    if (!currentSheet) return
    const sampleRows =
      checkedRowIds.size > 0
        ? processedRows.filter((r) => checkedRowIds.has(r.originalIndex)).map((r) => r.cells)
        : processedRows.slice(0, 40).map((r) => r.cells)
    if (sampleRows.length === 0) {
      notifications.show({ color: 'yellow', message: '当前没有可分析的数据行' })
      return
    }
    const md = buildMarkdownTableSnippet(
      `${artifact.name} · ${currentSheet.name}`,
      currentSheet.columns,
      sampleRows,
    )
    const instructions: Record<typeof scenario, string> = {
      summary: '请对以下表格数据生成结构化数据摘要，提炼 3-5 条核心业务发现与关键数字结论：',
      anomalies: '请排查以下表格中的异常极值、明显波动、缺失值或口径不一致问题，并按严重程度列出核查清单：',
      compare: '请按主要维度对以下表格做横向对比，指出领先项、薄弱项，并给出具体可执行的业务优化建议：',
      executive: '请将以下表格数据改写为面向管理层的 1 分钟汇报口径（结论先行 + 3 个关键支撑指标 + 下一步动作）：',
    }
    const prompt = `${instructions[scenario]}\n\n来源：\`${filePath}\`（共 ${processedRows.length} 行，附 ${sampleRows.length} 行样本）\n\n${md}\n`
    dispatchSheetPrompt(prompt, '已将表格与分析指令填入对话输入框')
  }

  const handleExportFiltered = async (fmt: 'xlsx' | 'csv') => {
    if (!currentSheet) return
    const rows = processedRows.map((r) => r.cells)
    const baseName = `${artifact.name.replace(/\.[^.]+$/, '')}_${currentSheet.name}`
    if (fmt === 'csv') {
      exportRowsToCsv({ columns: currentSheet.columns, rows, fileName: `${baseName}.csv` })
      notifications.show({ color: 'teal', message: '已导出当前筛选结果为 CSV' })
      return
    }
    setExporting(true)
    try {
      await exportSheetsToXlsx({
        fileName: `${baseName}.xlsx`,
        sheets: [{ name: currentSheet.name, columns: currentSheet.columns, rows }],
      })
      notifications.show({ color: 'teal', message: '已导出当前筛选结果为 Excel (.xlsx)' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '导出失败' })
    } finally {
      setExporting(false)
    }
  }

  return (
    <div className="sheet-viewer-shell">
      <Paper p="xs" withBorder style={{ flex: 'none', margin: '8px 8px 0', zIndex: 10, borderRadius: 10 }}>
        <Group justify="space-between" wrap="wrap" gap="xs">
          <Group gap={8} wrap="nowrap" style={{ minWidth: 0 }}>
            <Text size="xs" fw={600} truncate title={filePath} style={{ maxWidth: 200 }}>
              {artifact.name}
            </Text>
            {data && (
              <Badge size="xs" variant="light" color="teal">
                {data.format.toUpperCase()} · {data.encoding}
              </Badge>
            )}
            {currentSheet && (
              <Text size="xs" c="dimmed">
                {processedRows.length} / {currentSheet.total_rows} 行{currentSheet.truncated ? '（已截断）' : ''}
              </Text>
            )}
          </Group>

          <Group gap={6} wrap="wrap">
            {toolbarAction}
            <div className="sheet-view-switch" role="group" aria-label="表格视图模式">
              <button
                type="button"
                aria-pressed={viewMode === 'table'}
                className={`sheet-view-switch-btn${viewMode === 'table' ? ' is-active' : ''}`}
                onClick={() => setViewMode('table')}
              >
                <IconTable size={13} />
                <span>数据表</span>
              </button>
              <button
                type="button"
                aria-pressed={viewMode === 'chart'}
                className={`sheet-view-switch-btn${viewMode === 'chart' ? ' is-active' : ''}`}
                onClick={() => setViewMode('chart')}
              >
                <IconChartBar size={13} />
                <span>可视化图表</span>
              </button>
              <button
                type="button"
                aria-pressed={viewMode === 'pivot'}
                className={`sheet-view-switch-btn${viewMode === 'pivot' ? ' is-active' : ''}`}
                onClick={() => setViewMode('pivot')}
              >
                <IconTableOptions size={13} />
                <span>透视汇总</span>
              </button>
            </div>
            <TextInput
              size="xs"
              w={150}
              leftSection={<IconSearch size={13} />}
              placeholder="全文筛选…"
              value={searchText}
              onChange={(e) => setSearchText(e.currentTarget.value)}
            />
            <Menu shadow="md" position="bottom-end" withinPortal>
              <Menu.Target>
                <Button
                  size="compact-xs"
                  variant="light"
                  color="violet"
                  leftSection={<IconSparkles size={13} />}
                  disabled={!currentSheet || processedRows.length === 0}
                >
                  AI 分析
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item onClick={() => handleAiSheetAnalysis('summary')}>生成数据摘要与核心发现</Menu.Item>
                <Menu.Item onClick={() => handleAiSheetAnalysis('anomalies')}>排查异常值与缺失数据</Menu.Item>
                <Menu.Item onClick={() => handleAiSheetAnalysis('compare')}>按维度对比并给出业务建议</Menu.Item>
                <Menu.Item onClick={() => handleAiSheetAnalysis('executive')}>转为管理层汇报口径</Menu.Item>
              </Menu.Dropdown>
            </Menu>
            <Button
              size="compact-xs"
              variant="light"
              color="indigo"
              leftSection={<IconMessageForward size={13} />}
              onClick={handleSendToChat}
              disabled={!currentSheet || processedRows.length === 0}
            >
              {checkedRowIds.size > 0 ? `发送已选 (${checkedRowIds.size} 行)` : '发送给对话分析'}
            </Button>
            <Menu shadow="md" position="bottom-end" withinPortal>
              <Menu.Target>
                <Button
                  size="compact-xs"
                  variant="light"
                  color="teal"
                  loading={exporting}
                  leftSection={<IconFileSpreadsheet size={13} />}
                  disabled={!currentSheet}
                >
                  导出筛选
                </Button>
              </Menu.Target>
              <Menu.Dropdown>
                <Menu.Item onClick={() => void handleExportFiltered('xlsx')}>导出为 Excel (.xlsx)</Menu.Item>
                <Menu.Item onClick={() => void handleExportFiltered('csv')}>导出为 CSV (.csv)</Menu.Item>
              </Menu.Dropdown>
            </Menu>
            {artifact.path && (!artifact.connectionId || artifact.connectionId === 'local') && (
              <Tooltip label="用系统默认应用打开">
                <ActionIcon
                  variant="subtle"
                  size="sm"
                  aria-label="系统应用打开"
                  onClick={() => void api.openFile(artifact.path!, 'open')}
                >
                  <IconExternalLink size={14} />
                </ActionIcon>
              </Tooltip>
            )}
            <Tooltip label="刷新">
              <ActionIcon variant="subtle" size="sm" aria-label="刷新表格" onClick={() => setRevision((v) => v + 1)}>
                <IconRefresh size={14} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="下载原文件">
              <ActionIcon
                variant="subtle"
                size="sm"
                aria-label="下载表格"
                onClick={() => void downloadFile(`${artifact.readUrl}&download=1`, artifact.name)}
              >
                <IconDownload size={14} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </Paper>

      <div className="sheet-grid-container">
        <LoadingOverlay visible={loading} />
        {error ? (
          <div style={{ textAlign: 'center', padding: 36 }}>
            <Text size="sm" c="red" mb="sm">{error}</Text>
            <Button size="xs" variant="light" onClick={() => setRevision((v) => v + 1)}>重试</Button>
          </div>
        ) : currentSheet && currentSheet.columns.length > 0 && viewMode !== 'table' ? (
          <SheetChartStudio
            mode={viewMode}
            fileName={artifact.name}
            filePath={filePath}
            sheetName={currentSheet.name}
            columns={currentSheet.columns}
            rows={processedRows.map((r) => r.cells)}
            onSendToChat={(prompt) => dispatchSheetPrompt(prompt, '已将图表汇总结论填入对话输入框')}
          />
        ) : currentSheet && currentSheet.columns.length > 0 ? (
          <table className="sheet-table" aria-label={`表格数据：${currentSheet.name}`}>
            <thead>
              <tr>
                <th className="sheet-col-check">
                  <Checkbox
                    size="xs"
                    checked={allVisibleChecked}
                    onChange={toggleAllVisible}
                    aria-label="全选当前行"
                  />
                </th>
                <th className="sheet-col-rownum">#</th>
                {currentSheet.columns.map((col, colIdx) => {
                  const isSorted = sortCol === colIdx
                  const isSelected = selectedCol === colIdx
                  return (
                    <th
                      key={`${col}-${colIdx}`}
                      className={isSelected ? 'is-selected-col' : undefined}
                      onClick={() => handleHeaderClick(colIdx)}
                      title="点击查看列统计并切换升/降序"
                    >
                      <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                        <span>{col}</span>
                        {isSorted && (sortDir === 'asc' ? <IconArrowUp size={12} /> : <IconArrowDown size={12} />)}
                      </span>
                    </th>
                  )
                })}
              </tr>
            </thead>
            <tbody>
              {processedRows.length === 0 ? (
                <tr>
                  <td colSpan={currentSheet.columns.length + 2} style={{ textAlign: 'center', padding: 24, color: 'var(--mantine-color-dimmed)' }}>
                    无匹配数据行
                  </td>
                </tr>
              ) : (
                processedRows.map((row, visibleIdx) => {
                  const isChecked = checkedRowIds.has(row.originalIndex)
                  return (
                    <tr key={row.originalIndex} className={isChecked ? 'is-row-checked' : undefined}>
                      <td className="sheet-col-check">
                        <Checkbox
                          size="xs"
                          checked={isChecked}
                          onChange={() => toggleRow(row.originalIndex)}
                          aria-label={`选择第 ${visibleIdx + 1} 行`}
                        />
                      </td>
                      <td className="sheet-col-rownum">{row.originalIndex + 1}</td>
                      {currentSheet.columns.map((_col, colIdx) => {
                        const raw = row.cells[colIdx]
                        const display = cellToString(raw)
                        const isNum = typeof raw === 'number'
                        return (
                          <td
                            key={colIdx}
                            className={`${isNum ? 'is-num' : ''} ${selectedCol === colIdx ? 'is-selected-col' : ''}`.trim() || undefined}
                            title={display}
                            onClick={() => setSelectedCol(colIdx)}
                          >
                            {display}
                          </td>
                        )
                      })}
                    </tr>
                  )
                })
              )}
            </tbody>
          </table>
        ) : !loading ? (
          <div style={{ textAlign: 'center', padding: 36 }}>
            <Text size="sm" c="dimmed">该工作表暂无数据</Text>
          </div>
        ) : null}
      </div>

      <div className="sheet-footer-bar">
        <div className="sheet-tabs-strip" role="tablist" aria-label="工作表切换">
          {sheets.map((sh, idx) => (
            <button
              key={`${sh.name}-${idx}`}
              type="button"
              role="tab"
              aria-selected={idx === activeSheetIdx}
              className={`sheet-tab-btn${idx === activeSheetIdx ? ' is-active' : ''}`}
              onClick={() => handleSwitchSheet(idx)}
            >
              {sh.name} ({sh.total_rows})
            </button>
          ))}
        </div>

        <div className="sheet-stats-strip" aria-label="快速列统计">
          {columnStats ? (
            <>
              <span>列：<strong>{columnStats.columnName}</strong></span>
              <span>总数：<strong>{columnStats.total}</strong></span>
              <span>非空：<strong>{columnStats.nonEmpty}</strong></span>
              <span>去重：<strong>{columnStats.unique}</strong></span>
              {columnStats.numCount > 0 && (
                <>
                  <span>求和：<strong>{formatStatNumber(columnStats.sum)}</strong></span>
                  <span>均值：<strong>{formatStatNumber(columnStats.avg)}</strong></span>
                  <span>最小：<strong>{formatStatNumber(columnStats.min)}</strong></span>
                  <span>最大：<strong>{formatStatNumber(columnStats.max)}</strong></span>
                </>
              )}
            </>
          ) : (
            <span>提示：点击任意列头可排序并查看快速汇总统计</span>
          )}
        </div>
      </div>
    </div>
  )
}
