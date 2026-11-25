import { create } from 'zustand'
import { notifications } from '@mantine/notifications'
import {
  api,
  type LocalVoicePluginStatus,
  type MeetingActionItem,
  type MeetingRecord,
  type MeetingSettings,
  type MeetingTopic,
  type MeetingTranscriptSegment,
} from '../../api/client'
import { buildLocalVoiceStreamUrl, downsampleToPcm16 } from '../chat/localVoiceStream'
import { formatDurationClock, type AudioDeviceOption, type LiveRecordState } from './useMeetingLiveRecorder'

export const MEETING_SETTINGS_SYNC_KEY = 'astrorder:meeting-settings:sync'
export const AI_PROVIDERS_SYNC_KEY = 'astrorder:ai-providers:sync'

export function broadcastMeetingSettingsSync() {
  try {
    localStorage.setItem(MEETING_SETTINGS_SYNC_KEY, String(Date.now()))
  } catch {}
}

export function broadcastAiProvidersSync() {
  try {
    localStorage.setItem(AI_PROVIDERS_SYNC_KEY, String(Date.now()))
  } catch {}
}

export const DEFAULT_MEETING_SETTINGS: MeetingSettings = {
  model_source: 'global',
  provider_id: '',
  provider_name: '',
  base_url: 'https://api.deepseek.com/v1',
  api_key_set: false,
  model: 'deepseek-chat',
  auto_summarize: true,
  auto_summarize_interval_sec: 30,
  summary_style: 'concise',
  effective_model: 'deepseek-chat',
  effective_provider_name: '',
  effective_configured: false,
  global_model: 'deepseek-chat',
  global_provider_name: '',
  global_configured: false,
}

export interface DesktopMeetingCapsuleState {
  active: boolean
  paused: boolean
  stopping: boolean
  elapsed: string
  text: string
  bars: number[]
}

export type AutoSaveStatus = 'idle' | 'saving' | 'saved' | 'error'

interface DesktopMeetingBridge {
  showMeetingCapsule?: (state: DesktopMeetingCapsuleState) => Promise<void>
  updateMeetingCapsule?: (state: DesktopMeetingCapsuleState) => Promise<void>
  hideMeetingCapsule?: () => Promise<void>
}

const WAVE_BARS = 18
const ZERO_WAVE = Array.from({ length: WAVE_BARS }, () => 0.14)
const PERIODIC_AUTO_SAVE_MS = 15_000
const EDIT_AUTO_SAVE_DEBOUNCE_MS = 1_200
const DRAFT_BACKUP_STORAGE_KEY = 'astrorder:meeting-crash-draft'

