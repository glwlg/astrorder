import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import {
  IconCamera,
  IconExternalLink,
  IconGitBranch,
  IconGitCompare,
  IconHistory,
  IconLoader2,
  IconRefresh,
  IconRotate2,
  IconTrash,
} from '@tabler/icons-react'
import { ApiError, api, type SessionCheckpoint } from '../../api/client'
import type { Session } from '../../domain/types'
import { checkpointsQueryKey, useSessionCheckpoints } from '../../hooks/useSessionCheckpoints'
import './sessionCheckpoints.css'

const KIND_LABELS: Record<string, string> = {
  auto_after: '轮次快照',
  auto_before: '轮前基线',
  manual: '手动快照',
  pre_restore: '回滚前备份',
}

function errorDetail(err: unknown, fallback: string): string {
  if (err instanceof ApiError) return err.detail || fallback
  if (err instanceof Error && err.message) return err.message
  return fallback
}

function formatRelativeTime(iso: string): string {
  const ts = Date.parse(iso)
  if (!Number.isFinite(ts)) return iso
  const diffSec = Math.max(0, Math.floor((Date.now() - ts) / 1000))
  if (diffSec < 45) return '刚刚'
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)} 分钟前`
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)} 小时前`
  const date = new Date(ts)
  const mm = String(date.getMonth() + 1).padStart(2, '0')
  const dd = String(date.getDate()).padStart(2, '0')
  const hh = String(date.getHours()).padStart(2, '0')
  const min = String(date.getMinutes()).padStart(2, '0')
  return `${mm}-${dd} ${hh}:${min}`
}

function classifyDiffLine(line: string): string {
  if (line.startsWith('diff --git ') || line.startsWith('--- ') || line.startsWith('+++ ') || line.startsWith('index ')) {
    return 'is-header'
  }
  if (line.startsWith('@@')) return 'is-hunk'
  if (line.startsWith('+')) return 'is-add'
  if (line.startsWith('-')) return 'is-del'
  return ''
}

const VIRTUAL_DIFF_THRESHOLD = 300
const VIRTUAL_DIFF_LINE_HEIGHT = 18
const VIRTUAL_DIFF_OVERSCAN = 36
const VIRTUAL_DIFF_FALLBACK_VIEWPORT = 360

function VirtualCheckpointDiffPre({ lines }: { lines: readonly string[] }) {
  const preRef = useRef<HTMLPreElement>(null)
  const scrollRafRef = useRef<number | null>(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [viewportHeight, setViewportHeight] = useState(VIRTUAL_DIFF_FALLBACK_VIEWPORT)

  useEffect(() => {
    setScrollTop(0)
    if (preRef.current) {
      preRef.current.scrollTop = 0
      const measured = preRef.current.clientHeight
      if (measured > 0) setViewportHeight(measured)
    }
  }, [lines])

  useEffect(() => () => {
    if (scrollRafRef.current !== null) window.cancelAnimationFrame(scrollRafRef.current)
  }, [])

  const handleScroll = useCallback(() => {
    if (scrollRafRef.current !== null) return
    const sync = () => {
      scrollRafRef.current = null
      const el = preRef.current
      if (!el) return
      setScrollTop(el.scrollTop)
      if (el.clientHeight > 0 && el.clientHeight !== viewportHeight) {
        setViewportHeight(el.clientHeight)
      }
    }
    if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
      scrollRafRef.current = window.requestAnimationFrame(sync)
    } else {
      sync()
    }
  }, [viewportHeight])

  if (lines.length <= VIRTUAL_DIFF_THRESHOLD) {
    return (
      <pre className="cp-diff-pre" aria-label="代码差异内容">
        {lines.map((line, idx) => (
          <span key={idx} className={`cp-diff-line ${classifyDiffLine(line)}`}>
            {line || ' '}
          </span>
        ))}
      </pre>
    )
  }

  const visibleCount = Math.max(20, Math.ceil(viewportHeight / VIRTUAL_DIFF_LINE_HEIGHT))
  const startIdx = Math.max(0, Math.floor(scrollTop / VIRTUAL_DIFF_LINE_HEIGHT) - VIRTUAL_DIFF_OVERSCAN)
  const endIdx = Math.min(lines.length, startIdx + visibleCount + VIRTUAL_DIFF_OVERSCAN * 2)
  const totalHeight = lines.length * VIRTUAL_DIFF_LINE_HEIGHT
  const offsetY = startIdx * VIRTUAL_DIFF_LINE_HEIGHT

  return (
    <pre
      ref={preRef}
      className="cp-diff-pre is-virtual"
      aria-label="代码差异内容"
      data-total-lines={lines.length}
      onScroll={handleScroll}
    >
      <div className="cp-diff-virtual-spacer" style={{ height: `${totalHeight}px` }}>
        <div
          className="cp-diff-virtual-window"
          style={{ transform: `translateY(${offsetY}px)` }}
        >
          {lines.slice(startIdx, endIdx).map((line, i) => {
            const idx = startIdx + i
            return (
              <span key={idx} className={`cp-diff-line ${classifyDiffLine(line)}`}>
                {line || ' '}
              </span>
            )
          })}
        </div>
      </div>
    </pre>
  )
}

