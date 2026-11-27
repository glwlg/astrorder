import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Group, Stack, Text, Textarea } from '@mantine/core'
import {
  IconAlertTriangle,
  IconArrowUpRight,
  IconBook2,
  IconCheck,
  IconChevronDown,
  IconChevronRight,
  IconCode,
  IconFileText,
  IconGitCommit,
  IconHistory,
  IconMessageCircle,
  IconPlus,
  IconRadar2,
  IconRobot,
  IconSettings,
  IconSparkles,
} from '@tabler/icons-react'
import type { LearningOverviewStats, LearningTopic } from '../../api/learning'
import { categoryMeta, formatShortDateTime, priorityMeta, splitSessionKey } from './useLearningRadar'

type FeedFilter = 'all' | 'blackbox_code' | 'recurring_friction' | 'failure_pattern' | 'new_domain' | 'review_due'

interface RadarOverviewTabProps {
  stats: LearningOverviewStats
  topics: LearningTopic[]
  busyTopicId?: string | null
  startingStudyId?: string | null
  hasMentorBot: boolean
  mentorSlug?: string
  lookbackDays?: number
  bootstrappingBot?: boolean
  onBootstrapMentorBot?: () => void
  onStartStudy: (topic: LearningTopic) => void
  onEnrollReview: (topic: LearningTopic) => void
  onMarkStatus: (topic: LearningTopic, status: 'mastered' | 'snoozed' | 'dismissed') => void
  onSaveNotes: (topic: LearningTopic, notes: string) => void
  onSwitchMainTab: (tab: 'radar' | 'review' | 'archive' | 'schedule') => void
  onTriggerScan: () => void
  scanning: boolean
}

