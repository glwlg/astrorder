import { useState } from 'react'
import { notifications } from '@mantine/notifications'
import {
  IconArrowRight,
  IconArrowUp,
  IconCheck,
  IconCode,
  IconFileText,
  IconGitCommit,
  IconMessageCircle,
  IconMicrophone,
  IconPlayerStop,
  IconRobot,
  IconX,
} from '@tabler/icons-react'
import type { LearningEvidenceItem, LearningTopic } from '../../../api/learning'
import { formatShortDateTime } from '../../learning/useLearningRadar'
import { applyVoiceTranscript, useVoiceInput } from '../../chat/useVoiceInput'
import { VOICE_INPUT_PLUGIN_ID } from '../../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../../plugins/pluginSettingsStore'

export type MobileLearningSheetTab = 'evidence' | 'notes'

interface MobileLearningSheetProps {
  topic: LearningTopic
  initialTab: MobileLearningSheetTab
  mentorSlug?: string
  startingStudy?: boolean
  savingNotes?: boolean
  onClose: () => void
  onOpenSession: (agentId: string, sessionId: string) => void
  onSaveNotes: (topic: LearningTopic, notes: string) => Promise<void>
  onStartStudy: (topic: LearningTopic) => Promise<void>
}

function evidenceIcon(kind: string) {
  switch (kind) {
    case 'session':
      return <IconMessageCircle size={17} />
    case 'commit':
      return <IconGitCommit size={17} />
    case 'file':
      return <IconCode size={17} />
    default:
      return <IconFileText size={17} />
  }
}

