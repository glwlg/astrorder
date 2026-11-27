import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { notifications } from '@mantine/notifications'
import {
  IconArrowLeft,
  IconClockPause,
  IconEyeOff,
  IconLoader2,
  IconRadar2,
  IconRefresh,
  IconRobot,
  IconSparkles,
  IconVolume,
  IconVolumeOff,
} from '@tabler/icons-react'
import { learningApi, type LearningTopic, type LearningTopicStatus } from '../../../api/learning'
import { LEARNING_OVERVIEW_QUERY_KEY, useLearningOverview } from '../../learning/useLearningRadar'
import { hapticFeedback } from '../mobileGestures'
import { MobileLearningCard } from './MobileLearningCard'
import { MobileLearningSheet, type MobileLearningSheetTab } from './MobileLearningSheet'
import './mobileLearningFeed.css'

type FeedFilter = 'recommended' | 'blackbox' | 'review' | 'all'

interface UndoState {
  topicId: string
  title: string
  previousStatus: LearningTopicStatus
  timerId: number
}

interface MobileLearningFeedProps {
  onClose: () => void
  onOpenSession: (agentId: string, sessionId: string) => void
}

function priorityRank(priority: string): number {
  if (priority === 'high') return 0
  if (priority === 'medium') return 1
  return 2
}

