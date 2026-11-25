import { useState } from 'react'
import { notifications } from '@mantine/notifications'
import {
  IconAlertCircle,
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconCode,
  IconCopy,
  IconDatabase,
  IconFileSpreadsheet,
  IconLoader2,
  IconMessageForward,
  IconPlayerStop,
  IconSend,
  IconX,
} from '@tabler/icons-react'
import type { DbxExecuteResult, ProjectDatabaseTarget } from '../../api/client'
import type { Session } from '../../domain/types'
import { buildSheetsFromSqlResults, exportSheetsToXlsx } from '../office/export/exportService'
import { formatCellDisplay, formatSqlResultAsJson, formatSqlResultAsMarkdown } from './sqlSafety'
import { formatTargetLabel } from './projectDbStore'

export interface InlineSqlRunRecord {
  id: string
  sourceKind: 'snippet' | 'file'
  sourceLabel?: string
  sql: string
  target: ProjectDatabaseTarget
  running: boolean
  result?: DbxExecuteResult
  error?: string
  startedAt: number
}

export function InlineSqlResultCard({
  record,
  session,
  onCancel,
  onClose,
  onOpenInDbx,
}: {
  record: InlineSqlRunRecord
  session?: Session | null
  onCancel?: () => void
  onClose: () => void
  onOpenInDbx?: () => void
}) {
  const [collapsed, setCollapsed] = useState(false)
  const [activeStmtIdx, setActiveStmtIdx] = useState(0)
  const [copiedMode, setCopiedMode] = useState<'md' | 'json' | null>(null)
  const [sentNotice, setSentNotice] = useState(false)
  const [exportingXlsx, setExportingXlsx] = useState(false)

  const result = record.result
  const stmts = result?.results || []
  const safeIdx = Math.min(activeStmtIdx, Math.max(0, stmts.length - 1))
  const currentStmt = stmts[safeIdx]

  const isError = Boolean(record.error || (result && !result.ok))
  const targetDisplay = formatTargetLabel(record.target)
  const isProd = result ? result.is_prod : record.target.is_prod

  const buildMarkdown = () => {
    if (result) {
      return formatSqlResultAsMarkdown({
        result,
        sourceLabel: record.sourceLabel,
        sql: record.sql,
      })
    }
    const targetStr = `${targetDisplay}${isProd ? ' [PROD]' : ''}`
    return `SQL 执行失败（\`${targetStr}\`）${record.sourceLabel ? ` · 来源：\`${record.sourceLabel}\`` : ''}\n\n\`\`\`text\n${record.error || '未知错误'}\n\`\`\``
  }

  const handleCopy = async (mode: 'md' | 'json') => {
    try {
      const text = mode === 'json' && result ? formatSqlResultAsJson(result) : buildMarkdown()
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable')
      await navigator.clipboard.writeText(text)
      setCopiedMode(mode)
      window.setTimeout(() => setCopiedMode(null), 1600)
    } catch {
      notifications.show({ color: 'red', message: '无法写入剪贴板' })
    }
  }

  const handleQuoteToComposer = () => {
    if (!session) return
    window.dispatchEvent(
      new CustomEvent('astrorder:quote-to-composer', {
        detail: {
          agentId: session.agent_id,
          sessionId: session.id,
          text: buildMarkdown(),
        },
      }),
    )
    notifications.show({ color: 'teal', message: '已将 SQL 执行结果填入输入框' })
  }

  const handleSendToSession = () => {
    if (!session) return
    window.dispatchEvent(
      new CustomEvent('astrorder:send-to-composer', {
        detail: {
          agentId: session.agent_id,
          sessionId: session.id,
          text: buildMarkdown(),
        },
      }),
    )
    setSentNotice(true)
    window.setTimeout(() => setSentNotice(false), 2000)
  }

  const copyCellValue = (val: unknown) => {
    const text = val === null || val === undefined ? 'NULL' : typeof val === 'string' ? val : JSON.stringify(val)
    if (!navigator.clipboard?.writeText) return
    void navigator.clipboard.writeText(text).then(() => {
      notifications.show({ color: 'teal', message: '已复制单元格内容' })
    })
  }

  const totalRows = stmts.reduce((acc, s) => acc + (s.row_count || 0), 0)
  const hasExportableTable = stmts.some((s) => (s.columns?.length || 0) > 0)

  const handleExportExcel = async () => {
    if (!result || !hasExportableTable || exportingXlsx) return
    setExportingXlsx(true)
    try {
      const sheets = buildSheetsFromSqlResults(stmts, record.sourceLabel)
      const baseName = record.sourceLabel
        ? record.sourceLabel.replace(/\.sql$/i, '')
        : `${result.database || 'sql'}_result`
      await exportSheetsToXlsx({ sheets, fileName: `${baseName}.xlsx` })
      notifications.show({ color: 'teal', message: '已将 SQL 查询结果导出为 Excel (.xlsx)' })
    } catch (cause) {
      notifications.show({
        color: 'red',
        message: cause instanceof Error ? cause.message : '导出 Excel 失败',
      })
    } finally {
      setExportingXlsx(false)
    }
  }

  return (
    <div
      className={`inline-sql-result-card${collapsed ? ' is-collapsed' : ''}`}
      role="region"
      aria-label={record.sourceLabel ? `SQL 执行结果：${record.sourceLabel}` : 'SQL 执行结果'}
    >
      <div className="inline-sql-result-header">
        <div className="inline-sql-result-meta">
          {record.running ? (
            <span className="inline-sql-status-pill is-running">
              <IconLoader2 size={12} className="astrorder-spin" aria-hidden="true" />
              执行中…
            </span>
          ) : isError ? (
            <span className="inline-sql-status-pill is-error">
              <IconAlertCircle size={12} aria-hidden="true" />
              执行失败
            </span>
          ) : (
            <span className="inline-sql-status-pill is-ok">
              <IconCheck size={12} aria-hidden="true" />
              执行成功
            </span>
          )}
          {record.sourceLabel && <span className="inline-sql-source-tag">{record.sourceLabel}</span>}
          <span className="inline-sql-dim">{targetDisplay}</span>
          {isProd && <span className="inline-sql-prod-badge">PROD</span>}
          {result && !record.running && (
            <>
              <span className="inline-sql-dim">{result.total_elapsed_ms} ms</span>
              {stmts.length > 0 && stmts.some((s) => (s.columns?.length || 0) > 0) && (
                <span className="inline-sql-dim">
                  {totalRows} 行{result.truncated ? '（已截断）' : ''}
                </span>
              )}
            </>
          )}
        </div>
        <div className="inline-sql-footer-actions">
          {record.running && onCancel && (
            <button type="button" className="inline-sql-action-btn" onClick={onCancel}>
              <IconPlayerStop size={12} aria-hidden="true" />
              <span>取消</span>
            </button>
          )}
          <button
            type="button"
            className="inline-sql-action-btn"
            aria-label={collapsed ? '展开结果' : '收起结果'}
            onClick={() => setCollapsed((v) => !v)}
          >
            {collapsed ? <IconChevronDown size={12} aria-hidden="true" /> : <IconChevronUp size={12} aria-hidden="true" />}
            <span>{collapsed ? '展开' : '收起'}</span>
          </button>
          <button type="button" className="inline-sql-action-btn" aria-label="关闭执行结果" onClick={onClose}>
            <IconX size={12} aria-hidden="true" />
          </button>
        </div>
      </div>

      {!collapsed && (
        <>
          {record.running ? (
            <div className="inline-sql-empty-body">正在通过 DBX 执行查询，请稍候…</div>
          ) : (
            <>
              {stmts.length > 1 && (
                <div className="inline-sql-stmt-tabs" role="tablist" aria-label="多语句结果集">
                  {stmts.map((stmt, idx) => (
                    <button
                      key={stmt.index ?? idx}
                      type="button"
                      role="tab"
                      aria-selected={idx === safeIdx}
                      className={`inline-sql-stmt-tab${idx === safeIdx ? ' is-active' : ''}${stmt.error ? ' is-error' : ''}`}
                      onClick={() => setActiveStmtIdx(idx)}
                    >
                      <span>语句 {idx + 1}</span>
                      {stmt.error ? (
                        <span>· 失败</span>
                      ) : stmt.columns && stmt.columns.length > 0 ? (
                        <span>· {stmt.row_count} 行</span>
                      ) : typeof stmt.affected_rows === 'number' ? (
                        <span>· 影响 {stmt.affected_rows} 行</span>
                      ) : null}
                    </button>
                  ))}
                </div>
              )}

              {record.error ? (
                <div className="inline-sql-error-box">{record.error}</div>
              ) : currentStmt?.error ? (
                <div className="inline-sql-error-box">{currentStmt.error}</div>
              ) : !result?.ok && result?.error ? (
                <div className="inline-sql-error-box">{result.error}</div>
              ) : currentStmt && currentStmt.columns && currentStmt.columns.length > 0 ? (
                <div className="inline-sql-table-wrap">
                  <table className="inline-sql-table">
                    <thead>
                      <tr>
                        {currentStmt.columns.map((col, idx) => (
                          <th key={`${col.name}-${idx}`} title={col.data_type ? `${col.name} (${col.data_type})` : col.name}>
                            {col.name}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {(currentStmt.rows || []).length === 0 ? (
                        <tr>
                          <td colSpan={currentStmt.columns.length} className="inline-sql-null">
                            （0 行结果）
                          </td>
                        </tr>
                      ) : (
                        (currentStmt.rows || []).map((row, rowIdx) => (
                          <tr key={rowIdx}>
                            {currentStmt.columns!.map((_, colIdx) => {
                              const cell = row[colIdx]
                              const isNull = cell === null || cell === undefined
                              const display = formatCellDisplay(cell, 160)
                              return (
                                <td
                                  key={colIdx}
                                  className={isNull ? 'inline-sql-null' : undefined}
                                  title={`${display}\n（点击复制单元格）`}
                                  onClick={() => copyCellValue(cell)}
                                >
                                  {display}
                                </td>
                              )
                            })}
                          </tr>
                        ))
                      )}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div className="inline-sql-empty-body">
                  {typeof currentStmt?.affected_rows === 'number'
                    ? `执行成功，影响 ${currentStmt.affected_rows} 行。`
                    : currentStmt?.message || '执行成功，无返回结果集。'}
                </div>
              )}

              <div className="inline-sql-result-footer">
                <div className="inline-sql-footer-actions">
                  <button type="button" className="inline-sql-action-btn" onClick={() => void handleCopy('md')}>
                    {copiedMode === 'md' ? <IconCheck size={12} /> : <IconCopy size={12} />}
                    <span>{copiedMode === 'md' ? '已复制' : '复制 Markdown'}</span>
                  </button>
                  {result && stmts.length > 0 && (
                    <button type="button" className="inline-sql-action-btn" onClick={() => void handleCopy('json')}>
                      {copiedMode === 'json' ? <IconCheck size={12} /> : <IconCode size={12} />}
                      <span>{copiedMode === 'json' ? '已复制 JSON' : '复制 JSON'}</span>
                    </button>
                  )}
                  {result && hasExportableTable && (
                    <button
                      type="button"
                      className="inline-sql-action-btn"
                      disabled={exportingXlsx}
                      onClick={() => void handleExportExcel()}
                    >
                      {exportingXlsx ? <IconLoader2 size={12} className="astrorder-spin" /> : <IconFileSpreadsheet size={12} />}
                      <span>导出 Excel</span>
                    </button>
                  )}
                  {onOpenInDbx && (
                    <button type="button" className="inline-sql-action-btn" onClick={onOpenInDbx}>
                      <IconDatabase size={12} />
                      <span>在 DBX 打开</span>
                    </button>
                  )}
                </div>

                {session && (
                  <div className="inline-sql-footer-actions">
                    <button
                      type="button"
                      className="inline-sql-action-btn"
                      onClick={handleQuoteToComposer}
                      title="将结果追加到输入框，便于补充说明后再发送"
                    >
                      <IconMessageForward size={12} />
                      <span>填入输入框</span>
                    </button>
                    <button
                      type="button"
                      className="inline-sql-action-btn is-primary"
                      onClick={handleSendToSession}
                      title="直接将执行结果发送给当前会话"
                    >
                      {sentNotice ? <IconCheck size={12} /> : <IconSend size={12} />}
                      <span>{sentNotice ? '已发送' : '发送给当前会话'}</span>
                    </button>
                  </div>
                )}
              </div>
            </>
          )}
        </>
      )}
    </div>
  )
}