type DiffCompareTarget = 'turn' | 'working' | string

export function SessionCheckpointsPanel({
  session,
  compact = false,
  initialCheckpointId = null,
  onOpenFile,
}: {
  session: Session
  compact?: boolean
  initialCheckpointId?: string | null
  onOpenFile?: (relativePath: string) => void
}) {
  const queryClient = useQueryClient()
  const { items, isPending, isFetching, error, refetch, hasWorkspace, pluginEnabled, maxAutoSnapshots } = useSessionCheckpoints(session, true)
  const [labelInput, setLabelInput] = useState('')
  const [capturing, setCapturing] = useState(false)
  const [activeId, setActiveId] = useState<string | null>(initialCheckpointId)
  const [compareMode, setCompareMode] = useState<DiffCompareTarget>('working')
  const [selectedFile, setSelectedFile] = useState<string | null>(null)
  const [restoreConfirmId, setRestoreConfirmId] = useState<string | null>(null)
  const [restoreFileConfirm, setRestoreFileConfirm] = useState<{ cpId: string; path: string } | null>(null)
  const [restoringId, setRestoringId] = useState<string | null>(null)
  const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  useEffect(() => {
    if (!initialCheckpointId) return
    const target = items.find((item) => item.id === initialCheckpointId)
    setActiveId(initialCheckpointId)
    setCompareMode(target && target.kind !== 'auto_before' && target.prev_tree_sha ? 'turn' : 'working')
    setSelectedFile(null)
  }, [initialCheckpointId, items])

  const activeCheckpoint = useMemo(
    () => items.find((item) => item.id === activeId) ?? null,
    [activeId, items],
  )

  const diffRange = useMemo(() => {
    if (!activeCheckpoint) return null
    if (compareMode === 'turn' && activeCheckpoint.kind !== 'auto_before' && activeCheckpoint.prev_tree_sha) {
      return { from: activeCheckpoint.prev_tree_sha, to: activeCheckpoint.tree_sha }
    }
    if (compareMode === 'working') {
      return { from: activeCheckpoint.tree_sha, to: 'working' }
    }
    const peer = items.find((item) => item.id === compareMode)
    if (peer) {
      return { from: peer.tree_sha, to: activeCheckpoint.tree_sha }
    }
    return { from: activeCheckpoint.tree_sha, to: 'working' }
  }, [activeCheckpoint, compareMode, items])

  const diffQuery = useQuery({
    queryKey: [
      'astrorder',
      'checkpoint-diff',
      session.agent_id,
      session.id,
      diffRange?.from ?? '',
      diffRange?.to ?? '',
      selectedFile ?? '',
    ],
    queryFn: () =>
      api.diffCheckpoints(session.id, {
        agent_id: session.agent_id,
        from: diffRange!.from,
        to: diffRange!.to,
        path: selectedFile ?? undefined,
      }),
    enabled: Boolean(activeCheckpoint && diffRange),
    staleTime: 10_000,
    retry: false,
  })

  const diffLines = useMemo(
    () => (diffQuery.data?.diff ? diffQuery.data.diff.split('\n') : []),
    [diffQuery.data?.diff],
  )

  const invalidate = async (includeGit = false) => {
    await queryClient.invalidateQueries({ queryKey: checkpointsQueryKey(session.agent_id, session.id) })
    if (includeGit) {
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'git'] })
    }
  }

  const handleCapture = async () => {
    if (capturing) return
    setCapturing(true)
    try {
      const res = await api.createCheckpoint(session.id, {
        agent_id: session.agent_id,
        label: labelInput.trim() || undefined,
        kind: 'manual',
      })
      setLabelInput('')
      await invalidate(false)
      notifications.show({
        color: 'teal',
        message: `已保存检查点 #${res.checkpoint.turn_number}`,
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: errorDetail(err, '保存检查点失败'),
      })
    } finally {
      setCapturing(false)
    }
  }

  const openDiff = (cp: SessionCheckpoint, mode: DiffCompareTarget) => {
    if (activeId === cp.id && compareMode === mode) {
      setActiveId(null)
      setSelectedFile(null)
      return
    }
    setActiveId(cp.id)
    setCompareMode(mode)
    setSelectedFile(null)
  }

  const handleRestore = async (cp: SessionCheckpoint, filePath?: string) => {
    if (restoringId) return
    setRestoringId(cp.id)
    try {
      const res = await api.restoreCheckpoint(session.id, cp.id, session.agent_id, filePath)
      setRestoreConfirmId(null)
      setRestoreFileConfirm(null)
      await invalidate(true)
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'checkpoint-diff', session.agent_id, session.id] })
      const targetText = filePath ? `单文件 ${filePath} 至 #${cp.turn_number}` : `#${cp.turn_number}`
      notifications.show({
        color: 'teal',
        message: res.backup
          ? `已恢复 ${targetText}（当前工作区已自动备份为 #${res.backup.turn_number}）`
          : `已恢复 ${targetText}`,
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: errorDetail(err, filePath ? '单文件恢复失败' : '回滚检查点失败'),
      })
    } finally {
      setRestoringId(null)
    }
  }

  const handleDelete = async (cp: SessionCheckpoint) => {
    if (deletingId) return
    setDeletingId(cp.id)
    try {
      await api.deleteCheckpoint(session.id, cp.id, session.agent_id)
      if (activeId === cp.id) {
        setActiveId(null)
        setSelectedFile(null)
      }
      setDeleteConfirmId(null)
      await invalidate(false)
      notifications.show({
        color: 'gray',
        message: `已删除检查点 #${cp.turn_number}`,
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: errorDetail(err, '删除检查点失败'),
      })
    } finally {
      setDeletingId(null)
    }
  }

  if (!pluginEnabled) {
    return (
      <div className={`cp-panel${compact ? ' is-compact' : ''}`}>
        <div className="cp-empty">
          <IconHistory size={24} />
          <div>「代码检查点」插件已停用。如需自动记录轮次快照与安全回滚，请在「设置 → 插件」中重新启用。</div>
          <button
            type="button"
            className="cp-btn is-sm is-primary"
            onClick={() => window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: 'plugins' } }))}
          >
            前往插件管理
          </button>
        </div>
      </div>
    )
  }

  if (!hasWorkspace) {
    return (
      <div className={`cp-panel${compact ? ' is-compact' : ''}`}>
        <div className="cp-empty">
          <IconHistory size={24} />
          <div>当前会话未关联本地或远程工作区目录，无法记录代码检查点。</div>
        </div>
      </div>
    )
  }

  const latestBranch = items[0]?.branch

  return (
    <div className={`cp-panel${compact ? ' is-compact' : ''}`}>
      <div className="cp-toolbar">
        <div className="cp-toolbar-meta">
          <span className="cp-summary">
            <IconHistory size={15} />
            <span>{items.length > 0 ? `共 ${items.length} 个检查点 · 自动保留 ${maxAutoSnapshots} 个` : `按轮次自动保留最新 ${maxAutoSnapshots} 个快照`}</span>
          </span>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            {latestBranch && (
              <span className="cp-branch-pill" title={latestBranch}>
                <IconGitBranch size={12} />
                <span>{latestBranch}</span>
              </span>
            )}
            <button
              type="button"
              className="cp-btn is-sm is-icon"
              aria-label="刷新检查点"
              disabled={isFetching}
              onClick={() => void refetch()}
            >
              <IconRefresh size={14} className={isFetching ? 'm-spin' : undefined} />
            </button>
          </div>
        </div>
        <form
          className="cp-capture-row"
          onSubmit={(event) => {
            event.preventDefault()
            void handleCapture()
          }}
        >
          <input
            type="text"
            className="cp-capture-input"
            aria-label="检查点备注"
            placeholder="可选备注，例如：重构登录逻辑前"
            value={labelInput}
            onChange={(event) => setLabelInput(event.target.value)}
            disabled={capturing}
          />
          <button
            type="submit"
            className="cp-btn is-primary"
            disabled={capturing}
          >
            {capturing ? <IconLoader2 size={14} className="m-spin" /> : <IconCamera size={14} />}
            <span>{capturing ? '保存中…' : '保存快照'}</span>
          </button>
        </form>
      </div>

      {isPending && (
        <div className="cp-empty">
          <IconLoader2 size={20} className="m-spin" />
          <div>正在读取检查点记录…</div>
        </div>
      )}

      {!isPending && error && (
        <div className="cp-error" role="alert">
          <div>{errorDetail(error, '读取检查点失败')}</div>
          <button type="button" className="cp-btn is-sm" onClick={() => void refetch()}>
            重试
          </button>
        </div>
      )}

      {!isPending && !error && items.length === 0 && (
        <div className="cp-empty">
          <IconHistory size={24} />
          <div>
            尚无检查点。每轮对话自动记录零污染 Git 树快照（不改动暂存区与 <code>git stash</code>），你也可以随时点击上方「保存快照」手动留档。
          </div>
        </div>
      )}

      {!isPending && !error && items.length > 0 && (
        <div className="cp-timeline" role="list" aria-label="代码检查点时间线">
          {items.map((cp) => {
            const isSelected = activeId === cp.id
            const isConfirmingRestore = restoreConfirmId === cp.id
            const isConfirmingDelete = deleteConfirmId === cp.id
            const kindLabel = KIND_LABELS[cp.kind] || '检查点'
            const isBaseline = cp.kind === 'auto_before'
            const hasTurnDiff = !isBaseline && Boolean(cp.prev_tree_sha)
            const otherCheckpoints = items.filter((item) => item.id !== cp.id)
            return (
              <article
                key={cp.id}
                role="listitem"
                className={`cp-card${isSelected ? ' is-selected' : ''}`}
              >
                <div className="cp-card-head">
                  <div className="cp-card-title-row">
                    <span className="cp-turn-tag">#{cp.turn_number}</span>
                    <span className={`cp-kind-badge is-${cp.kind}`}>{kindLabel}</span>
                  </div>
                  <span className="cp-time" title={cp.created_at}>
                    {formatRelativeTime(cp.created_at)}
                  </span>
                </div>

                <div className="cp-label" title={cp.label}>
                  {cp.label || '工作区检查点'}
                </div>

                <div className="cp-meta-row">
                  {cp.branch && <span>{cp.branch}</span>}
                  {isBaseline ? (
                    <span>本轮开始前工作区基线（可供安全回滚）</span>
                  ) : cp.files_changed > 0 ? (
                    <>
                      <span>{cp.files_changed} 个文件变化</span>
                      {cp.insertions > 0 && <span className="cp-stat-add">+{cp.insertions}</span>}
                      {cp.deletions > 0 && <span className="cp-stat-del">-{cp.deletions}</span>}
                    </>
                  ) : (
                    <span>与上一快照无文件差异</span>
                  )}
                </div>

                <div className="cp-actions">
                  {hasTurnDiff && (
                    <button
                      type="button"
                      className={`cp-btn is-sm${isSelected && compareMode === 'turn' ? ' is-active' : ''}`}
                      onClick={() => openDiff(cp, 'turn')}
                    >
                      <IconGitCompare size={13} />
                      <span>本轮变更</span>
                    </button>
                  )}
                  <button
                    type="button"
                    className={`cp-btn is-sm${isSelected && compareMode === 'working' ? ' is-active' : ''}`}
                    onClick={() => openDiff(cp, 'working')}
                  >
                    <IconGitCompare size={13} />
                    <span>对比当前</span>
                  </button>
                  <button
                    type="button"
                    className={`cp-btn is-sm is-warn${isConfirmingRestore ? ' is-active' : ''}`}
                    disabled={restoringId !== null}
                    onClick={() => {
                      setDeleteConfirmId(null)
                      setRestoreConfirmId(isConfirmingRestore ? null : cp.id)
                    }}
                  >
                    <IconRotate2 size={13} />
                    <span>回滚到此</span>
                  </button>
                  <span className="cp-actions-spacer" />
                  {isConfirmingDelete ? (
                    <>
                      <button
                        type="button"
                        className="cp-btn is-sm"
                        disabled={deletingId === cp.id}
                        onClick={() => setDeleteConfirmId(null)}
                      >
                        取消
                      </button>
                      <button
                        type="button"
                        className="cp-btn is-sm is-danger"
                        disabled={deletingId === cp.id}
                        onClick={() => void handleDelete(cp)}
                      >
                        {deletingId === cp.id ? '删除中…' : '确认删除'}
                      </button>
                    </>
                  ) : (
                    <button
                      type="button"
                      className="cp-btn is-sm is-icon"
                      aria-label={`删除检查点 #${cp.turn_number}`}
                      disabled={deletingId !== null}
                      onClick={() => {
                        setRestoreConfirmId(null)
                        setDeleteConfirmId(cp.id)
                      }}
                    >
                      <IconTrash size={13} />
                    </button>
                  )}
                </div>

                {isConfirmingRestore && (
                  <div className="cp-confirm-box" role="alertdialog" aria-label={`确认回滚到 #${cp.turn_number}`}>
                    <div className="cp-confirm-title">确认将工作区回滚至 #{cp.turn_number}？</div>
                    <div>
                      星序会先将当前工作区自动保存为「回滚前备份」检查点，可随时撤销回滚；被 <code>.gitignore</code> 忽略的文件（如 <code>.env</code>、<code>node_modules</code>）会完整保留。
                    </div>
                    <div className="cp-confirm-actions">
                      <button
                        type="button"
                        className="cp-btn is-sm"
                        disabled={restoringId === cp.id}
                        onClick={() => setRestoreConfirmId(null)}
                      >
                        取消
                      </button>
                      <button
                        type="button"
                        className="cp-btn is-sm is-warn"
                        disabled={restoringId === cp.id}
                        onClick={() => void handleRestore(cp)}
                      >
                        {restoringId === cp.id ? '正在回滚…' : '确认安全回滚'}
                      </button>
                    </div>
                  </div>
                )}

                {isSelected && (
                  <div className="cp-diff-box">
                    <div className="cp-diff-controls">
                      {hasTurnDiff && (
                        <button
                          type="button"
                          className={`cp-btn is-sm${compareMode === 'turn' ? ' is-active' : ''}`}
                          onClick={() => { setCompareMode('turn'); setSelectedFile(null) }}
                        >
                          本轮变更
                        </button>
                      )}
                      <button
                        type="button"
                        className={`cp-btn is-sm${compareMode === 'working' ? ' is-active' : ''}`}
                        onClick={() => { setCompareMode('working'); setSelectedFile(null) }}
                      >
                        与当前工作区对比
                      </button>
                      {otherCheckpoints.length > 0 && (
                        <select
                          aria-label="对比其他检查点"
                          className="cp-diff-select"
                          value={compareMode !== 'turn' && compareMode !== 'working' ? compareMode : ''}
                          onChange={(event) => {
                            const val = event.target.value
                            if (val) {
                              setCompareMode(val)
                              setSelectedFile(null)
                            }
                          }}
                        >
                          <option value="">对比其他检查点…</option>
                          {otherCheckpoints.map((other) => (
                            <option key={other.id} value={other.id}>
                              对比 #{other.turn_number} · {other.label.slice(0, 18)}
                            </option>
                          ))}
                        </select>
                      )}
                    </div>

                    {diffQuery.isPending && (
                      <div className="cp-diff-empty">正在计算差异…</div>
                    )}

                    {diffQuery.error && (
                      <div className="cp-error" role="alert">
                        {errorDetail(diffQuery.error, '差异计算失败')}
                      </div>
                    )}

                    {!diffQuery.isPending && !diffQuery.error && diffQuery.data && (
                      <>
                        {diffQuery.data.files.length > 0 && (
                          <div className="cp-file-chips">
                            <button
                              type="button"
                              className={`cp-file-chip${selectedFile === null ? ' is-active' : ''}`}
                              onClick={() => setSelectedFile(null)}
                            >
                              全部 ({diffQuery.data.files.length})
                            </button>
                            {diffQuery.data.files.map((file) => (
                              <span key={file.path} className="cp-file-chip-group">
                                <button
                                  type="button"
                                  className={`cp-file-chip${selectedFile === file.path ? ' is-active' : ''}`}
                                  title={file.path}
                                  onClick={() => setSelectedFile(selectedFile === file.path ? null : file.path)}
                                >
                                  <span className={`cp-file-status is-${file.status[0] || 'M'}`}>
                                    {file.status}
                                  </span>
                                  <span className="cp-file-path">{file.path}</span>
                                  {file.insertions > 0 && <span className="cp-stat-add">+{file.insertions}</span>}
                                  {file.deletions > 0 && <span className="cp-stat-del">-{file.deletions}</span>}
                                </button>
                                {onOpenFile && file.status !== 'D' && (
                                  <button
                                    type="button"
                                    className="cp-file-open-btn"
                                    aria-label={`在工作台打开 ${file.path}`}
                                    title={`在工作台打开 ${file.path}`}
                                    onClick={() => onOpenFile(file.path)}
                                  >
                                    <IconExternalLink size={12} />
                                  </button>
                                )}
                                <button
                                  type="button"
                                  className="cp-file-open-btn"
                                  aria-label={`仅恢复 ${file.path} 至 #${cp.turn_number}`}
                                  title={`仅恢复 ${file.path} 至 #${cp.turn_number}`}
                                  disabled={restoringId !== null}
                                  onClick={() => {
                                    setRestoreConfirmId(null)
                                    setRestoreFileConfirm(
                                      restoreFileConfirm?.cpId === cp.id && restoreFileConfirm.path === file.path
                                        ? null
                                        : { cpId: cp.id, path: file.path },
                                    )
                                  }}
                                >
                                  <IconRotate2 size={12} />
                                </button>
                              </span>
                            ))}
                          </div>
                        )}

                        {restoreFileConfirm?.cpId === cp.id && (
                          <div className="cp-confirm-box" role="alertdialog" aria-label={`确认恢复单文件 ${restoreFileConfirm.path}`}>
                            <div className="cp-confirm-title">
                              仅恢复单文件 <code>{restoreFileConfirm.path}</code> 至 #{cp.turn_number}？
                            </div>
                            <div>
                              其余工作区文件保持不变，且星序会先将当前完整工作区自动备份为「回滚前备份」检查点。
                            </div>
                            <div className="cp-confirm-actions">
                              <button
                                type="button"
                                className="cp-btn is-sm"
                                disabled={restoringId === cp.id}
                                onClick={() => setRestoreFileConfirm(null)}
                              >
                                取消
                              </button>
                              <button
                                type="button"
                                className="cp-btn is-sm is-warn"
                                disabled={restoringId === cp.id}
                                onClick={() => void handleRestore(cp, restoreFileConfirm.path)}
                              >
                                {restoringId === cp.id ? '正在恢复…' : '确认恢复单文件'}
                              </button>
                            </div>
                          </div>
                        )}

                        {diffQuery.data.truncated && (
                          <div className="cp-truncated-note">
                            差异内容较大，已截断显示；点击上方单个文件可查看精确改动。
                          </div>
                        )}

                        {diffLines.length > 0 ? (
                          <VirtualCheckpointDiffPre lines={diffLines} />
                        ) : (
                          <div className="cp-diff-empty">两个快照之间没有代码差异。</div>
                        )}
                      </>
                    )}
                  </div>
                )}
              </article>
            )
          })}
        </div>
      )}
    </div>
  )
}
