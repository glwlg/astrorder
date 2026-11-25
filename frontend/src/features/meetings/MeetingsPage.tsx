import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { Button, Group, Modal, Stack, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAlertTriangle,
  IconBookmark,
  IconBookmarkFilled,
  IconCheck,
  IconChevronDown,
  IconCopy,
  IconFolder,
  IconHistory,
  IconLoader2,
  IconMessageForward,
  IconMicrophone,
  IconPencil,
  IconPictureInPicture,
  IconPlayerPause,
  IconPlayerPlay,
  IconPlayerStop,
  IconPlus,
  IconSearch,
  IconSparkles,
  IconTrash,
  IconUpload,
  IconUser,
  IconX,
} from '@tabler/icons-react'
import type { MeetingActionItem, MeetingTranscriptSegment } from '../../api/client'
import { MarkdownContent } from '../../components/MarkdownContent'
import { useAstrorderStore } from '../../state/store'
import { ExportMenu } from '../office/export/ExportMenu'
import { MeetingModelConfigPopover } from './MeetingModelConfigPopover'
import {
  parseParticipants,
  readCrashDraftSnapshot,
  segmentsToPlainTranscript,
  segmentsToTranscript,
  useMeetingSessionStore,
  type MeetingCrashDraft,
} from './meetingSessionStore'
import { formatDurationClock } from './useMeetingLiveRecorder'
import './meetings.css'

function formatReadableDate(isoDate: string): string {
  const parts = isoDate.split('-')
  if (parts.length !== 3) return isoDate
  return `${parts[0]}年${Number(parts[1])}月${Number(parts[2])}日`
}

function formatShortMinutes(sec?: number): string {
  if (!sec || sec <= 0) return ''
  const mins = Math.max(1, Math.round(sec / 60))
  return `${mins}m`
}

