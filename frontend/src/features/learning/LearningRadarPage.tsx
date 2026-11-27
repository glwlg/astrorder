import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Alert,
  Button,
  Group,
  Loader,
  SegmentedControl,
  Stack,
  Text,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowUpRight,
  IconCalendarClock,
  IconCompass,
  IconPlus,
  IconRadar2,
  IconRobot,
  IconSettings,
} from '@tabler/icons-react'
import { api } from '../../api/client'
import {
  learningApi,
  type LearningScheduleConfig,
  type LearningTopic,
} from '../../api/learning'
import { MasteryArchivePanel } from './MasteryArchivePanel'
import { RadarOverviewTab } from './RadarOverviewTab'
import { ReviewQueueTab } from './ReviewQueueTab'
import { ScheduleAndHistoryTab } from './ScheduleAndHistoryTab'
import {
  LEARNING_OVERVIEW_QUERY_KEY,
  WEEKDAY_LABELS,
  formatShortDate,
  splitSessionKey,
  useLearningOverview,
} from './useLearningRadar'
import './learningRadar.css'

type LearningTab = 'radar' | 'review' | 'archive' | 'schedule'

export function LearningRadarPage() {
  const navigate = useNavigate()
  const location = useLocation()
  const queryClient = useQueryClient()
  const [tabOverride, setTabOverride] = useState<{ search: string; tab: LearningTab } | null>(null)
  const [lookbackOverride, setLookbackOverride] = useState<string | null>(null)
  const [scanning, setScanning] = useState(false)
  const [savingConfig, setSavingConfig] = useState(false)
  const [bootstrappingBot, setBootstrappingBot] = useState(false)
  const [busyTopicId, setBusyTopicId] = useState<string | null>(null)
  const [startingStudyId, setStartingStudyId] = useState<string | null>(null)

  const overview = useLearningOverview(true)
  const botsQuery = useQuery({
    queryKey: ['astrorder', 'bots'],
    queryFn: () => api.listBots(),
    retry: false,
    staleTime: 30_000,
  })

  const rawUrlTab = new URLSearchParams(location.search).get('tab')
  const urlTab: LearningTab =
    rawUrlTab === 'review' || rawUrlTab === 'archive' || rawUrlTab === 'schedule' ? rawUrlTab : 'radar'
  const activeTab: LearningTab =
    tabOverride && tabOverride.search === location.search ? tabOverride.tab : urlTab
  const setActiveTab = (tab: LearningTab) => setTabOverride({ search: location.search, tab })
  const lookbackDays = lookbackOverride ?? String(overview.data?.config.lookback_days || 7)
  const setLookbackDays = (value: string) => setLookbackOverride(value)

  const refreshAll = async () => {
    await queryClient.invalidateQueries({ queryKey: LEARNING_OVERVIEW_QUERY_KEY })
  }

  const handleBootstrapMentorBot = async () => {
    setBootstrappingBot(true)
    try {
      const resp = await learningApi.bootstrapMentorBot({})
      notifications.show({
        color: 'blue',
        message: resp.created
          ? `已创建并绑定官方「${resp.bot.name}」(@${resp.bot.slug})`
          : `已复用并绑定已有 Bot「${resp.bot.name}」(@${resp.bot.slug})`,
      })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'bots'] })
      await refreshAll()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '初始化导师 Bot 失败' })
    } finally {
      setBootstrappingBot(false)
    }
  }

  const handleTriggerScan = async () => {
    setScanning(true)
    try {
      const resp = await learningApi.triggerScan({ lookback_days: Number(lookbackDays) || 7 })
      notifications.show({
        color: 'blue',
        message: resp.message || '已派发成长导师 Bot 执行深度盲区扫描',
      })
      await refreshAll()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '触发扫描失败' })
    } finally {
      setScanning(false)
    }
  }

  const handleSaveConfig = async (next: LearningScheduleConfig) => {
    setSavingConfig(true)
    try {
      await learningApi.saveConfig(next)
      notifications.show({ color: 'blue', message: '每周定时扫描计划与导师 Bot 配置已保存' })
      await refreshAll()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '保存配置失败' })
    } finally {
      setSavingConfig(false)
    }
  }

  const handleStartStudy = async (topic: LearningTopic, mode: 'study' | 'review_drill' = 'study') => {
    setStartingStudyId(topic.id)
    try {
      const resp = await learningApi.startStudy(topic.id, {
        mode,
        sync_session_schedule: true,
      })
      notifications.show({ color: 'blue', message: resp.message || '已开启导师 Bot 伴学会话' })
      await refreshAll()
      if (resp.agent_id && resp.session_id) {
        navigate(`/chat/${encodeURIComponent(resp.session_id)}?agent_id=${encodeURIComponent(resp.agent_id)}`)
      }
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '开启伴学会话失败' })
    } finally {
      setStartingStudyId(null)
    }
  }

  const handleMarkStatus = async (topic: LearningTopic, status: 'mastered' | 'snoozed' | 'dismissed' | 'open') => {
    setBusyTopicId(topic.id)
    try {
      await learningApi.updateTopic(topic.id, {
        status,
        ...(status === 'snoozed' ? { snooze_days: 14 } : {}),
      })
      const msg =
        status === 'mastered'
          ? `已归档「${topic.title}」至已掌握档案`
          : status === 'snoozed'
            ? `已将「${topic.title}」延后 14 天再提醒`
            : status === 'open'
              ? `已将「${topic.title}」移回待学列表`
              : `已忽略「${topic.title}」，后续扫描不再推荐`
      notifications.show({ color: 'gray', message: msg })
      await refreshAll()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '更新状态失败' })
    } finally {
      setBusyTopicId(null)
    }
  }

  const handleSaveNotes = async (topic: LearningTopic, notes: string) => {
    setBusyTopicId(topic.id)
    try {
      await learningApi.updateTopic(topic.id, { study_notes: notes })
      notifications.show({ color: 'blue', message: '学习笔记已保存' })
      await refreshAll()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '保存笔记失败' })
    } finally {
      setBusyTopicId(null)
    }
  }

  const handleSubmitReview = async (
    topic: LearningTopic,
    outcome: 'enroll' | 'fuzzy' | 'remembered' | 'mastered',
    syncSchedule = true,
  ) => {
    setBusyTopicId(topic.id)
    try {
      const resp = await learningApi.submitReview(topic.id, {
        outcome,
        sync_session_schedule: syncSchedule,
      })
      notifications.show({ color: 'blue', message: resp.message || '已更新间隔复习状态' })
      await refreshAll()
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '提交复习反馈失败' })
    } finally {
      setBusyTopicId(null)
    }
  }

  const handleExportProfile = async () => {
    try {
      const data = await learningApi.exportProfile()
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `astrorder-learning-profile-${new Date().toISOString().slice(0, 10)}.json`
      a.click()
      URL.revokeObjectURL(url)
      notifications.show({ color: 'blue', message: '已导出个人技能掌握档案 JSON' })
    } catch (err) {
      notifications.show({ color: 'red', message: err instanceof Error ? err.message : '导出失败' })
    }
  }

  const data = overview.data
  const bots = botsQuery.data?.items || []
  const activeRun = data?.active_run
  const hasMentorBot = Boolean(data?.mentor_bot || data?.config.mentor_bot_id)
  const scheduleSummary = data?.config.enabled
    ? `每${WEEKDAY_LABELS[data.config.weekday] || '周日'} ${data.config.time} 自动扫描${data.config.next_run_at ? ` · 下次 ${formatShortDate(data.config.next_run_at)}` : ''}`
    : '每周自动扫描已暂停'

  const hasActiveTopics = (data?.topics || []).some(
    (t) => t.status === 'open' || t.status === 'learning' || t.status === 'review_due',
  )
  const showCompactBotBanner = !hasMentorBot && (activeTab !== 'radar' || hasActiveTopics)

  return (
    <div className="route-page learning-radar-page">
      {/* 48px 极简顶栏（居中留白，避免与顶部悬浮灵动岛重叠） */}
      <header className="learning-radar-topbar">
        <div className="learning-radar-topbar-left">
          <div className="learning-radar-brand">
            <IconCompass size={17} />
            <span>成长雷达</span>
          </div>
          <span className="learning-radar-vsep" aria-hidden="true" />
          <nav className="learning-radar-nav-tabs" aria-label="成长雷达视图切换">
            <button
              type="button"
              className={`learning-radar-nav-tab${activeTab === 'radar' ? ' is-active' : ''}`}
              onClick={() => setActiveTab('radar')}
            >
              盲区雷达
              {data && <span className="learning-radar-tab-count">{data.stats.pending_count}</span>}
            </button>
            <button
              type="button"
              className={`learning-radar-nav-tab${activeTab === 'review' ? ' is-active' : ''}`}
              onClick={() => setActiveTab('review')}
            >
              复习队列
              {data && <span className="learning-radar-tab-count">{data.stats.due_review_count}</span>}
            </button>
            <button
              type="button"
              className={`learning-radar-nav-tab${activeTab === 'archive' ? ' is-active' : ''}`}
              onClick={() => setActiveTab('archive')}
            >
              已掌握档案
              {data && <span className="learning-radar-tab-count">{data.stats.mastered_count}</span>}
            </button>
            <button
              type="button"
              className={`learning-radar-nav-tab${activeTab === 'schedule' ? ' is-active' : ''}`}
              onClick={() => setActiveTab('schedule')}
            >
              定时与历史
            </button>
          </nav>
        </div>

        <div className="learning-radar-topbar-actions">
          {data?.mentor_bot && (
            <span className="learning-mono-chip" title={`已绑定导师 Bot：${data.mentor_bot.name}`}>
              <IconRobot size={12} />@{data.mentor_bot.slug}
            </span>
          )}
          <button
            type="button"
            className="learning-schedule-pill"
            onClick={() => setActiveTab('schedule')}
            title="点击配置每周定时扫描计划与成长导师 Bot"
          >
            <IconCalendarClock size={14} />
            <span>{scheduleSummary}</span>
          </button>

          <SegmentedControl
            size="xs"
            value={lookbackDays}
            onChange={setLookbackDays}
            data={[
              { label: '近 7 天', value: '7' },
              { label: '14 天', value: '14' },
              { label: '30 天', value: '30' },
            ]}
          />

          <Button
            size="xs"
            color="dark"
            leftSection={<IconRadar2 size={14} />}
            loading={scanning || activeRun?.status === 'running'}
            disabled={!hasMentorBot}
            onClick={() => { void handleTriggerScan() }}
          >
            立即扫描
          </Button>
        </div>
      </header>

      {/* 扫描运行中实时提示条 */}
      {activeRun && activeRun.status === 'running' && (
        <div className="learning-active-banner" role="status">
          <Group gap={8}>
            <Loader size={14} color="indigo" />
            <Text size="xs" fw={500}>
              {activeRun.bot_name || '成长导师 Bot'} 正在深度分析近 {activeRun.lookback_days} 天的 {activeRun.evidence_count} 条本地工作线索，完成后将自动回写盲区卡片…
            </Text>
          </Group>
          {activeRun.session_key && (
            <Button
              size="compact-xs"
              variant="default"
              rightSection={<IconArrowUpRight size={13} />}
              onClick={() => {
                const parsed = splitSessionKey(activeRun.session_key)
                if (parsed) {
                  navigate(`/chat/${encodeURIComponent(parsed.sessionId)}?agent_id=${encodeURIComponent(parsed.agentId)}`)
                }
              }}
            >
              查看 Bot 扫描会话
            </Button>
          )}
        </div>
      )}

      {/* 主内容滚动区 */}
      <div className="learning-radar-scroll">
        <div className="learning-radar-container">
          {overview.isLoading ? (
            <Stack align="center" justify="center" py={64} gap="xs">
              <Loader size="sm" color="gray" />
              <Text size="xs" c="dimmed">正在加载成长雷达数据…</Text>
            </Stack>
          ) : overview.isError || !data ? (
            <Alert color="red" title="无法读取成长雷达数据">
              <Group justify="space-between" align="center">
                <Text size="xs">{overview.error instanceof Error ? overview.error.message : '服务端暂不可用'}</Text>
                <Button size="xs" variant="default" onClick={() => { void overview.refetch() }}>
                  重试
                </Button>
              </Group>
            </Alert>
          ) : (
            <>
              {/* 页面标题说明行（对齐 Stitch 主视图层级） */}
              <div className="learning-radar-intro">
                <div>
                  <Group gap={10} align="center">
                    <h1 className="learning-radar-title">
                      {activeTab === 'radar'
                        ? '成长雷达'
                        : activeTab === 'review'
                          ? '间隔复习队列'
                          : activeTab === 'archive'
                            ? '已掌握档案'
                            : '定时计划与扫描历史'}
                    </h1>
                    <span className="learning-mono-chip">Learning Radar</span>
                  </Group>
                  <p className="learning-radar-subtitle">
                    基于你的真实会话、代码改动、CI 失败与会议记录，由成长导师 Bot 自动发现知识盲区并用你的项目代码讲透
                  </p>
                </div>
              </div>

              {/* 尚未绑定成长导师 Bot 的轻引导（仅在非空雷达页或其他 Tab 展示，避免与空白引导 Hero 重复） */}
              {showCompactBotBanner && (
                <div className="learning-panel-card">
                  <Group justify="space-between" align="center" wrap="wrap" gap="md">
                    <Group gap={10}>
                      <IconRobot size={24} />
                      <div>
                        <Text fw={600} size="sm">先绑定一个「成长导师 Bot」即可启动自动盲区发现</Text>
                        <Text size="xs" c="dimmed" mt={2}>
                          无需配置内置 LLM 接口——成长雷达直接派发你信任的成熟 Agent Bot 读取真实代码与会话，提炼盲区并做 1v1 伴学。
                        </Text>
                      </div>
                    </Group>
                    <Group gap="xs">
                      <Button
                        size="xs"
                        color="dark"
                        leftSection={<IconPlus size={14} />}
                        loading={bootstrappingBot}
                        onClick={() => { void handleBootstrapMentorBot() }}
                      >
                        一键创建官方成长导师 Bot
                      </Button>
                      <Button
                        size="xs"
                        variant="default"
                        leftSection={<IconSettings size={14} />}
                        onClick={() => setActiveTab('schedule')}
                      >
                        绑定已有 Bot
                      </Button>
                    </Group>
                  </Group>
                </div>
              )}

              {activeTab === 'radar' && (
                <RadarOverviewTab
                  stats={data.stats}
                  topics={data.topics}
                  busyTopicId={busyTopicId}
                  startingStudyId={startingStudyId}
                  hasMentorBot={hasMentorBot}
                  mentorSlug={data.mentor_bot?.slug}
                  lookbackDays={Number(lookbackDays) || 7}
                  bootstrappingBot={bootstrappingBot}
                  onBootstrapMentorBot={() => { void handleBootstrapMentorBot() }}
                  onStartStudy={(topic) => { void handleStartStudy(topic, 'study') }}
                  onEnrollReview={(topic) => { void handleSubmitReview(topic, 'enroll', true) }}
                  onMarkStatus={(topic, status) => { void handleMarkStatus(topic, status) }}
                  onSaveNotes={(topic, notes) => { void handleSaveNotes(topic, notes) }}
                  onSwitchMainTab={setActiveTab}
                  onTriggerScan={() => { void handleTriggerScan() }}
                  scanning={scanning}
                />
              )}

              {activeTab === 'review' && (
                <ReviewQueueTab
                  topics={data.topics}
                  busyTopicId={busyTopicId}
                  startingStudyId={startingStudyId}
                  onSubmitReview={(topic, outcome, sync) => { void handleSubmitReview(topic, outcome, sync) }}
                  onStartReviewDrill={(topic) => { void handleStartStudy(topic, 'review_drill') }}
                  onSwitchToRadar={() => setActiveTab('radar')}
                />
              )}

              {activeTab === 'archive' && (
                <MasteryArchivePanel
                  topics={data.topics}
                  busyTopicId={busyTopicId}
                  onRestoreTopic={(topic) => { void handleMarkStatus(topic, 'open') }}
                  onExport={() => { void handleExportProfile() }}
                />
              )}

              {activeTab === 'schedule' && (
                <ScheduleAndHistoryTab
                  key={`${data.config.updated_at || ''}:${data.config.mentor_bot_id || ''}`}
                  config={data.config}
                  bots={bots}
                  mentorBot={data.mentor_bot}
                  runs={data.runs}
                  topics={data.topics}
                  savingConfig={savingConfig}
                  bootstrappingBot={bootstrappingBot}
                  busyTopicId={busyTopicId}
                  onSaveConfig={(next) => { void handleSaveConfig(next) }}
                  onBootstrapMentorBot={() => { void handleBootstrapMentorBot() }}
                  onRestoreTopic={(topic) => { void handleMarkStatus(topic, 'open') }}
                  onExportProfile={() => { void handleExportProfile() }}
                  onJumpToRadar={() => setActiveTab('radar')}
                />
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