export function MobileLearningFeed({ onClose, onOpenSession }: MobileLearningFeedProps) {
  const queryClient = useQueryClient()
  const overview = useLearningOverview(true)

  const [filter, setFilter] = useState<FeedFilter>('recommended')
  const [activeIndex, setActiveIndex] = useState(0)
  const [sheetState, setSheetState] = useState<{ topicId: string; tab: MobileLearningSheetTab } | null>(null)
  const [moreTopic, setMoreTopic] = useState<LearningTopic | null>(null)
  const [busyTopicId, setBusyTopicId] = useState<string | null>(null)
  const [startingStudyId, setStartingStudyId] = useState<string | null>(null)
  const [savingNotes, setSavingNotes] = useState(false)
  const [scanning, setScanning] = useState(false)
  const [bootstrapping, setBootstrapping] = useState(false)
  const [speaking, setSpeaking] = useState(false)
  const [undoState, setUndoState] = useState<UndoState | null>(null)
  const [sessionStats, setSessionStats] = useState({ viewedIds: new Set<string>(), mastered: 0, reviewed: 0 })

  const deckRef = useRef<HTMLDivElement | null>(null)
  const slideRefs = useRef<Array<HTMLDivElement | null>>([])

  const data = overview.data
  const hasMentorBot = Boolean(data?.mentor_bot || data?.config.mentor_bot_id)
  const activeRun = data?.active_run

  const feedTopics = useMemo(() => {
    const all = data?.topics || []
    const unmastered = all.filter(
      (t) => t.status === 'open' || t.status === 'learning' || t.status === 'review_due',
    )
    let filtered = unmastered
    if (filter === 'blackbox') {
      filtered = unmastered.filter((t) => t.category === 'blackbox_code')
    } else if (filter === 'review') {
      filtered = unmastered.filter((t) => t.status === 'review_due' || t.review.stage > 0)
    } else if (filter === 'all') {
      filtered = all.filter((t) => t.status !== 'dismissed')
    }

    return [...filtered].sort((a, b) => {
      const aDue = a.status === 'review_due' ? 0 : 1
      const bDue = b.status === 'review_due' ? 0 : 1
      if (filter === 'recommended' && aDue !== bDue) return aDue - bDue
      const pr = priorityRank(a.priority) - priorityRank(b.priority)
      if (pr !== 0) return pr
      return (b.updated_at || '').localeCompare(a.updated_at || '')
    })
  }, [data?.topics, filter])

  const clampedIndex = feedTopics.length > 0 ? Math.min(activeIndex, feedTopics.length) : 0
  const currentTopic = clampedIndex < feedTopics.length ? feedTopics[clampedIndex] : null
  const sheetTopic = sheetState
    ? (data?.topics || []).find((t) => t.id === sheetState.topicId) || null
    : null

  const stopSpeech = useCallback(() => {
    if (typeof window !== 'undefined' && 'speechSynthesis' in window) {
      try {
        window.speechSynthesis.cancel()
      } catch {}
    }
    setSpeaking(false)
  }, [])

  useEffect(() => {
    return () => {
      stopSpeech()
    }
  }, [stopSpeech])

  useEffect(() => {
    return () => {
      if (undoState?.timerId) window.clearTimeout(undoState.timerId)
    }
  }, [undoState])

  // Observe active snap slide index
  useEffect(() => {
    const deck = deckRef.current
    if (!deck || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting && entry.intersectionRatio >= 0.6) {
            const idx = Number((entry.target as HTMLElement).dataset.slideIndex)
            if (Number.isFinite(idx)) {
              setActiveIndex((prev) => {
                if (prev !== idx) {
                  hapticFeedback(10)
                  stopSpeech()
                }
                return idx
              })
              const topic = feedTopics[idx]
              if (topic) {
                setSessionStats((prev) => {
                  if (prev.viewedIds.has(topic.id)) return prev
                  const next = new Set(prev.viewedIds)
                  next.add(topic.id)
                  return { ...prev, viewedIds: next }
                })
              }
            }
          }
        }
      },
      { root: deck, threshold: [0.6] },
    )
    for (const node of slideRefs.current) {
      if (node) observer.observe(node)
    }
    return () => observer.disconnect()
  }, [feedTopics, filter, stopSpeech])

  const refreshOverview = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: LEARNING_OVERVIEW_QUERY_KEY })
  }, [queryClient])

  const scrollToSlide = useCallback((idx: number) => {
    const target = slideRefs.current[idx]
    if (target && typeof target.scrollIntoView === 'function') {
      target.scrollIntoView({ behavior: 'smooth', block: 'start' })
    }
    setActiveIndex(idx)
  }, [])

  const toggleSpeech = () => {
    if (speaking) {
      stopSpeech()
      return
    }
    if (!currentTopic) return
    if (typeof window === 'undefined' || !('speechSynthesis' in window)) {
      notifications.show({ color: 'yellow', message: '当前浏览器不支持语音朗读' })
      return
    }
    const text = [
      currentTopic.title,
      currentTopic.why_now,
      ...currentTopic.key_points.slice(0, 3),
    ]
      .filter(Boolean)
      .join('。')
    try {
      window.speechSynthesis.cancel()
      const utter = new SpeechSynthesisUtterance(text)
      utter.lang = 'zh-CN'
      utter.rate = 1.05
      utter.onend = () => setSpeaking(false)
      utter.onerror = () => setSpeaking(false)
      setSpeaking(true)
      window.speechSynthesis.speak(utter)
    } catch {
      setSpeaking(false)
    }
  }

  const handleBootstrapBot = async () => {
    setBootstrapping(true)
    try {
      const resp = await learningApi.bootstrapMentorBot({})
      notifications.show({
        color: 'blue',
        message: resp.created
          ? `已创建「${resp.bot.name}」(@${resp.bot.slug})`
          : `已绑定「${resp.bot.name}」(@${resp.bot.slug})`,
      })
      await refreshOverview()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '初始化导师 Bot 失败' })
    } finally {
      setBootstrapping(false)
    }
  }

  const handleTriggerScan = async () => {
    setScanning(true)
    try {
      const resp = await learningApi.triggerScan({ lookback_days: data?.config.lookback_days || 7 })
      notifications.show({ color: 'blue', message: resp.message || '已派发成长导师 Bot 扫描盲区' })
      await refreshOverview()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '触发扫描失败' })
    } finally {
      setScanning(false)
    }
  }

  const handleMarkMastered = async (topic: LearningTopic) => {
    setBusyTopicId(topic.id)
    hapticFeedback([12, 30, 18])
    try {
      const previousStatus = topic.status
      await learningApi.updateTopic(topic.id, { status: 'mastered' })
      setSessionStats((prev) => ({ ...prev, mastered: prev.mastered + 1 }))
      if (undoState?.timerId) window.clearTimeout(undoState.timerId)
      const timerId = window.setTimeout(() => setUndoState(null), 3500)
      setUndoState({ topicId: topic.id, title: topic.title, previousStatus, timerId })
      await refreshOverview()
      if (clampedIndex < feedTopics.length - 1) {
        scrollToSlide(clampedIndex)
      }
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '标记已掌握失败' })
    } finally {
      setBusyTopicId(null)
    }
  }

  const handleUndoMastered = async () => {
    if (!undoState) return
    const pending = undoState
    window.clearTimeout(pending.timerId)
    setUndoState(null)
    try {
      await learningApi.updateTopic(pending.topicId, { status: pending.previousStatus })
      setSessionStats((prev) => ({ ...prev, mastered: Math.max(0, prev.mastered - 1) }))
      notifications.show({ color: 'gray', message: '已撤销，恢复至信息流' })
      await refreshOverview()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '撤销失败' })
    }
  }

  const handleReviewAction = async (
    topic: LearningTopic,
    outcome: 'enroll' | 'fuzzy' | 'remembered' | 'mastered',
  ) => {
    setBusyTopicId(topic.id)
    hapticFeedback(15)
    try {
      const resp = await learningApi.submitReview(topic.id, {
        outcome,
        sync_session_schedule: true,
      })
      setSessionStats((prev) => ({
        ...prev,
        reviewed: prev.reviewed + 1,
        mastered: outcome === 'mastered' ? prev.mastered + 1 : prev.mastered,
      }))
      notifications.show({ color: 'blue', message: resp.message || '已更新间隔复习计划' })
      await refreshOverview()
      if (clampedIndex < feedTopics.length - 1) {
        scrollToSlide(clampedIndex + 1)
      }
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '更新复习状态失败' })
    } finally {
      setBusyTopicId(null)
    }
  }

  const handleStartStudy = async (topic: LearningTopic) => {
    setStartingStudyId(topic.id)
    stopSpeech()
    try {
      const resp = await learningApi.startStudy(topic.id, {
        mode: topic.status === 'review_due' ? 'review_drill' : 'study',
        sync_session_schedule: true,
      })
      notifications.show({ color: 'blue', message: resp.message || '已开启导师 Bot 伴学会话' })
      setSheetState(null)
      await refreshOverview()
      if (resp.agent_id && resp.session_id) {
        onOpenSession(resp.agent_id, resp.session_id)
      }
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '开启伴学会话失败' })
    } finally {
      setStartingStudyId(null)
    }
  }

  const handleSaveNotes = async (topic: LearningTopic, notes: string) => {
    setSavingNotes(true)
    try {
      await learningApi.updateTopic(topic.id, { study_notes: notes })
      notifications.show({ color: 'blue', message: '速记已归档至卡片' })
      await refreshOverview()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '保存速记失败' })
    } finally {
      setSavingNotes(false)
    }
  }

  const handleSnoozeOrDismiss = async (topic: LearningTopic, status: 'snoozed' | 'dismissed') => {
    setMoreTopic(null)
    setBusyTopicId(topic.id)
    try {
      await learningApi.updateTopic(topic.id, {
        status,
        ...(status === 'snoozed' ? { snooze_days: 14 } : {}),
      })
      notifications.show({
        color: 'gray',
        message: status === 'snoozed' ? '已延后 14 天再推送' : '已忽略，后续扫描不再推荐',
      })
      await refreshOverview()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '操作失败' })
    } finally {
      setBusyTopicId(null)
    }
  }

  const totalCount = feedTopics.length
  const displayIndex = totalCount === 0 ? 0 : Math.min(clampedIndex + 1, totalCount)
  const progressPct = totalCount > 0 ? Math.min(100, (displayIndex / totalCount) * 100) : 0

  return (
    <div className="m-learning-shell" data-mobile-learning="feed">
      {/* Top Floating Nav */}
      <header className="m-learning-topbar">
        <div className="m-learning-topbar-row">
          <button
            type="button"
            className="m-learning-icon-btn"
            aria-label="返回会话"
            onClick={() => {
              stopSpeech()
              onClose()
            }}
          >
            <IconArrowLeft size={20} />
          </button>

          <nav className="m-learning-tabs" aria-label="知识信息流筛选">
            {([
              ['recommended', '推荐'],
              ['blackbox', '黑盒'],
              ['review', '复习'],
              ['all', '全部'],
            ] as const).map(([key, label]) => (
              <button
                key={key}
                type="button"
                className={`m-learning-tab${filter === key ? ' is-active' : ''}`}
                aria-pressed={filter === key}
                onClick={() => {
                  setFilter(key)
                  setActiveIndex(0)
                  if (deckRef.current) deckRef.current.scrollTop = 0
                }}
              >
                {label}
              </button>
            ))}
          </nav>

          <div className="m-learning-topbar-right">
            <button
              type="button"
              className={`m-learning-icon-btn${speaking ? ' is-active' : ''}`}
              aria-label={speaking ? '停止语音朗读' : '通勤语音朗读当前知识'}
              disabled={!currentTopic}
              onClick={toggleSpeech}
            >
              {speaking ? <IconVolumeOff size={18} /> : <IconVolume size={18} />}
            </button>
            {hasMentorBot && (
              <button
                type="button"
                className="m-learning-icon-btn"
                aria-label="让导师 Bot 立即扫描"
                disabled={scanning || activeRun?.status === 'running'}
                onClick={() => void handleTriggerScan()}
              >
                {scanning || activeRun?.status === 'running' ? (
                  <IconLoader2 size={17} className="m-spin" />
                ) : (
                  <IconRadar2 size={18} />
                )}
              </button>
            )}
            <span className="m-learning-counter" aria-label="当前进度">
              {displayIndex} / {totalCount}
            </span>
          </div>
        </div>

        <div className="m-learning-progress-track" aria-hidden="true">
          <div className="m-learning-progress-fill" style={{ width: `${progressPct}%` }} />
        </div>
      </header>

      {activeRun?.status === 'running' && (
        <div className="m-learning-scan-strip" role="status">
          <IconLoader2 size={14} className="m-spin" />
          <span>成长导师 Bot 正在分析近 {activeRun.lookback_days} 天工作痕迹…</span>
        </div>
      )}

      {/* Main Content */}
      {overview.isLoading && !data ? (
        <div className="m-learning-slide">
          <div className="m-learning-summary-card">
            <IconLoader2 size={28} className="m-spin" />
            <strong>正在加载成长雷达知识流…</strong>
          </div>
        </div>
      ) : !hasMentorBot ? (
        <div className="m-learning-slide">
          <div className="m-learning-summary-card">
            <IconRobot size={34} />
            <h2 style={{ margin: 0, fontSize: 19 }}>开启你的成长导师 Bot</h2>
            <p style={{ margin: 0, fontSize: 13.5, color: 'var(--ml-muted)', lineHeight: 1.55 }}>
              成长雷达由你选定的 Bot 驱动：每周自动提炼你真实会话与代码里的知识盲区，刷卡即学、30 秒自测。
            </p>
            <button
              type="button"
              className="m-learning-primary-cta"
              disabled={bootstrapping}
              onClick={() => void handleBootstrapBot()}
            >
              <IconSparkles size={17} />
              <span>{bootstrapping ? '正在初始化…' : '一键创建「成长导师」Bot'}</span>
            </button>
          </div>
        </div>
      ) : feedTopics.length === 0 ? (
        <div className="m-learning-slide">
          <div className="m-learning-summary-card">
            <IconRadar2 size={32} />
            <h2 style={{ margin: 0, fontSize: 19 }}>
              {filter === 'recommended' ? '当前暂无待学习知识卡片' : '当前筛选下暂无卡片'}
            </h2>
            <p style={{ margin: 0, fontSize: 13.5, color: 'var(--ml-muted)', lineHeight: 1.55 }}>
              你可以切换上方筛选，或立刻派发 @{data?.mentor_bot?.slug || 'learning-mentor'} 扫描最近 7 天的会话与 Git 改动。
            </p>
            <button
              type="button"
              className="m-learning-primary-cta"
              disabled={scanning || activeRun?.status === 'running'}
              onClick={() => void handleTriggerScan()}
            >
              <IconRadar2 size={17} />
              <span>{scanning ? '正在派发扫描…' : '让导师 Bot 立刻扫描'}</span>
            </button>
            {filter !== 'all' && (
              <button
                type="button"
                className="m-learning-secondary-cta"
                onClick={() => setFilter('all')}
              >
                查看全部知识卡片
              </button>
            )}
          </div>
        </div>
      ) : (
        <main ref={deckRef} className="m-learning-deck" aria-label="成长雷达知识信息流">
          {feedTopics.map((topic, idx) => (
            <div
              key={topic.id}
              ref={(el) => {
                slideRefs.current[idx] = el
              }}
              data-slide-index={idx}
              style={{ height: '100%' }}
            >
              <MobileLearningCard
                topic={topic}
                isLast={idx === feedTopics.length - 1}
                busy={busyTopicId === topic.id}
                startingStudy={startingStudyId === topic.id}
                onMarkMastered={(t) => void handleMarkMastered(t)}
                onReviewAction={(t, outcome) => void handleReviewAction(t, outcome)}
                onStartStudy={(t) => void handleStartStudy(t)}
                onOpenSheet={(t, tab) => setSheetState({ topicId: t.id, tab })}
                onOpenMore={(t) => setMoreTopic(t)}
              />
            </div>
          ))}

          {/* End-of-Deck Summary Slide */}
          <div
            ref={(el) => {
              slideRefs.current[feedTopics.length] = el
            }}
            data-slide-index={feedTopics.length}
            className="m-learning-slide"
          >
            <div className="m-learning-summary-card">
              <IconSparkles size={32} />
              <h2 style={{ margin: 0, fontSize: 20 }}>本轮知识卡片已刷完</h2>
              <p style={{ margin: 0, fontSize: 13.5, color: 'var(--ml-muted)', lineHeight: 1.55 }}>
                碎片时间也能把 AI 代写的代码真正吃透。
              </p>
              <div className="m-learning-summary-stats">
                <div className="m-learning-summary-stat">
                  <strong>{Math.max(sessionStats.viewedIds.size, feedTopics.length > 0 ? 1 : 0)}</strong>
                  <span>本轮已看</span>
                </div>
                <div className="m-learning-summary-stat">
                  <strong>{sessionStats.mastered}</strong>
                  <span>已掌握</span>
                </div>
                <div className="m-learning-summary-stat">
                  <strong>{sessionStats.reviewed}</strong>
                  <span>已复习</span>
                </div>
              </div>
              <button
                type="button"
                className="m-learning-primary-cta"
                onClick={() => scrollToSlide(0)}
              >
                <IconRefresh size={16} />
                <span>回到顶部再刷一轮</span>
              </button>
              <button
                type="button"
                className="m-learning-secondary-cta"
                disabled={scanning || activeRun?.status === 'running'}
                onClick={() => void handleTriggerScan()}
              >
                <IconRadar2 size={16} />
                <span>让导师 Bot 重新扫描近 {data?.config.lookback_days || 7} 天</span>
              </button>
            </div>
          </div>
        </main>
      )}

      {/* 3-Second Undo Toast */}
      {undoState && (
        <div className="m-learning-undo-toast" role="status" aria-live="polite">
          <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
            已归档「{undoState.title}」至已掌握
          </span>
          <button
            type="button"
            className="m-learning-undo-btn"
            onClick={() => void handleUndoMastered()}
          >
            撤销
          </button>
        </div>
      )}

      {/* Bottom Sheet: Evidence + Quick Voice Notes */}
      {sheetTopic && sheetState && (
        <MobileLearningSheet
          key={`${sheetTopic.id}:${sheetState.tab}:${sheetTopic.updated_at}`}
          topic={sheetTopic}
          initialTab={sheetState.tab}
          mentorSlug={data?.mentor_bot?.slug}
          startingStudy={startingStudyId === sheetTopic.id}
          savingNotes={savingNotes}
          onClose={() => setSheetState(null)}
          onOpenSession={onOpenSession}
          onSaveNotes={handleSaveNotes}
          onStartStudy={handleStartStudy}
        />
      )}

      {/* More Actions Sheet (Snooze / Dismiss) */}
      {moreTopic && (
        <div
          className="m-learning-sheet-backdrop"
          role="presentation"
          onClick={() => setMoreTopic(null)}
        >
          <section
            className="m-learning-sheet"
            role="dialog"
            aria-modal="true"
            aria-label="更多卡片操作"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="m-learning-sheet-handle" aria-hidden="true" />
            <strong style={{ fontSize: 15 }}>{moreTopic.title}</strong>
            <div className="m-learning-more-actions">
              <button
                type="button"
                className="m-learning-more-btn"
                onClick={() => void handleSnoozeOrDismiss(moreTopic, 'snoozed')}
              >
                <span>稍后再学（延后 14 天提醒）</span>
                <IconClockPause size={18} />
              </button>
              <button
                type="button"
                className="m-learning-more-btn is-danger"
                onClick={() => void handleSnoozeOrDismiss(moreTopic, 'dismissed')}
              >
                <span>不再关注（以后扫描不再推荐）</span>
                <IconEyeOff size={18} />
              </button>
            </div>
            <button
              type="button"
              className="m-learning-secondary-cta"
              onClick={() => setMoreTopic(null)}
            >
              取消
            </button>
          </section>
        </div>
      )}
    </div>
  )
}
