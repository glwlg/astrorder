import React, { useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  CopyButton,
  Group,
  Modal,
  Progress,
  SegmentedControl,
  Stack,
  Text,
  Textarea,
  Tooltip,
  Table,
} from '@mantine/core'
import {
  IconCheck,
  IconX,
  IconCopy,
  IconEdit,
  IconDeviceFloppy,
  IconTerminal2,
  IconActivity,
  IconGitPullRequest,
  IconTrash,
  IconTrophy,
} from '@tabler/icons-react'
import { Renderer, JSONUIProvider } from '@json-render/react'
import { nestedToFlat, type Spec } from '@json-render/core'
import { api } from '../../api/client'
import { notifications } from '@mantine/notifications'

/**
 * 1. 五子棋 / 棋盘对弈彩蛋组件 (GomokuBoard)
 */
function GomokuBoardRenderer({ element }: { element: any }) {
  const { status, winner, winning_move, total_moves, ascii_board, meta } = element.props || {}

  const handleCellClick = (coord: string, isEmpty: boolean) => {
    if (!isEmpty || status === 'finished' || Boolean(winner)) return
    const isBlackTurn = (meta?.current_turn || '').toLowerCase().includes('black')
    const evt = new CustomEvent('blackboard-action', {
      bubbles: true,
      detail: {
        action: 'gomoku_move',
        payload: { move: coord, isBlack: isBlackTurn }
      }
    })
    window.dispatchEvent(evt)
  }

  const { grid, headers } = useMemo(() => {
    if (!ascii_board || typeof ascii_board !== 'string') {
      return { grid: [], headers: [] }
    }
    const cols = ['A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O']
    const normalized = ascii_board.replace(/\\n/g, '\n')
    const lines = normalized.trim().split('\n')
    const rows: string[][] = []

    if (lines.length >= 10) {
      for (const line of lines) {
        const trimmed = line.trim()
        if (/^[A-O\s]+$/i.test(trimmed)) continue
        const match = trimmed.match(/^\d+\s+(.*)$/)
        if (match) {
          const cells = match[1].trim().split(/\s+/)
          if (cells.length > 0) rows.push(cells.slice(0, 15))
        }
      }
    }
    if (rows.length < 10) {
      const parts = normalized.split(/(?:^|\s+)(?:[1-9]|1[0-5])\s+/)
      for (let i = 1; i < parts.length; i++) {
        const cells = parts[i].trim().split(/\s+/).slice(0, 15)
        if (cells.length >= 10) rows.push(cells)
      }
    }
    return { grid: rows.slice(0, 15), headers: cols }
  }, [ascii_board])

  const lastCoord = useMemo(() => {
    if (!winning_move || typeof winning_move !== 'string') return null
    const match = winning_move.match(/([A-O])(\d+)/i)
    if (!match) return null
    const colLetter = match[1].toUpperCase()
    const rowNum = parseInt(match[2], 10) - 1
    const colIdx = 'ABCDEFGHIJKLMNO'.indexOf(colLetter)
    return colIdx >= 0 && rowNum >= 0 ? { r: rowNum, c: colIdx } : null
  }, [winning_move])

  const isFinished = status === 'finished' || Boolean(winner)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10, background: 'var(--astr-surface, #ffffff)', borderRadius: 8 }}>
      <div
        style={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'center',
          justifyContent: 'space-between',
          padding: '8px 12px',
          background: isFinished ? '#ECFDF5' : '#F0F9FF',
          border: `1px solid ${isFinished ? '#A7F3D0' : '#BAE6FD'}`,
          borderRadius: 8,
          gap: 8,
        }}
      >
        <Group gap="xs">
          <IconTrophy size={18} color={isFinished ? '#059669' : '#0284C7'} />
          <Text size="sm" fw={700} c={isFinished ? '#065F46' : '#0369A1'}>
            {winner ? `胜者: ${winner}` : isFinished ? '对局已完结' : '对局进行中'}
          </Text>
          {status && (
            <Badge size="xs" color={isFinished ? 'teal' : 'blue'} variant="filled">
              {status}
            </Badge>
          )}
        </Group>

        <Group gap="xs">
          {total_moves && (
            <Badge size="xs" variant="light" color="gray">
              共 {total_moves} 手
            </Badge>
          )}
          {winning_move && (
            <Badge size="xs" color="indigo" variant="light">
              关键手: {winning_move}
            </Badge>
          )}
        </Group>
      </div>

      {grid.length > 0 && (
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            alignItems: 'center',
            padding: 12,
            background: 'linear-gradient(135deg, #e4b680 0%, #c99355 100%)',
            borderRadius: 8,
            boxShadow: 'inset 0 0 12px rgba(0,0,0,0.2), 0 4px 12px rgba(0,0,0,0.08)',
            border: '2px solid #8d5c2d',
            userSelect: 'none',
          }}
        >
          <div style={{ display: 'flex', marginLeft: 24, marginBottom: 2 }}>
            {headers.map((h) => (
              <div key={h} style={{ width: 20, textAlign: 'center', fontSize: 10, fontWeight: 700, color: '#5c3917', fontFamily: 'monospace' }}>
                {h}
              </div>
            ))}
          </div>

          <div style={{ display: 'flex', flexDirection: 'column' }}>
            {grid.map((row, rIdx) => (
              <div key={rIdx} style={{ display: 'flex', alignItems: 'center' }}>
                <div style={{ width: 24, fontSize: 10, fontWeight: 700, color: '#5c3917', fontFamily: 'monospace', textAlign: 'right', paddingRight: 4 }}>
                  {rIdx + 1}
                </div>
                {row.map((cell, cIdx) => {
                  const isBlack = cell === '●' || cell.toUpperCase() === 'B' || cell === 'X'
                  const isWhite = cell === '○' || cell.toUpperCase() === 'W' || cell === 'O'
                  const isLastMove = lastCoord?.r === rIdx && lastCoord?.c === cIdx
                  const isStarPoint =
                    (rIdx === 3 && (cIdx === 3 || cIdx === 11)) ||
                    (rIdx === 7 && cIdx === 7) ||
                    (rIdx === 11 && (cIdx === 3 || cIdx === 11))
                  const isEmpty = !isBlack && !isWhite
                  const coord = String(headers[cIdx]) + String(rIdx + 1)

                  return (
                    <div
                      key={cIdx}
                      onClick={() => handleCellClick(coord, isEmpty)}
                      title={isEmpty ? '点击落子: ' + coord : String(cell) + ' (' + coord + ')'}
                      style={{
                        position: 'relative',
                        width: 20,
                        height: 20,
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        cursor: isEmpty && !isFinished ? 'pointer' : 'default',
                      }}
                    >
                      <div style={{ position: 'absolute', left: 0, right: 0, top: '50%', height: 1, background: '#7c4c1f' }} />
                      <div style={{ position: 'absolute', top: 0, bottom: 0, left: '50%', width: 1, background: '#7c4c1f' }} />

                      {isStarPoint && !isBlack && !isWhite && (
                        <div style={{ position: 'absolute', width: 3.5, height: 3.5, borderRadius: '50%', background: '#5c3917', zIndex: 1 }} />
                      )}

                      {isBlack && (
                        <div
                          style={{
                            position: 'relative',
                            width: 15,
                            height: 15,
                            borderRadius: '50%',
                            background: 'radial-gradient(circle at 35% 30%, #475569 0%, #0f172a 80%)',
                            boxShadow: '0 2px 4px rgba(0,0,0,0.4)',
                            zIndex: 2,
                            border: isLastMove ? '2px solid #F59E0B' : undefined,
                          }}
                          title={`黑子 (${headers[cIdx]}${rIdx + 1})`}
                        />
                      )}

                      {isWhite && (
                        <div
                          style={{
                            position: 'relative',
                            width: 15,
                            height: 15,
                            borderRadius: '50%',
                            background: 'radial-gradient(circle at 35% 30%, #ffffff 0%, #cbd5e1 85%)',
                            boxShadow: '0 2px 4px rgba(0,0,0,0.3)',
                            zIndex: 2,
                            border: isLastMove ? '2px solid #F59E0B' : undefined,
                          }}
                          title={`白子 (${headers[cIdx]}${rIdx + 1})`}
                        />
                      )}

                      {isLastMove && (
                        <div
                          style={{
                            position: 'absolute',
                            inset: -1,
                            borderRadius: '50%',
                            border: '1.5px solid #F59E0B',
                            animation: 'lastMovePulse 1.5s infinite',
                            zIndex: 3,
                            pointerEvents: 'none',
                          }}
                        />
                      )}
                    </div>
                  )
                })}
              </div>
            ))}
          </div>
        </div>
      )}

      {meta && typeof meta === 'object' && (
        <div style={{ padding: '8px 10px', background: 'var(--astr-surface-muted, #F8FAFC)', borderRadius: 6, border: '1px solid var(--astr-border, #E5E7EB)', fontSize: 11 }}>
          <div style={{ fontWeight: 600, color: '#64748B', marginBottom: 4 }}>对局阵营与席位:</div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 6 }}>
            {Object.entries(meta).map(([mk, mv]) => (
              <div key={mk} style={{ display: 'flex', gap: 4 }}>
                <span style={{ color: '#94A3B8' }}>{mk}:</span>
                <span style={{ fontWeight: 500, fontFamily: 'monospace' }}>
                  {typeof mv === 'object' ? JSON.stringify(mv) : String(mv)}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      <style>{`
        @keyframes lastMovePulse {
          0%, 100% { transform: scale(1); opacity: 1; }
          50% { transform: scale(1.3); opacity: 0.4; }
        }
      `}</style>
    </div>
  )
}

/**
 * 2. 状态与概览卡片 (StatusCard)
 */
function StatusCardRenderer({ element }: { element: any }) {
  const { title, status = 'info', summary, badge, timestamp, details } = element.props || {}
  const statusColorMap: Record<string, { bg: string; border: string; text: string; dot: string }> = {
    success: { bg: '#ECFDF5', border: '#A7F3D0', text: '#065F46', dot: '#10B981' },
    running: { bg: '#F0FDF4', border: '#86EFAC', text: '#15803D', dot: '#22C55E' },
    ready: { bg: '#F0F9FF', border: '#BAE6FD', text: '#0369A1', dot: '#0284C7' },
    warning: { bg: '#FFFBEB', border: '#FDE68A', text: '#92400E', dot: '#F59E0B' },
    error: { bg: '#FEF2F2', border: '#FECACA', text: '#991B1B', dot: '#EF4444' },
    info: { bg: '#F8FAFC', border: '#E2E8F0', text: '#334155', dot: '#64748B' },
  }
  const theme = statusColorMap[status.toLowerCase()] || statusColorMap.info

  return (
    <div
      style={{
        padding: '10px 14px',
        borderRadius: 8,
        background: theme.bg,
        border: `1px solid ${theme.border}`,
        display: 'flex',
        flexDirection: 'column',
        gap: 6,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Group gap="xs">
          <span style={{ width: 8, height: 8, borderRadius: '50%', background: theme.dot }} />
          <Text fw={700} size="sm" c={theme.text}>
            {title || '状态通知'}
          </Text>
          {badge && (
            <Badge size="xs" variant="filled" color={status === 'error' ? 'red' : status === 'warning' ? 'yellow' : 'blue'}>
              {badge}
            </Badge>
          )}
        </Group>
        {timestamp && (
          <Text size="xs" c="dimmed" style={{ fontFamily: 'monospace' }}>
            {timestamp}
          </Text>
        )}
      </div>
      {summary && (
        <Text size="xs" style={{ color: theme.text, lineHeight: 1.5, opacity: 0.9 }}>
          {summary}
        </Text>
      )}
      {details && typeof details === 'object' && (
        <div
          style={{
            marginTop: 4,
            padding: '6px 8px',
            background: 'rgba(255, 255, 255, 0.75)',
            borderRadius: 6,
            fontSize: 11,
            display: 'flex',
            flexDirection: 'column',
            gap: 4,
          }}
        >
          {Object.entries(details).map(([dk, dv]) => (
            <div key={dk} style={{ display: 'flex', gap: 6, alignItems: 'baseline' }}>
              <span style={{ color: '#64748B', whiteSpace: 'nowrap' }}>{dk}:</span>
              <span style={{ fontWeight: 600, fontFamily: 'monospace', color: '#1E293B', wordBreak: 'break-all' }}>
                {typeof dv === 'object' ? JSON.stringify(dv) : String(dv)}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * 3. 核心指标矩阵 (MetricGrid)
 */
function MetricGridRenderer({ element }: { element: any }) {
  const { title, metrics = [] } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {title && (
        <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
          {title}
        </Text>
      )}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(110px, 1fr))', gap: 8 }}>
        {metrics.map((m: any, idx: number) => {
          const isWarn = m.status === 'warn'
          const isBad = m.status === 'bad'
          const isGood = m.status === 'good'
          const valColor = isBad ? '#DC2626' : isWarn ? '#D97706' : isGood ? '#16A34A' : '#5B5BD6'

          return (
            <div
              key={idx}
              style={{
                padding: '8px 10px',
                background: 'var(--astr-surface-muted, #F8FAFC)',
                borderRadius: 6,
                border: '1px solid var(--astr-border, #E5E7EB)',
                display: 'flex',
                flexDirection: 'column',
                justifyContent: 'space-between',
              }}
            >
              <div style={{ fontSize: 10.5, color: '#64748B', display: 'flex', justifyContent: 'space-between' }}>
                <span>{m.label}</span>
                {m.change && (
                  <span style={{ fontSize: 9.5, fontWeight: 600, color: m.change.startsWith('+') ? '#16A34A' : '#DC2626' }}>
                    {m.change}
                  </span>
                )}
              </div>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 3, marginTop: 4 }}>
                <span style={{ fontSize: 15, fontWeight: 700, fontFamily: 'monospace', color: valColor }}>
                  {m.value}
                </span>
                {m.unit && <span style={{ fontSize: 10, color: '#94A3B8' }}>{m.unit}</span>}
              </div>
              {m.hint && <div style={{ fontSize: 9.5, color: '#94A3B8', marginTop: 2 }}>{m.hint}</div>}
            </div>
          )
        })}
      </div>
    </div>
  )
}

/**
 * 4. 阶段与任务工作流时间线 (StepTimeline)
 */
function StepTimelineRenderer({ element }: { element: any }) {
  const { title, steps = [] } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      {title && (
        <Group justify="space-between">
          <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
            {title}
          </Text>
          <Badge size="xs" variant="light" color="indigo">
            {steps.filter((s: any) => s.status === 'completed').length} / {steps.length} 达成
          </Badge>
        </Group>
      )}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
        {steps.map((step: any, idx: number) => {
          const isDone = step.status === 'completed' || step.status === 'done'
          const isCurrent = step.status === 'in_progress' || step.status === 'running'
          const isFailed = step.status === 'failed' || step.status === 'error'

          const toggleStep = () => {
            const nextStatus = isDone ? 'in_progress' : isCurrent ? 'completed' : 'in_progress'
            window.dispatchEvent(new CustomEvent('blackboard-action', {
              bubbles: true,
              detail: { action: 'timeline_step_toggle', payload: { stepIndex: idx, status: nextStatus } }
            }))
          }

          return (
            <div
              key={idx}
              onClick={toggleStep}
              title="点击切换步骤状态"
              style={{
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'flex-start',
                gap: 10,
                padding: '8px 10px',
                borderRadius: 6,
                background: isCurrent ? 'rgba(91, 91, 214, 0.05)' : 'var(--astr-surface-muted, #F8FAFC)',
                border: `1px solid ${isCurrent ? '#5B5BD6' : isFailed ? '#FCA5A5' : 'var(--astr-border, #E5E7EB)'}`,
              }}
            >
              <div style={{ marginTop: 2 }}>
                {isDone ? (
                  <div style={{ width: 18, height: 18, borderRadius: '50%', background: '#10B981', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <IconCheck size={12} color="#ffffff" />
                  </div>
                ) : isCurrent ? (
                  <div style={{ width: 18, height: 18, borderRadius: '50%', background: '#5B5BD6', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <IconActivity size={12} color="#ffffff" />
                  </div>
                ) : isFailed ? (
                  <div style={{ width: 18, height: 18, borderRadius: '50%', background: '#EF4444', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                    <IconX size={12} color="#ffffff" />
                  </div>
                ) : (
                  <div style={{ width: 18, height: 18, borderRadius: '50%', border: '1.5px solid #CBD5E1', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 10, color: '#94A3B8' }}>
                    {idx + 1}
                  </div>
                )}
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                  <Text size="xs" fw={isCurrent ? 700 : 600} c={isCurrent ? '#5B5BD6' : isFailed ? '#DC2626' : 'var(--astr-text, #1E293B)'}>
                    {step.title || step.name || `步骤 ${idx + 1}`}
                  </Text>
                  {step.time && (
                    <Text size="10px" c="dimmed" style={{ fontFamily: 'monospace' }}>
                      {step.time}
                    </Text>
                  )}
                </div>
                {step.description && (
                  <Text size="11px" c="dimmed" mt={2} style={{ lineHeight: 1.4 }}>
                    {step.description}
                  </Text>
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

/**
 * 5. 结构化数据表格 (DataTable)
 */
function DataTableRenderer({ element }: { element: any }) {
  const { title, columns = [], rows = [], caption } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {title && (
        <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
          {title}
        </Text>
      )}
      <div style={{ borderRadius: 6, border: '1px solid var(--astr-border, #E5E7EB)', background: 'var(--astr-surface, #ffffff)', overflowX: 'auto', maxHeight: 260 }}>
        <Table striped highlightOnHover withTableBorder={false} style={{ fontSize: 11 }}>
          <Table.Thead style={{ background: 'var(--astr-surface-muted, #F8FAFC)', position: 'sticky', top: 0, zIndex: 1 }}>
            <Table.Tr>
              {columns.map((col: any) => (
                <Table.Th key={col.key} style={{ padding: '6px 10px', color: '#64748B', fontWeight: 600 }}>
                  {col.label || col.key}
                </Table.Th>
              ))}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map((row: any, rIdx: number) => (
              <Table.Tr key={rIdx}>
                {columns.map((col: any) => {
                  const val = row[col.key]
                  const isBadge = col.type === 'badge' || col.key.includes('status') || col.key.includes('state')
                  const isMono = col.type === 'mono' || col.key.includes('id') || col.key.includes('ip') || col.key.includes('port')

                  return (
                    <Table.Td key={col.key} style={{ padding: '6px 10px' }}>
                      {isBadge && typeof val === 'string' ? (
                        <Badge size="xs" variant="light" color={val === 'active' || val === 'ready' || val === 'ok' ? 'teal' : val === 'warning' ? 'yellow' : 'gray'}>
                          {val}
                        </Badge>
                      ) : isMono ? (
                        <span style={{ fontFamily: 'monospace', color: '#5B5BD6' }}>{String(val ?? '')}</span>
                      ) : (
                        <span>{typeof val === 'object' ? JSON.stringify(val) : String(val ?? '')}</span>
                      )}
                    </Table.Td>
                  )
                })}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </div>
      {caption && <Text size="10px" c="dimmed">{caption}</Text>}
    </div>
  )
}

/**
 * 6. 代码/差异对比视图 (DiffViewer)
 */
function DiffViewerRenderer({ element }: { element: any }) {
  const { file, diff } = element.props || {}
  const lines = useMemo(() => (diff && typeof diff === 'string' ? diff.split('\n') : []), [diff])

  return (
    <div style={{ borderRadius: 6, border: '1px solid var(--astr-border, #E5E7EB)', background: 'var(--astr-surface-muted, #F8FAFC)', overflow: 'hidden', fontSize: 11 }}>
      {file && (
        <div style={{ padding: '6px 10px', borderBottom: '1px solid var(--astr-border, #E5E7EB)', background: 'var(--astr-surface, #ffffff)', display: 'flex', alignItems: 'center', gap: 6, fontWeight: 600, fontFamily: 'monospace', color: '#1E293B' }}>
          <IconGitPullRequest size={14} color="#5B5BD6" />
          <span>{file}</span>
        </div>
      )}
      <div style={{ maxHeight: 240, overflowY: 'auto', padding: '6px 0', fontFamily: 'Consolas, monospace', lineHeight: 1.4 }}>
        {lines.length > 0 ? (
          lines.map((line: string, idx: number) => {
            const isAdd = line.startsWith('+') && !line.startsWith('+++')
            const isDel = line.startsWith('-') && !line.startsWith('---')
            const isHunk = line.startsWith('@@')

            return (
              <div
                key={idx}
                style={{
                  padding: '1px 10px',
                  background: isAdd ? 'rgba(16, 185, 129, 0.12)' : isDel ? 'rgba(239, 68, 68, 0.12)' : isHunk ? 'rgba(91, 91, 214, 0.08)' : 'transparent',
                  color: isAdd ? '#047857' : isDel ? '#B91C1C' : isHunk ? '#5B5BD6' : '#334155',
                  whiteSpace: 'pre',
                }}
              >
                {line}
              </div>
            )
          })
        ) : (
          <div style={{ padding: 10, color: '#94A3B8' }}>暂无代码差异内容</div>
        )}
      </div>


    </div>
  )
}

/**
 * 7. 执行清单与待办检查表 (Checklist)
 */
function ChecklistRenderer({ element }: { element: any }) {
  const { title, items = [] } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      {title && (
        <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
          {title}
        </Text>
      )}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {items.map((item: any, idx: number) => (
          <div
            key={idx}
            style={{
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'space-between',
              padding: '6px 10px',
              borderRadius: 6,
              background: 'var(--astr-surface-muted, #F8FAFC)',
              border: '1px solid var(--astr-border, #E5E7EB)',
              fontSize: 11.5,
            }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div
                style={{
                  width: 14,
                  height: 14,
                  borderRadius: 4,
                  border: item.done ? 'none' : '1px solid #CBD5E1',
                  background: item.done ? '#10B981' : '#ffffff',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                }}
              >
                {item.done && <IconCheck size={10} color="#ffffff" />}
              </div>
              <span style={{ textDecoration: item.done ? 'line-through' : 'none', color: item.done ? '#94A3B8' : '#1E293B' }}>
                {item.label || item.text}
              </span>
            </div>
            {item.assignee && <Badge size="xs" variant="light" color="gray">{item.assignee}</Badge>}
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * 8. 终端日志输出流 (TerminalLog)
 */
function TerminalLogRenderer({ element }: { element: any }) {
  const { title, lines = [], status } = element.props || {}
  const list = Array.isArray(lines) ? lines : typeof lines === 'string' ? lines.split('\n') : []

  return (
    <div style={{ borderRadius: 6, background: '#0F172A', border: '1px solid #1E293B', overflow: 'hidden', fontSize: 11 }}>
      <div style={{ padding: '6px 10px', background: '#1E293B', display: 'flex', alignItems: 'center', justifyContent: 'space-between', color: '#94A3B8', fontSize: 10.5 }}>
        <Group gap="xs">
          <IconTerminal2 size={13} color="#38BDF8" />
          <span style={{ fontWeight: 600, color: '#F1F5F9' }}>{title || '执行终端输出'}</span>
        </Group>
        {status && <Badge size="xs" color={status === 'ok' ? 'teal' : 'gray'}>{status}</Badge>}
      </div>
      <div style={{ padding: 8, maxHeight: 220, overflowY: 'auto', fontFamily: 'Consolas, Monaco, monospace', color: '#E2E8F0', lineHeight: 1.4, whiteSpace: 'pre-wrap' }}>
        {list.map((l: string, idx: number) => (
          <div key={idx} style={{ color: l.includes('ERROR') || l.includes('fail') ? '#F87171' : l.includes('WARN') ? '#FBBF24' : '#94A3B8' }}>
            {l}
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * 9. API 接口与契约清单 (ApiEndpointsCard)
 */
function ApiEndpointsCardRenderer({ element }: { element: any }) {
  const { title = 'API 接口契约清单', endpoints = [], baseUrl } = element.props || {}
  const methodColor: Record<string, { bg: string; text: string }> = {
    GET: { bg: '#ECFDF5', text: '#059669' },
    POST: { bg: '#EFF6FF', text: '#2563EB' },
    PUT: { bg: '#FFFBEB', text: '#D97706' },
    DELETE: { bg: '#FEF2F2', text: '#DC2626' },
    PATCH: { bg: '#FAF5FF', text: '#9333EA' },
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <Group justify="space-between">
        <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
          {title}
        </Text>
        {baseUrl && <Badge size="xs" variant="light" color="gray">{baseUrl}</Badge>}
      </Group>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {endpoints.map((ep: any, idx: number) => {
          const method = (ep.method || 'GET').toUpperCase()
          const style = methodColor[method] || { bg: '#F1F5F9', text: '#475569' }
          return (
            <div
              key={idx}
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: '5px 8px',
                borderRadius: 6,
                background: 'var(--astr-surface-muted, #F8FAFC)',
                border: '1px solid var(--astr-border, #E5E7EB)',
                fontSize: 11,
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                <span style={{ padding: '1px 5px', borderRadius: 4, fontSize: 9.5, fontWeight: 800, fontFamily: 'monospace', background: style.bg, color: style.text }}>
                  {method}
                </span>
                <span style={{ fontFamily: 'monospace', fontWeight: 600, color: 'var(--astr-text, #1E293B)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {ep.path || ep.url}
                </span>
                {ep.desc && <span style={{ color: '#94A3B8', fontSize: 10.5 }}>- {ep.desc}</span>}
              </div>
              {ep.status && (
                <span style={{ fontSize: 10, fontFamily: 'monospace', color: ep.status < 300 ? '#10B981' : '#EF4444' }}>
                  {ep.status}
                </span>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

/**
 * 10. 资源配额与负载条 (ResourceUsageBar)
 */
function ResourceUsageBarRenderer({ element }: { element: any }) {
  const { title = '系统资源负载与配额', resources = [] } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
        {title}
      </Text>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', gap: 8 }}>
        {resources.map((r: any, idx: number) => {
          const percent = typeof r.percent === 'number' ? r.percent : typeof r.used === 'number' && typeof r.total === 'number' ? Math.round((r.used / r.total) * 100) : 50
          const color = percent > 85 ? 'red' : percent > 70 ? 'yellow' : 'indigo'

          return (
            <div key={idx} style={{ padding: '8px 10px', background: 'var(--astr-surface-muted, #F8FAFC)', borderRadius: 6, border: '1px solid var(--astr-border, #E5E7EB)' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, marginBottom: 4 }}>
                <span style={{ color: '#64748B' }}>{r.name || r.label}</span>
                <span style={{ fontWeight: 600, fontFamily: 'monospace' }}>{r.display || `${percent}%`}</span>
              </div>
              <Progress value={percent} size="sm" color={color} radius="xl" />
              {r.used !== undefined && r.total !== undefined && (
                <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 10, color: '#94A3B8', marginTop: 4 }}>
                  <span>已用: {r.used}{r.unit || ''}</span>
                  <span>总量: {r.total}{r.unit || ''}</span>
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

/**
 * 11. 自动化测试执行报告 (TestReport)
 */
function TestReportRenderer({ element }: { element: any }) {
  const { title = '自动化测试套件执行报告', passed = 0, failed = 0, skipped = 0, duration, suite = '', cases = [] } = element.props || {}
  const total = passed + failed + skipped || cases.length || 1
  const passRate = Math.round((passed / total) * 100)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
            {title}
          </Text>
          {suite && <Text size="11px" c="dimmed">{suite}</Text>}
        </div>
        {duration && <Badge size="xs" variant="light" color="gray">耗时 {duration}</Badge>}
      </div>
      <div style={{ display: 'flex', gap: 8 }}>
        <div style={{ flex: 1, padding: '6px 10px', background: '#ECFDF5', border: '1px solid #A7F3D0', borderRadius: 6, textAlign: 'center' }}>
          <div style={{ fontSize: 10, color: '#047857' }}>通过 (Passed)</div>
          <div style={{ fontSize: 16, fontWeight: 700, color: '#065F46', fontFamily: 'monospace' }}>{passed}</div>
        </div>
        <div style={{ flex: 1, padding: '6px 10px', background: failed > 0 ? '#FEF2F2' : '#F8FAFC', border: `1px solid ${failed > 0 ? '#FECACA' : '#E5E7EB'}`, borderRadius: 6, textAlign: 'center' }}>
          <div style={{ fontSize: 10, color: failed > 0 ? '#DC2626' : '#64748B' }}>失败 (Failed)</div>
          <div style={{ fontSize: 16, fontWeight: 700, color: failed > 0 ? '#B91C1C' : '#94A3B8', fontFamily: 'monospace' }}>{failed}</div>
        </div>
        <div style={{ flex: 1, padding: '6px 10px', background: '#F8FAFC', border: '1px solid #E5E7EB', borderRadius: 6, textAlign: 'center' }}>
          <div style={{ fontSize: 10, color: '#64748B' }}>通过率</div>
          <div style={{ fontSize: 16, fontWeight: 700, color: passRate === 100 ? '#10B981' : '#5B5BD6', fontFamily: 'monospace' }}>{passRate}%</div>
        </div>
      </div>
      {cases.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 3, maxHeight: 160, overflowY: 'auto' }}>
          {cases.slice(0, 12).map((c: any, idx: number) => {
            const isPass = c.status === 'passed' || c.pass
            return (
              <div key={idx} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '4px 8px', borderRadius: 4, background: isPass ? 'transparent' : '#FEF2F2', fontSize: 10.5 }}>
                <Group gap={6}>
                  {isPass ? <IconCheck size={11} color="#10B981" /> : <IconX size={11} color="#EF4444" />}
                  <span style={{ color: isPass ? '#334155' : '#B91C1C', fontFamily: 'monospace' }}>{c.name || c.title}</span>
                </Group>
                {c.duration && <span style={{ color: '#94A3B8', fontSize: 10, fontFamily: 'monospace' }}>{c.duration}</span>}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

/**
 * 12. 安全审计与漏洞合规报告 (CveSecurityReport)
 */
function CveSecurityReportRenderer({ element }: { element: any }) {
  const { title = '安全合规与漏洞审计报告', critical = 0, high = 0, medium = 0, low = 0, vulnerabilities = [] } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <Group justify="space-between">
        <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
          {title}
        </Text>
        <Badge size="xs" color={critical > 0 ? 'red' : high > 0 ? 'orange' : 'teal'}>
          {critical > 0 ? '发现阻断性高危' : high > 0 ? '需关注风险' : '通过基线'}
        </Badge>
      </Group>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 6 }}>
        <div style={{ padding: '6px 8px', background: '#FEF2F2', border: '1px solid #FECACA', borderRadius: 6, textAlign: 'center' }}>
          <div style={{ fontSize: 9.5, color: '#B91C1C' }}>CRITICAL</div>
          <div style={{ fontSize: 15, fontWeight: 800, color: '#DC2626', fontFamily: 'monospace' }}>{critical}</div>
        </div>
        <div style={{ padding: '6px 8px', background: '#FFF7ED', border: '1px solid #FED7AA', borderRadius: 6, textAlign: 'center' }}>
          <div style={{ fontSize: 9.5, color: '#C2410C' }}>HIGH</div>
          <div style={{ fontSize: 15, fontWeight: 800, color: '#EA580C', fontFamily: 'monospace' }}>{high}</div>
        </div>
        <div style={{ padding: '6px 8px', background: '#FFFBEB', border: '1px solid #FDE68A', borderRadius: 6, textAlign: 'center' }}>
          <div style={{ fontSize: 9.5, color: '#B45309' }}>MEDIUM</div>
          <div style={{ fontSize: 15, fontWeight: 700, color: '#D97706', fontFamily: 'monospace' }}>{medium}</div>
        </div>
        <div style={{ padding: '6px 8px', background: '#F8FAFC', border: '1px solid #E5E7EB', borderRadius: 6, textAlign: 'center' }}>
          <div style={{ fontSize: 9.5, color: '#64748B' }}>LOW</div>
          <div style={{ fontSize: 15, fontWeight: 700, color: '#64748B', fontFamily: 'monospace' }}>{low}</div>
        </div>
      </div>
      {vulnerabilities.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, maxHeight: 160, overflowY: 'auto' }}>
          {vulnerabilities.map((v: any, idx: number) => (
            <div key={idx} style={{ padding: '5px 8px', background: 'var(--astr-surface-muted, #F8FAFC)', border: '1px solid var(--astr-border, #E5E7EB)', borderRadius: 4, fontSize: 11, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                <span style={{ fontFamily: 'monospace', fontWeight: 700, color: '#5B5BD6' }}>{v.cve || v.id}</span>
                <span style={{ color: '#64748B' }}>{v.package || v.pkg}</span>
              </div>
              <Badge size="xs" color={v.severity === 'CRITICAL' ? 'red' : 'orange'} variant="light">{v.severity}</Badge>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * 13. Git 提交记录与发布变更日志 (GitCommitLog)
 */
function GitCommitLogRenderer({ element }: { element: any }) {
  const { title = 'Git 提交与发布记录', branch = 'main', commits = [] } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <Group justify="space-between">
        <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
          {title}
        </Text>
        <Badge size="xs" variant="light" color="indigo">{branch}</Badge>
      </Group>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        {commits.map((c: any, idx: number) => (
          <div key={idx} style={{ display: 'flex', alignItems: 'flex-start', gap: 8, padding: '6px 8px', borderRadius: 6, background: 'var(--astr-surface-muted, #F8FAFC)', border: '1px solid var(--astr-border, #E5E7EB)', fontSize: 11 }}>
            <span style={{ fontFamily: 'monospace', fontWeight: 600, color: '#5B5BD6', background: 'rgba(91, 91, 214, 0.08)', padding: '1px 5px', borderRadius: 4 }}>
              {(c.hash || c.commit || '').slice(0, 7)}
            </span>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontWeight: 600, color: '#1E293B', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {c.message || c.title}
              </div>
              <div style={{ display: 'flex', gap: 8, color: '#94A3B8', fontSize: 10, marginTop: 2 }}>
                {c.author && <span>{c.author}</span>}
                {c.time && <span>{c.time}</span>}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

/**
 * 14. 架构与调用链路流程图 (ArchitectureFlow)
 */
function ArchitectureFlowRenderer({ element }: { element: any }) {
  const { title = '微服务调用链路与架构拓扑', nodes = [] } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
      <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
        {title}
      </Text>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, overflowX: 'auto', padding: '6px 2px' }}>
        {nodes.map((node: any, idx: number) => (
          <React.Fragment key={idx}>
            <div style={{ minWidth: 110, padding: '8px 10px', background: 'var(--astr-surface, #ffffff)', border: '1.5px solid var(--astr-border, #E5E7EB)', borderRadius: 8, textAlign: 'center', boxShadow: '0 1px 3px rgba(0,0,0,0.04)' }}>
              <Badge size="xs" variant="light" color={node.status === 'ok' ? 'teal' : 'indigo'} mb={4}>
                {node.role || 'Service'}
              </Badge>
              <div style={{ fontWeight: 700, fontSize: 11.5, color: '#1E293B' }}>{node.name || node.label}</div>
              {node.desc && <div style={{ fontSize: 9.5, color: '#94A3B8', marginTop: 2 }}>{node.desc}</div>}
            </div>
            {idx < nodes.length - 1 && (
              <div style={{ display: 'flex', alignItems: 'center', color: '#94A3B8', fontSize: 12 }}>
                →
              </div>
            )}
          </React.Fragment>
        ))}
      </div>
    </div>
  )
}

/**
 * 15. 键值配置网格 (KeyValueGrid)
 */
function KeyValueGridRenderer({ element }: { element: any }) {
  const { entries = [] } = element.props || {}

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(130px, 1fr))', gap: 6 }}>
      {entries.map((item: any, idx: number) => (
        <div key={idx} style={{ padding: '6px 8px', background: 'var(--astr-surface-muted, #F8FAFC)', borderRadius: 6, border: '1px solid var(--astr-border, #E5E7EB)' }}>
          <div style={{ fontSize: 10, color: '#64748B', marginBottom: 2 }}>{item.key}</div>
          <div style={{ fontSize: 11.5, fontWeight: 600, color: '#5B5BD6', fontFamily: 'monospace', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={String(item.value)}>
            {String(item.value)}
          </div>
        </div>
      ))}
    </div>
  )
}

/**
 * json-render 全量核心组件注册表
 */

/**
 * 10. 主机与环境性能体检卡 (HostNodeTelemetryCard)
 */
function HostNodeTelemetryCardRenderer({ element }: { element: any }) {
  const { title, os, cpu, load, memory_available, uptime, agent, status = 'completed' } = element.props || {}

  // 解析可用内存百分比 (如 "21 GiB (91.3%)")
  const memPercent = useMemo(() => {
    if (typeof memory_available === 'string') {
      const match = memory_available.match(/\((\d+(?:\.\d+)?)\s*%\)/)
      if (match) return parseFloat(match[1])
    }
    return null
  }, [memory_available])

  const isUbuntu = typeof os === 'string' && os.toLowerCase().includes('ubuntu')
  const isDebian = typeof os === 'string' && os.toLowerCase().includes('debian')
  const osBadgeColor = isUbuntu ? 'orange' : isDebian ? 'red' : 'indigo'

  return (
    <div
      style={{
        borderRadius: 8,
        background: 'var(--astr-surface, #ffffff)',
        border: '1px solid var(--astr-border, #E5E7EB)',
        padding: '12px 14px',
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      {/* 头部：系统与状态 */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Group gap="xs">
          <Badge size="sm" variant="filled" color={osBadgeColor}>
            {isUbuntu ? '🐧 Ubuntu WSL' : isDebian ? '🍥 Debian Host' : '🖥️ 服务器节点'}
          </Badge>
          <Text size="sm" fw={700} c="var(--astr-text, #1E293B)">
            {title || os || '节点体检报告'}
          </Text>
        </Group>
        <Badge size="xs" variant="light" color="teal">
          {status}
        </Badge>
      </div>

      {/* 硬件规格与负荷矩阵 */}
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(130px, 1fr))',
          gap: 8,
          padding: 8,
          borderRadius: 6,
          background: 'var(--astr-surface-muted, #F8FAFC)',
          border: '1px solid var(--astr-border, #E5E7EB)',
        }}
      >
        <div>
          <div style={{ fontSize: 10, color: '#64748B' }}>CPU 处理器</div>
          <div style={{ fontSize: 11.5, fontWeight: 600, color: '#1E293B', marginTop: 2 }} title={cpu}>
            {cpu || '未知 CPU'}
          </div>
        </div>
        <div>
          <div style={{ fontSize: 10, color: '#64748B' }}>系统负载 (1/5/15m)</div>
          <div style={{ fontSize: 11.5, fontWeight: 700, fontFamily: 'monospace', color: '#5B5BD6', marginTop: 2 }}>
            {load || '未知'}
          </div>
        </div>
        {uptime && (
          <div>
            <div style={{ fontSize: 10, color: '#64748B' }}>运行时长 (Uptime)</div>
            <div style={{ fontSize: 11.5, fontWeight: 600, color: '#1E293B', marginTop: 2 }}>
              {uptime}
            </div>
          </div>
        )}
      </div>

      {/* 内存利用率健康条 */}
      {memory_available && (
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11, marginBottom: 4 }}>
            <span style={{ color: '#64748B' }}>物理可用内存</span>
            <span style={{ fontWeight: 600, fontFamily: 'monospace', color: '#059669' }}>
              {memory_available}
            </span>
          </div>
          <Progress
            value={memPercent ?? 80}
            size="sm"
            color="teal"
            radius="xl"
          />
        </div>
      )}

      {/* 关联 Agent */}
      {agent && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 10.5, color: '#94A3B8' }}>
          <span>执行 Agent:</span>
          <span style={{ fontFamily: 'monospace', color: '#64748B' }}>{agent}</span>
        </div>
      )}
    </div>
  )
}


/**
 * 11. 多节点集群性能横向对比看板 (MultiNodeClusterSummary)
 */
function MultiNodeClusterSummaryRenderer({ element }: { element: any }) {
  const { title = '跨环境集群节点性能横向对比', nodes = [] } = element.props || {}

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <Group justify="space-between">
        <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
          {title}
        </Text>
        <Badge size="xs" variant="light" color="indigo">
          共 {nodes.length} 个采集节点
        </Badge>
      </Group>

      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))',
          gap: 10,
        }}
      >
        {nodes.map((node: any, idx: number) => {
          const isWSL = node.name.toLowerCase().includes('wsl') || (node.os && node.os.includes('Ubuntu'))
          const badgeColor = isWSL ? 'orange' : 'red'

          return (
            <div
              key={idx}
              style={{
                borderRadius: 8,
                background: 'var(--astr-surface, #ffffff)',
                border: '1.5px solid var(--astr-border, #E5E7EB)',
                padding: '10px 12px',
                display: 'flex',
                flexDirection: 'column',
                gap: 8,
                boxShadow: '0 1px 4px rgba(0,0,0,0.03)',
              }}
            >
              {/* 节点标题行 */}
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
                <Group gap={6}>
                  <Badge size="xs" variant="filled" color={badgeColor}>
                    {node.name.toUpperCase()}
                  </Badge>
                  <Text size="xs" fw={700} c="#1E293B">
                    {node.os || node.name}
                  </Text>
                </Group>
              </div>

              {/* 关键性能指标清单 */}
              <div style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 11 }}>
                <div style={{ display: 'flex', justifyContent: 'space-between', padding: '2px 0', borderBottom: '1px dashed var(--astr-border, #F1F5F9)' }}>
                  <span style={{ color: '#64748B' }}>处理器</span>
                  <span style={{ fontWeight: 600, color: '#1E293B', maxWidth: 130, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={node.cpu}>
                    {node.cpu}
                  </span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', padding: '2px 0', borderBottom: '1px dashed var(--astr-border, #F1F5F9)' }}>
                  <span style={{ color: '#64748B' }}>系统负载</span>
                  <span style={{ fontWeight: 700, fontFamily: 'monospace', color: '#5B5BD6' }}>
                    {node.load}
                  </span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', padding: '2px 0', borderBottom: '1px dashed var(--astr-border, #F1F5F9)' }}>
                  <span style={{ color: '#64748B' }}>可用内存</span>
                  <span style={{ fontWeight: 600, color: '#059669', fontFamily: 'monospace' }}>
                    {node.mem_available}
                  </span>
                </div>
                <div style={{ display: 'flex', justifyContent: 'space-between', padding: '2px 0', borderBottom: '1px dashed var(--astr-border, #F1F5F9)' }}>
                  <span style={{ color: '#64748B' }}>磁盘剩余</span>
                  <span style={{ fontWeight: 600, fontFamily: 'monospace' }}>
                    {node.disk_root_free}
                  </span>
                </div>
                {node.top_process && (
                  <div style={{ display: 'flex', justifyContent: 'space-between', padding: '2px 0' }}>
                    <span style={{ color: '#64748B' }}>TOP 进程</span>
                    <span style={{ fontWeight: 500, color: '#D97706', fontSize: 10.5 }}>
                      {node.top_process}
                    </span>
                  </div>
                )}
              </div>

              {node.uptime && (
                <div style={{ fontSize: 10, color: '#94A3B8', marginTop: 2 }}>
                  ⏱️ 运行时长: {node.uptime}
                </div>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}


/**
 * 12. 作战任务契约与指挥规格卡 (MissionSpecCard)
 */
function MissionSpecCardRenderer({ element }: { element: any }) {
  const { mission, phase, targets = [], commander } = element.props || {}

  return (
    <div
      style={{
        borderRadius: 8,
        background: 'linear-gradient(135deg, rgba(91, 91, 214, 0.04) 0%, rgba(56, 189, 248, 0.04) 100%)',
        border: '1.5px solid rgba(91, 91, 214, 0.25)',
        padding: '12px 14px',
        display: 'flex',
        flexDirection: 'column',
        gap: 10,
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <Group gap="xs">
          <Badge variant="gradient" gradient={{ from: 'indigo', to: 'cyan' }} size="sm">
            🎯 作战任务指令
          </Badge>
          <Text fw={700} size="sm" c="#1E293B">
            {mission || '多 Agent 协同任务'}
          </Text>
        </Group>
        {phase && (
          <Badge size="xs" variant="light" color="blue">
            {phase}
          </Badge>
        )}
      </div>

      {/* 指派协同目标环境 */}
      {targets.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          <div style={{ fontSize: 11, fontWeight: 600, color: '#64748B' }}>协同派生目标 (Targets):</div>
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {targets.map((t: any, idx: number) => (
              <div
                key={idx}
                style={{
                  padding: '4px 8px',
                  borderRadius: 6,
                  background: 'var(--astr-surface, #ffffff)',
                  border: '1px solid var(--astr-border, #E5E7EB)',
                  fontSize: 11,
                  display: 'flex',
                  alignItems: 'center',
                  gap: 6,
                }}
              >
                <span style={{ fontWeight: 700, color: '#5B5BD6' }}>{t.machine}</span>
                <Badge size="xs" variant="light" color="gray">{t.agent_kind || 'agent'}</Badge>
                {t.machine_id && (
                  <span style={{ fontSize: 9.5, color: '#94A3B8', fontFamily: 'monospace' }}>
                    {t.machine_id.slice(0, 8)}...
                  </span>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {commander && (
        <div style={{ fontSize: 10.5, color: '#94A3B8', display: 'flex', gap: 6 }}>
          <span>主星调度中枢:</span>
          <span style={{ fontFamily: 'monospace', color: '#5B5BD6' }}>{commander}</span>
        </div>
      )}
    </div>
  )
}

export const blackboardComponentRegistry: Record<string, React.ComponentType<any>> = {
  GomokuBoard: GomokuBoardRenderer,
  StatusCard: StatusCardRenderer,
  MetricGrid: MetricGridRenderer,
  StepTimeline: StepTimelineRenderer,
  DataTable: DataTableRenderer,
  DiffViewer: DiffViewerRenderer,
  Checklist: ChecklistRenderer,
  TerminalLog: TerminalLogRenderer,
  ApiEndpointsCard: ApiEndpointsCardRenderer,
  ResourceUsageBar: ResourceUsageBarRenderer,
  TestReport: TestReportRenderer,
  CveSecurityReport: CveSecurityReportRenderer,
  GitCommitLog: GitCommitLogRenderer,
  ArchitectureFlow: ArchitectureFlowRenderer,
  HostNodeTelemetryCard: HostNodeTelemetryCardRenderer,
  MultiNodeClusterSummary: MultiNodeClusterSummaryRenderer,
  MissionSpecCard: MissionSpecCardRenderer,
  KeyValueGrid: KeyValueGridRenderer,
}

/**
 * 智能启发式将任意 Blackboard JSON 映射为最高生产力的 UI Spec
 */
export function autoTransformBlackboardToSpec(key: string, rawVal: unknown): Spec | null {
  let val = rawVal
  if (typeof val === 'string') {
    try {
      val = JSON.parse(val)
    } catch {
      val = rawVal
    }
  }

  // 1. 显式 json-render 契约；兼容 Agent 常用的 component 别名
  if (val && typeof val === 'object' && ('type' in val || 'component' in val)) {
    try {
      const raw = val as Record<string, unknown>
      const compName = ((raw.type || raw.component) as string)
      if (compName in blackboardComponentRegistry && !('props' in raw)) {
        const { component: _c, type: _t, ...restProps } = raw
        return nestedToFlat({ type: compName, props: restProps } as any)
      }
      if ('props' in raw) {
        const { component, ...spec } = raw
        return nestedToFlat({ ...spec, type: spec.type || component } as any)
      }
    } catch {
      return null
    }
  }

  if (!val || typeof val !== 'object') return null
  const obj = val as Record<string, any>

  // 2. 五子棋对弈彩蛋（包含 ascii_board 或 gomoku 关键字）
  if (
    key.toLowerCase().includes('gomoku') ||
    key.toLowerCase().includes('chessboard') ||
    'ascii_board' in obj ||
    ('winning_move' in obj && 'winner' in obj)
  ) {
    return nestedToFlat({
      type: 'GomokuBoard',
      props: {
        title: obj.title || key,
        status: obj.status || 'finished',
        winner: obj.winner || null,
        winning_move: obj.winning_move || null,
        total_moves: obj.total_moves || null,
        ascii_board: obj.ascii_board || null,
        meta: obj.meta || null,
      },
    })
  }

    // 2. 任务清单/验收检查项 (items / checklist / todos / tasks)
    if (Array.isArray(obj.items) || Array.isArray(obj.checklist) || Array.isArray(obj.todos) || Array.isArray(obj.tasks)) {
      const rawItems = obj.items || obj.checklist || obj.todos || obj.tasks
      // 如果数组元素有 title/status/done 特征，或者属于任务项，自适应转换为 StepTimeline 或 Checklist
      const isTaskLike = rawItems.some((it: any) => typeof it === 'object' && it && ('status' in it || 'done' in it || 'title' in it || 'task' in it))
      if (isTaskLike) {
        const steps = rawItems.map((s: any) => {
          if (typeof s === 'string') return { title: s, status: 'pending' }
          const title = s.title || s.name || s.task || s.label || '任务项'
          let status = s.status || (s.done ? 'completed' : 'pending')
          if (status === 'finished' || status === 'success' || status === 'done' || status === 'closed') status = 'completed'
          return {
            title,
            status,
            description: s.description || s.summary || s.desc,
          }
        })
        return nestedToFlat({
          type: 'StepTimeline',
          props: {
            title: obj.title || obj.task_name || obj.name || key,
            steps,
          },
        })
      }
    }
  if ('mission' in obj && ('targets' in obj || 'phase' in obj)) {
    return nestedToFlat({
      type: 'MissionSpecCard',
      props: {
        mission: obj.mission,
        phase: obj.phase,
        targets: obj.targets,
        commander: obj.commander,
      },
    })
  }

  // 4. 多节点集群性能横向对比看板 (cluster_nodes_perf_summary)
  if (key.includes('cluster_nodes_perf') || (('wsl' in obj || 'debian' in obj) && typeof obj.wsl === 'object')) {
    const nodes = Object.entries(obj).map(([name, data]) => ({
      name,
      ...(typeof data === 'object' && data ? data : {}),
    }))
    return nestedToFlat({
      type: 'MultiNodeClusterSummary',
      props: {
        title: obj.title || '跨环境集群节点性能横向对比',
        nodes,
      },
    })
  }

  // 5. 单个主机/节点硬件体检卡 (cpu, load, memory_available, os)
  if ('cpu' in obj && 'load' in obj && ('memory_available' in obj || 'os' in obj)) {
    return nestedToFlat({
      type: 'HostNodeTelemetryCard',
      props: {
        title: obj.title || key,
        os: obj.os,
        cpu: obj.cpu,
        load: obj.load,
        memory_available: obj.memory_available,
        uptime: obj.uptime,
        agent: obj.agent,
        status: obj.status || 'completed',
      },
    })
  }

  // 6. API 接口与契约清单 (endpoints / apis / routes)
  if (Array.isArray(obj.endpoints) || Array.isArray(obj.apis) || Array.isArray(obj.routes)) {
    return nestedToFlat({
      type: 'ApiEndpointsCard',
      props: {
        title: obj.title || key,
        endpoints: obj.endpoints || obj.apis || obj.routes,
        baseUrl: obj.baseUrl || obj.base_url,
      },
    })
  }

  // 4. 系统与集群资源仪表 (resources / cpu / memory / disk)
  if (Array.isArray(obj.resources) || ('cpu' in obj && 'memory' in obj)) {
    const resList = Array.isArray(obj.resources)
      ? obj.resources
      : [
          obj.cpu !== undefined ? { name: 'CPU 负载', percent: typeof obj.cpu === 'number' ? obj.cpu : obj.cpu?.percent, display: typeof obj.cpu === 'object' ? obj.cpu.display : undefined } : null,
          obj.memory !== undefined ? { name: '内存占用', percent: typeof obj.memory === 'number' ? obj.memory : obj.memory?.percent, used: obj.memory?.used, total: obj.memory?.total, unit: obj.memory?.unit || 'GB' } : null,
          obj.disk !== undefined ? { name: '磁盘存储', percent: typeof obj.disk === 'number' ? obj.disk : obj.disk?.percent, used: obj.disk?.used, total: obj.disk?.total, unit: obj.disk?.unit || 'GB' } : null,
        ].filter(Boolean)

    return nestedToFlat({
      type: 'ResourceUsageBar',
      props: { title: obj.title || key, resources: resList },
    })
  }

  // 5. 自动化测试报告 (passed / failed / suite / cases / test_results)
  if ('passed' in obj && 'failed' in obj) {
    return nestedToFlat({
      type: 'TestReport',
      props: {
        title: obj.title || key,
        passed: obj.passed,
        failed: obj.failed,
        skipped: obj.skipped || 0,
        duration: obj.duration || obj.time,
        suite: obj.suite || obj.test_suite,
        cases: obj.cases || obj.test_cases || [],
      },
    })
  }

  // 6. 安全合规与 CVE 漏洞报告 (vulnerabilities / cve / security / audit)
  if (Array.isArray(obj.vulnerabilities) || ('critical' in obj && 'high' in obj)) {
    return nestedToFlat({
      type: 'CveSecurityReport',
      props: {
        title: obj.title || key,
        critical: obj.critical || 0,
        high: obj.high || 0,
        medium: obj.medium || 0,
        low: obj.low || 0,
        vulnerabilities: obj.vulnerabilities || [],
      },
    })
  }

  // 7. Git 提交与版本发布日志 (commits / git_log / changelog)
  if (Array.isArray(obj.commits) || Array.isArray(obj.changelog) || Array.isArray(obj.git_log)) {
    return nestedToFlat({
      type: 'GitCommitLog',
      props: {
        title: obj.title || key,
        branch: obj.branch || 'main',
        commits: obj.commits || obj.changelog || obj.git_log,
      },
    })
  }

  // 8. 架构与调用链路流程图 (flow / nodes / architecture)
  if (Array.isArray(obj.nodes) && (Array.isArray(obj.edges) || key.toLowerCase().includes('flow') || key.toLowerCase().includes('arch'))) {
    return nestedToFlat({
      type: 'ArchitectureFlow',
      props: {
        title: obj.title || key,
        nodes: obj.nodes,
      },
    })
  }

  // 9. 步骤/流水线/时间线 (steps / pipeline / milestones / stages)
  if (Array.isArray(obj.steps) || Array.isArray(obj.pipeline) || Array.isArray(obj.stages) || Array.isArray(obj.milestones)) {
    const rawSteps = obj.steps || obj.pipeline || obj.stages || obj.milestones
    const steps = rawSteps.map((s: any) =>
      typeof s === 'string'
        ? { title: s, status: 'completed' }
        : {
            title: s.title || s.name || s.step || '阶段',
            status: s.status || (s.done ? 'completed' : 'pending'),
            description: s.description || s.summary || s.desc,
            time: s.time || s.duration,
          }
    )
    return nestedToFlat({
      type: 'StepTimeline',
      props: { title: obj.title || key, steps },
    })
  }

  // 10. 指标与统计面板 (metrics / stats / counters / kpi)
  if (Array.isArray(obj.metrics) || Array.isArray(obj.stats)) {
    const rawMetrics = obj.metrics || obj.stats
    return nestedToFlat({
      type: 'MetricGrid',
      props: { title: obj.title || key, metrics: rawMetrics },
    })
  }

  // 11. 结构化数据表格 (table / items / records / list)
  const arrayProp = Array.isArray(obj.rows)
    ? obj.rows
    : Array.isArray(obj.items)
    ? obj.items
    : Array.isArray(obj.records)
    ? obj.records
    : Array.isArray(obj.data)
    ? obj.data
    : null

  if (arrayProp && arrayProp.length > 0 && typeof arrayProp[0] === 'object') {
    const keys = Array.from(
      new Set(arrayProp.flatMap((item) => (typeof item === 'object' && item ? Object.keys(item) : [])))
    ).slice(0, 6)
    const columns = keys.map((k) => ({ key: k, label: k }))
    return nestedToFlat({
      type: 'DataTable',
      props: {
        title: obj.title || key,
        columns,
        rows: arrayProp.slice(0, 20),
        caption: arrayProp.length > 20 ? `展示前 20 项，共 ${arrayProp.length} 项` : undefined,
      },
    })
  }

  // 12. 代码/配置差异对比 (diff / patch)
  if (typeof obj.diff === 'string' || typeof obj.patch === 'string') {
    return nestedToFlat({
      type: 'DiffViewer',
      props: {
        file: obj.file || obj.filename || key,
        diff: obj.diff || obj.patch,
      },
    })
  }

  // 13. 清单与待办检查项 (checklist / todos / tasks)
  if (Array.isArray(obj.checklist) || Array.isArray(obj.todos) || Array.isArray(obj.tasks)) {
    const rawItems = obj.checklist || obj.todos || obj.tasks
    const items = rawItems.map((item: any) =>
      typeof item === 'string'
        ? { label: item, done: false }
        : {
            label: item.label || item.title || item.text,
            done: Boolean(item.done || item.completed || item.checked),
            assignee: item.assignee,
            priority: item.priority,
          }
    )
    return nestedToFlat({
      type: 'Checklist',
      props: { title: obj.title || key, items },
    })
  }

  // 14. 终端日志/输出 (logs / stdout / console)
  if (Array.isArray(obj.logs) || typeof obj.stdout === 'string' || Array.isArray(obj.output)) {
    return nestedToFlat({
      type: 'TerminalLog',
      props: {
        title: obj.title || key,
        lines: obj.logs || obj.stdout || obj.output,
        status: obj.status,
      },
    })
  }

  // 15. 状态与告警卡片 (具备 status / state / error / result 属性)
  if (obj.status || obj.state || obj.result || obj.message) {
    const rawStatus = (obj.status || obj.state || (obj.error ? 'error' : 'info')) as string
    const status =
      ['success', 'ok', 'completed', 'finished'].includes(rawStatus.toLowerCase())
        ? 'success'
        : ['running', 'in_progress', 'active'].includes(rawStatus.toLowerCase())
        ? 'running'
        : ['ready', 'idle'].includes(rawStatus.toLowerCase())
        ? 'ready'
        : ['warning', 'blocked', 'waiting_approval'].includes(rawStatus.toLowerCase())
        ? 'warning'
        : ['error', 'failed'].includes(rawStatus.toLowerCase())
        ? 'error'
        : 'info'

    const { status: _s, state: _st, title: _t, summary: _sm, message: _m, badge: _b, ...rest } = obj
    return nestedToFlat({
      type: 'StatusCard',
      props: {
        title: obj.title || key,
        status,
        summary: obj.summary || obj.message || (typeof obj.result === 'string' ? obj.result : undefined),
        badge: obj.badge || rawStatus,
        timestamp: obj.timestamp || obj.time,
        details: Object.keys(rest).length > 0 ? rest : undefined,
      },
    })
  }

  // 16. 通用配置对象 -> 键值 Bento 网格
  const entries = Object.entries(obj).map(([k, v]) => ({
    key: k,
    value: typeof v === 'object' ? JSON.stringify(v) : v,
  }))
  if (entries.length > 0) {
    return nestedToFlat({
      type: 'KeyValueGrid',
      props: { entries },
    })
  }

  return null
}

export function JsonRenderView({ itemKey, itemValue }: { itemKey: string; itemValue: unknown }) {
  const spec = useMemo(() => autoTransformBlackboardToSpec(itemKey, itemValue), [itemKey, itemValue])
  if (!spec) return null
  return (
    <JSONUIProvider registry={blackboardComponentRegistry}>
      <Renderer spec={spec} registry={blackboardComponentRegistry} />
    </JSONUIProvider>
  )
}

/**
 * 独立的黑板卡片渲染容器：支持在 [🎨 UI 视图] 与 [{ } JSON 原始码] 之间自由切换
 */
export function BlackboardItemRenderer({
  itemKey,
  itemValue,
  namespace = 'global',
  onUpdated,
  onDelete,
}: {
  itemKey: string
  itemValue: unknown
  namespace?: string
  onUpdated?: () => void
  onDelete?: () => void
}) {
  const spec = useMemo(() => autoTransformBlackboardToSpec(itemKey, itemValue), [itemKey, itemValue])
  const [viewMode, setViewMode] = useState<'ui' | 'json'>(spec ? 'ui' : 'json')
  const [editOpened, setEditOpened] = useState(false)
  const [editText, setEditText] = useState('')
  const [editError, setEditError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    const onAction = async (e: any) => {
      const action = e.detail?.action
      const payload = e.detail?.payload
      if (!action || !payload || typeof itemValue !== 'object' || itemValue === null) return

      try {
        if (action === 'gomoku_move') {
          const current = { ...(itemValue as any) }
          const { move, isBlack } = payload
          const char = isBlack ? 'X' : 'O'
          const rows = (current.ascii_board || '').trim().split('\n')
          const match = move.match(/([A-O])(\d+)/i)
          if (match && rows.length >= 10) {
            const colIdx = 'ABCDEFGHIJKLMNO'.indexOf(match[1].toUpperCase())
            const rowIdx = parseInt(match[2], 10)
            for (let i = 0; i < rows.length; i++) {
              const m = rows[i].trim().match(/^(\d+)\s+(.*)$/)
              if (m && parseInt(m[1], 10) === rowIdx) {
                const cells = m[2].trim().split(/\s+/)
                if (colIdx >= 0 && colIdx < cells.length) {
                  cells[colIdx] = char
                  rows[i] = m[1].padEnd(2, ' ') + ' ' + cells.join(' ')
                  break
                }
              }
            }
            current.ascii_board = rows.join('\n')
          }
          current.winning_move = move
          current.total_moves = (current.total_moves || 0) + 1
          current.status = 'playing'
          if (!current.meta) current.meta = {}
          current.meta.last_move = (isBlack ? '黑子 (X)' : '白子 (O)') + ' 落于 ' + move
          current.meta.current_turn = isBlack ? 'White (O)' : 'Black (X)'

          await api.setBlackboard(itemKey, current, namespace)
          notifications.show({ color: 'teal', message: '已落子 ' + move + ' 并同步至黑板' })
          onUpdated?.()
        } else if (action === 'timeline_step_toggle') {
          const current = { ...(itemValue as any) }
          const { stepIndex, status } = payload
          if (Array.isArray(current.steps) && current.steps[stepIndex]) {
            current.steps[stepIndex] = { ...current.steps[stepIndex], status }
            await api.setBlackboard(itemKey, current, namespace)
            notifications.show({
              color: 'indigo',
              message: '步骤「' + (current.steps[stepIndex].title || stepIndex) + '」状态已变更为 ' + status,
            })
            onUpdated?.()
          }
        }
      } catch (err: any) {
        notifications.show({ color: 'red', message: '操作失败: ' + (err.message || '未知错误') })
      }
    }

    window.addEventListener('blackboard-action', onAction)
    return () => window.removeEventListener('blackboard-action', onAction)
  }, [itemKey, itemValue, namespace, onUpdated])

  const jsonString = useMemo(() => {
    if (typeof itemValue === 'string') {
      try {
        return JSON.stringify(JSON.parse(itemValue), null, 2)
      } catch {
        return itemValue
      }
    }
    return JSON.stringify(itemValue, null, 2)
  }, [itemValue])

  const handleOpenEdit = () => {
    setEditText(jsonString)
    setEditError(null)
    setEditOpened(true)
  }

  const handleSave = async () => {
    let parsed: any = editText
    try {
      parsed = JSON.parse(editText)
    } catch (e: any) {
      setEditError('JSON 语法无效: ' + e.message)
      return
    }
    setSaving(true)
    try {
      await api.setBlackboard(itemKey, parsed, namespace)
      notifications.show({
        color: 'teal',
        message: `黑板变量「${itemKey}」已更新并重新渲染`,
      })
      setEditOpened(false)
      onUpdated?.()
    } catch (e: any) {
      setEditError('写回黑板失败: ' + (e.message || '未知错误'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <div
      style={{
        borderRadius: 8,
        border: '1px solid var(--astr-border, #E5E7EB)',
        background: 'var(--astr-surface, #ffffff)',
        overflow: 'hidden',
        boxShadow: '0 1px 3px rgba(0,0,0,0.03)',
      }}
    >
      <div
        style={{
          padding: '6px 10px',
          borderBottom: '1px solid var(--astr-border, #E5E7EB)',
          background: 'var(--astr-surface-muted, #F8FAFC)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
        }}
      >
        <span
          style={{
            fontSize: 12,
            fontWeight: 700,
            color: '#5B5BD6',
            fontFamily: 'monospace',
          }}
        >
          {itemKey}
        </span>

        <Group gap={6}>
          {spec && (
            <SegmentedControl
              size="xs"
              value={viewMode}
              onChange={(val) => setViewMode(val as 'ui' | 'json')}
              data={[
                { label: '🎨 UI', value: 'ui' },
                { label: '{ } JSON', value: 'json' },
              ]}
              style={{ fontSize: 11 }}
            />
          )}

          {/* 编辑修改并写回黑板按钮 */}
          <Tooltip label="编辑并写回黑板" withArrow>
            <ActionIcon
              size="xs"
              variant="subtle"
              color="indigo"
              onClick={handleOpenEdit}
              aria-label={`编辑 ${itemKey}`}
            >
              <IconEdit size={13} />
            </ActionIcon>
          </Tooltip>

          <CopyButton value={jsonString}>
            {({ copied, copy }) => (
              <Button
                size="compact-xs"
                variant="subtle"
                color={copied ? 'teal' : 'gray'}
                onClick={copy}
                leftSection={<IconCopy size={11} />}
              >
                {copied ? '已复制' : '复制'}
              </Button>
            )}
          </CopyButton>
          {onDelete && (
            <Tooltip label="删除" withArrow>
              <ActionIcon size="xs" variant="subtle" color="red" onClick={onDelete} aria-label={`删除 ${itemKey}`}>
                <IconTrash size={13} />
              </ActionIcon>
            </Tooltip>
          )}
        </Group>
      </div>

      <div style={{ padding: '10px 12px' }}>
        {viewMode === 'ui' && spec ? (
          <JSONUIProvider registry={blackboardComponentRegistry}>
            <Renderer spec={spec} registry={blackboardComponentRegistry} />
          </JSONUIProvider>
        ) : (
          <pre
            style={{
              margin: 0,
              padding: '8px 10px',
              fontSize: 11,
              fontFamily: 'Consolas, Monaco, "Courier New", monospace',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-word',
              borderRadius: 6,
              background: 'var(--astr-surface-muted, #F8FAFC)',
              border: '1px solid var(--astr-border, #E5E7EB)',
              color: 'var(--astr-text, #1e293b)',
              maxHeight: 260,
              overflowY: 'auto',
              lineHeight: 1.4,
            }}
          >
            {jsonString}
          </pre>
        )}
      </div>

      {/* 编辑黑板变量写回弹窗 */}
      <Modal
        opened={editOpened}
        onClose={() => !saving && setEditOpened(false)}
        title={
          <Group gap="xs">
            <IconEdit size={18} color="#5B5BD6" />
            <Text fw={700} size="md">
              编辑黑板变量: <code style={{ fontFamily: 'monospace', color: '#5B5BD6' }}>{itemKey}</code>
            </Text>
          </Group>
        }
        size="lg"
        radius="lg"
        centered
      >
        <Stack gap="sm">
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 11, color: '#64748B' }}>
            <span>所属空间: <code style={{ fontFamily: 'monospace' }}>{namespace}</code></span>
            <span>修改后将即时同步并重新渲染 UI</span>
          </div>

          <Textarea
            value={editText}
            onChange={(e) => {
              setEditText(e.currentTarget.value)
              if (editError) setEditError(null)
            }}
            minRows={10}
            maxRows={22}
            autosize
            styles={{
              input: {
                fontFamily: 'Consolas, Monaco, monospace',
                fontSize: 12,
                lineHeight: 1.45,
                background: '#0F172A',
                color: '#E2E8F0',
                border: '1px solid #1E293B',
              },
            }}
          />

          {editError && (
            <Text size="xs" c="red" fw={500}>
              {editError}
            </Text>
          )}

          <Group justify="flex-end" gap="sm" mt="xs">
            <Button variant="default" onClick={() => setEditOpened(false)} disabled={saving}>
              取消
            </Button>
            <Button
              color="indigo"
              leftSection={<IconDeviceFloppy size={16} />}
              onClick={handleSave}
              loading={saving}
            >
              保存并写回黑板
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