export function MeetingsPage() {
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const sessions = useAstrorderStore((s) => s.sessions)
  const projects = useAstrorderStore((s) => s.projects)

  // Persistent meeting session state (survives route switches without interrupting audio/WS)
  const savedMeetings = useMeetingSessionStore((s) => s.savedMeetings)
  const settings = useMeetingSessionStore((s) => s.settings)
  const activeId = useMeetingSessionStore((s) => s.activeId)
  const title = useMeetingSessionStore((s) => s.title)
  const meetingDate = useMeetingSessionStore((s) => s.meetingDate)
  const participantsText = useMeetingSessionStore((s) => s.participantsText)
  const projectKey = useMeetingSessionStore((s) => s.projectKey)
  const workspace = useMeetingSessionStore((s) => s.workspace)
  const segments = useMeetingSessionStore((s) => s.segments)
  const rawTranscript = useMeetingSessionStore((s) => s.rawTranscript)
  const liveText = useMeetingSessionStore((s) => s.liveText)
  const liveStartSec = useMeetingSessionStore((s) => s.liveStartSec)
  const summary = useMeetingSessionStore((s) => s.summary)
  const topics = useMeetingSessionStore((s) => s.topics)
  const decisions = useMeetingSessionStore((s) => s.decisions)
  const risks = useMeetingSessionStore((s) => s.risks)
  const actionItems = useMeetingSessionStore((s) => s.actionItems)
  const minutesMarkdown = useMeetingSessionStore((s) => s.minutesMarkdown)
  const modelName = useMeetingSessionStore((s) => s.modelName)
  const summarizing = useMeetingSessionStore((s) => s.summarizing)
  const finalizingSummary = useMeetingSessionStore((s) => s.finalizingSummary)
  const lastSyncedAt = useMeetingSessionStore((s) => s.lastSyncedAt)
  const nextSyncCountdown = useMeetingSessionStore((s) => s.nextSyncCountdown)
  const autoSaveState = useMeetingSessionStore((s) => s.autoSaveState)
  const lastSavedAt = useMeetingSessionStore((s) => s.lastSavedAt)
  const transcribingFile = useMeetingSessionStore((s) => s.transcribingFile)

  const recordState = useMeetingSessionStore((s) => s.recordState)
  const elapsedSeconds = useMeetingSessionStore((s) => s.elapsedSeconds)
  const waveform = useMeetingSessionStore((s) => s.waveform)
  const devices = useMeetingSessionStore((s) => s.devices)
  const selectedDeviceId = useMeetingSessionStore((s) => s.selectedDeviceId)
  const r2t2Status = useMeetingSessionStore((s) => s.r2t2Status)
  const r2t2Loading = useMeetingSessionStore((s) => s.r2t2Loading)

  const loadInitial = useMeetingSessionStore((s) => s.loadInitial)
  const setSelectedDeviceId = useMeetingSessionStore((s) => s.setSelectedDeviceId)
  const toggleR2t2 = useMeetingSessionStore((s) => s.toggleR2t2)
  const restartR2t2 = useMeetingSessionStore((s) => s.restartR2t2)
  const setTitle = useMeetingSessionStore((s) => s.setTitle)
  const setMeetingDate = useMeetingSessionStore((s) => s.setMeetingDate)
  const setParticipantsText = useMeetingSessionStore((s) => s.setParticipantsText)
  const setWorkspaceLink = useMeetingSessionStore((s) => s.setWorkspaceLink)
  const setMinutesMarkdown = useMeetingSessionStore((s) => s.setMinutesMarkdown)
  const setSettings = useMeetingSessionStore((s) => s.setSettings)
  const startRecording = useMeetingSessionStore((s) => s.startRecording)
  const startNewMeetingRecording = useMeetingSessionStore((s) => s.startNewMeetingRecording)
  const pauseRecording = useMeetingSessionStore((s) => s.pauseRecording)
  const resumeRecording = useMeetingSessionStore((s) => s.resumeRecording)
  const bookmarkNow = useMeetingSessionStore((s) => s.bookmarkNow)
  const stopRecording = useMeetingSessionStore((s) => s.stopRecording)
  const openFloatingCapsule = useMeetingSessionStore((s) => s.openFloatingCapsule)
  const runSummarize = useMeetingSessionStore((s) => s.runSummarize)
  const persistCurrentMeeting = useMeetingSessionStore((s) => s.persistCurrentMeeting)
  const resetMeeting = useMeetingSessionStore((s) => s.resetMeeting)
  const openMeetingRecord = useMeetingSessionStore((s) => s.openMeetingRecord)
  const deleteMeeting = useMeetingSessionStore((s) => s.deleteMeeting)
  const uploadAudio = useMeetingSessionStore((s) => s.uploadAudio)
  const insertQuickNote = useMeetingSessionStore((s) => s.insertQuickNote)
  const commitPastedTranscript = useMeetingSessionStore((s) => s.commitPastedTranscript)
  const toggleBookmarkSegment = useMeetingSessionStore((s) => s.toggleBookmarkSegment)
  const updateSegment = useMeetingSessionStore((s) => s.updateSegment)
  const toggleActionDone = useMeetingSessionStore((s) => s.toggleActionDone)
  const updateActionItem = useMeetingSessionStore((s) => s.updateActionItem)
  const addActionItem = useMeetingSessionStore((s) => s.addActionItem)
  const removeActionItem = useMeetingSessionStore((s) => s.removeActionItem)
  const restoreCrashDraft = useMeetingSessionStore((s) => s.restoreCrashDraft)
  const dismissCrashDraft = useMeetingSessionStore((s) => s.dismissCrashDraft)

  // Local view-only UI state
  const [historyOpen, setHistoryOpen] = useState(false)
  const [historyQuery, setHistoryQuery] = useState('')
  const [configOpen, setConfigOpen] = useState(false)
  const [quickNote, setQuickNote] = useState('')
  const [followScroll, setFollowScroll] = useState(true)
  const [flashSegId, setFlashSegId] = useState<string | null>(null)
  const [pasteMode, setPasteMode] = useState(false)
  const [pasteDraft, setPasteDraft] = useState('')
  const [docTab, setDocTab] = useState<'structured' | 'transcript' | 'markdown'>('structured')
  const [transcriptViewMode, setTranscriptViewMode] = useState<'timestamped' | 'plain'>('timestamped')
  const [copiedTranscript, setCopiedTranscript] = useState(false)
  const [mdEditing, setMdEditing] = useState(false)
  const [addingAction, setAddingAction] = useState(false)
  const [newActionText, setNewActionText] = useState('')
  const [newActionOwner, setNewActionOwner] = useState('')
  const [newActionDue, setNewActionDue] = useState('')
  const [exportScope, setExportScope] = useState<'minutes' | 'transcript' | 'combined'>('minutes')
  const [crashDraft, setCrashDraft] = useState<MeetingCrashDraft | null>(() => readCrashDraftSnapshot())

  // Inline editing states for transcript segments and action items
  const [editingSegId, setEditingSegId] = useState<string | null>(null)
  const [editingSegText, setEditingSegText] = useState('')
  const [editingSpeakerSegId, setEditingSpeakerSegId] = useState<string | null>(null)
  const [editingSpeakerText, setEditingSpeakerText] = useState('')
  const [editingActionId, setEditingActionId] = useState<string | null>(null)
  const [editActionText, setEditActionText] = useState('')
  const [editActionOwner, setEditActionOwner] = useState('')
  const [editActionDue, setEditActionDue] = useState('')
  const [editActionPriority, setEditActionPriority] = useState('medium')

  // Session picker modal when dispatching action item to AI chat
  const [dispatchAction, setDispatchAction] = useState<MeetingActionItem | null>(null)
  const [dispatchSearch, setDispatchSearch] = useState('')

  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const transcriptScrollRef = useRef<HTMLDivElement | null>(null)
  const openedDeepLinkRef = useRef('')

  useEffect(() => {
    void loadInitial()
  }, [loadInitial])

  // Deep-link support: /meetings?id=<meeting_id> opens the referenced meeting directly
  const deepLinkId = searchParams.get('id') || ''
  useEffect(() => {
    if (!deepLinkId || deepLinkId === openedDeepLinkRef.current) return
    if (recordState !== 'idle') return
    const target = savedMeetings.find((m) => m.id === deepLinkId)
    if (!target) return
    if (openMeetingRecord(target)) {
      openedDeepLinkRef.current = deepLinkId
      setDocTab('structured')
      setSearchParams({}, { replace: true })
    }
  }, [deepLinkId, openMeetingRecord, recordState, savedMeetings, setSearchParams])

  // Auto-scroll live transcript to bottom when followScroll is enabled
  useEffect(() => {
    if (!followScroll || !transcriptScrollRef.current) return
    transcriptScrollRef.current.scrollTop = transcriptScrollRef.current.scrollHeight
  }, [followScroll, liveText, segments.length])

  const isRecordingActive =
    recordState === 'recording' ||
    recordState === 'paused' ||
    recordState === 'starting' ||
    recordState === 'stopping'

  const hasContent =
    isRecordingActive ||
    segments.length > 0 ||
    rawTranscript.trim().length > 0 ||
    liveText.trim().length > 0 ||
    minutesMarkdown.trim().length > 0 ||
    summary.trim().length > 0 ||
    actionItems.length > 0

  const fullTimestampedTranscript = useMemo(
    () => segmentsToTranscript(segments, liveText, liveStartSec) || rawTranscript,
    [segments, liveText, liveStartSec, rawTranscript],
  )

  const fullPlainTranscript = useMemo(
    () => segmentsToPlainTranscript(segments, liveText) || rawTranscript,
    [segments, liveText, rawTranscript],
  )

  const handleReset = () => {
    resetMeeting()
    setPasteMode(false)
    setPasteDraft('')
    setDocTab('structured')
  }

  const handleInsertQuickNote = () => {
    if (!quickNote.trim()) return
    insertQuickNote(quickNote)
    setQuickNote('')
  }

  const handleCommitPaste = async () => {
    const txt = pasteDraft.trim()
    if (!txt) return
    setPasteMode(false)
    setPasteDraft('')
    await commitPastedTranscript(txt)
  }

  const handleCopyTranscript = async () => {
    const text =
      transcriptViewMode === 'plain' ? fullPlainTranscript.trim() : fullTimestampedTranscript.trim()
    if (!text) return
    try {
      await navigator.clipboard.writeText(text)
      setCopiedTranscript(true)
      notifications.show({ color: 'teal', message: '已复制录音转文字原文' })
      window.setTimeout(() => setCopiedTranscript(false), 1800)
    } catch {
      notifications.show({ color: 'red', message: '复制失败，请手动全选复制' })
    }
  }

  const scrollToTimestamp = (ts: string) => {
    const match =
      segments.find((s) => s.timestamp === ts) ||
      segments.find((s) => s.timestamp.startsWith(ts.slice(0, 4)))
    if (!match) return
    setFlashSegId(match.id)
    document.getElementById(`mtg-seg-${match.id}`)?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    window.setTimeout(() => setFlashSegId((cur) => (cur === match.id ? null : cur)), 1800)
  }

  const handleAddActionItem = () => {
    if (!newActionText.trim()) return
    addActionItem(newActionText, newActionOwner, newActionDue)
    setNewActionText('')
    setNewActionOwner('')
    setNewActionDue('')
    setAddingAction(false)
  }

  const sendActionToSession = (item: MeetingActionItem) => {
    setDispatchSearch('')
    setDispatchAction(item)
  }

  const dispatchPayloadText = (item: MeetingActionItem) =>
    `请协助跟进会议《${title || '会议纪要'}》的行动项：${item.text}${item.owner ? `（负责人：${item.owner}）` : ''}${item.due_date ? `（截止：${item.due_date}）` : ''}`

  const dispatchToSelectedSession = (agentId: string, sessionId: string) => {
    if (!dispatchAction) return
    const payload = dispatchPayloadText(dispatchAction)
    setDispatchAction(null)
    navigate(`/chat/${encodeURIComponent(sessionId)}?agent_id=${encodeURIComponent(agentId)}`)
    window.setTimeout(() => {
      window.dispatchEvent(
        new CustomEvent('astrorder:quote-to-composer', {
          detail: { agentId, sessionId, text: payload },
        }),
      )
    }, 180)
    notifications.show({ color: 'teal', message: '已发送到所选对话' })
  }

  const localWorkspaceOptions = useMemo(() => {
    const seen = new Set<string>()
    const opts: Array<{ key: string; name: string; workspace: string }> = []
    for (const p of Object.values(projects)) {
      if (!p.workspace) continue
      const k = p.project_id || p.workspace
      if (seen.has(k)) continue
      seen.add(k)
      opts.push({ key: k, name: p.project_name || p.workspace, workspace: p.workspace })
    }
    return opts
  }, [projects])

  const speakerSuggestions = useMemo(() => {
    const base = ['我', ...parseParticipants(participantsText)]
    return Array.from(new Set(base.filter(Boolean)))
  }, [participantsText])

  const exportMarkdown = useMemo(() => {
    const heading = `# ${title || `会议纪要 ${meetingDate}`}`
    const transcriptMd = fullTimestampedTranscript.trim()
      ? `## 原始转写稿\n\n${fullTimestampedTranscript.trim()}`
      : ''
    if (exportScope === 'transcript') {
      return transcriptMd ? `${heading}\n\n${transcriptMd}` : minutesMarkdown
    }
    if (exportScope === 'combined') {
      const parts = [minutesMarkdown.trim(), transcriptMd].filter(Boolean)
      return parts.join('\n\n---\n\n')
    }
    return minutesMarkdown.trim() || (transcriptMd ? `${heading}\n\n${transcriptMd}` : '')
  }, [exportScope, fullTimestampedTranscript, meetingDate, minutesMarkdown, title])

  const recentSessions = useMemo(() => {
    const list = Object.values(sessions).sort((a, b) =>
      (b.updated_at || '').localeCompare(a.updated_at || ''),
    )
    const q = dispatchSearch.trim().toLowerCase()
    if (!q) return list.slice(0, 20)
    return list
      .filter(
        (s) =>
          (s.title || '').toLowerCase().includes(q) ||
          (s.project_name || '').toLowerCase().includes(q) ||
          (s.workspace || '').toLowerCase().includes(q),
      )
      .slice(0, 20)
  }, [dispatchSearch, sessions])

  const startEditSegment = (seg: MeetingTranscriptSegment) => {
    setEditingSegId(seg.id)
    setEditingSegText(seg.text)
  }

  const commitEditSegment = (segId: string) => {
    const trimmed = editingSegText.trim()
    if (trimmed) {
      updateSegment(segId, { text: trimmed })
    }
    setEditingSegId(null)
  }

  const startEditSpeaker = (seg: MeetingTranscriptSegment) => {
    setEditingSpeakerSegId(seg.id)
    setEditingSpeakerText(seg.speaker || '')
  }

  const commitEditSpeaker = (segId: string) => {
    updateSegment(segId, { speaker: editingSpeakerText.trim() })
    setEditingSpeakerSegId(null)
  }

  const startEditAction = (item: MeetingActionItem) => {
    setEditingActionId(item.id)
    setEditActionText(item.text)
    setEditActionOwner(item.owner || '')
    setEditActionDue(item.due_date || '')
    setEditActionPriority(item.priority || 'medium')
  }

  const commitEditAction = async (item: MeetingActionItem) => {
    if (!editActionText.trim()) return
    await updateActionItem(item, {
      text: editActionText.trim(),
      owner: editActionOwner.trim(),
      due_date: editActionDue.trim(),
      priority: editActionPriority,
    })
    setEditingActionId(null)
  }

  const filteredHistory = useMemo(() => {
    const q = historyQuery.trim().toLowerCase()
    if (!q) return savedMeetings
    return savedMeetings.filter(
      (m) =>
        m.title.toLowerCase().includes(q) ||
        (m.summary || '').toLowerCase().includes(q) ||
        (m.transcript || '').toLowerCase().includes(q),
    )
  }, [historyQuery, savedMeetings])

  const r2t2Running = r2t2Status?.state === 'running'
  const r2t2Starting = r2t2Status?.state === 'starting' || r2t2Status?.state === 'installing'
  const r2t2StatusText =
    recordState === 'recording'
      ? 'R2T2 实时转写中'
      : r2t2Running
        ? 'R2T2 已就绪'
        : r2t2Starting
          ? 'R2T2 加载中…'
          : 'R2T2 未启用'

  const effectiveModelLabel = settings.effective_model || settings.model || 'deepseek-chat'
  const cadenceLabel = settings.auto_summarize ? `每 ${settings.auto_summarize_interval_sec || 30} 秒` : '手动总结'
  const transcriptChars = fullPlainTranscript.replace(/\s+/g, '').length

  return (
    <div className="route-page mtg-studio" aria-label="实时会议工作台">
      <input
        ref={fileInputRef}
        type="file"
        accept=".mp3,.wav,.m4a,.webm,.ogg,.flac,.aac,audio/*"
        style={{ display: 'none' }}
        onChange={(e) => {
          const f = e.target.files?.[0]
          e.currentTarget.value = ''
          if (f) void uploadAudio(f)
        }}
      />

      {/* 1. Minimalist 48px Top Bar */}
      <header className="mtg-topbar">
        <div className="mtg-topbar-left">
          <button
            type="button"
            className={`mtg-ghost-btn${historyOpen ? ' is-active' : ''}`}
            aria-label="切换历史会议"
            onClick={() => setHistoryOpen((v) => !v)}
          >
            <IconHistory size={15} />
            <span>历史{savedMeetings.length > 0 ? ` (${savedMeetings.length})` : ''}</span>
          </button>
          <span className="mtg-vsep" aria-hidden="true" />
          <button
            type="button"
            className="mtg-engine-trigger"
            onClick={() => setConfigOpen(true)}
            title="点击查看或切换 R2T2 本地实时转写引擎"
          >
            <span
              className={`mtg-dot ${
                recordState === 'recording'
                  ? 'is-red'
                  : r2t2Running
                    ? 'is-green'
                    : r2t2Starting
                      ? 'is-amber'
                      : 'is-muted'
              }`}
            />
            <span>{r2t2StatusText}</span>
          </button>
        </div>

        <div className="mtg-topbar-right">
          <button
            type="button"
            className={`mtg-model-trigger${configOpen ? ' is-open' : ''}`}
            aria-label="配置会议总结模型"
            title={
              settings.effective_provider_name
                ? `当前使用 ${settings.effective_provider_name} / ${effectiveModelLabel}，点击切换`
                : '点击切换会议总结模型与自动总结频率'
            }
            onClick={() => setConfigOpen((v) => !v)}
          >
            <IconSparkles size={14} style={{ color: 'var(--mtg-violet)' }} />
            <span>
              {settings.effective_provider_name
                ? `${settings.effective_provider_name} · ${effectiveModelLabel}`
                : `总结模型 · ${effectiveModelLabel}`}
            </span>
            <span className="mtg-model-trigger-muted">· {cadenceLabel}</span>
            <IconChevronDown size={13} style={{ color: 'var(--mtg-faint)' }} />
          </button>

          <span className="mtg-vsep" aria-hidden="true" />

          <button
            type="button"
            className="mtg-ghost-btn"
            disabled={transcribingFile || isRecordingActive}
            onClick={() => fileInputRef.current?.click()}
          >
            {transcribingFile ? <IconLoader2 size={14} className="astrorder-spin" /> : <IconUpload size={14} />}
            <span>导入音频</span>
          </button>

          {hasContent && (
            <>
              {autoSaveState === 'saving' ? (
                <span className="mtg-autosave-badge" role="status">
                  <IconLoader2 size={12} className="astrorder-spin" />
                  <span>自动保存中…</span>
                </span>
              ) : autoSaveState === 'error' ? (
                <button
                  type="button"
                  className="mtg-autosave-badge is-error"
                  onClick={() => void persistCurrentMeeting(undefined, false)}
                >
                  <span>保存失败 · 点击重试</span>
                </button>
              ) : autoSaveState === 'saved' ? (
                <span className="mtg-autosave-badge is-saved" title={lastSavedAt ? `最近保存于 ${lastSavedAt}` : '已自动保存'}>
                  <IconCheck size={12} />
                  <span>已自动保存</span>
                </span>
              ) : null}
              <select
                className="mtg-export-scope"
                aria-label="导出内容范围"
                value={exportScope}
                onChange={(e) => setExportScope(e.currentTarget.value as 'minutes' | 'transcript' | 'combined')}
              >
                <option value="minutes">纪要</option>
                <option value="transcript">转写稿</option>
                <option value="combined">完整版</option>
              </select>
              <ExportMenu
                markdown={exportMarkdown}
                title={title || `会议纪要_${meetingDate}`}
                fileName={title || `会议纪要_${meetingDate}`}
                workspace={workspace || undefined}
                variant="button"
                buttonLabel="导出"
              />
            </>
          )}

          <button type="button" className="mtg-obsidian-btn" onClick={handleReset}>
            <IconPlus size={14} />
            <span>新建</span>
          </button>
        </div>
      </header>

      {/* 2. Workspace Body */}
      <div className="mtg-body">
        {historyOpen && (
          <aside className="mtg-history-rail" aria-label="历史会议列表">
            <div className="mtg-history-head">
              <span>历史会议</span>
              <span>{savedMeetings.length}</span>
            </div>
            <div className="mtg-history-search">
              <IconSearch size={13} />
              <input
                type="text"
                placeholder="搜索纪要或转写…"
                value={historyQuery}
                onChange={(e) => setHistoryQuery(e.target.value)}
              />
            </div>
            <div className="mtg-history-list">
              {filteredHistory.length === 0 ? (
                <div style={{ padding: '16px 8px', fontSize: 12, color: 'var(--mtg-faint)' }}>
                  暂无会议记录
                </div>
              ) : (
                filteredHistory.map((rec) => {
                  const openCnt = (rec.action_items || []).filter((a) => a.status !== 'done').length
                  const dur = formatShortMinutes(rec.duration_seconds)
                  return (
                    <div
                      key={rec.id}
                      className={`mtg-history-row${activeId === rec.id ? ' is-active' : ''}`}
                      onClick={() => {
                        if (openMeetingRecord(rec)) {
                          setDocTab('structured')
                        }
                      }}
                    >
                      <div className="mtg-history-row-main">
                        <span className="mtg-history-title">{rec.title}</span>
                        <span className="mtg-history-sub">
                          {rec.meeting_date.slice(5)}
                          {dur ? ` · ${dur}` : ''}
                          {openCnt > 0 ? ` · ${openCnt} 待办` : ''}
                        </span>
                      </div>
                      <button
                        type="button"
                        className="mtg-history-del"
                        aria-label={`删除 ${rec.title}`}
                        onClick={(e) => {
                          e.stopPropagation()
                          void deleteMeeting(rec.id)
                        }}
                      >
                        <IconTrash size={13} />
                      </button>
                    </div>
                  )
                })
              )}
            </div>
          </aside>
        )}

        {!hasContent ? (
          /* State A: Airy Ready-to-Record Launch Canvas */
          <main className="mtg-launch-canvas">
            {crashDraft && (
              <div className="mtg-crash-banner" role="status">
                <span>
                  检测到上次未保存的会议草稿
                  {crashDraft.title ? `「${crashDraft.title}」` : ''}
                  （{crashDraft.segments?.length || 0} 段转写）
                </span>
                <div className="mtg-crash-banner-ops">
                  <button
                    type="button"
                    className="mtg-obsidian-btn"
                    style={{ height: 26, fontSize: 12, padding: '0 10px' }}
                    onClick={() => {
                      if (restoreCrashDraft()) {
                        setCrashDraft(null)
                        notifications.show({ color: 'teal', message: '已恢复未保存的会议草稿' })
                      }
                    }}
                  >
                    恢复草稿
                  </button>
                  <button
                    type="button"
                    className="mtg-inline-link"
                    onClick={() => {
                      dismissCrashDraft()
                      setCrashDraft(null)
                    }}
                  >
                    忽略
                  </button>
                </div>
              </div>
            )}
            <div className="mtg-launch-header">
              <div className="mtg-launch-date">
                {formatReadableDate(meetingDate)} · {r2t2Running ? '准备就绪' : r2t2StatusText}
              </div>
              <input
                type="text"
                className="mtg-title-input"
                placeholder="未命名会议"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
              />
            </div>

            {pasteMode ? (
              <div style={{ width: '100%', maxWidth: 560, display: 'flex', flexDirection: 'column', gap: 12 }}>
                <textarea
                  className="mtg-md-editor"
                  style={{ minHeight: 200, padding: 14, background: 'var(--mtg-rail)', borderRadius: 12 }}
                  placeholder="粘贴会议记录或转写文本…"
                  value={pasteDraft}
                  onChange={(e) => setPasteDraft(e.target.value)}
                />
                <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
                  <button type="button" className="mtg-ghost-btn" onClick={() => setPasteMode(false)}>
                    返回录制
                  </button>
                  <button
                    type="button"
                    className="mtg-obsidian-btn"
                    disabled={!pasteDraft.trim() || summarizing}
                    onClick={() => void handleCommitPaste()}
                  >
                    {summarizing ? '提炼中…' : '提炼纪要'}
                  </button>
                </div>
              </div>
            ) : (
              <div className="mtg-launch-hero">
                <button
                  type="button"
                  className="mtg-mic-orb"
                  aria-label="开始实时录制"
                  onClick={() => void startRecording(0)}
                >
                  <IconMicrophone size={28} stroke={1.8} />
                  <span className="mtg-mic-orb-dot" aria-hidden="true" />
                </button>
                <h2 className="mtg-launch-cta-title">开始实时录制</h2>
                <p className="mtg-launch-cta-sub">
                  R2T2 实时转写并自动定期存档，录音结束自动整理终稿纪要
                </p>

                <div className="mtg-launch-tools">
                  <select
                    className="mtg-mic-select"
                    aria-label="选择麦克风"
                    value={selectedDeviceId}
                    onChange={(e) => setSelectedDeviceId(e.target.value)}
                  >
                    {devices.length === 0 ? (
                      <option value="default">默认麦克风</option>
                    ) : (
                      devices.map((d) => (
                        <option key={d.deviceId} value={d.deviceId}>
                          {d.label}
                        </option>
                      ))
                    )}
                  </select>
                  <span aria-hidden="true">·</span>
                  <button type="button" className="mtg-inline-link" onClick={() => fileInputRef.current?.click()}>
                    导入本地录音
                  </button>
                  <span aria-hidden="true">·</span>
                  <button type="button" className="mtg-inline-link" onClick={() => setPasteMode(true)}>
                    粘贴记录
                  </button>
                </div>

                {!r2t2Running && (
                  <div className="mtg-r2t2-callout">
                    <span className={`mtg-dot ${r2t2Starting ? 'is-amber' : 'is-muted'}`} />
                    <span>{r2t2Starting ? '正在加载本地 R2T2 模型…' : '本地 R2T2 听写尚未启动'}</span>
                    {!r2t2Starting && (
                      <button
                        type="button"
                        className="mtg-inline-link"
                        style={{ color: 'var(--mtg-ink)', fontWeight: 550 }}
                        disabled={r2t2Loading}
                        onClick={() => void toggleR2t2(true)}
                      >
                        立即启动
                      </button>
                    )}
                  </div>
                )}
              </div>
            )}
          </main>
        ) : (
          /* State B: Open Dual-Pane Real-Time Editorial Studio */
          <div className="mtg-dual-stage">
            {/* Left 38%: Open Flowing R2T2 Transcript Stream */}
            <section className="mtg-transcript-col" aria-label="实时转写流">
              <div className="mtg-col-header">
                <div className="mtg-col-header-left">
                  <span>{isRecordingActive ? '实时转写' : '录音转写时间轴'}</span>
                  {segments.length > 0 && <span className="mtg-count-pill">{segments.length} 段</span>}
                </div>
                <button
                  type="button"
                  className="mtg-follow-toggle"
                  onClick={() => setFollowScroll((v) => !v)}
                >
                  <span>跟随最新</span>
                  <span className={`mtg-toggle${followScroll ? ' is-on' : ''}`} style={{ width: 28, height: 16 }}>
                    <span
                      className="mtg-toggle-thumb"
                      style={{
                        width: 12,
                        height: 12,
                        transform: followScroll ? 'translateX(12px)' : 'translateX(0)',
                      }}
                    />
                  </span>
                </button>
              </div>

              <div ref={transcriptScrollRef} className="mtg-transcript-scroll">
                <datalist id="mtg-speaker-suggestions">
                  {speakerSuggestions.map((sp) => (
                    <option key={sp} value={sp} />
                  ))}
                </datalist>
                {segments.map((seg) => {
                  const isEditingText = editingSegId === seg.id
                  const isEditingSpeaker = editingSpeakerSegId === seg.id
                  return (
                    <div
                      key={seg.id}
                      id={`mtg-seg-${seg.id}`}
                      className={`mtg-seg-row${seg.bookmarked ? ' is-bookmarked' : ''}${seg.kind === 'note' ? ' is-note' : ''}${flashSegId === seg.id ? ' is-flash' : ''}`}
                    >
                      <div className="mtg-seg-ts">
                        <span>{seg.timestamp}</span>
                        {seg.bookmarked && <IconBookmarkFilled size={11} />}
                      </div>
                      <div className="mtg-seg-body">
                        <div className="mtg-seg-head-row">
                          {seg.kind === 'note' && <span className="mtg-seg-note-label">笔记</span>}
                          {isEditingSpeaker ? (
                            <input
                              type="text"
                              list="mtg-speaker-suggestions"
                              className="mtg-seg-speaker-input"
                              placeholder="发言人…"
                              aria-label="编辑发言人"
                              autoFocus
                              value={editingSpeakerText}
                              onChange={(e) => setEditingSpeakerText(e.currentTarget.value)}
                              onBlur={() => commitEditSpeaker(seg.id)}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter') {
                                  e.preventDefault()
                                  commitEditSpeaker(seg.id)
                                } else if (e.key === 'Escape') {
                                  setEditingSpeakerSegId(null)
                                }
                              }}
                            />
                          ) : (
                            <button
                              type="button"
                              className={`mtg-seg-speaker${seg.speaker ? ' has-speaker' : ''}`}
                              title="点击标记或修改发言人"
                              onClick={() => startEditSpeaker(seg)}
                            >
                              <IconUser size={10} />
                              <span>{seg.speaker || '标记发言人'}</span>
                            </button>
                          )}
                        </div>
                        {isEditingText ? (
                          <textarea
                            className="mtg-seg-edit-box"
                            aria-label="编辑转写片段"
                            autoFocus
                            rows={2}
                            value={editingSegText}
                            onChange={(e) => setEditingSegText(e.currentTarget.value)}
                            onBlur={() => commitEditSegment(seg.id)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter' && !e.shiftKey) {
                                e.preventDefault()
                                commitEditSegment(seg.id)
                              } else if (e.key === 'Escape') {
                                setEditingSegId(null)
                              }
                            }}
                          />
                        ) : (
                          <span onDoubleClick={() => startEditSegment(seg)} title="双击修正转写错字">
                            {seg.text}
                          </span>
                        )}
                      </div>
                      <div className="mtg-seg-actions">
                        <button
                          type="button"
                          className="mtg-seg-action-btn"
                          aria-label="编辑转写文本"
                          title="编辑修正错字"
                          onClick={() => startEditSegment(seg)}
                        >
                          <IconPencil size={12} />
                        </button>
                        <button
                          type="button"
                          className={`mtg-seg-action-btn${seg.bookmarked ? ' is-active' : ''}`}
                          aria-label={seg.bookmarked ? '取消重点标记' : '标记重点'}
                          onClick={() => toggleBookmarkSegment(seg.id)}
                        >
                          {seg.bookmarked ? <IconBookmarkFilled size={13} /> : <IconBookmark size={13} />}
                        </button>
                      </div>
                    </div>
                  )
                })}

                {(recordState === 'recording' || liveText.trim().length > 0) && (
                  <div className="mtg-seg-row is-live">
                    <div className="mtg-seg-ts">{formatDurationClock(liveStartSec)}</div>
                    <div className="mtg-seg-body">
                      {liveText || <span style={{ color: 'var(--mtg-faint)' }}>正在聆听…</span>}
                      {recordState === 'recording' && <span className="mtg-live-caret" aria-hidden="true" />}
                      {recordState === 'recording' && (
                        <div className="mtg-live-caption">
                          <span className="mtg-dot is-green" />
                          <span>R2T2 实时识别中…</span>
                        </div>
                      )}
                    </div>
                  </div>
                )}
              </div>

              <div className="mtg-quick-note-bar">
                <IconPencil size={14} style={{ color: 'var(--mtg-faint)', flexShrink: 0 }} />
                <input
                  type="text"
                  placeholder="记一句会议笔记，按 Enter 插入时间轴…"
                  value={quickNote}
                  onChange={(e) => setQuickNote(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault()
                      handleInsertQuickNote()
                    }
                  }}
                />
              </div>
            </section>

            {/* Right 62%: Pure White Editorial Live AI Document */}
            <section className="mtg-editor-col" aria-label="实时 AI 纪要文档">
              <div className="mtg-doc-container">
                <input
                  type="text"
                  className="mtg-title-input"
                  placeholder="未命名会议"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                />

                <div className="mtg-doc-meta">
                  <input
                    type="date"
                    className="mtg-date-input"
                    aria-label="会议日期"
                    value={meetingDate}
                    onChange={(e) => setMeetingDate(e.currentTarget.value)}
                  />
                  <span aria-hidden="true">·</span>
                  <input
                    type="text"
                    className="mtg-meta-input"
                    placeholder="添加参会人…"
                    value={participantsText}
                    onChange={(e) => setParticipantsText(e.target.value)}
                  />
                  {localWorkspaceOptions.length > 0 && (
                    <>
                      <span aria-hidden="true">·</span>
                      <label className="mtg-workspace-wrap" title="关联到办公空间，周报将自动归类并支持直接导出到该目录">
                        <IconFolder size={12} />
                        <select
                          className="mtg-workspace-select"
                          aria-label="关联工作空间"
                          value={projectKey}
                          onChange={(e) => {
                            const val = e.currentTarget.value
                            const matched = localWorkspaceOptions.find((o) => o.key === val)
                            setWorkspaceLink(matched?.key || '', matched?.workspace || '')
                          }}
                        >
                          <option value="">关联工作空间…</option>
                          {localWorkspaceOptions.map((o) => (
                            <option key={o.key} value={o.key}>
                              {o.name}
                            </option>
                          ))}
                        </select>
                      </label>
                    </>
                  )}
                  <span aria-hidden="true">·</span>
                  <span className="mtg-live-sync-status">
                    {finalizingSummary ? (
                      <>
                        <IconLoader2 size={12} className="astrorder-spin" />
                        <span>录音已结束，正在整理最终整体纪要…</span>
                      </>
                    ) : summarizing ? (
                      <>
                        <IconLoader2 size={12} className="astrorder-spin" />
                        <span>正在提炼纪要…</span>
                      </>
                    ) : recordState === 'recording' && settings.auto_summarize ? (
                      <>
                        <IconSparkles size={12} />
                        <span>
                          边听边总结
                          {lastSyncedAt ? ` · 已更新至 ${lastSyncedAt}` : ` · ${nextSyncCountdown}s 后更新`}
                        </span>
                      </>
                    ) : lastSyncedAt ? (
                      <>
                        <IconSparkles size={12} />
                        <span>已更新至 {lastSyncedAt}</span>
                      </>
                    ) : (
                      <span>{modelName || effectiveModelLabel}</span>
                    )}
                  </span>
                  <button
                    type="button"
                    className="mtg-inline-link"
                    disabled={summarizing}
                    onClick={() => void runSummarize({ incremental: false, autoSave: true })}
                  >
                    {summary || topics.length > 0 ? '重新整体总结' : '立即总结'}
                  </button>
                </div>

                <div className="mtg-doc-tabs" role="tablist">
                  <button
                    type="button"
                    role="tab"
                    aria-selected={docTab === 'structured'}
                    className={`mtg-doc-tab${docTab === 'structured' ? ' is-active' : ''}`}
                    onClick={() => setDocTab('structured')}
                  >
                    {isRecordingActive ? '实时纪要' : '会议纪要'}
                  </button>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={docTab === 'transcript'}
                    className={`mtg-doc-tab${docTab === 'transcript' ? ' is-active' : ''}`}
                    onClick={() => setDocTab('transcript')}
                  >
                    转写原文
                  </button>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={docTab === 'markdown'}
                    className={`mtg-doc-tab${docTab === 'markdown' ? ' is-active' : ''}`}
                    onClick={() => setDocTab('markdown')}
                  >
                    Markdown 原文
                  </button>
                </div>

                {docTab === 'structured' ? (
                  <div className="mtg-doc-sections">
                    {!summary &&
                    topics.length === 0 &&
                    decisions.length === 0 &&
                    risks.length === 0 &&
                    actionItems.length === 0 ? (
                      <div className="mtg-empty-summary">
                        {finalizingSummary
                          ? '录音已自动保存，正在基于完整录音原文进行全篇整体总结…'
                          : recordState === 'recording'
                            ? `正在实时聆听并由 R2T2 转写，${settings.auto_summarize ? `每 ${settings.auto_summarize_interval_sec || 30} 秒自动提炼结构化纪要与行动项` : '录音结束将自动整体总结并保存'}。`
                            : '点击上方「立即总结」即可按当前模型提炼核心摘要、议题脉络、决议与行动项。'}
                      </div>
                    ) : (
                      <>
                        {summary && (
                          <div>
                            <div className="mtg-section-label">核心摘要</div>
                            <p className="mtg-summary-prose">{summary}</p>
                          </div>
                        )}

                        {topics.length > 0 && (
                          <div>
                            <div className="mtg-section-label">议题脉络</div>
                            <ul className="mtg-topic-list">
                              {topics.map((t, idx) => (
                                <li key={idx} className="mtg-topic-item">
                                  <span className="mtg-topic-bullet" aria-hidden="true" />
                                  <div>
                                    <strong style={{ fontWeight: 600, color: 'var(--mtg-ink)' }}>{t.title}</strong>
                                    {t.timestamp && (
                                      <button
                                        type="button"
                                        className="mtg-topic-ts"
                                        onClick={() => scrollToTimestamp(t.timestamp!)}
                                        title="定位到对应转写位置"
                                      >
                                        {t.timestamp}
                                      </button>
                                    )}
                                    {t.detail && <span>— {t.detail}</span>}
                                  </div>
                                </li>
                              ))}
                            </ul>
                          </div>
                        )}

                        {(decisions.length > 0 || risks.length > 0) && (
                          <div>
                            <div className="mtg-section-label">决议与风险</div>
                            <ul className="mtg-signal-list">
                              {decisions.map((d, idx) => (
                                <li key={`d-${idx}`} className="mtg-signal-item">
                                  <IconCheck size={15} className="mtg-signal-icon is-decision" />
                                  <span>{d}</span>
                                </li>
                              ))}
                              {risks.map((r, idx) => (
                                <li key={`r-${idx}`} className="mtg-signal-item">
                                  <IconAlertTriangle size={14} className="mtg-signal-icon is-risk" />
                                  <span>{r}</span>
                                </li>
                              ))}
                            </ul>
                          </div>
                        )}
                      </>
                    )}

                    {/* Action Items Checklist */}
                    <div>
                      <div className="mtg-section-label">
                        <span>行动项{actionItems.length > 0 ? ` (${actionItems.length})` : ''}</span>
                        <button
                          type="button"
                          className="mtg-inline-link"
                          onClick={() => setAddingAction((v) => !v)}
                        >
                          <IconPlus size={12} />
                          <span>添加</span>
                        </button>
                      </div>

                      {actionItems.length > 0 && (
                        <div className="mtg-action-list">
                          {actionItems.map((item) => {
                            const done = item.status === 'done'
                            const isEditingAct = editingActionId === item.id
                            if (isEditingAct) {
                              return (
                                <div key={item.id} className="mtg-add-action-row">
                                  <input
                                    type="text"
                                    style={{ flex: 2 }}
                                    aria-label="编辑行动项内容"
                                    value={editActionText}
                                    onChange={(e) => setEditActionText(e.currentTarget.value)}
                                    onKeyDown={(e) => {
                                      if (e.key === 'Enter') {
                                        e.preventDefault()
                                        void commitEditAction(item)
                                      } else if (e.key === 'Escape') {
                                        setEditingActionId(null)
                                      }
                                    }}
                                    autoFocus
                                  />
                                  <input
                                    type="text"
                                    style={{ width: 68 }}
                                    placeholder="负责人"
                                    value={editActionOwner}
                                    onChange={(e) => setEditActionOwner(e.currentTarget.value)}
                                  />
                                  <input
                                    type="date"
                                    style={{ width: 112, fontSize: 12, color: 'var(--mtg-muted)' }}
                                    aria-label="编辑截止日期"
                                    value={editActionDue}
                                    onChange={(e) => setEditActionDue(e.currentTarget.value)}
                                  />
                                  <select
                                    className="mtg-export-scope"
                                    aria-label="编辑优先级"
                                    value={editActionPriority}
                                    onChange={(e) => setEditActionPriority(e.currentTarget.value)}
                                  >
                                    <option value="high">高</option>
                                    <option value="medium">中</option>
                                    <option value="low">低</option>
                                  </select>
                                  <button type="button" className="mtg-inline-link" onClick={() => void commitEditAction(item)}>
                                    保存
                                  </button>
                                  <button type="button" className="mtg-inline-link" onClick={() => setEditingActionId(null)}>
                                    <IconX size={12} />
                                  </button>
                                </div>
                              )
                            }
                            return (
                              <div key={item.id} className={`mtg-action-item${done ? ' is-done' : ''}`}>
                                <button
                                  type="button"
                                  className={`mtg-check-circle${done ? ' is-checked' : ''}`}
                                  aria-label={`完成行动项 ${item.text}`}
                                  onClick={() => void toggleActionDone(item)}
                                >
                                  {done && <IconCheck size={11} stroke={2.8} />}
                                </button>
                                <span className="mtg-action-text">{item.text}</span>
                                {item.priority === 'high' && (
                                  <span className="mtg-priority-pill is-high">高</span>
                                )}
                                {(item.owner || item.due_date) && (
                                  <span className="mtg-action-meta">
                                    {[item.owner, item.due_date ? item.due_date.slice(5) : '']
                                      .filter(Boolean)
                                      .join(' · ')}
                                  </span>
                                )}
                                <div className="mtg-action-hover-ops">
                                  <button
                                    type="button"
                                    className="mtg-action-icon-btn"
                                    title="编辑行动项"
                                    aria-label="编辑行动项"
                                    onClick={() => startEditAction(item)}
                                  >
                                    <IconPencil size={13} />
                                  </button>
                                  <button
                                    type="button"
                                    className="mtg-action-icon-btn"
                                    title="发送到 AI 对话协助跟进"
                                    aria-label="派发给会话"
                                    onClick={() => sendActionToSession(item)}
                                  >
                                    <IconMessageForward size={13} />
                                  </button>
                                  <button
                                    type="button"
                                    className="mtg-action-icon-btn"
                                    title="移除行动项"
                                    aria-label="移除行动项"
                                    onClick={() => removeActionItem(item.id)}
                                  >
                                    <IconTrash size={13} />
                                  </button>
                                </div>
                              </div>
                            )
                          })}
                        </div>
                      )}

                      {addingAction && (
                        <div className="mtg-add-action-row">
                          <input
                            type="text"
                            style={{ flex: 2 }}
                            placeholder="输入行动项内容，按 Enter 添加…"
                            value={newActionText}
                            onChange={(e) => setNewActionText(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                handleAddActionItem()
                              }
                            }}
                            autoFocus
                          />
                          <input
                            type="text"
                            style={{ width: 72 }}
                            placeholder="负责人"
                            value={newActionOwner}
                            onChange={(e) => setNewActionOwner(e.target.value)}
                          />
                          <input
                            type="date"
                            style={{ width: 118, fontSize: 12, color: 'var(--mtg-muted)' }}
                            aria-label="截止日期"
                            value={newActionDue}
                            onChange={(e) => setNewActionDue(e.target.value)}
                          />
                          <button type="button" className="mtg-inline-link" onClick={handleAddActionItem}>
                            确定
                          </button>
                        </div>
                      )}
                    </div>
                  </div>
                ) : docTab === 'transcript' ? (
                  <div className="mtg-raw-transcript-pane" aria-label="录音转文字原文">
                    <div className="mtg-raw-transcript-toolbar">
                      <span className="mtg-raw-transcript-meta">
                        共 {segments.length} 段 · {transcriptChars} 字
                      </span>
                      <div className="mtg-raw-transcript-ops">
                        <div className="mtg-raw-mode-switch" role="group" aria-label="原文格式">
                          <button
                            type="button"
                            className={`mtg-raw-mode-btn${transcriptViewMode === 'timestamped' ? ' is-active' : ''}`}
                            onClick={() => setTranscriptViewMode('timestamped')}
                          >
                            带时间戳
                          </button>
                          <button
                            type="button"
                            className={`mtg-raw-mode-btn${transcriptViewMode === 'plain' ? ' is-active' : ''}`}
                            onClick={() => setTranscriptViewMode('plain')}
                          >
                            连续纯文本
                          </button>
                        </div>
                        <button
                          type="button"
                          className="mtg-inline-link"
                          disabled={!fullTimestampedTranscript.trim()}
                          onClick={() => void handleCopyTranscript()}
                        >
                          {copiedTranscript ? <IconCheck size={13} /> : <IconCopy size={13} />}
                          <span>{copiedTranscript ? '已复制' : '复制原文'}</span>
                        </button>
                      </div>
                    </div>

                    {!fullTimestampedTranscript.trim() ? (
                      <div className="mtg-empty-summary">暂无录音转文字原文。</div>
                    ) : transcriptViewMode === 'plain' ? (
                      <div className="mtg-raw-transcript-plain">{fullPlainTranscript}</div>
                    ) : (
                      <div className="mtg-raw-transcript-lines">
                        {segments.length > 0 ? (
                          segments.map((seg) => (
                            <div key={seg.id} className="mtg-raw-line">
                              <span className="mtg-raw-line-ts">{seg.timestamp}</span>
                              <span className="mtg-raw-line-text">
                                {seg.kind === 'note' && <span className="mtg-seg-note-label">笔记</span>}
                                {seg.text}
                              </span>
                            </div>
                          ))
                        ) : (
                          <div className="mtg-raw-transcript-plain">{rawTranscript}</div>
                        )}
                        {liveText.trim() && (
                          <div className="mtg-raw-line is-live">
                            <span className="mtg-raw-line-ts">{formatDurationClock(liveStartSec)}</span>
                            <span className="mtg-raw-line-text">{liveText}</span>
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                ) : (
                  <div style={{ marginTop: 22 }}>
                    <div style={{ display: 'flex', justifyContent: 'flex-end', marginBottom: 10 }}>
                      <button
                        type="button"
                        className="mtg-inline-link"
                        onClick={() => setMdEditing((v) => !v)}
                      >
                        {mdEditing ? '完成编辑' : '编辑原文'}
                      </button>
                    </div>
                    {mdEditing ? (
                      <textarea
                        className="mtg-md-editor"
                        value={minutesMarkdown}
                        onChange={(e) => setMinutesMarkdown(e.target.value)}
                        placeholder="# 会议纪要…"
                      />
                    ) : minutesMarkdown.trim() ? (
                      <MarkdownContent value={minutesMarkdown} />
                    ) : (
                      <div className="mtg-empty-summary">暂无 Markdown 完整稿，请先提炼纪要。</div>
                    )}
                  </div>
                )}
              </div>
            </section>
          </div>
        )}

        {/* 3. Floating Bottom-Center Glass Recording Dock */}
        {(isRecordingActive || hasContent) && (
          <div className="mtg-floating-dock-wrap">
            {isRecordingActive ? (
              <div className="mtg-floating-dock" role="region" aria-label="实时录制控制栏">
                <span className={`mtg-dot ${recordState === 'recording' ? 'is-red' : 'is-amber'}`} />
                <span className="mtg-dock-timer">{formatDurationClock(elapsedSeconds)}</span>

                <div className="mtg-waveform" aria-hidden="true">
                  {waveform.map((amp, idx) => (
                    <span
                      key={idx}
                      className="mtg-wave-bar"
                      style={{ height: `${Math.round(4 + amp * 18)}px` }}
                    />
                  ))}
                </div>

                <span className="mtg-vsep" style={{ height: 18 }} aria-hidden="true" />

                <button
                  type="button"
                  className="mtg-dock-btn"
                  onClick={() => (recordState === 'recording' ? pauseRecording() : resumeRecording())}
                >
                  {recordState === 'recording' ? <IconPlayerPause size={14} /> : <IconPlayerPlay size={14} />}
                  <span>{recordState === 'recording' ? '暂停' : '继续'}</span>
                </button>

                <button
                  type="button"
                  className="mtg-dock-btn"
                  onClick={() => {
                    const committed = bookmarkNow()
                    notifications.show({
                      color: 'indigo',
                      message: committed ? '已标记当前片段为重点' : '已标记，下句识别完成将自动高亮',
                    })
                  }}
                >
                  <IconBookmark size={14} />
                  <span>标记</span>
                </button>

                <button
                  type="button"
                  className="mtg-dock-btn"
                  title="弹出独立悬浮录音胶囊"
                  aria-label="弹出录音胶囊"
                  onClick={() => void openFloatingCapsule()}
                >
                  <IconPictureInPicture size={14} />
                  <span>胶囊</span>
                </button>

                <button
                  type="button"
                  className="mtg-dock-stop"
                  disabled={recordState === 'stopping'}
                  onClick={() => stopRecording()}
                >
                  <IconPlayerStop size={13} />
                  <span>{recordState === 'stopping' ? '收尾中…' : '结束'}</span>
                </button>
              </div>
            ) : (
              <div className="mtg-floating-dock" style={{ padding: '0 10px 0 16px', gap: 10 }}>
                <span className="mtg-dock-finished-label">本次录音已结束</span>
                <button
                  type="button"
                  className="mtg-obsidian-btn"
                  onClick={() => void startNewMeetingRecording()}
                >
                  <IconMicrophone size={14} />
                  <span>开始新会议录制</span>
                </button>
              </div>
            )}
          </div>
        )}
      </div>

      {/* 4. Floating Summary Model & R2T2 Configuration Popover */}
      <MeetingModelConfigPopover
        opened={configOpen}
        settings={settings}
        r2t2Status={r2t2Status}
        r2t2Loading={r2t2Loading}
        onClose={() => setConfigOpen(false)}
        onSaved={(next) => setSettings(next)}
        onToggleR2t2={toggleR2t2}
        onRestartR2t2={restartR2t2}
      />

      {/* 5. Session Picker Modal for Dispatching Action Item to AI Chat */}
      <Modal
        opened={dispatchAction !== null}
        onClose={() => setDispatchAction(null)}
        title="发送到 AI 对话协助处理"
        size="sm"
        withinPortal
      >
        <Stack gap="sm">
          {dispatchAction && (
            <Text size="xs" c="dimmed">
              行动项：{dispatchAction.text}
            </Text>
          )}
          <TextInput
            size="xs"
            leftSection={<IconSearch size={13} />}
            placeholder="搜索对话标题或工作空间…"
            value={dispatchSearch}
            onChange={(e) => setDispatchSearch(e.currentTarget.value)}
          />
          <div className="mtg-session-picker-list">
            {recentSessions.length === 0 ? (
              <Text size="xs" c="dimmed" ta="center" py="md">
                暂无可派发的对话，可直接复制内容后新建对话粘贴。
              </Text>
            ) : (
              recentSessions.map((s) => (
                <button
                  key={`${s.agent_id}:${s.id}`}
                  type="button"
                  className="mtg-session-picker-row"
                  onClick={() => dispatchToSelectedSession(s.agent_id, s.id)}
                >
                  <span className="mtg-session-picker-title">{s.title || '未命名对话'}</span>
                  {s.project_name && <span className="mtg-session-picker-sub">{s.project_name}</span>}
                </button>
              ))
            )}
          </div>
          <Group justify="space-between">
            <Button
              size="xs"
              variant="subtle"
              color="gray"
              leftSection={<IconCopy size={13} />}
              onClick={() => {
                if (!dispatchAction) return
                void navigator.clipboard.writeText(dispatchPayloadText(dispatchAction))
                setDispatchAction(null)
                notifications.show({ color: 'teal', message: '已复制到剪贴板' })
              }}
            >
              仅复制到剪贴板
            </Button>
            <Button size="xs" variant="subtle" color="gray" onClick={() => setDispatchAction(null)}>
              取消
            </Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