export function todayDateString(): string {
  const d = new Date()
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

function nowClockString(): string {
  const d = new Date()
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:${String(d.getSeconds()).padStart(2, '0')}`
}

export function parseParticipants(raw: string): string[] {
  return raw
    .split(/[,，、;\n]+/)
    .map((s) => s.trim())
    .filter(Boolean)
}

export function segmentsToTranscript(
  segments: MeetingTranscriptSegment[],
  liveText: string,
  liveSec: number,
): string {
  const lines = segments.map((s) => {
    const prefix = s.kind === 'note' ? `[${s.timestamp}] [笔记]` : `[${s.timestamp}]`
    const mark = s.bookmarked ? ' [重点]' : ''
    const speakerPrefix = s.speaker?.trim() ? `${s.speaker.trim()}：` : ''
    return `${prefix}${mark} ${speakerPrefix}${s.text}`
  })
  if (liveText.trim()) {
    lines.push(`[${formatDurationClock(liveSec)}] ${liveText.trim()}`)
  }
  return lines.join('\n')
}

export function segmentsToPlainTranscript(
  segments: MeetingTranscriptSegment[],
  liveText: string,
): string {
  const parts = segments
    .filter((s) => s.kind !== 'note')
    .map((s) => {
      const txt = s.text.trim()
      if (!txt) return ''
      return s.speaker?.trim() ? `${s.speaker.trim()}：${txt}` : txt
    })
    .filter(Boolean)
  if (liveText.trim()) {
    parts.push(liveText.trim())
  }
  return parts.join('\n')
}

export function transcriptToSegments(raw: string): MeetingTranscriptSegment[] {
  return raw
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line, idx) => {
      const m = line.match(/^\[?(\d{1,2}:\d{2})\]?\s*(.+)$/)
      const ts = m ? m[1] : formatDurationClock(idx * 15)
      let text = m ? m[2] : line
      let kind: 'speech' | 'note' = 'speech'
      let bookmarked = false
      if (text.startsWith('[笔记]')) {
        kind = 'note'
        text = text.slice('[笔记]'.length).trim()
      }
      if (text.startsWith('[重点]')) {
        bookmarked = true
        text = text.slice('[重点]'.length).trim()
      }
      return {
        id: `seg_imp_${idx}_${Date.now()}`,
        timestamp: ts,
        seconds: idx * 15,
        text,
        kind,
        bookmarked,
      }
    })
}

export function sampleCapsuleBars(waveform: number[]): number[] {
  const indices = [1, 4, 8, 12, 16]
  return indices.map((idx, i) => {
    const raw = waveform[idx] ?? 0.2
    const floor = i === 0 || i === 2 ? 0.24 : 0.18
    return Math.max(floor, Math.min(1, raw))
  })
}

export function buildCapsuleDisplayText(
  recordState: LiveRecordState,
  liveText: string,
): string {
  if (recordState === 'starting') return '正在连接实时转写...'
  if (recordState === 'stopping') return '正在整理会议纪要...'
  if (recordState === 'paused') return '录音已暂停'
  const cleaned = liveText.replace(/\s+/g, ' ').trim()
  if (!cleaned) return '实时转写中...'
  if (cleaned.length <= 26) return cleaned
  return `…${cleaned.slice(-25)}`
}

function getDesktopBridge(): DesktopMeetingBridge | undefined {
  if (typeof window === 'undefined') return undefined
  return (window as unknown as { astrorderDesktop?: DesktopMeetingBridge }).astrorderDesktop
}

function notifyMeetingsChanged() {
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent('astrorder:meetings-changed'))
  }
}

function isAutoGeneratedTitle(title: string, meetingDate: string): boolean {
  const trimmed = title.trim()
  if (!trimmed) return true
  return (
    trimmed === `会议纪要 ${meetingDate}` ||
    trimmed === `会议纪要（${meetingDate}）` ||
    trimmed === `会议纪要 (${meetingDate})`
  )
}

// ============================================================================
// Module-level persistent audio / WebSocket / auto-save / summary runtime
// Lives outside React component lifecycles so route switches NEVER interrupt
// recording, periodic crash-safe auto-save, or final whole-meeting summary.
// ============================================================================

let socket: WebSocket | null = null
let audioCtx: AudioContext | null = null
let mediaStream: MediaStream | null = null
let processor: ScriptProcessorNode | null = null
let elapsedTimer: number | null = null
let waveTimer: number | null = null
let autoSummarizeTimer: number | null = null
let periodicSaveTimer: number | null = null
let editSaveDebounceTimer: number | null = null
let r2t2PollTimer: number | null = null

let elapsedRuntime = 0
let segStartSecRuntime = 0
let segSpeechSecRuntime = 0
let silenceMsRuntime = 0
let liveTextRuntime = ''
let pendingBookmarkRuntime = false
let pendingCommitMeta: Array<{ seconds: number; bookmarked: boolean }> = []
let committingRuntime = false

let lastSummarizedChars = 0
let lastSummarizedSegCount = 0
let summarizingLock = false
let pendingFinalFullSummary = false
let saveInFlight: Promise<MeetingRecord | null> | null = null
let pendingSaveRequested = false
let dirtyVersion = 0
let savedVersion = 0
let lastSavedTranscriptChars = 0
let lastCapsulePushAt = 0
let capsuleActionListenerInstalled = false

function markDirty() {
  dirtyVersion += 1
}

export interface MeetingCrashDraft {
  activeId?: string
  title?: string
  meetingDate?: string
  participantsText?: string
  projectKey?: string
  workspace?: string
  durationSeconds?: number
  segments?: MeetingTranscriptSegment[]
  liveText?: string
  liveStartSec?: number
  summary?: string
  topics?: MeetingTopic[]
  decisions?: string[]
  risks?: string[]
  actionItems?: MeetingActionItem[]
  minutesMarkdown?: string
  updatedAt?: number
}

export function readCrashDraftSnapshot(): MeetingCrashDraft | null {
  if (typeof localStorage === 'undefined') return null
  try {
    const raw = localStorage.getItem(DRAFT_BACKUP_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as MeetingCrashDraft
    const hasData =
      (Array.isArray(parsed.segments) && parsed.segments.length > 0) ||
      Boolean(parsed.liveText?.trim()) ||
      Boolean(parsed.summary?.trim()) ||
      Boolean(parsed.minutesMarkdown?.trim())
    return hasData ? parsed : null
  } catch {
    return null
  }
}

function writeCrashDraftSnapshot() {
  if (typeof localStorage === 'undefined') return
  const st = useMeetingSessionStore.getState()
  const transcript = segmentsToTranscript(st.segments, st.liveText, st.liveStartSec).trim()
  if (!transcript && !st.summary.trim() && !st.minutesMarkdown.trim()) return
  try {
    localStorage.setItem(
      DRAFT_BACKUP_STORAGE_KEY,
      JSON.stringify({
        activeId: st.activeId,
        title: st.title,
        meetingDate: st.meetingDate,
        participantsText: st.participantsText,
        projectKey: st.projectKey,
        workspace: st.workspace,
        durationSeconds: st.durationSeconds,
        segments: st.segments,
        liveText: st.liveText,
        liveStartSec: st.liveStartSec,
        summary: st.summary,
        topics: st.topics,
        decisions: st.decisions,
        risks: st.risks,
        actionItems: st.actionItems,
        minutesMarkdown: st.minutesMarkdown,
        updatedAt: Date.now(),
      }),
    )
  } catch {}
}

function clearCrashDraftSnapshot() {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.removeItem(DRAFT_BACKUP_STORAGE_KEY)
  } catch {}
}

function clearEditDebounceTimer() {
  if (editSaveDebounceTimer !== null) {
    window.clearTimeout(editSaveDebounceTimer)
    editSaveDebounceTimer = null
  }
}

function clearPeriodicSaveTimer() {
  if (periodicSaveTimer !== null) {
    window.clearInterval(periodicSaveTimer)
    periodicSaveTimer = null
  }
}

function clearMediaRuntime() {
  if (elapsedTimer !== null) {
    window.clearInterval(elapsedTimer)
    elapsedTimer = null
  }
  if (waveTimer !== null) {
    window.clearInterval(waveTimer)
    waveTimer = null
  }
  if (autoSummarizeTimer !== null) {
    window.clearInterval(autoSummarizeTimer)
    autoSummarizeTimer = null
  }
  clearPeriodicSaveTimer()
  if (processor) {
    processor.onaudioprocess = null
    try {
      processor.disconnect()
    } catch {}
    processor = null
  }
  if (mediaStream) {
    mediaStream.getTracks().forEach((t) => t.stop())
    mediaStream = null
  }
  if (audioCtx) {
    void audioCtx.close().catch(() => {})
    audioCtx = null
  }
}

function closeSocketRuntime() {
  const ws = socket
  socket = null
  if (ws) {
    ws.onmessage = null
    ws.onerror = null
    ws.onclose = null
    if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
      try {
        ws.close(1000)
      } catch {}
    }
  }
}

function clearR2t2PollTimer() {
  if (r2t2PollTimer !== null) {
    window.clearInterval(r2t2PollTimer)
    r2t2PollTimer = null
  }
}

function syncR2t2Polling(status: LocalVoicePluginStatus | null) {
  clearR2t2PollTimer()
  if (
    typeof window === 'undefined' ||
    !status ||
    (status.state !== 'installing' && status.state !== 'starting' && status.state !== 'stopping')
  ) {
    return
  }
  r2t2PollTimer = window.setInterval(() => {
    void useMeetingSessionStore.getState().refreshR2t2Status()
  }, 1500)
}

function buildCapsulePayload(state: MeetingSessionState): DesktopMeetingCapsuleState {
  return {
    active:
      state.recordState === 'starting' ||
      state.recordState === 'recording' ||
      state.recordState === 'paused' ||
      state.recordState === 'stopping',
    paused: state.recordState === 'paused',
    stopping: state.recordState === 'stopping',
    elapsed: formatDurationClock(state.elapsedSeconds),
    text: buildCapsuleDisplayText(state.recordState, state.liveText),
    bars: sampleCapsuleBars(state.waveform),
  }
}

function pushNativeCapsuleUpdate(force = false) {
  const state = useMeetingSessionStore.getState()
  if (!state.nativeCapsuleActive) return
  const bridge = getDesktopBridge()
  if (!bridge?.updateMeetingCapsule) return
  const now = Date.now()
  if (!force && now - lastCapsulePushAt < 110) return
  lastCapsulePushAt = now
  void bridge.updateMeetingCapsule(buildCapsulePayload(state)).catch(() => {})
}

async function showNativeCapsuleIfAvailable() {
  const bridge = getDesktopBridge()
  if (!bridge?.showMeetingCapsule) {
    useMeetingSessionStore.setState({ nativeCapsuleActive: false })
    return false
  }
  const payload = buildCapsulePayload(useMeetingSessionStore.getState())
  try {
    await bridge.showMeetingCapsule(payload)
    lastCapsulePushAt = Date.now()
    useMeetingSessionStore.setState({ nativeCapsuleActive: true, inAppCapsuleForced: false })
    return true
  } catch {
    useMeetingSessionStore.setState({ nativeCapsuleActive: false })
    return false
  }
}

function hideNativeCapsule() {
  const bridge = getDesktopBridge()
  if (bridge?.hideMeetingCapsule) {
    void bridge.hideMeetingCapsule().catch(() => {})
  }
  useMeetingSessionStore.setState({ nativeCapsuleActive: false, inAppCapsuleForced: false })
}

function scheduleDebouncedAutoSave() {
  if (typeof window === 'undefined') return
  markDirty()
  writeCrashDraftSnapshot()
  clearEditDebounceTimer()
  editSaveDebounceTimer = window.setTimeout(() => {
    editSaveDebounceTimer = null
    const st = useMeetingSessionStore.getState()
    const hasPersistable =
      Boolean(st.activeId) ||
      st.segments.length > 0 ||
      st.liveText.trim().length > 0 ||
      st.summary.trim().length > 0 ||
      st.minutesMarkdown.trim().length > 0 ||
      st.actionItems.length > 0
    if (hasPersistable) {
      void st.persistCurrentMeeting(undefined, true)
    }
  }, EDIT_AUTO_SAVE_DEBOUNCE_MS)
}

function startPeriodicAutoSaveTimer() {
  clearPeriodicSaveTimer()
  if (typeof window === 'undefined') return
  periodicSaveTimer = window.setInterval(() => {
    const st = useMeetingSessionStore.getState()
    if (st.recordState !== 'recording' && st.recordState !== 'paused') {
      clearPeriodicSaveTimer()
      return
    }
    const transcriptLen = segmentsToTranscript(st.segments, st.liveText, st.liveStartSec).trim().length
    if (transcriptLen === 0 && st.segments.length === 0) return
    writeCrashDraftSnapshot()
    if (dirtyVersion > savedVersion || transcriptLen !== lastSavedTranscriptChars) {
      void st.persistCurrentMeeting(undefined, true)
    }
  }, PERIODIC_AUTO_SAVE_MS)
}

function startAutoSummarizeTimer() {
  if (autoSummarizeTimer !== null) {
    window.clearInterval(autoSummarizeTimer)
    autoSummarizeTimer = null
  }
  const st = useMeetingSessionStore.getState()
  if (st.recordState !== 'recording' || !st.settings.auto_summarize) return
  const interval = st.settings.auto_summarize_interval_sec || 30
  useMeetingSessionStore.setState({ nextSyncCountdown: interval })
  autoSummarizeTimer = window.setInterval(() => {
    const cur = useMeetingSessionStore.getState()
    if (cur.recordState !== 'recording' || !cur.settings.auto_summarize) {
      if (autoSummarizeTimer !== null) {
        window.clearInterval(autoSummarizeTimer)
        autoSummarizeTimer = null
      }
      return
    }
    if (cur.nextSyncCountdown > 1) {
      useMeetingSessionStore.setState({ nextSyncCountdown: cur.nextSyncCountdown - 1 })
      return
    }
    const totalChars = segmentsToTranscript(cur.segments, cur.liveText, cur.liveStartSec).trim().length
    if (totalChars >= 24 && totalChars - lastSummarizedChars >= 16) {
      void cur.runSummarize({ incremental: true, autoSave: true, silentError: true })
    }
    useMeetingSessionStore.setState({ nextSyncCountdown: cur.settings.auto_summarize_interval_sec || 30 })
  }, 1000)
}

function sendCommitFrame(bookmarked = false) {
  if (!socket || socket.readyState !== WebSocket.OPEN || committingRuntime) return
  committingRuntime = true
  const startSec = segStartSecRuntime
  const isBookmarked = bookmarked || pendingBookmarkRuntime
  pendingBookmarkRuntime = false
  pendingCommitMeta.push({ seconds: startSec, bookmarked: isBookmarked })
  segStartSecRuntime = elapsedRuntime
  segSpeechSecRuntime = 0
  silenceMsRuntime = 0
  liveTextRuntime = ''
  useMeetingSessionStore.setState({ liveText: '', liveStartSec: elapsedRuntime })
  pushNativeCapsuleUpdate(true)
  socket.send(JSON.stringify({ type: 'commit' }))
}

function finalizeCompletedRecording(finalTextRaw: string) {
  const finalText = finalTextRaw.trim()
  const total = elapsedRuntime
  const segStart = segStartSecRuntime
  const isBookmarked = pendingBookmarkRuntime
  clearMediaRuntime()
  closeSocketRuntime()
  liveTextRuntime = ''
  pendingBookmarkRuntime = false

  useMeetingSessionStore.setState((prev) => {
    const nextSegs = finalText
      ? [
          ...prev.segments,
          {
            id: `seg_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
            timestamp: formatDurationClock(segStart),
            seconds: segStart,
            text: finalText,
            kind: 'speech' as const,
            bookmarked: isBookmarked,
          },
        ]
      : prev.segments
    return {
      recordState: 'idle',
      meetingCompleted: true,
      waveform: ZERO_WAVE,
      durationSeconds: total,
      liveText: '',
      liveStartSec: total,
      segments: nextSegs,
    }
  })
  markDirty()
  writeCrashDraftSnapshot()
  hideNativeCapsule()

  void (async () => {
    const store = useMeetingSessionStore.getState()
    const hasTranscript =
      segmentsToTranscript(store.segments, '', store.durationSeconds).trim().length > 0
    // 1. Immediately persist raw transcript & segments BEFORE running final LLM call
    if (hasTranscript || store.segments.length > 0 || store.activeId) {
      await store.persistCurrentMeeting(undefined, true)
    }
    // 2. Always perform a final full-meeting summary (never dropped if incremental summary is in flight)
    if (hasTranscript) {
      useMeetingSessionStore.setState({ finalizingSummary: true })
      pendingFinalFullSummary = true
      if (!summarizingLock) {
        await useMeetingSessionStore.getState().runSummarize({
          incremental: false,
          autoSave: true,
          isFinalFull: true,
        })
      }
    }
  })()
}

