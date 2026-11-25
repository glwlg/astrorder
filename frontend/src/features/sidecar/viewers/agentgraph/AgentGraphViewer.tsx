import { useContext, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { QueryClientContext } from '@tanstack/react-query'
import { Drawer, Group, Paper, Stack, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAlertTriangle,
  IconArrowDown,
  IconArrowUp,
  IconBulb,
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconChevronsDown,
  IconChevronsUp,
  IconCode,
  IconCopy,
  IconExternalLink,
  IconFileCode,
  IconFilePlus,
  IconFileText,
  IconFilter,
  IconGitCompare,
  IconHistory,
  IconMessageShare,
  IconSearch,
  IconShieldCheck,
  IconTerminal2,
  IconX,
} from '@tabler/icons-react'
import { useShallow } from 'zustand/react/shallow'
import type { ViewerContext } from '../../types'
import { selectMessages, selectTasks, useAstrorderStore } from '../../../../state/store'
import { scopeKey } from '../../../../domain/semantics'
import type { Message, Session, Task } from '../../../../domain/types'
import type { SessionCheckpoint } from '../../../../api/client'
import { useSessionCheckpoints } from '../../../../hooks/useSessionCheckpoints'
import { MarkdownContent } from '../../../../components/MarkdownContent'
import { openFileInSidecar } from '../../../chat/toolPresentation'
import {
  buildExecutionHubData,
  type AgentGraphNode,
  type ExecutionTurn,
  type FileTouchAction,
  type RawStepEntry,
  type StepKind,
  type StepStatus,
  type VerificationEntry,
} from './parseAgentGraph'
import './agentGraphViewer.css'

const EMPTY_MESSAGES: Message[] = []
const EMPTY_TASKS: Task[] = []
const EMPTY_CHECKPOINTS: SessionCheckpoint[] = []

export type { AgentGraphNode, StepKind, StepStatus }

type HubTab = 'turns' | 'files' | 'verify'
type FileFilter = 'all' | 'modified' | 'created' | 'read'
type VerifyFilter = 'all' | 'failed' | 'passed'

function formatTime(iso?: string): string {
  if (!iso) return ''
  const ms = Date.parse(iso)
  if (!Number.isFinite(ms)) return ''
  return new Date(ms).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}

function formatDuration(sec: number | null): string {
  if (sec === null) return ''
  if (sec < 1) return '<1s'
  if (sec < 60) return `${sec}s`
  return `${Math.floor(sec / 60)}m ${sec % 60}s`
}

function outcomeBadge(turn: ExecutionTurn): { label: string; cls: string } {
  switch (turn.outcome) {
    case 'running':
      return { label: '执行中', cls: 'is-running' }
    case 'has_errors':
      return { label: `存在 ${turn.diagnostics.filter((d) => !d.resolved).length} 处报错`, cls: 'is-error' }
    case 'recovered':
      return { label: `经历 ${turn.diagnostics.length} 次失败后修复`, cls: 'is-recovered' }
    case 'passed_clean':
      return { label: '一次性通过', cls: 'is-ok' }
    default:
      return { label: '已完成', cls: 'is-ok' }
  }
}

function rawBreakdownSummary(steps: RawStepEntry[]): string {
  const counts = { research: 0, edit: 0, verify: 0, decision: 0, subagent: 0, other: 0 }
  for (const step of steps) counts[step.phase] += 1
  const parts: string[] = []
  if (counts.research) parts.push(`${counts.research} 次检索读取`)
  if (counts.edit) parts.push(`${counts.edit} 次代码改动`)
  if (counts.verify) parts.push(`${counts.verify} 次命令验证`)
  if (counts.decision) parts.push(`${counts.decision} 次思考总结`)
  if (counts.subagent) parts.push(`${counts.subagent} 次子代理`)
  if (counts.other) parts.push(`${counts.other} 次其他工具`)
  return parts.length > 0 ? `（${parts.join(' · ')}）` : ''
}

function fileTagLabel(action: FileTouchAction, editCount: number, readCount: number): { text: string; cls: string } {
  if (action === 'created') return { text: editCount > 1 ? `+新建 ×${editCount}` : '+新建', cls: 'is-created' }
  if (action === 'modified') return { text: editCount > 1 ? `×${editCount}` : '已改', cls: 'is-modified' }
  return { text: readCount > 1 ? `只读 ×${readCount}` : '只读', cls: 'is-read' }
}

function verifyCategoryLabel(category: VerificationEntry['category']): string {
  switch (category) {
    case 'test': return '测试'
    case 'build': return '构建'
    case 'lint': return '检查'
    case 'git': return 'Git'
    case 'tool_error': return '工具异常'
    default: return '命令'
  }
}

function CheckpointsBridge({
  session,
  onChange,
}: {
  session: Session
  onChange: (items: SessionCheckpoint[]) => void
}) {
  const { items } = useSessionCheckpoints(session, Boolean(session.workspace))
  useEffect(() => {
    onChange(items)
  }, [items, onChange])
  return null
}

export function AgentGraphViewer({ artifact }: ViewerContext) {
  const sessionId = artifact.sessionId
  const agentId = artifact.agentId
  const session = useAstrorderStore((state) => state.sessions[scopeKey(agentId, sessionId)])
  const messages = useAstrorderStore(
    useShallow((s) => (sessionId && agentId ? selectMessages(s, agentId, sessionId) : EMPTY_MESSAGES)),
  )
  const tasks = useAstrorderStore(
    useShallow((s) => (sessionId && agentId ? selectTasks(s, agentId, sessionId) : EMPTY_TASKS)),
  )
  const hasQueryClient = Boolean(useContext(QueryClientContext))
  const [checkpoints, setCheckpoints] = useState<SessionCheckpoint[]>(EMPTY_CHECKPOINTS)

  const [activeTab, setActiveTab] = useState<HubTab>('turns')
  const [query, setQuery] = useState('')
  const [newestFirst, setNewestFirst] = useState(true)
  const [onlySignificant, setOnlySignificant] = useState(false)
  const [fileFilter, setFileFilter] = useState<FileFilter>('all')
  const [verifyFilter, setVerifyFilter] = useState<VerifyFilter>('all')
  const [expandedTurnIds, setExpandedTurnIds] = useState<Record<string, boolean>>({})
  const [expandedVerifyIds, setExpandedVerifyIds] = useState<Record<string, boolean>>({})
  const [selectedStep, setSelectedStep] = useState<RawStepEntry | null>(null)

  const searchInputRef = useRef<HTMLInputElement>(null)
  const tabRefs = useRef<Array<HTMLButtonElement | null>>([])
  const idPrefix = useId()

  const hub = useMemo(
    () => buildExecutionHubData(messages, tasks, checkpoints, session?.workspace),
    [messages, tasks, checkpoints, session?.workspace],
  )

  const normalizedQuery = query.trim().toLowerCase()

  const filteredTurns = useMemo(() => {
    let list = hub.turns
    if (onlySignificant) {
      list = list.filter(
        (t) =>
          t.outcome === 'running' ||
          t.diagnostics.length > 0 ||
          t.phases.editFiles > 0 ||
          t.phases.verifyTotal > 0,
      )
    }
    if (normalizedQuery) {
      list = list.filter((t) => {
        if (t.title.toLowerCase().includes(normalizedQuery)) return true
        if (t.decisions.some((d) => d.toLowerCase().includes(normalizedQuery))) return true
        if (t.files.some((f) => f.displayPath.toLowerCase().includes(normalizedQuery) || f.fileName.toLowerCase().includes(normalizedQuery))) return true
        if (t.verifications.some((v) => v.command.toLowerCase().includes(normalizedQuery) || (v.errorExcerpt || '').toLowerCase().includes(normalizedQuery))) return true
        if (t.diagnostics.some((d) => d.headline.toLowerCase().includes(normalizedQuery))) return true
        return false
      })
    }
    return newestFirst ? [...list].reverse() : list
  }, [hub.turns, newestFirst, normalizedQuery, onlySignificant])

  const filteredFiles = useMemo(() => {
    let list = hub.files
    if (fileFilter !== 'all') {
      list = list.filter((f) => f.action === fileFilter)
    }
    if (normalizedQuery) {
      list = list.filter(
        (f) =>
          f.displayPath.toLowerCase().includes(normalizedQuery) ||
          f.fileName.toLowerCase().includes(normalizedQuery),
      )
    }
    return list
  }, [fileFilter, hub.files, normalizedQuery])

  const filteredVerifications = useMemo(() => {
    let list = [...hub.verifications].reverse()
    if (verifyFilter === 'failed') list = list.filter((v) => v.status === 'failed')
    else if (verifyFilter === 'passed') list = list.filter((v) => v.status === 'passed')
    if (normalizedQuery) {
      list = list.filter(
        (v) =>
          v.command.toLowerCase().includes(normalizedQuery) ||
          (v.errorExcerpt || '').toLowerCase().includes(normalizedQuery) ||
          v.turnTitle.toLowerCase().includes(normalizedQuery) ||
          v.output.toLowerCase().includes(normalizedQuery),
      )
    }
    return list
  }, [hub.verifications, normalizedQuery, verifyFilter])

  // Determine which turns are expanded by default: running turn + latest 2 turns
  const defaultExpandedIds = useMemo(() => {
    const set = new Set<string>()
    const latestTwo = hub.turns.slice(-2)
    for (const t of latestTwo) set.add(t.id)
    for (const t of hub.turns) {
      if (t.outcome === 'running' || t.outcome === 'has_errors') set.add(t.id)
    }
    return set
  }, [hub.turns])

  const isTurnExpanded = (turnId: string): boolean => {
    if (turnId in expandedTurnIds) return expandedTurnIds[turnId]
    return defaultExpandedIds.has(turnId)
  }

  const allExpanded = filteredTurns.length > 0 && filteredTurns.every((t) => isTurnExpanded(t.id))

  const toggleAllTurns = () => {
    const nextState = !allExpanded
    const map: Record<string, boolean> = {}
    for (const t of hub.turns) map[t.id] = nextState
    setExpandedTurnIds(map)
  }

  const toggleTurn = (turnId: string) => {
    setExpandedTurnIds((prev) => ({ ...prev, [turnId]: !isTurnExpanded(turnId) }))
  }

  const handleContainerKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'f') {
      event.preventDefault()
      searchInputRef.current?.focus()
      searchInputRef.current?.select()
    }
  }

  const handleTabKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const order: HubTab[] = ['turns', 'files', 'verify']
    const idx = order.indexOf(activeTab)
    let nextIdx = idx
    if (event.key === 'ArrowRight') nextIdx = (idx + 1) % order.length
    else if (event.key === 'ArrowLeft') nextIdx = (idx - 1 + order.length) % order.length
    else if (event.key === 'Home') nextIdx = 0
    else if (event.key === 'End') nextIdx = order.length - 1
    else return
    event.preventDefault()
    const nextTab = order[nextIdx]
    setActiveTab(nextTab)
    tabRefs.current[nextIdx]?.focus()
    tabRefs.current[nextIdx]?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }

  const jumpToMessage = (messageId?: string) => {
    if (!messageId) return
    window.dispatchEvent(
      new CustomEvent('astrorder:focus-message', {
        detail: { agentId, sessionId, messageId },
      }),
    )
  }

  const openCheckpoint = (checkpointId?: string | null) => {
    window.dispatchEvent(
      new CustomEvent('astrorder:open-checkpoint', {
        detail: { agentId, sessionId, checkpointId: checkpointId ?? null },
      }),
    )
  }

  const openTouchedFile = (filePath: string) => {
    if (!session) return
    openFileInSidecar(filePath, session)
  }

  const copyCommand = async (cmd: string) => {
    try {
      await navigator.clipboard.writeText(cmd)
      notifications.show({ color: 'teal', message: '已复制命令' })
    } catch {
      notifications.show({ color: 'red', message: '无法复制命令，请检查剪贴板权限。' })
    }
  }

  const changedFilesTotal = hub.summary.modifiedFileCount + hub.summary.createdFileCount
  const tabIds: Record<HubTab, string> = {
    turns: `${idPrefix}-tab-turns`,
    files: `${idPrefix}-tab-files`,
    verify: `${idPrefix}-tab-verify`,
  }

  return (
    <div className="decision-hub agent-graph-viewer" onKeyDown={handleContainerKeyDown}>
      {hasQueryClient && session?.workspace && (
        <CheckpointsBridge session={session} onChange={setCheckpoints} />
      )}

      {/* Row 1: Title & Quick Controls */}
      <header className="dh-titlebar">
        <div className="dh-titlebar-left">
          <h2 className="dh-title">任务轨迹与决策中枢</h2>
          <span className={`dh-status-pill${hub.summary.runningTurns > 0 ? ' is-running' : ''}`}>
            <span className="dh-dot" aria-hidden="true" />
            <span>
              {hub.summary.runningTurns > 0
                ? `执行中 · 第 ${hub.summary.turnCount} 轮`
                : hub.summary.turnCount > 0
                  ? `就绪 · ${hub.summary.turnCount} 轮任务`
                  : '等待指令'}
            </span>
          </span>
        </div>

        <div className="dh-titlebar-right">
          {activeTab === 'turns' && (
            <>
              <button
                type="button"
                className="dh-icon-btn"
                aria-pressed={onlySignificant}
                onClick={() => setOnlySignificant((v) => !v)}
                title="仅显示有代码改动、命令验证或报错的轮次"
              >
                <IconFilter size={13} aria-hidden="true" />
                <span className="dh-titlebar-label">只看关键轮</span>
              </button>
              <button
                type="button"
                className="dh-icon-btn"
                onClick={() => setNewestFirst((v) => !v)}
                title={newestFirst ? '当前：最新轮次置顶（点击切换正序）' : '当前：按时间正序（点击切换倒序）'}
              >
                {newestFirst ? <IconArrowDown size={13} aria-hidden="true" /> : <IconArrowUp size={13} aria-hidden="true" />}
                <span className="dh-titlebar-label">{newestFirst ? '最新优先' : '时间正序'}</span>
              </button>
              <button
                type="button"
                className="dh-icon-btn is-square"
                aria-expanded={allExpanded}
                aria-label={allExpanded ? '全部折叠' : '全部展开'}
                title={allExpanded ? '全部折叠' : '全部展开'}
                onClick={toggleAllTurns}
              >
                {allExpanded ? <IconChevronsUp size={14} aria-hidden="true" /> : <IconChevronsDown size={14} aria-hidden="true" />}
              </button>
            </>
          )}
        </div>
      </header>

      {/* Row 2: Segmented Tabs + Search */}
      <div className="dh-toolbar">
        <div
          className="dh-tablist"
          role="tablist"
          aria-label="决策与轨迹视图切换"
          onKeyDown={handleTabKeyDown}
        >
          <button
            ref={(el) => { tabRefs.current[0] = el }}
            id={tabIds.turns}
            type="button"
            role="tab"
            className="dh-tab"
            aria-selected={activeTab === 'turns'}
            tabIndex={activeTab === 'turns' ? 0 : -1}
            onClick={() => setActiveTab('turns')}
          >
            <span>阶段决策流</span>
            <span className="dh-tab-count">{hub.summary.turnCount}</span>
          </button>
          <button
            ref={(el) => { tabRefs.current[1] = el }}
            id={tabIds.files}
            type="button"
            role="tab"
            className="dh-tab"
            aria-selected={activeTab === 'files'}
            tabIndex={activeTab === 'files' ? 0 : -1}
            onClick={() => setActiveTab('files')}
          >
            <span>文件影响</span>
            <span className="dh-tab-count">{hub.summary.totalTouchedFiles}</span>
          </button>
          <button
            ref={(el) => { tabRefs.current[2] = el }}
            id={tabIds.verify}
            type="button"
            role="tab"
            className="dh-tab"
            aria-selected={activeTab === 'verify'}
            tabIndex={activeTab === 'verify' ? 0 : -1}
            onClick={() => setActiveTab('verify')}
          >
            <span>验证与报错</span>
            <span className={`dh-tab-count${hub.summary.verifyFailed > 0 ? ' is-alert' : ''}`}>
              {hub.verifications.length}
            </span>
          </button>
        </div>

        <label className="dh-search-wrap">
          <IconSearch size={13} className="dh-search-icon" aria-hidden="true" />
          <input
            ref={searchInputRef}
            type="search"
            className="dh-search-input"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜索文件、命令或报错…"
            aria-label="搜索文件、命令或报错"
          />
          {query ? (
            <button
              type="button"
              className="dh-clear-btn"
              onClick={() => setQuery('')}
              aria-label="清除搜索"
            >
              <IconX size={12} aria-hidden="true" />
            </button>
          ) : (
            <span className="dh-kbd" aria-hidden="true">⌘F</span>
          )}
        </label>
      </div>

      {/* 4-Column KPI Summary Strip */}
      <section className="dh-kpi-strip" aria-label="会话执行指标概览">
        <div className="dh-kpi-cell">
          <span className="dh-kpi-label">
            <IconHistory size={12} aria-hidden="true" />
            <span>任务轮次</span>
          </span>
          <span className="dh-kpi-value">
            {hub.summary.turnCount} 轮
            {hub.summary.totalRawSteps > 0 && (
              <>
                <span className="is-sep">·</span>
                <span>{hub.summary.totalRawSteps} 步</span>
              </>
            )}
          </span>
        </div>

        <div className="dh-kpi-cell">
          <span className="dh-kpi-label">
            <IconFileCode size={12} aria-hidden="true" />
            <span>改动文件</span>
          </span>
          <span className="dh-kpi-value">
            {changedFilesTotal > 0 ? (
              <>
                <span>{hub.summary.modifiedFileCount} 改</span>
                <span className="is-sep">·</span>
                <span className="is-ok">{hub.summary.createdFileCount} 新建</span>
              </>
            ) : hub.summary.readOnlyFileCount > 0 ? (
              <span>{hub.summary.readOnlyFileCount} 只读</span>
            ) : (
              <span>暂无改动</span>
            )}
          </span>
        </div>

        <div className="dh-kpi-cell">
          <span className="dh-kpi-label">
            <IconShieldCheck size={12} aria-hidden="true" />
            <span>命令验证</span>
          </span>
          <span className="dh-kpi-value">
            {hub.verifications.length > 0 ? (
              <>
                <span className="is-ok">{hub.summary.verifyPassed} 通过</span>
                <span className="is-sep">·</span>
                <span className={hub.summary.verifyFailed > 0 ? 'is-err' : ''}>{hub.summary.verifyFailed} 失败</span>
              </>
            ) : (
              <span>暂无验证</span>
            )}
          </span>
        </div>

        <div className="dh-kpi-cell">
          <span className="dh-kpi-label">
            <IconGitCompare size={12} aria-hidden="true" />
            <span>{hub.summary.checkpointCount > 0 || session?.workspace ? '检查点' : '子代理'}</span>
          </span>
          <span className="dh-kpi-value">
            {hub.summary.checkpointCount > 0 || session?.workspace
              ? `${hub.summary.checkpointCount} 个快照`
              : `${hub.summary.subagentCount} 个任务`}
          </span>
        </div>
      </section>

      {/* Main Content Area */}
      <div className="dh-scroll" role="tabpanel" aria-labelledby={tabIds[activeTab]}>
        {/* VIEW 1: 阶段决策流 */}
        {activeTab === 'turns' && (
          filteredTurns.length === 0 ? (
            <div className="dh-empty">
              <div className="dh-empty-title">
                {hub.turns.length === 0 ? '等待任务开始' : '没有匹配的轮次'}
              </div>
              <div className="dh-empty-desc">
                {hub.turns.length === 0
                  ? '发送指令后，这里会自动按轮次提炼阶段进展、影响文件、命令验证与错误自愈轨迹。'
                  : '尝试清空搜索关键词或关闭「只看关键轮」筛选。'}
              </div>
            </div>
          ) : (
            <div className="dh-timeline">
              {filteredTurns.map((turn, idx) => {
                const expanded = isTurnExpanded(turn.id)
                const badge = outcomeBadge(turn)
                const editedFiles = turn.files.filter((f) => f.action !== 'read')
                const visibleFiles = editedFiles.length > 0 ? editedFiles : turn.files.slice(0, 6)
                const isLast = idx === filteredTurns.length - 1
                const nodeTone =
                  turn.outcome === 'running' || idx === 0
                    ? 'is-active'
                    : turn.outcome === 'has_errors'
                      ? 'is-error'
                      : turn.outcome === 'recovered'
                        ? 'is-recovered'
                        : ''
                const durText = formatDuration(turn.durationSec)

                return (
                  <div className="dh-turn-row" key={turn.id}>
                    <div className="dh-rail" aria-hidden="true">
                      <span className={`dh-rail-node ${nodeTone}`}>{turn.index}</span>
                      {!isLast && <span className="dh-rail-line" />}
                    </div>

                    <article className={`dh-turn-card${turn.outcome === 'running' ? ' is-running' : ''}`}>
                      <div
                        className="dh-turn-head"
                        onClick={() => toggleTurn(turn.id)}
                      >
                        <div className="dh-turn-head-main">
                          <div className="dh-turn-meta-top">
                            <span className="dh-turn-num">#{String(turn.index).padStart(2, '0')}</span>
                            <span className={`dh-outcome-pill ${badge.cls}`}>
                              <span className="dh-dot" aria-hidden="true" />
                              <span>{badge.label}</span>
                            </span>
                            {turn.startedAt && <span className="dh-turn-time">{formatTime(turn.startedAt)}</span>}
                            {durText && <span className="dh-turn-time">· {durText}</span>}
                          </div>
                          <h3 className="dh-turn-title">{turn.title}</h3>
                        </div>

                        <div
                          className="dh-turn-actions"
                          onClick={(e) => e.stopPropagation()}
                        >
                          {turn.checkpoint && (
                            <button
                              type="button"
                              className="dh-ghost-btn"
                              onClick={() => openCheckpoint(turn.checkpoint?.id)}
                              title={`查看检查点快照 ${turn.checkpoint.tree_sha.slice(0, 7)}`}
                            >
                              <IconGitCompare size={12} aria-hidden="true" />
                              <span>对比 Diff</span>
                            </button>
                          )}
                          {turn.anchorMessageId && (
                            <button
                              type="button"
                              className="dh-ghost-btn"
                              onClick={() => jumpToMessage(turn.anchorMessageId)}
                              title="在左侧对话流中定位并高亮本轮消息"
                            >
                              <IconMessageShare size={12} aria-hidden="true" />
                              <span>定位对话</span>
                            </button>
                          )}
                          <button
                            type="button"
                            className="dh-caret-btn"
                            aria-expanded={expanded}
                            aria-label={expanded ? '折叠该轮详情' : '展开该轮详情'}
                            onClick={() => toggleTurn(turn.id)}
                          >
                            {expanded ? <IconChevronUp size={15} aria-hidden="true" /> : <IconChevronDown size={15} aria-hidden="true" />}
                          </button>
                        </div>
                      </div>

                      {!expanded ? (
                        <div className="dh-turn-collapsed-meta">
                          {editedFiles.length > 0 && <span>{editedFiles.length} 个改动文件</span>}
                          {turn.phases.verifyTotal > 0 && (
                            <span>
                              验证 {turn.phases.verifyPassed}/{turn.phases.verifyTotal}
                            </span>
                          )}
                          {(turn.addedLines > 0 || turn.removedLines > 0) && (
                            <span className="dh-diff-delta">
                              <span className="is-add">+{turn.addedLines}</span> / <span className="is-del">-{turn.removedLines}</span>
                            </span>
                          )}
                          <span>{turn.rawSteps.length} 步</span>
                        </div>
                      ) : (
                        <div className="dh-turn-body">
                          {/* Semantic Stage Pipeline */}
                          <div className="dh-pipeline" aria-label="阶段执行管道">
                            {turn.phases.research > 0 && (
                              <span className="dh-stage-pill">
                                <IconSearch size={12} aria-hidden="true" />
                                <span>调研检索 {turn.phases.research}</span>
                              </span>
                            )}
                            {turn.phases.research > 0 && (turn.phases.decision > 0 || turn.phases.editFiles > 0 || turn.phases.verifyTotal > 0) && (
                              <span className="dh-stage-arrow" aria-hidden="true">→</span>
                            )}
                            {turn.phases.decision > 0 && (
                              <span className="dh-stage-pill">
                                <IconBulb size={12} aria-hidden="true" />
                                <span>关键决策 {turn.phases.decision}</span>
                              </span>
                            )}
                            {turn.phases.decision > 0 && (turn.phases.editFiles > 0 || turn.phases.verifyTotal > 0) && (
                              <span className="dh-stage-arrow" aria-hidden="true">→</span>
                            )}
                            {turn.phases.editFiles > 0 && (
                              <span className="dh-stage-pill">
                                <IconCode size={12} aria-hidden="true" />
                                <span>代码改动 {turn.phases.editFiles} 文件</span>
                              </span>
                            )}
                            {turn.phases.editFiles > 0 && turn.phases.verifyTotal > 0 && (
                              <span className="dh-stage-arrow" aria-hidden="true">→</span>
                            )}
                            {turn.phases.verifyTotal > 0 && (
                              <span
                                className={`dh-stage-pill ${
                                  turn.phases.verifyFailed > 0 && turn.outcome === 'has_errors'
                                    ? 'is-fail'
                                    : turn.phases.verifyPassed > 0
                                      ? 'is-done'
                                      : ''
                                }`}
                              >
                                {turn.outcome === 'has_errors' ? (
                                  <IconAlertTriangle size={12} aria-hidden="true" />
                                ) : (
                                  <IconCheck size={12} aria-hidden="true" />
                                )}
                                <span>
                                  {turn.outcome === 'has_errors'
                                    ? `验证失败 ${turn.phases.verifyFailed}/${turn.phases.verifyTotal}`
                                    : `验证通过 ${turn.phases.verifyPassed}/${turn.phases.verifyTotal}`}
                                </span>
                              </span>
                            )}
                          </div>

                          {/* Failure → Fix Diagnostic Callouts */}
                          {turn.diagnostics.map((diag) => (
                            <div className="dh-diag-box" key={diag.id}>
                              <div className="dh-diag-title">
                                <IconAlertTriangle size={14} style={{ flexShrink: 0, marginTop: 2 }} aria-hidden="true" />
                                <span>
                                  {diag.resolved ? '首次失败（已修复）：' : '执行失败：'}
                                  {diag.headline}
                                </span>
                              </div>
                              <div className="dh-diag-cmd" title={diag.commandOrTool}>
                                {diag.commandOrTool}
                              </div>
                              {diag.resolutionNote && (
                                <div className="dh-diag-note">→ {diag.resolutionNote}</div>
                              )}
                            </div>
                          ))}

                          {/* Decision Summary Well */}
                          {turn.decisions.length > 0 && (
                            <div className="dh-decision-well">
                              <div className="dh-decision-head">
                                <IconBulb size={13} aria-hidden="true" />
                                <span>关键结论与决策</span>
                              </div>
                              <ul className="dh-decision-list">
                                {turn.decisions.map((item, i) => (
                                  <li key={i}>{item}</li>
                                ))}
                              </ul>
                            </div>
                          )}

                          {/* Touched Files Chips */}
                          {visibleFiles.length > 0 && (
                            <div className="dh-files-block">
                              <div className="dh-files-head">
                                <span>
                                  {editedFiles.length > 0
                                    ? `影响文件 (${editedFiles.length})`
                                    : `查阅文件 (${turn.files.length})`}
                                </span>
                                {(turn.addedLines > 0 || turn.removedLines > 0) && (
                                  <span className="dh-diff-delta">
                                    <span className="is-add">+{turn.addedLines}</span> / <span className="is-del">-{turn.removedLines}</span> 行
                                  </span>
                                )}
                              </div>
                              <div className="dh-file-chips">
                                {visibleFiles.slice(0, 10).map((file) => {
                                  const tag = fileTagLabel(file.action, file.editCount, file.readCount)
                                  return (
                                    <button
                                      key={file.path}
                                      type="button"
                                      className="dh-file-chip"
                                      onClick={() => openTouchedFile(file.path)}
                                      title={`在 Sidecar 打开：${file.displayPath}`}
                                    >
                                      {file.action === 'created' ? (
                                        <IconFilePlus size={12} aria-hidden="true" />
                                      ) : file.action === 'modified' ? (
                                        <IconFileCode size={12} aria-hidden="true" />
                                      ) : (
                                        <IconFileText size={12} aria-hidden="true" />
                                      )}
                                      <span className="dh-file-chip-name">{file.fileName}</span>
                                      <span className={`dh-file-tag ${tag.cls}`}>{tag.text}</span>
                                    </button>
                                  )
                                })}
                                {visibleFiles.length > 10 && (
                                  <button
                                    type="button"
                                    className="dh-file-chip"
                                    onClick={() => setActiveTab('files')}
                                  >
                                    +{visibleFiles.length - 10} 个文件
                                  </button>
                                )}
                              </div>
                            </div>
                          )}

                          {/* Verification Pills */}
                          {turn.verifications.length > 0 && (
                            <div className="dh-verify-pills">
                              {turn.verifications.slice(0, 4).map((v) => (
                                <button
                                  key={v.id}
                                  type="button"
                                  className={`dh-verify-pill ${v.status === 'passed' ? 'is-passed' : v.status === 'failed' ? 'is-failed' : ''}`}
                                  onClick={() => {
                                    setActiveTab('verify')
                                    setExpandedVerifyIds((prev) => ({ ...prev, [v.id]: true }))
                                  }}
                                  title={v.command}
                                >
                                  {v.status === 'failed' ? (
                                    <IconAlertTriangle size={12} aria-hidden="true" />
                                  ) : (
                                    <IconCheck size={12} aria-hidden="true" />
                                  )}
                                  <span className="dh-verify-pill-cmd">{v.summary || v.command}</span>
                                  <span>
                                    · {v.status === 'passed' ? '通过' : v.status === 'failed' ? (v.recovered ? '曾失败(已修复)' : '失败') : '运行中'}
                                    {v.durationSec !== null ? ` (${formatDuration(v.durationSec)})` : ''}
                                  </span>
                                </button>
                              ))}
                              {turn.checkpoint && (
                                <button
                                  type="button"
                                  className="dh-verify-pill"
                                  onClick={() => openCheckpoint(turn.checkpoint?.id)}
                                >
                                  <IconGitCompare size={12} aria-hidden="true" />
                                  <span>检查点 {turn.checkpoint.tree_sha.slice(0, 7)}</span>
                                </button>
                              )}
                            </div>
                          )}

                          {/* Native <details> Raw Steps Disclosure */}
                          {turn.rawSteps.length > 0 && (
                            <details className="dh-raw-details">
                              <summary>
                                <span>
                                  展开全部 {turn.rawSteps.length} 个底层步骤{rawBreakdownSummary(turn.rawSteps)}
                                </span>
                                <IconChevronDown size={13} aria-hidden="true" />
                              </summary>
                              <div className="dh-raw-list">
                                {turn.rawSteps.map((step) => (
                                  <button
                                    key={step.id}
                                    type="button"
                                    className="dh-raw-item"
                                    onClick={() => setSelectedStep(step)}
                                  >
                                    <span className="dh-raw-item-left">
                                      <span
                                        className="dh-dot"
                                        style={{
                                          color:
                                            step.status === 'failed'
                                              ? 'var(--dh-red-text)'
                                              : step.status === 'running'
                                                ? 'var(--dh-blue-text)'
                                                : 'var(--dh-green-dot)',
                                        }}
                                        aria-hidden="true"
                                      />
                                      <span className="dh-raw-item-title">{step.title}</span>
                                    </span>
                                    <span className="dh-raw-item-meta">
                                      {step.toolName ? `${step.toolName} · ` : ''}
                                      {formatTime(step.timestamp)}
                                    </span>
                                  </button>
                                ))}
                              </div>
                            </details>
                          )}
                        </div>
                      )}
                    </article>
                  </div>
                )
              })}
            </div>
          )
        )}

        {/* VIEW 2: 文件影响热图 */}
        {activeTab === 'files' && (
          <>
            <div className="dh-subfilter-row">
              <div className="dh-filter-pills" role="group" aria-label="文件改动类型筛选">
                {(
                  [
                    ['all', `全部 (${hub.files.length})`],
                    ['modified', `已修改 (${hub.summary.modifiedFileCount})`],
                    ['created', `新建 (${hub.summary.createdFileCount})`],
                    ['read', `仅读取 (${hub.summary.readOnlyFileCount})`],
                  ] as const
                ).map(([key, label]) => (
                  <button
                    key={key}
                    type="button"
                    className="dh-filter-pill"
                    aria-pressed={fileFilter === key}
                    onClick={() => setFileFilter(key)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>

            {filteredFiles.length === 0 ? (
              <div className="dh-empty">
                <div className="dh-empty-title">暂无匹配的文件记录</div>
                <div className="dh-empty-desc">
                  Agent 读取、编辑或创建工作区文件后，这里会自动去重汇总，支持一键打开查看代码。
                </div>
              </div>
            ) : (
              <div className="dh-file-list">
                {filteredFiles.map((file) => {
                  const tag = fileTagLabel(file.action, file.editCount, file.readCount)
                  return (
                    <div className="dh-file-row" key={file.path}>
                      <div className="dh-file-row-main">
                        <div className="dh-file-row-top">
                          <span className="dh-file-row-name" title={file.path}>
                            {file.fileName}
                          </span>
                          <span className={`dh-file-tag ${tag.cls}`}>{tag.text}</span>
                          {(file.addedLines > 0 || file.removedLines > 0) && (
                            <span className="dh-diff-delta">
                              <span className="is-add">+{file.addedLines}</span> / <span className="is-del">-{file.removedLines}</span>
                            </span>
                          )}
                        </div>
                        <span className="dh-file-row-path" title={file.displayPath}>
                          {file.displayPath} · 最近于 #{String(file.lastTurn).padStart(2, '0')}
                        </span>
                      </div>

                      <div className="dh-file-row-right">
                        {file.lastMessageId && (
                          <button
                            type="button"
                            className="dh-ghost-btn"
                            onClick={() => jumpToMessage(file.lastMessageId)}
                            title="定位到最近操作该文件的对话位置"
                          >
                            <IconMessageShare size={12} aria-hidden="true" />
                            <span>定位</span>
                          </button>
                        )}
                        <button
                          type="button"
                          className="dh-ghost-btn"
                          onClick={() => openTouchedFile(file.path)}
                          disabled={!session}
                          title="在 Sidecar 打开文件"
                        >
                          <IconExternalLink size={12} aria-hidden="true" />
                          <span>打开</span>
                        </button>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </>
        )}

        {/* VIEW 3: 验证与报错审计 */}
        {activeTab === 'verify' && (
          <>
            <div className="dh-subfilter-row">
              <div className="dh-filter-pills" role="group" aria-label="验证状态筛选">
                {(
                  [
                    ['all', `全部 (${hub.verifications.length})`],
                    ['failed', `失败 / 报错 (${hub.summary.verifyFailed})`],
                    ['passed', `通过 (${hub.summary.verifyPassed})`],
                  ] as const
                ).map(([key, label]) => (
                  <button
                    key={key}
                    type="button"
                    className="dh-filter-pill"
                    aria-pressed={verifyFilter === key}
                    onClick={() => setVerifyFilter(key)}
                  >
                    {label}
                  </button>
                ))}
              </div>
            </div>

            {filteredVerifications.length === 0 ? (
              <div className="dh-empty">
                <div className="dh-empty-title">暂无命令验证或报错记录</div>
                <div className="dh-empty-desc">
                  Agent 执行测试、构建、Lint 或发生工具调用错误时，这里会集中展示命令与诊断输出。
                </div>
              </div>
            ) : (
              <div className="dh-audit-list">
                {filteredVerifications.map((entry) => {
                  const isOutputOpen = Boolean(expandedVerifyIds[entry.id])
                  return (
                    <article
                      className={`dh-audit-card${entry.status === 'failed' ? ' is-failed' : ''}`}
                      key={entry.id}
                    >
                      <div className="dh-audit-head">
                        <div style={{ minWidth: 0, flex: 1 }}>
                          <div className="dh-audit-cmd">
                            <IconTerminal2
                              size={13}
                              style={{ verticalAlign: '-2px', marginRight: 6, color: 'var(--dh-muted)' }}
                              aria-hidden="true"
                            />
                            {entry.command}
                          </div>
                          <div className="dh-audit-meta">
                            <span className="dh-turn-num">#{String(entry.turnIndex).padStart(2, '0')}</span>
                            <span className={`dh-outcome-pill ${entry.status === 'passed' ? 'is-ok' : entry.status === 'failed' ? (entry.recovered ? 'is-recovered' : 'is-error') : 'is-running'}`}>
                              {entry.status === 'passed'
                                ? '通过'
                                : entry.status === 'failed'
                                  ? entry.recovered
                                    ? '曾失败 · 后续已修复'
                                    : '失败'
                                  : '执行中'}
                            </span>
                            <span>{verifyCategoryLabel(entry.category)}</span>
                            {entry.durationSec !== null && <span>· {formatDuration(entry.durationSec)}</span>}
                            {entry.timestamp && <span>· {formatTime(entry.timestamp)}</span>}
                          </div>
                        </div>

                        <div className="dh-turn-actions">
                          <button
                            type="button"
                            className="dh-ghost-btn"
                            onClick={() => void copyCommand(entry.command)}
                            title="复制命令"
                          >
                            <IconCopy size={12} aria-hidden="true" />
                            <span>复制</span>
                          </button>
                          {entry.messageId && (
                            <button
                              type="button"
                              className="dh-ghost-btn"
                              onClick={() => jumpToMessage(entry.messageId)}
                            >
                              <IconMessageShare size={12} aria-hidden="true" />
                              <span>定位</span>
                            </button>
                          )}
                          {entry.output && (
                            <button
                              type="button"
                              className="dh-ghost-btn"
                              aria-expanded={isOutputOpen}
                              onClick={() =>
                                setExpandedVerifyIds((prev) => ({ ...prev, [entry.id]: !isOutputOpen }))
                              }
                            >
                              <span>{isOutputOpen ? '收起输出' : '查看输出'}</span>
                            </button>
                          )}
                        </div>
                      </div>

                      {entry.errorExcerpt && !isOutputOpen && (
                        <div className="dh-audit-excerpt">{entry.errorExcerpt}</div>
                      )}

                      {isOutputOpen && entry.output && (
                        <pre className="dh-audit-output">{entry.output}</pre>
                      )}
                    </article>
                  )
                })}
              </div>
            )}
          </>
        )}
      </div>

      {/* Raw Step Detail Drawer (on demand only) */}
      <Drawer
        opened={Boolean(selectedStep)}
        onClose={() => setSelectedStep(null)}
        title={selectedStep?.title || '步骤详情'}
        position="right"
        size="md"
      >
        {selectedStep && (
          <Stack gap="md">
            <Group justify="space-between">
              <span className="dh-turn-num">{selectedStep.toolName || selectedStep.phase}</span>
              {selectedStep.messageId && (
                <button
                  type="button"
                  className="dh-ghost-btn"
                  onClick={() => {
                    jumpToMessage(selectedStep.messageId)
                    setSelectedStep(null)
                  }}
                >
                  <IconMessageShare size={12} aria-hidden="true" />
                  <span>定位到对话</span>
                </button>
              )}
            </Group>

            {selectedStep.toolPayload != null && (
              <div>
                <Text size="xs" fw={600} mb={4}>
                  工具参数 (Payload)
                </Text>
                <Paper
                  p="xs"
                  withBorder
                  style={{
                    background: 'var(--astr-surface)',
                    fontFamily: 'monospace',
                    fontSize: 12,
                    maxHeight: 240,
                    overflow: 'auto',
                  }}
                >
                  <pre style={{ margin: 0 }}>
                    {JSON.stringify(selectedStep.toolPayload, null, 2)}
                  </pre>
                </Paper>
              </div>
            )}

            {selectedStep.detail && (
              <div>
                <Text size="xs" fw={600} mb={4}>
                  详细执行输出
                </Text>
                <Paper p="xs" withBorder style={{ maxHeight: 380, overflow: 'auto' }}>
                  <MarkdownContent value={selectedStep.detail} session={session} />
                </Paper>
              </div>
            )}
          </Stack>
        )}
      </Drawer>
    </div>
  )
}