export function MobileLearningSheet({
  topic,
  initialTab,
  mentorSlug,
  startingStudy = false,
  savingNotes = false,
  onClose,
  onOpenSession,
  onSaveNotes,
  onStartStudy,
}: MobileLearningSheetProps) {
  const [tab, setTab] = useState<MobileLearningSheetTab>(initialTab)
  const [quickNote, setQuickNote] = useState('')
  const [notesDraft, setNotesDraft] = useState(topic.study_notes || '')
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const voicePluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(VOICE_INPUT_PLUGIN_ID))

  const voice = useVoiceInput(`learning-note:${topic.id}`, (transcript, event) => {
    if (tab === 'notes') {
      setNotesDraft((prev) => applyVoiceTranscript(prev, transcript, event))
    } else {
      setQuickNote((prev) => applyVoiceTranscript(prev, transcript, event))
    }
  })

  const copyText = async (text: string, key: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopiedKey(key)
      window.setTimeout(() => setCopiedKey(null), 1600)
      notifications.show({ color: 'gray', message: '已复制路径/标识' })
    } catch {
      notifications.show({ color: 'yellow', message: '浏览器未允许复制，请手动选择' })
    }
  }

  const handleEvidenceClick = (item: LearningEvidenceItem, index: number) => {
    if (item.agent_id && item.session_id) {
      onClose()
      onOpenSession(item.agent_id, item.session_id)
      return
    }
    const value = item.file_path || item.commit_sha || item.label
    if (value) {
      void copyText(value, `ev-${index}`)
    }
  }

  const handleAppendQuickNote = async () => {
    const trimmed = quickNote.trim()
    if (!trimmed) return
    const nextNotes = notesDraft.trim() ? `${notesDraft.trim()}\n- ${trimmed}` : `- ${trimmed}`
    setNotesDraft(nextNotes)
    setQuickNote('')
    await onSaveNotes(topic, nextNotes)
  }

  const handleSaveFullNotes = async () => {
    await onSaveNotes(topic, notesDraft)
  }

  const isRecording = voice.status === 'recording' || voice.status === 'requesting'

  return (
    <div
      className="m-learning-sheet-backdrop"
      role="presentation"
      onClick={onClose}
    >
      <section
        className="m-learning-sheet"
        role="dialog"
        aria-modal="true"
        aria-label="溯源证据与速记抽屉"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="m-learning-sheet-handle" aria-hidden="true" />

        <header className="m-learning-sheet-head">
          <div className="m-learning-sheet-tabs" role="tablist" aria-label="抽屉内容切换">
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'evidence'}
              className={`m-learning-sheet-tab${tab === 'evidence' ? ' is-active' : ''}`}
              onClick={() => setTab('evidence')}
            >
              溯源证据 ({topic.evidence.length})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={tab === 'notes'}
              className={`m-learning-sheet-tab${tab === 'notes' ? ' is-active' : ''}`}
              onClick={() => setTab('notes')}
            >
              速记与语音笔记
            </button>
          </div>
          <button
            type="button"
            className="m-learning-sheet-close"
            aria-label="关闭抽屉"
            onClick={onClose}
          >
            <IconX size={17} />
          </button>
        </header>

        <div className="m-learning-sheet-body">
          {tab === 'evidence' ? (
            <>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <div>
                  <strong style={{ fontSize: 14 }}>原工作现场线索</strong>
                  <div style={{ fontSize: 12, color: 'var(--ml-muted)', marginTop: 2 }}>
                    来自近期真实会话、代码文件与提交记录的证据链
                  </div>
                </div>
                {topic.project_name && (
                  <span className="m-learning-mono-chip">{topic.project_name}</span>
                )}
              </div>

              {topic.evidence.length === 0 ? (
                <div className="m-learning-evidence-row">
                  <span className="m-learning-evidence-sub">暂无详细证据条目</span>
                </div>
              ) : (
                topic.evidence.map((ev, idx) => {
                  const hasSession = Boolean(ev.agent_id && ev.session_id)
                  const copied = copiedKey === `ev-${idx}`
                  const subParts = [
                    ev.timestamp ? formatShortDateTime(ev.timestamp) : '',
                    ev.snippet || ev.file_path || ev.commit_sha || '',
                  ].filter(Boolean)
                  return (
                    <div key={`${ev.kind}-${ev.label}-${idx}`} className="m-learning-evidence-row">
                      <div className="m-learning-evidence-icon">{evidenceIcon(ev.kind)}</div>
                      <div className="m-learning-evidence-main">
                        <span className="m-learning-evidence-label">{ev.label}</span>
                        {subParts.length > 0 && (
                          <span className="m-learning-evidence-sub">{subParts.join(' · ')}</span>
                        )}
                      </div>
                      <button
                        type="button"
                        className="m-learning-evidence-action"
                        onClick={() => handleEvidenceClick(ev, idx)}
                      >
                        {hasSession ? (
                          '进会话 ›'
                        ) : copied ? (
                          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3 }}>
                            <IconCheck size={12} /> 已复制
                          </span>
                        ) : ev.kind === 'commit' ? (
                          '复制 SHA ›'
                        ) : (
                          '复制路径 ›'
                        )}
                      </button>
                    </div>
                  )
                })
              )}
            </>
          ) : (
            <>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <strong style={{ fontSize: 14 }}>我的理解速记</strong>
                {topic.notes_file_path && (
                  <span className="m-learning-mono-chip" title={topic.notes_file_path}>
                    {topic.notes_file_path.split(/[/\\]/).pop()}
                  </span>
                )}
              </div>
              <textarea
                className="m-learning-notes-textarea"
                aria-label="学习笔记内容"
                placeholder="用自己的话总结核心原理，或点下方麦克风直接口述记录…"
                value={notesDraft}
                onChange={(e) => setNotesDraft(e.target.value)}
              />
              <button
                type="button"
                className="m-learning-secondary-cta"
                disabled={savingNotes}
                onClick={() => void handleSaveFullNotes()}
              >
                {savingNotes ? '保存中…' : '保存速记到主题卡片'}
              </button>
            </>
          )}
        </div>

        {/* 快捷速记与语音输入栏 */}
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 11.5, color: 'var(--ml-muted)', marginBottom: 6 }}>
            <span>快捷速记与闪念（归档至本题卡片）</span>
            <span style={{ fontFamily: 'var(--ml-mono)', color: isRecording ? 'var(--ml-red)' : 'var(--ml-indigo)' }}>
              {isRecording ? '● REC' : voice.status === 'transcribing' ? '● TRANSCRIBING' : '● READY'}
            </span>
          </div>
          {voice.error && (
            <div style={{ fontSize: 11.5, color: 'var(--ml-red)', marginBottom: 5 }}>{voice.error}</div>
          )}
          <div className="m-learning-quicknote-bar">
            <input
              type="text"
              className="m-learning-quicknote-input"
              aria-label="快捷速记输入"
              placeholder="记一句你的理解或待办想法，按回车即可保存…"
              value={quickNote}
              onChange={(e) => setQuickNote(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  void handleAppendQuickNote()
                }
              }}
            />
            {voicePluginEnabled && (
              <button
                type="button"
                className={`m-learning-voice-btn${isRecording ? ' is-recording' : ''}`}
                aria-label={isRecording ? '停止语音速记' : '开始语音速记'}
                disabled={voice.status === 'transcribing'}
                onClick={voice.toggle}
              >
                {isRecording ? <IconPlayerStop size={16} /> : <IconMicrophone size={16} />}
              </button>
            )}
            <button
              type="button"
              className="m-learning-send-note-btn"
              aria-label="保存快捷速记"
              disabled={!quickNote.trim() || savingNotes}
              onClick={() => void handleAppendQuickNote()}
            >
              <IconArrowUp size={16} />
            </button>
          </div>
        </div>

        <button
          type="button"
          className="m-learning-primary-cta"
          disabled={startingStudy}
          onClick={() => void onStartStudy(topic)}
        >
          <IconRobot size={18} />
          <span>{startingStudy ? '正在派发导师 Bot…' : '让成长导师 Bot 用我的代码讲透这条'}</span>
          <IconArrowRight size={16} />
        </button>
        <p className="m-learning-sheet-caption">
          依托绑定的 @{mentorSlug || 'learning-mentor'} 展开 1v1 交互式讲解与代码推演
        </p>
      </section>
    </div>
  )
}