export function ensureMeetingCapsuleActionListener() {
  if (typeof window === 'undefined' || capsuleActionListenerInstalled) return
  capsuleActionListenerInstalled = true
  window.addEventListener('astrorder:meeting-capsule-action', (event: Event) => {
    const detail = (event as CustomEvent<{ action?: string }>).detail
    const store = useMeetingSessionStore.getState()
    if (detail?.action === 'stop') {
      store.stopRecording()
    } else if (detail?.action === 'toggle_pause') {
      if (store.recordState === 'recording') store.pauseRecording()
      else if (store.recordState === 'paused') store.resumeRecording()
    }
  })
}

export interface MeetingSessionState {
  // Persisted list & config
  savedMeetings: MeetingRecord[]
  settings: MeetingSettings
  hydrated: boolean

  // Active meeting document
  activeId: string
  title: string
  meetingDate: string
  participantsText: string
  projectKey: string
  workspace: string
  durationSeconds: number
  segments: MeetingTranscriptSegment[]
  rawTranscript: string
  liveText: string
  liveStartSec: number
  summary: string
  topics: MeetingTopic[]
  decisions: string[]
  risks: string[]
  actionItems: MeetingActionItem[]
  minutesMarkdown: string
  modelName: string
  meetingCompleted: boolean

  // Operational state
  summarizing: boolean
  finalizingSummary: boolean
  lastSyncedAt: string
  nextSyncCountdown: number
  saving: boolean
  autoSaveState: AutoSaveStatus
  lastSavedAt: string
  transcribingFile: boolean

  // Live recorder state
  recordState: LiveRecordState
  elapsedSeconds: number
  waveform: number[]
  devices: AudioDeviceOption[]
  selectedDeviceId: string
  r2t2Status: LocalVoicePluginStatus | null
  r2t2Loading: boolean

  // Floating capsule state
  nativeCapsuleActive: boolean
  inAppCapsuleForced: boolean