export function RadarOverviewTab({
  stats,
  topics,
  busyTopicId,
  startingStudyId,
  hasMentorBot,
  mentorSlug,
  lookbackDays = 7,
  bootstrappingBot = false,
  onBootstrapMentorBot,
  onStartStudy,
  onEnrollReview,
  onMarkStatus,
  onSaveNotes,
  onSwitchMainTab,
  onTriggerScan,
  scanning,
}: RadarOverviewTabProps) {
  const navigate = useNavigate()
  const [filter, setFilter] = useState<FeedFilter>('all')
  const [selectedId, setSelectedId] = useState<string>('')
  const [expandedQuiz, setExpandedQuiz] = useState<Record<string, boolean>>({})
  const [draftNotesById, setDraftNotesById] = useState<Record<string, string>>({})

  const activeTopics = useMemo(
    () => topics.filter((t) => t.status === 'open' || t.status === 'learning' || t.status === 'review_due'),
    [topics],
  )

  const filteredTopics = useMemo(() => {
    if (filter === 'all') return activeTopics
    if (filter === 'review_due') return activeTopics.filter((t) => t.status === 'review_due')
    return activeTopics.filter((t) => t.category === filter)
  }, [activeTopics, filter])

  const selectedTopic = useMemo(() => {
    if (filteredTopics.length === 0) return null
    return filteredTopics.find((t) => t.id === selectedId) || filteredTopics[0]
  }, [filteredTopics, selectedId])

  const noteValue = selectedTopic
    ? draftNotesById[selectedTopic.id] ?? selectedTopic.study_notes ?? ''
    : ''

  const openSessionKey = (sessionKey?: string) => {
    const parsed = splitSessionKey(sessionKey)
    if (!parsed) return
    navigate(`/chat/${encodeURIComponent(parsed.sessionId)}?agent_id=${encodeURIComponent(parsed.agentId)}`)
  }

  return (
    <Stack gap={16}>
      {/* 4 项极简 KPI 条 */}
      <div className="learning-kpi-grid">
        <button type="button" className="learning-kpi-card" onClick={() => setFilter('all')}>
          <div>
            <div className="learning-kpi-label">待补课盲区</div>
            <div className="learning-kpi-sub">本周新增 {stats.weekly_new_count} · 关联 {stats.project_count} 个项目</div>
          </div>
          <div className="learning-kpi-value">{stats.pending_count}</div>
        </button>

        <button type="button" className="learning-kpi-card" onClick={() => setFilter('blackbox_code')}>
          <div>
            <div className="learning-kpi-label">AI 代写黑盒</div>
            <div className="learning-kpi-sub">高改动未细读模块</div>
          </div>
          <div className="learning-kpi-value">{stats.blackbox_count}</div>
        </button>

        <button type="button" className="learning-kpi-card" onClick={() => onSwitchMainTab('review')}>
          <div>
            <div className="learning-kpi-label">到期复习</div>
            <div className="learning-kpi-sub">间隔重复抽查队列</div>
          </div>
          <div className="learning-kpi-value">{stats.due_review_count}</div>
        </button>

        <button type="button" className="learning-kpi-card" onClick={() => onSwitchMainTab('archive')}>
          <div>
            <div className="learning-kpi-label">已掌握档案</div>
            <div className="learning-kpi-sub">自动排除重复推荐</div>
          </div>
          <div className="learning-kpi-value">{stats.mastered_count}</div>
        </button>
      </div>

      {/* 无待学主题时展示整宽引导面板，避免出现残缺双栏与孤立右侧占位框 */}
      {activeTopics.length === 0 ? (
        <div className="learning-onboarding-hero">
          <Stack gap={12}>
            <Group gap={8}>
              <span className="learning-chip is-blackbox">
                <IconRobot size={13} />
                {hasMentorBot ? `已绑定 @${mentorSlug || 'learning-mentor'}` : '100% Bot 驱动 · 零内置 LLM'}
              </span>
              <span className="learning-mono-chip">每周定时 + 手动触发</span>
            </Group>
            <div>
              <Text fw={700} size="lg" lh={1.35}>
                {hasMentorBot
                  ? '成长导师 Bot 已就位，随时可以开始首轮盲区扫描'
                  : '绑定或一键创建「成长导师 Bot」，让星序自动发现你的知识盲区'}
              </Text>
              <Text size="sm" c="dimmed" mt={6} lh={1.6}>
                {hasMentorBot
                  ? `点击下方按钮即可派发成长导师 Bot 深度分析近 ${lookbackDays} 天的工作痕迹，自动识别 AI 代写黑盒、高频卡点与报错模式，生成可交互自测与伴学卡片。`
                  : '无需配置内置大模型接口。星序会自动采集你的本地工作痕迹并交付给你选定的成熟 Agent Bot 深入阅读真实源码，提炼盲区卡片、展开 1v1 伴学，并支持移动端竖滑刷卡复习。'}
              </Text>
            </div>
            <Group gap={10} mt={4}>
              {hasMentorBot ? (
                <>
                  <Button
                    size="sm"
                    color="dark"
                    leftSection={<IconRadar2 size={15} />}
                    loading={scanning}
                    onClick={onTriggerScan}
                  >
                    让导师 Bot 立即扫描近 {lookbackDays} 天
                  </Button>
                  <Button
                    size="sm"
                    variant="default"
                    leftSection={<IconSettings size={15} />}
                    onClick={() => onSwitchMainTab('schedule')}
                  >
                    调整定时计划与范围
                  </Button>
                </>
              ) : (
                <>
                  <Button
                    size="sm"
                    color="dark"
                    leftSection={<IconPlus size={15} />}
                    loading={bootstrappingBot}
                    onClick={onBootstrapMentorBot}
                  >
                    一键创建官方成长导师 Bot
                  </Button>
                  <Button
                    size="sm"
                    variant="default"
                    leftSection={<IconSettings size={15} />}
                    onClick={() => onSwitchMainTab('schedule')}
                  >
                    绑定已有 Bot
                  </Button>
                </>
              )}
            </Group>
          </Stack>

          <div className="learning-onboarding-channels">
            <div className="learning-channel-card">
              <div className="learning-channel-title">
                <IconMessageCircle size={15} />
                <span>跨 Agent 会话轨迹</span>
              </div>
              <div className="learning-channel-desc">
                识别反复追问、高频概念卡点与尚未消化透彻的技术原理解释。
              </div>
            </div>
            <div className="learning-channel-card">
              <div className="learning-channel-title">
                <IconGitCommit size={15} />
                <span>Git 提交与代码检查点</span>
              </div>
              <div className="learning-channel-desc">
                定位 AI 大量代写且核心逻辑复杂的黑盒改动，用你自己的代码讲透。
              </div>
            </div>
            <div className="learning-channel-card">
              <div className="learning-channel-title">
                <IconAlertTriangle size={15} />
                <span>任务与 GitLab CI 失败</span>
              </div>
              <div className="learning-channel-desc">
                自动归纳重复报错与流水线失败模式，提炼成防踩坑自测卡片。
              </div>
            </div>
            <div className="learning-channel-card">
              <div className="learning-channel-title">
                <IconFileText size={15} />
                <span>会议纪要与间隔复习</span>
              </div>
              <div className="learning-channel-desc">
                抽取新涉足领域术语，结合 1 / 3 / 7 / 21 天间隔抽查巩固记忆。
              </div>
            </div>
          </div>
        </div>
      ) : (
      /* 主从分栏 */
      <div className="learning-master-detail">
        {/* 左列：筛选 + 盲区卡片流 */}
        <div>
          <div className="learning-filter-bar" role="tablist" aria-label="盲区分类筛选">
            <button
              type="button"
              role="tab"
              aria-selected={filter === 'all'}
              className={`learning-filter-btn${filter === 'all' ? ' is-active' : ''}`}
              onClick={() => setFilter('all')}
            >
              全部待学 ({stats.pending_count})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={filter === 'blackbox_code'}
              className={`learning-filter-btn${filter === 'blackbox_code' ? ' is-active' : ''}`}
              onClick={() => setFilter('blackbox_code')}
            >
              AI 代写黑盒 ({stats.blackbox_count})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={filter === 'recurring_friction'}
              className={`learning-filter-btn${filter === 'recurring_friction' ? ' is-active' : ''}`}
              onClick={() => setFilter('recurring_friction')}
            >
              高频卡点 ({stats.friction_count})
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={filter === 'failure_pattern'}
              className={`learning-filter-btn${filter === 'failure_pattern' ? ' is-active' : ''}`}
              onClick={() => setFilter('failure_pattern')}
            >
              报错模式 ({stats.failure_count})
            </button>
            {stats.new_domain_count > 0 && (
              <button
                type="button"
                role="tab"
                aria-selected={filter === 'new_domain'}
                className={`learning-filter-btn${filter === 'new_domain' ? ' is-active' : ''}`}
                onClick={() => setFilter('new_domain')}
              >
                新域 ({stats.new_domain_count})
              </button>
            )}
            <button
              type="button"
              role="tab"
              aria-selected={filter === 'review_due'}
              className={`learning-filter-btn${filter === 'review_due' ? ' is-active' : ''}`}
              onClick={() => setFilter('review_due')}
            >
              待复习 ({stats.due_review_count})
            </button>
          </div>

          {filteredTopics.length === 0 ? (
            <div className="learning-empty-box">
              <Stack align="center" gap="xs">
                <IconSparkles size={28} color="var(--astr-muted)" />
                <Text fw={600} size="sm">
                  {activeTopics.length === 0 ? '当前没有待补课的盲区卡片' : '该分类下暂无待学卡片'}
                </Text>
                <Text size="xs" c="dimmed" maw={420}>
                  {hasMentorBot
                    ? '点击「立即扫描」，成长导师 Bot 会自动梳理你近期的会话、代码改动与 CI 失败记录，提炼高价值知识盲区。'
                    : '请先在顶部或「定时与历史」中绑定一个成长导师 Bot（支持一键创建官方导师 Bot）。'}
                </Text>
                {hasMentorBot ? (
                  <Button size="xs" color="dark" mt="xs" loading={scanning} onClick={onTriggerScan}>
                    让导师 Bot 立即扫描
                  </Button>
                ) : (
                  <Button size="xs" color="dark" mt="xs" onClick={() => onSwitchMainTab('schedule')}>
                    前往配置导师 Bot
                  </Button>
                )}
              </Stack>
            </div>
          ) : (
            <div className="learning-topic-list">
              {filteredTopics.map((topic) => {
                const cat = categoryMeta(topic.category)
                const prio = priorityMeta(topic.priority)
                const isSelected = selectedTopic?.id === topic.id
                return (
                  <div
                    key={topic.id}
                    className={`learning-topic-card${isSelected ? ' is-selected' : ''}`}
                    onClick={() => setSelectedId(topic.id)}
                  >
                    <Group justify="space-between" gap="xs" wrap="wrap">
                      <Group gap={6} wrap="wrap">
                        <span className={`learning-chip ${cat.chipClass}`}>{cat.label}</span>
                        {topic.project_name && <span className="learning-mono-chip">{topic.project_name}</span>}
                        <Text size="xs" c="dimmed">约 {topic.estimated_minutes || 10} 分钟</Text>
                        {topic.status === 'review_due' && (
                          <span className="learning-chip">待复习 · 第 {topic.review.stage || 1} 轮</span>
                        )}
                      </Group>
                      <Group gap={5} wrap="nowrap">
                        <span className="learning-dot" style={{ backgroundColor: prio.dotColor }} />
                        <Text size="xs" c="dimmed">{prio.label}</Text>
                      </Group>
                    </Group>

                    <h3 className="learning-topic-title">{topic.title}</h3>
                    {topic.why_now && <p className="learning-topic-why">{topic.why_now}</p>}

                    {topic.evidence.length > 0 && (
                      <div className="learning-evidence-chips">
                        {topic.evidence.slice(0, 3).map((ev, idx) => (
                          <span key={`${ev.kind}-${ev.label}-${idx}`} className="learning-mono-chip">
                            {ev.kind === 'session' ? '会话: ' : ev.kind === 'commit' ? '提交 ' : ''}
                            {ev.label}
                          </span>
                        ))}
                      </div>
                    )}

                    <Group justify="space-between" align="center" pt={4}>
                      <Button
                        size="compact-sm"
                        color="dark"
                        leftSection={<IconBook2 size={14} />}
                        loading={startingStudyId === topic.id}
                        onClick={(e) => {
                          e.stopPropagation()
                          onStartStudy(topic)
                        }}
                      >
                        用我的代码讲给我听
                      </Button>

                      <Group gap={4}>
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          color="gray"
                          loading={busyTopicId === topic.id}
                          onClick={(e) => {
                            e.stopPropagation()
                            onMarkStatus(topic, 'mastered')
                          }}
                        >
                          已掌握
                        </Button>
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          color="gray"
                          loading={busyTopicId === topic.id}
                          onClick={(e) => {
                            e.stopPropagation()
                            onMarkStatus(topic, 'dismissed')
                          }}
                        >
                          忽略
                        </Button>
                      </Group>
                    </Group>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* 右列：选中盲区的溯源证据与伴学详情 */}
        {selectedTopic ? (
          <div className="learning-inspector">
            <div>
              <Group justify="space-between" gap="xs" wrap="wrap" mb={6}>
                <span className={`learning-chip ${categoryMeta(selectedTopic.category).chipClass}`}>
                  {categoryMeta(selectedTopic.category).label}
                </span>
                {selectedTopic.workspace && (
                  <Text size="xs" c="dimmed" ff="monospace" truncate maw={280} title={selectedTopic.workspace}>
                    {selectedTopic.workspace}
                  </Text>
                )}
              </Group>
              <Text fw={650} size="md" lh={1.4}>{selectedTopic.title}</Text>
              {selectedTopic.why_now && (
                <Text size="xs" c="dimmed" mt={6} lh={1.55}>{selectedTopic.why_now}</Text>
              )}
            </div>

            <Group gap={8} wrap="wrap">
              <Button
                style={{ flex: 1 }}
                size="xs"
                color="dark"
                leftSection={<IconSparkles size={14} />}
                loading={startingStudyId === selectedTopic.id}
                onClick={() => onStartStudy(selectedTopic)}
              >
                用我的代码讲给我听 · 开启伴学
              </Button>
              {selectedTopic.review.stage > 0 ? (
                <Button
                  size="xs"
                  variant="default"
                  leftSection={<IconHistory size={14} />}
                  onClick={() => onSwitchMainTab('review')}
                >
                  进入复习队列
                </Button>
              ) : (
                <Button
                  size="xs"
                  variant="default"
                  leftSection={<IconHistory size={14} />}
                  loading={busyTopicId === selectedTopic.id}
                  onClick={() => onEnrollReview(selectedTopic)}
                >
                  加入间隔复习
                </Button>
              )}
            </Group>

            {selectedTopic.study_session_key && (
              <div className="learning-evidence-row">
                <Text size="xs" c="dimmed">已关联导师伴学会话</Text>
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="indigo"
                  rightSection={<IconArrowUpRight size={13} />}
                  onClick={() => openSessionKey(selectedTopic.study_session_key)}
                >
                  继续原伴学会话
                </Button>
              </div>
            )}

            {/* 溯源证据 */}
            <div>
              <div className="learning-section-head">
                <span className="learning-section-title">溯源证据 ({selectedTopic.evidence.length} 条)</span>
                <Text size="xs" c="dimmed">Git / Chat Trace</Text>
              </div>
              {selectedTopic.evidence.length === 0 ? (
                <Text size="xs" c="dimmed">暂无附带溯源链接。</Text>
              ) : (
                <Stack gap={6}>
                  {selectedTopic.evidence.map((ev, idx) => (
                    <div key={`${ev.kind}-${idx}`} className="learning-evidence-row">
                      <Group gap={8} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                        {ev.kind === 'session' ? (
                          <IconMessageCircle size={14} style={{ flexShrink: 0 }} />
                        ) : ev.kind === 'commit' ? (
                          <IconGitCommit size={14} style={{ flexShrink: 0 }} />
                        ) : (
                          <IconCode size={14} style={{ flexShrink: 0 }} />
                        )}
                        <div style={{ minWidth: 0, flex: 1 }}>
                          <Text size="xs" fw={500} truncate title={ev.file_path || ev.label}>
                            {ev.label}
                          </Text>
                          {(ev.file_path || ev.timestamp || ev.commit_sha) && (
                            <Text size="xs" c="dimmed" ff="monospace" truncate>
                              {ev.file_path || ev.commit_sha || formatShortDateTime(ev.timestamp)}
                            </Text>
                          )}
                        </div>
                      </Group>
                      {ev.agent_id && ev.session_id && (
                        <Button
                          size="compact-xs"
                          variant="subtle"
                          color="indigo"
                          rightSection={<IconArrowUpRight size={12} />}
                          onClick={() => openSessionKey(`${ev.agent_id}::${ev.session_id}`)}
                        >
                          跳回会话
                        </Button>
                      )}
                    </div>
                  ))}
                </Stack>
              )}
            </div>

            {/* 核心要点 */}
            {selectedTopic.key_points.length > 0 && (
              <div>
                <div className="learning-section-head">
                  <span className="learning-section-title">核心要点（速懂精华）</span>
                  <Text size="xs" c="dimmed">提炼自工程现状</Text>
                </div>
                <Stack gap={6}>
                  {selectedTopic.key_points.map((pt, idx) => (
                    <Text key={idx} size="xs" lh={1.55}>
                      <strong style={{ marginRight: 6 }}>{idx + 1}.</strong>
                      {pt}
                    </Text>
                  ))}
                </Stack>
              </div>
            )}

            {/* 快速自测题 */}
            {selectedTopic.quiz_questions.length > 0 && (
              <div>
                <div className="learning-section-head">
                  <span className="learning-section-title">快速自测题 ({selectedTopic.quiz_questions.length} 题)</span>
                  <Text size="xs" c="dimmed">主动回忆检验</Text>
                </div>
                <Stack gap={8}>
                  {selectedTopic.quiz_questions.map((q, idx) => {
                    const qKey = `${selectedTopic.id}:${idx}`
                    const open = Boolean(expandedQuiz[qKey])
                    return (
                      <div key={qKey} className="learning-quiz-item">
                        <Text size="xs" fw={600} lh={1.5}>
                          Q{idx + 1}: {q.question}
                        </Text>
                        <Button
                          mt={4}
                          p={0}
                          h="auto"
                          size="compact-xs"
                          variant="transparent"
                          color="indigo"
                          rightSection={open ? <IconChevronDown size={12} /> : <IconChevronRight size={12} />}
                          onClick={() => setExpandedQuiz((prev) => ({ ...prev, [qKey]: !prev[qKey] }))}
                        >
                          {open ? '收起参考提示' : '查看参考提示与要点'}
                        </Button>
                        {open && (
                          <div className="learning-quiz-answer">
                            {q.hint && <Text size="xs" c="dimmed" mb={4}>切入点：{q.hint}</Text>}
                            <Text size="xs">{q.answer || '建议点击上方「用我的代码讲给我听」，让导师 Bot 结合源码逐步验证你的理解。'}</Text>
                          </div>
                        )}
                      </div>
                    )
                  })}
                </Stack>
              </div>
            )}

            {/* 学习笔记 */}
            <div>
              <div className="learning-section-head">
                <span className="learning-section-title">我的学习笔记</span>
                {selectedTopic.notes_file_path && (
                  <span className="learning-mono-chip">
                    <IconFileText size={11} />
                    {selectedTopic.notes_file_path.split('/').pop()}
                  </span>
                )}
              </div>
              <Textarea
                size="xs"
                minRows={3}
                autosize
                placeholder="记录你的理解或实践结论，支持与已掌握档案一同保存…"
                value={noteValue}
                onChange={(e) => {
                  const value = e.currentTarget.value
                  setDraftNotesById((prev) => ({ ...prev, [selectedTopic.id]: value }))
                }}
              />
              {noteValue !== (selectedTopic.study_notes || '') && (
                <Group justify="flex-end" mt={6}>
                  <Button
                    size="compact-xs"
                    color="dark"
                    loading={busyTopicId === selectedTopic.id}
                    onClick={() => onSaveNotes(selectedTopic, noteValue)}
                  >
                    保存笔记
                  </Button>
                </Group>
              )}
            </div>

            {/* 底部操作条 */}
            <Group justify="space-between" align="center" pt={8} style={{ borderTop: '1px solid var(--astr-border)' }}>
              <Button
                size="xs"
                variant="default"
                leftSection={<IconCheck size={14} />}
                loading={busyTopicId === selectedTopic.id}
                onClick={() => onMarkStatus(selectedTopic, 'mastered')}
              >
                标记已掌握
              </Button>
              <Group gap={6}>
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="gray"
                  loading={busyTopicId === selectedTopic.id}
                  onClick={() => onMarkStatus(selectedTopic, 'snoozed')}
                >
                  14 天后再提醒
                </Button>
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="gray"
                  loading={busyTopicId === selectedTopic.id}
                  onClick={() => onMarkStatus(selectedTopic, 'dismissed')}
                >
                  永久忽略
                </Button>
              </Group>
            </Group>
          </div>
        ) : (
          <div className="learning-inspector">
            <Text size="xs" c="dimmed" ta="center" py="xl">
              当前筛选条件下暂无盲区卡片，请切换左侧分类查看其他待学主题。
            </Text>
          </div>
        )}
      </div>
      )}
    </Stack>
  )
}