  // Actions
  loadInitial: () => Promise<void>
  refreshSettings: () => Promise<void>
  refreshR2t2Status: () => Promise<LocalVoicePluginStatus | null>
  refreshDevices: () => Promise<void>
  setSelectedDeviceId: (deviceId: string) => void
  toggleR2t2: (enabled: boolean) => Promise<LocalVoicePluginStatus>
  restartR2t2: () => Promise<LocalVoicePluginStatus>

  setTitle: (title: string) => void
  setMeetingDate: (meetingDate: string) => void
  setParticipantsText: (text: string) => void
  setWorkspaceLink: (projectKey: string, workspace: string) => void
  setMinutesMarkdown: (md: string) => void
  setSettings: (next: MeetingSettings) => void

  startRecording: (initialElapsedSeconds?: number) => Promise<void>
  startNewMeetingRecording: () => Promise<void>
  pauseRecording: () => void
  resumeRecording: () => void
  bookmarkNow: () => boolean
  stopRecording: () => void
  openFloatingCapsule: () => Promise<void>
  dismissInAppCapsule: () => void

  runSummarize: (opts?: {
    incremental?: boolean
    autoSave?: boolean
    silentError?: boolean
    isFinalFull?: boolean
    extraSegments?: MeetingTranscriptSegment[]
  }) => Promise<void>
  persistCurrentMeeting: (overrides?: Partial<MeetingRecord>, silent?: boolean) => Promise<MeetingRecord | null>
  resetMeeting: () => void
  openMeetingRecord: (rec: MeetingRecord) => boolean
  deleteMeeting: (id: string) => Promise<void>
  uploadAudio: (file: File) => Promise<void>
  insertQuickNote: (text: string) => void
  commitPastedTranscript: (raw: string) => Promise<void>
  toggleBookmarkSegment: (segId: string) => void
  updateSegment: (segId: string, patch: { text?: string; speaker?: string }) => void
  toggleActionDone: (item: MeetingActionItem) => Promise<void>
  updateActionItem: (
    item: MeetingActionItem,
    patch: { text?: string; owner?: string; due_date?: string; priority?: string },
  ) => Promise<void>
  addActionItem: (text: string, owner: string, dueDate: string) => void
  removeActionItem: (id: string) => void
  restoreCrashDraft: () => boolean
  dismissCrashDraft: () => void

  _resetForTests: () => void
}

function initialStateSnapshot() {
  return {
    savedMeetings: [] as MeetingRecord[],
    settings: DEFAULT_MEETING_SETTINGS,
    hydrated: false,
    activeId: '',
    title: '',
    meetingDate: todayDateString(),
    participantsText: '',
    projectKey: '',
    workspace: '',
    durationSeconds: 0,
    segments: [] as MeetingTranscriptSegment[],
    rawTranscript: '',
    liveText: '',
    liveStartSec: 0,
    summary: '',
    topics: [] as MeetingTopic[],
    decisions: [] as string[],
    risks: [] as string[],
    actionItems: [] as MeetingActionItem[],
    minutesMarkdown: '',
    modelName: '',
    meetingCompleted: false,
    summarizing: false,
    finalizingSummary: false,
    lastSyncedAt: '',
    nextSyncCountdown: 30,
    saving: false,
    autoSaveState: 'idle' as AutoSaveStatus,
    lastSavedAt: '',
    transcribingFile: false,
    recordState: 'idle' as LiveRecordState,
    elapsedSeconds: 0,
    waveform: ZERO_WAVE,
    devices: [] as AudioDeviceOption[],
    selectedDeviceId: 'default',
    r2t2Status: null as LocalVoicePluginStatus | null,
    r2t2Loading: false,
    nativeCapsuleActive: false,
    inAppCapsuleForced: false,
  }
}

export const useMeetingSessionStore = create<MeetingSessionState>((set, get) => ({
  ...initialStateSnapshot(),

  loadInitial: async () => {
    ensureMeetingCapsuleActionListener()
    try {
      const [mtgRes, cfgRes] = await Promise.all([
        api.listMeetings().catch(() => ({ items: [] })),
        api.getMeetingSettings().catch(() => DEFAULT_MEETING_SETTINGS),
      ])
      set((prev) => ({
        savedMeetings: mtgRes.items || [],
        settings: cfgRes,
        hydrated: true,
        nextSyncCountdown:
          prev.recordState === 'recording' ? prev.nextSyncCountdown : cfgRes.auto_summarize_interval_sec || 30,
      }))
    } catch {
      // handled per call
    }
    void get().refreshR2t2Status()
    void get().refreshDevices()
  },

  refreshSettings: async () => {
    try {
      const cfgRes = await api.getMeetingSettings()
      get().setSettings(cfgRes)
    } catch {
      // ignore transient refresh failure
    }
  },

  refreshR2t2Status: async () => {
    try {
      const status = await api.getLocalVoicePlugin()
      set({ r2t2Status: status })
      syncR2t2Polling(status)
      return status
    } catch {
      return null
    }
  },

  refreshDevices: async () => {
    if (typeof navigator === 'undefined' || !navigator.mediaDevices?.enumerateDevices) return
    try {
      const list = await navigator.mediaDevices.enumerateDevices()
      const inputs = list
        .filter((d) => d.kind === 'audioinput')
        .map((d, idx) => ({
          deviceId: d.deviceId || `mic-${idx}`,
          label: d.label || (idx === 0 ? '默认麦克风' : `麦克风 ${idx + 1}`),
        }))
      if (inputs.length > 0) {
        set({ devices: inputs })
      }
    } catch {
      // ignore permission-gated device list
    }
  },

  setSelectedDeviceId: (deviceId) => set({ selectedDeviceId: deviceId }),

  toggleR2t2: async (enabled) => {
    set({ r2t2Loading: true })
    try {
      const next = await api.setLocalVoicePlugin(enabled)
      set({ r2t2Status: next })
      syncR2t2Polling(next)
      return next
    } finally {
      set({ r2t2Loading: false })
    }
  },

  restartR2t2: async () => {
    set({ r2t2Loading: true })
    try {
      await api.setLocalVoicePlugin(false)
      const next = await api.setLocalVoicePlugin(true)
      set({ r2t2Status: next })
      syncR2t2Polling(next)
      return next
    } finally {
      set({ r2t2Loading: false })
    }
  },

  setTitle: (title) => {
    set({ title })
    scheduleDebouncedAutoSave()
  },
  setMeetingDate: (meetingDate) => {
    set({ meetingDate: meetingDate || todayDateString() })
    scheduleDebouncedAutoSave()
  },
  setParticipantsText: (participantsText) => {
    set({ participantsText })
    scheduleDebouncedAutoSave()
  },
  setWorkspaceLink: (projectKey, workspace) => {
    set({ projectKey, workspace })
    scheduleDebouncedAutoSave()
  },
  setMinutesMarkdown: (minutesMarkdown) => {
    set({ minutesMarkdown })
    scheduleDebouncedAutoSave()
  },
  setSettings: (next) => {
    set({ settings: next, nextSyncCountdown: next.auto_summarize_interval_sec || 30 })
    if (get().recordState === 'recording') {
      startAutoSummarizeTimer()
    }
  },

  startNewMeetingRecording: async () => {
    const cur = get()
    if (cur.recordState !== 'idle') return
    clearEditDebounceTimer()
    if (
      dirtyVersion > savedVersion &&
      (cur.segments.length > 0 || cur.summary.trim() || cur.minutesMarkdown.trim())
    ) {
      await cur.persistCurrentMeeting(undefined, true)
    }
    get().resetMeeting()
    await get().startRecording(0)
  },

  startRecording: async (initialElapsedSeconds = 0) => {
    ensureMeetingCapsuleActionListener()
    if (get().recordState !== 'idle') return
    // Finished meetings are immutable recordings — starting again always creates a brand-new meeting.
    if (get().meetingCompleted) {
      await get().startNewMeetingRecording()
      return
    }
    set({ recordState: 'starting', meetingCompleted: false })

    try {
      if (typeof navigator === 'undefined' || !navigator.mediaDevices?.getUserMedia) {
        throw new Error('当前环境不支持麦克风录音')
      }
      const status = await get().refreshR2t2Status()
      if (!status || status.state !== 'running') {
        throw new Error(
          status?.state === 'starting' || status?.state === 'installing'
            ? 'R2T2 引擎正在加载中，请稍候几秒再试'
            : 'R2T2 本地实时转写未启动，请先在右上角配置面板中启用 R2T2',
        )
      }

      const selectedDeviceId = get().selectedDeviceId
      const audioConstraint: boolean | MediaTrackConstraints =
        selectedDeviceId && selectedDeviceId !== 'default'
          ? { deviceId: { ideal: selectedDeviceId }, echoCancellation: true, noiseSuppression: true }
          : { echoCancellation: true, noiseSuppression: true }

      const media = await navigator.mediaDevices.getUserMedia({ audio: audioConstraint })
      mediaStream = media
      void get().refreshDevices()

      const AudioCtx =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
      if (!AudioCtx || typeof WebSocket === 'undefined') {
        throw new Error('当前运行环境不支持实时音频流')
      }

      const ws = new WebSocket(buildLocalVoiceStreamUrl())
      ws.binaryType = 'arraybuffer'
      socket = ws

      const ctx = new AudioCtx()
      audioCtx = ctx
      const source = ctx.createMediaStreamSource(media)
      const nodeAnalyser = ctx.createAnalyser()
      nodeAnalyser.fftSize = 64
      nodeAnalyser.smoothingTimeConstant = 0.72

      const nodeProcessor = ctx.createScriptProcessor(4096, 1, 1)
      processor = nodeProcessor
      const mute = ctx.createGain()
      mute.gain.value = 0

      source.connect(nodeAnalyser)
      source.connect(nodeProcessor)
      nodeProcessor.connect(mute)
      mute.connect(ctx.destination)

      elapsedRuntime = initialElapsedSeconds
      segStartSecRuntime = initialElapsedSeconds
      segSpeechSecRuntime = 0
      silenceMsRuntime = 0
      liveTextRuntime = ''
      pendingBookmarkRuntime = false
      pendingCommitMeta = []
      committingRuntime = false

      set({
        elapsedSeconds: initialElapsedSeconds,
        durationSeconds: initialElapsedSeconds,
        liveText: '',
        liveStartSec: initialElapsedSeconds,
      })

      let ready = false

      nodeProcessor.onaudioprocess = (event) => {
        if (!ready || get().recordState !== 'recording' || ws.readyState !== WebSocket.OPEN) return
        const channel = event.inputBuffer.getChannelData(0)
        let sumSq = 0
        for (let i = 0; i < channel.length; i += 4) {
          const v = channel[i]
          sumSq += v * v
        }
        const rms = Math.sqrt(sumSq / Math.max(1, channel.length / 4))
        const frameMs = (channel.length / ctx.sampleRate) * 1000
        if (rms < 0.015) {
          silenceMsRuntime += frameMs
        } else {
          silenceMsRuntime = 0
        }
        segSpeechSecRuntime += frameMs / 1000

        const pcm = downsampleToPcm16(channel, ctx.sampleRate)
        if (pcm.byteLength > 0) {
          ws.send(pcm.buffer as ArrayBuffer)
        }

        if (
          !committingRuntime &&
          liveTextRuntime.trim().length > 0 &&
          ((silenceMsRuntime >= 1800 && segSpeechSecRuntime >= 6) || segSpeechSecRuntime >= 30)
        ) {
          sendCommitFrame(false)
        }
      }

      await new Promise<void>((resolve, reject) => {
        ws.onerror = () => {
          if (!ready) {
            reject(new Error('无法连接 R2T2 实时流式听写服务'))
          } else if (get().recordState !== 'idle') {
            finalizeCompletedRecording(liveTextRuntime)
            notifications.show({ color: 'red', message: 'R2T2 实时转写连接中断，已自动保存当前内容' })
          }
        }
        ws.onclose = () => {
          if (!ready) {
            reject(new Error('R2T2 实时转写连接已关闭'))
          } else {
            const st = get().recordState
            if (st === 'recording' || st === 'paused' || st === 'stopping') {
              finalizeCompletedRecording(liveTextRuntime)
            }
          }
        }
        ws.onmessage = (event) => {
          let msg: { type?: string; text?: string; message?: string }
          try {
            msg = JSON.parse(String(event.data)) as typeof msg
          } catch {
            return
          }
          if (msg.type === 'ready') {
            ready = true
            set({ recordState: 'recording', meetingCompleted: false })
            startAutoSummarizeTimer()
            startPeriodicAutoSaveTimer()
            void showNativeCapsuleIfAvailable()

            elapsedTimer = window.setInterval(() => {
              if (get().recordState === 'recording') {
                elapsedRuntime += 1
                set({ elapsedSeconds: elapsedRuntime, durationSeconds: elapsedRuntime })
                pushNativeCapsuleUpdate(true)
              }
            }, 1000)

            const freqBins = new Uint8Array(nodeAnalyser.frequencyBinCount)
            waveTimer = window.setInterval(() => {
              if (get().recordState !== 'recording') {
                set({ waveform: ZERO_WAVE })
                return
              }
              nodeAnalyser.getByteFrequencyData(freqBins)
              const bars: number[] = []
              for (let i = 0; i < WAVE_BARS; i++) {
                const idx = Math.min(freqBins.length - 1, Math.floor((i * freqBins.length) / WAVE_BARS))
                const norm = Math.min(1, Math.max(0.14, freqBins[idx] / 210))
                bars.push(norm)
              }
              set({ waveform: bars })
              pushNativeCapsuleUpdate(false)
            }, 65)

            resolve()
          } else if (msg.type === 'transcript' && typeof msg.text === 'string') {
            liveTextRuntime = msg.text
            set({ liveText: msg.text, liveStartSec: segStartSecRuntime })
            pushNativeCapsuleUpdate(true)
          } else if (msg.type === 'segment' && typeof msg.text === 'string') {
            committingRuntime = false
            const meta = pendingCommitMeta.shift() || {
              seconds: segStartSecRuntime,
              bookmarked: false,
            }
            const trimmed = msg.text.trim()
            if (trimmed) {
              const seg: MeetingTranscriptSegment = {
                id: `seg_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`,
                timestamp: formatDurationClock(meta.seconds),
                seconds: meta.seconds,
                text: trimmed,
                kind: 'speech',
                bookmarked: meta.bookmarked,
              }
              set((prev) => ({ segments: [...prev.segments, seg] }))
              markDirty()
              writeCrashDraftSnapshot()
              // Auto-save on first segment immediately so record appears in history early
              if (!get().activeId) {
                void get().persistCurrentMeeting(undefined, true)
              }
            }
          } else if (msg.type === 'done') {
            finalizeCompletedRecording(msg.text || liveTextRuntime || '')
          } else if (msg.type === 'error') {
            const errMsg = msg.message || 'R2T2 实时转写失败'
            if (!ready) {
              clearMediaRuntime()
              closeSocketRuntime()
              set({ recordState: 'idle', waveform: ZERO_WAVE })
              hideNativeCapsule()
              reject(new Error(errMsg))
            } else {
              finalizeCompletedRecording(liveTextRuntime)
              notifications.show({ color: 'red', message: `${errMsg}（已自动保存已有转写）` })
            }
          }
        }
      })
    } catch (cause) {
      clearMediaRuntime()
      closeSocketRuntime()
      set({ recordState: 'idle', waveform: ZERO_WAVE })
      hideNativeCapsule()
      const msg =
        cause instanceof DOMException && cause.name === 'NotAllowedError'
          ? '麦克风权限被拒绝，请在系统/浏览器设置中允许麦克风访问'
          : cause instanceof Error
            ? cause.message
            : '无法启动实时录制'
      notifications.show({ color: 'red', message: msg })
    }
  },

  pauseRecording: () => {
    if (get().recordState !== 'recording') return
    if (liveTextRuntime.trim()) {
      sendCommitFrame(false)
    }
    if (autoSummarizeTimer !== null) {
      window.clearInterval(autoSummarizeTimer)
      autoSummarizeTimer = null
    }
    set({ recordState: 'paused', waveform: ZERO_WAVE })
    pushNativeCapsuleUpdate(true)
    void get().persistCurrentMeeting(undefined, true)
  },

  resumeRecording: () => {
    if (get().recordState !== 'paused') return
    segStartSecRuntime = elapsedRuntime
    segSpeechSecRuntime = 0
    silenceMsRuntime = 0
    set({ recordState: 'recording' })
    startAutoSummarizeTimer()
    pushNativeCapsuleUpdate(true)
  },

  bookmarkNow: () => {
    const st = get().recordState
    if (st !== 'recording' && st !== 'paused') return false
    if (liveTextRuntime.trim()) {
      sendCommitFrame(true)
      return true
    }
    pendingBookmarkRuntime = true
    return false
  },

  stopRecording: () => {
    const st = get().recordState
    if (st !== 'recording' && st !== 'paused') return
    set({ recordState: 'stopping', waveform: ZERO_WAVE })
    pushNativeCapsuleUpdate(true)
    clearMediaRuntime()
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: 'stop' }))
    } else {
      finalizeCompletedRecording(liveTextRuntime)
    }
  },

  openFloatingCapsule: async () => {
    const st = get().recordState
    if (st === 'idle') return
    const shownNative = await showNativeCapsuleIfAvailable()
    if (!shownNative) {
      set({ inAppCapsuleForced: true })
    }
  },

  dismissInAppCapsule: () => set({ inAppCapsuleForced: false }),

  persistCurrentMeeting: async (overrides, silent = true) => {
    // Serialize concurrent saves so initial save assigns activeId before follow-up saves run (zero duplicates)
    if (saveInFlight) {
      if (!overrides) {
        pendingSaveRequested = true
        return saveInFlight
      }
      await saveInFlight.catch(() => null)
    }

    const runSave = async (): Promise<MeetingRecord | null> => {
      const cur = get()
      const versionAtStart = dirtyVersion
      const finalSegments = overrides?.segments ?? cur.segments
      const finalTitle = (overrides?.title ?? cur.title).trim() || `会议纪要 ${cur.meetingDate}`
      const computedTranscript = segmentsToTranscript(finalSegments, cur.liveText, cur.liveStartSec)
      const fullTranscript = overrides?.transcript ?? (computedTranscript || cur.rawTranscript)

      set({ saving: true, autoSaveState: 'saving' })
      try {
        const saved = await api.saveMeeting({
          id: (overrides?.id ?? cur.activeId) || undefined,
          title: finalTitle,
          meeting_date: cur.meetingDate,
          duration_seconds: overrides?.duration_seconds ?? cur.durationSeconds,
          participants: parseParticipants(cur.participantsText),
          project_key: (overrides?.project_key ?? cur.projectKey) || undefined,
          workspace: (overrides?.workspace ?? cur.workspace) || undefined,
          transcript: fullTranscript,
          segments: finalSegments,
          minutes_markdown: overrides?.minutes_markdown ?? cur.minutesMarkdown,
          summary: overrides?.summary ?? cur.summary,
          topics: overrides?.topics ?? cur.topics,
          decisions: overrides?.decisions ?? cur.decisions,
          risks: overrides?.risks ?? cur.risks,
          action_items: overrides?.action_items ?? cur.actionItems,
          model: overrides?.model ?? cur.modelName,
        })
        savedVersion = Math.max(savedVersion, versionAtStart)
        lastSavedTranscriptChars = fullTranscript.trim().length
        set((prev) => ({
          activeId: saved.id,
          title: prev.title.trim() ? prev.title : saved.title,
          rawTranscript: fullTranscript,
          actionItems: saved.action_items || prev.actionItems,
          savedMeetings: [saved, ...prev.savedMeetings.filter((m) => m.id !== saved.id)],
          autoSaveState: 'saved',
          lastSavedAt: nowClockString(),
        }))
        clearCrashDraftSnapshot()
        notifyMeetingsChanged()
        if (!silent) {
          notifications.show({ color: 'teal', message: '已保存会议纪要与行动项' })
        }
        return saved
      } catch (cause) {
        set({ autoSaveState: 'error' })
        if (!silent) {
          notifications.show({
            color: 'red',
            message: cause instanceof Error ? cause.message : '保存会议失败',
          })
        }
        return null
      } finally {
        set({ saving: false })
      }
    }

    const promise = runSave()
    saveInFlight = promise
    const result = await promise
    if (saveInFlight === promise) {
      saveInFlight = null
    }
    if (pendingSaveRequested) {
      pendingSaveRequested = false
      return get().persistCurrentMeeting(undefined, true)
    }
    return result
  },

  runSummarize: async (opts) => {
    if (summarizingLock) {
      if (opts?.isFinalFull) {
        pendingFinalFullSummary = true
      }
      return
    }
    const cur = get()
    const segs = opts?.extraSegments ?? cur.segments
    const fullTranscript = (
      segmentsToTranscript(segs, cur.liveText, cur.liveStartSec) || cur.rawTranscript
    ).trim()
    if (!fullTranscript) {
      if (opts?.isFinalFull) {
        pendingFinalFullSummary = false
        set({ finalizingSummary: false })
      }
      return
    }

    const isFinalFull = Boolean(opts?.isFinalFull)
    const isIncremental = Boolean(
      !isFinalFull && opts?.incremental && (cur.summary || cur.topics.length > 0),
    )
    const newSegs = segs.slice(lastSummarizedSegCount)
    const newChunk = segmentsToTranscript(newSegs, cur.liveText, cur.liveStartSec).trim()

    if (isFinalFull) {
      pendingFinalFullSummary = false
    }

    summarizingLock = true
    set({ summarizing: true, ...(isFinalFull ? { finalizingSummary: true } : {}) })
    try {
      const resp = await api.summarizeMeeting({
        title: isAutoGeneratedTitle(cur.title, cur.meetingDate) ? undefined : cur.title.trim(),
        meeting_date: cur.meetingDate,
        participants: parseParticipants(cur.participantsText),
        transcript: fullTranscript,
        summary_style: cur.settings.summary_style,
        incremental: isIncremental,
        new_transcript: isIncremental ? newChunk : undefined,
        previous_summary: isIncremental ? cur.summary : undefined,
        previous_topics: isIncremental ? cur.topics : undefined,
        previous_decisions: isIncremental ? cur.decisions : undefined,
        previous_risks: isIncremental ? cur.risks : undefined,
        previous_action_items: cur.actionItems,
      })
      const latestAfter = get()
      lastSummarizedChars = fullTranscript.length
      lastSummarizedSegCount = segs.length
      const nextTitle =
        isAutoGeneratedTitle(latestAfter.title, latestAfter.meetingDate) && resp.title
          ? resp.title
          : latestAfter.title
      set({
        title: nextTitle,
        rawTranscript: fullTranscript,
        summary: resp.summary || '',
        topics: resp.topics || [],
        decisions: resp.decisions || [],
        risks: resp.risks || [],
        actionItems: resp.action_items || [],
        minutesMarkdown: resp.minutes_markdown || '',
        modelName: resp.model || latestAfter.modelName,
        lastSyncedAt: formatDurationClock(latestAfter.durationSeconds),
      })
      markDirty()

      if (opts?.autoSave || isFinalFull) {
        await get().persistCurrentMeeting(
          {
            title: nextTitle,
            segments: segs,
            transcript: fullTranscript,
            summary: resp.summary || '',
            topics: resp.topics || [],
            decisions: resp.decisions || [],
            risks: resp.risks || [],
            action_items: resp.action_items || [],
            minutes_markdown: resp.minutes_markdown || '',
            model: resp.model || latestAfter.modelName,
          },
          true,
        )
      }
      if (isFinalFull) {
        notifications.show({ color: 'teal', message: '录音已自动保存并完成整体总结' })
      }
    } catch (cause) {
      if (isFinalFull) {
        notifications.show({
          color: 'yellow',
          message: `录音原文已自动保存，整体总结失败：${cause instanceof Error ? cause.message : '请稍后重试'}`,
        })
      } else if (!opts?.silentError) {
        notifications.show({
          color: 'red',
          message: cause instanceof Error ? cause.message : '生成会议纪要失败',
        })
      }
    } finally {
      summarizingLock = false
      const needFollowUpFinal = pendingFinalFullSummary
      set({ summarizing: false, finalizingSummary: needFollowUpFinal })
      if (needFollowUpFinal) {
        void get().runSummarize({ incremental: false, autoSave: true, isFinalFull: true })
      }
    }
  },

  resetMeeting: () => {
    const st = get().recordState
    if (st === 'recording' || st === 'paused') {
      get().stopRecording()
    }
    clearEditDebounceTimer()
    pendingFinalFullSummary = false
    lastSummarizedChars = 0
    lastSummarizedSegCount = 0
    lastSavedTranscriptChars = 0
    dirtyVersion = 0
    savedVersion = 0
    set({
      activeId: '',
      title: '',
      meetingDate: todayDateString(),
      participantsText: '',
      projectKey: '',
      workspace: '',
      durationSeconds: 0,
      segments: [],
      rawTranscript: '',
      liveText: '',
      liveStartSec: 0,
      summary: '',
      topics: [],
      decisions: [],
      risks: [],
      actionItems: [],
      minutesMarkdown: '',
      modelName: '',
      meetingCompleted: false,
      finalizingSummary: false,
      lastSyncedAt: '',
      autoSaveState: 'idle',
      lastSavedAt: '',
    })
  },

  openMeetingRecord: (rec) => {
    const st = get().recordState
    if (st === 'recording' || st === 'paused' || st === 'starting' || st === 'stopping') {
      notifications.show({ color: 'yellow', message: '请先结束当前录制再切换历史会议' })
      return false
    }
    clearEditDebounceTimer()
    const loadedSegs =
      rec.segments && rec.segments.length > 0
        ? rec.segments
        : rec.transcript
          ? transcriptToSegments(rec.transcript)
          : []
    const fullText = rec.transcript || segmentsToTranscript(loadedSegs, '', 0)
    lastSummarizedChars = fullText.length
    lastSummarizedSegCount = loadedSegs.length
    lastSavedTranscriptChars = fullText.trim().length
    dirtyVersion = 0
    savedVersion = 0
    set({
      activeId: rec.id,
      title: rec.title,
      meetingDate: rec.meeting_date || todayDateString(),
      participantsText: (rec.participants || []).join('、'),
      projectKey: rec.project_key || '',
      workspace: rec.workspace || '',
      durationSeconds: rec.duration_seconds || 0,
      segments: loadedSegs,
      rawTranscript: fullText,
      liveText: '',
      summary: rec.summary || '',
      topics: rec.topics || [],
      decisions: rec.decisions || [],
      risks: rec.risks || [],
      actionItems: rec.action_items || [],
      minutesMarkdown: rec.minutes_markdown || '',
      modelName: rec.model || '',
      meetingCompleted: true,
      finalizingSummary: false,
      lastSyncedAt: '',
      autoSaveState: 'saved',
      lastSavedAt: '',
    })
    return true
  },

  deleteMeeting: async (id) => {
    try {
      await api.deleteMeeting(id)
      set((prev) => ({ savedMeetings: prev.savedMeetings.filter((item) => item.id !== id) }))
      if (get().activeId === id) {
        get().resetMeeting()
      }
      notifyMeetingsChanged()
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '删除失败' })
    }
  },

  uploadAudio: async (file) => {
    if (get().meetingCompleted) {
      get().resetMeeting()
    }
    set({ transcribingFile: true })
    try {
      const res = await api.transcribeAudioFile(file, file.name)
      const text = (res.text || '').trim()
      if (!text) {
        notifications.show({ color: 'yellow', message: '未识别到有效语音内容' })
        return
      }
      const importedSegs = transcriptToSegments(text)
      const cur = get()
      const merged = [...cur.segments, ...importedSegs]
      const fullText = segmentsToTranscript(merged, '', 0)
      set({
        segments: merged,
        rawTranscript: fullText,
        meetingCompleted: true,
        title: cur.title.trim() ? cur.title : file.name.replace(/\.[^.]+$/, ''),
      })
      markDirty()
      await get().persistCurrentMeeting(undefined, true)
      notifications.show({ color: 'teal', message: '录音转写完成并已自动保存，正在整体总结…' })
      await get().runSummarize({ incremental: false, autoSave: true, extraSegments: merged })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '音频转写失败' })
    } finally {
      set({ transcribingFile: false })
    }
  },

  insertQuickNote: (text) => {
    const txt = text.trim()
    if (!txt) return
    const cur = get()
    const sec =
      cur.recordState === 'recording' || cur.recordState === 'paused'
        ? cur.elapsedSeconds
        : cur.durationSeconds
    const noteSeg: MeetingTranscriptSegment = {
      id: `note_${Date.now()}`,
      timestamp: formatDurationClock(sec),
      seconds: sec,
      text: txt,
      kind: 'note',
      bookmarked: true,
    }
    set((prev) => ({ segments: [...prev.segments, noteSeg] }))
    scheduleDebouncedAutoSave()
  },

  commitPastedTranscript: async (raw) => {
    const txt = raw.trim()
    if (!txt) return
    const imported = transcriptToSegments(txt)
    set({ segments: imported, rawTranscript: txt, meetingCompleted: true })
    markDirty()
    await get().persistCurrentMeeting(undefined, true)
    await get().runSummarize({ incremental: false, autoSave: true, extraSegments: imported })
  },

  toggleBookmarkSegment: (segId) => {
    set((prev) => ({
      segments: prev.segments.map((s) => (s.id === segId ? { ...s, bookmarked: !s.bookmarked } : s)),
    }))
    scheduleDebouncedAutoSave()
  },

  updateSegment: (segId, patch) => {
    set((prev) => {
      const nextSegs = prev.segments.map((s) =>
        s.id === segId
          ? {
              ...s,
              ...(patch.text !== undefined ? { text: patch.text } : {}),
              ...(patch.speaker !== undefined ? { speaker: patch.speaker.trim() || undefined } : {}),
            }
          : s,
      )
      return {
        segments: nextSegs,
        rawTranscript: segmentsToTranscript(nextSegs, prev.liveText, prev.liveStartSec),
      }
    })
    scheduleDebouncedAutoSave()
  },

  toggleActionDone: async (item) => {
    const nextStatus = item.status === 'done' ? 'open' : 'done'
    const activeId = get().activeId
    set((prev) => ({
      actionItems: prev.actionItems.map((a) => (a.id === item.id ? { ...a, status: nextStatus } : a)),
    }))
    if (activeId && item.id) {
      try {
        await api.patchMeetingActionItem({ meeting_id: activeId, item_id: item.id, status: nextStatus })
        set((prev) => ({
          savedMeetings: prev.savedMeetings.map((m) =>
            m.id === activeId
              ? {
                  ...m,
                  action_items: m.action_items.map((a) =>
                    a.id === item.id ? { ...a, status: nextStatus } : a,
                  ),
                }
              : m,
          ),
        }))
        notifyMeetingsChanged()
      } catch {
        scheduleDebouncedAutoSave()
      }
    } else {
      scheduleDebouncedAutoSave()
    }
  },

  updateActionItem: async (item, patch) => {
    const activeId = get().activeId
    const nextText = patch.text !== undefined ? patch.text.trim() || item.text : item.text
    const nextOwner = patch.owner !== undefined ? patch.owner.trim() : item.owner
    const nextDue = patch.due_date !== undefined ? patch.due_date.trim() : item.due_date
    const nextPriority = patch.priority !== undefined ? patch.priority : item.priority
    set((prev) => ({
      actionItems: prev.actionItems.map((a) =>
        a.id === item.id
          ? { ...a, text: nextText, owner: nextOwner, due_date: nextDue, priority: nextPriority }
          : a,
      ),
    }))
    if (activeId && item.id) {
      try {
        await api.patchMeetingActionItem({
          meeting_id: activeId,
          item_id: item.id,
          text: nextText,
          owner: nextOwner,
          due_date: nextDue,
          priority:
            nextPriority === 'high' || nextPriority === 'medium' || nextPriority === 'low'
              ? nextPriority
              : undefined,
        })
        set((prev) => ({
          savedMeetings: prev.savedMeetings.map((m) =>
            m.id === activeId
              ? {
                  ...m,
                  action_items: m.action_items.map((a) =>
                    a.id === item.id
                      ? { ...a, text: nextText, owner: nextOwner, due_date: nextDue, priority: nextPriority }
                      : a,
                  ),
                }
              : m,
          ),
        }))
        notifyMeetingsChanged()
      } catch {
        scheduleDebouncedAutoSave()
      }
    } else {
      scheduleDebouncedAutoSave()
    }
  },

  addActionItem: (text, owner, dueDate) => {
    const txt = text.trim()
    if (!txt) return
    const cur = get()
    set((prev) => ({
      actionItems: [
        ...prev.actionItems,
        {
          id: `act_${Date.now()}_${Math.random().toString(36).slice(2, 5)}`,
          meeting_id: cur.activeId,
          meeting_title: cur.title || `会议纪要 ${cur.meetingDate}`,
          text: txt,
          owner: owner.trim(),
          due_date: dueDate.trim(),
          priority: 'medium',
          status: 'open',
        },
      ],
    }))
    scheduleDebouncedAutoSave()
  },

  removeActionItem: (id) => {
    set((prev) => ({ actionItems: prev.actionItems.filter((a) => a.id !== id) }))
    scheduleDebouncedAutoSave()
  },

  restoreCrashDraft: () => {
    if (get().recordState !== 'idle') return false
    const draft = readCrashDraftSnapshot()
    if (!draft) return false
    const segs = Array.isArray(draft.segments) ? draft.segments : []
    const liveTxt = draft.liveText || ''
    const liveSec = draft.liveStartSec || 0
    const fullText = segmentsToTranscript(segs, liveTxt, liveSec)
    set({
      activeId: draft.activeId || '',
      title: draft.title || '',
      meetingDate: draft.meetingDate || todayDateString(),
      participantsText: draft.participantsText || '',
      projectKey: draft.projectKey || '',
      workspace: draft.workspace || '',
      durationSeconds: draft.durationSeconds || 0,
      segments: segs,
      rawTranscript: fullText,
      liveText: '',
      liveStartSec: liveSec,
      summary: draft.summary || '',
      topics: draft.topics || [],
      decisions: draft.decisions || [],
      risks: draft.risks || [],
      actionItems: draft.actionItems || [],
      minutesMarkdown: draft.minutesMarkdown || '',
      meetingCompleted: true,
      autoSaveState: 'idle',
    })
    scheduleDebouncedAutoSave()
    return true
  },

  dismissCrashDraft: () => {
    clearCrashDraftSnapshot()
  },

  _resetForTests: () => {
    clearMediaRuntime()
    closeSocketRuntime()
    clearR2t2PollTimer()
    clearEditDebounceTimer()
    clearCrashDraftSnapshot()
    elapsedRuntime = 0
    segStartSecRuntime = 0
    segSpeechSecRuntime = 0
    silenceMsRuntime = 0
    liveTextRuntime = ''
    pendingBookmarkRuntime = false
    pendingCommitMeta = []
    committingRuntime = false
    lastSummarizedChars = 0
    lastSummarizedSegCount = 0
    summarizingLock = false
    pendingFinalFullSummary = false
    saveInFlight = null
    pendingSaveRequested = false
    dirtyVersion = 0
    savedVersion = 0
    lastSavedTranscriptChars = 0
    lastCapsulePushAt = 0
    set(initialStateSnapshot())
  },
}))
