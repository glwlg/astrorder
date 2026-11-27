import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Button, Checkbox, Group, Stack, Text } from '@mantine/core'
import {
  IconArrowUpRight,
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconCode,
  IconHistory,
  IconSparkles,
} from '@tabler/icons-react'
import type { LearningTopic } from '../../api/learning'
import {
  LEITNER_ROUNDS,
  categoryMeta,
  formatDaysUntil,
  splitSessionKey,
} from './useLearningRadar'

interface ReviewQueueTabProps {
  topics: LearningTopic[]
  busyTopicId?: string | null
  startingStudyId?: string | null
  onSubmitReview: (topic: LearningTopic, outcome: 'fuzzy' | 'remembered' | 'mastered', syncSchedule: boolean) => void
  onStartReviewDrill: (topic: LearningTopic) => void
  onSwitchToRadar: () => void
}

export function ReviewQueueTab({
  topics,
  busyTopicId,
  startingStudyId,
  onSubmitReview,
  onStartReviewDrill,
  onSwitchToRadar,
}: ReviewQueueTabProps) {
  const navigate = useNavigate()
  const [selectedId, setSelectedId] = useState<string>('')
  const [questionIndex, setQuestionIndex] = useState<number>(0)
  const [showAnswer, setShowAnswer] = useState<boolean>(true)
  const [syncSchedule, setSyncSchedule] = useState<boolean>(true)

  const { dueItems, upcomingItems } = useMemo(() => {
    const due: LearningTopic[] = []
    const upcoming: LearningTopic[] = []
    for (const t of topics) {
      if (t.status === 'review_due') {
        due.push(t)
      } else if (t.status === 'learning' && t.review.stage > 0) {
        upcoming.push(t)
      }
    }
    upcoming.sort((a, b) => (a.review.next_review_at || '').localeCompare(b.review.next_review_at || ''))
    return { dueItems: due, upcomingItems: upcoming }
  }, [topics])

  const allQueue = useMemo(() => [...dueItems, ...upcomingItems], [dueItems, upcomingItems])
  const activeTopic = useMemo(() => {
    if (allQueue.length === 0) return null
    return allQueue.find((t) => t.id === selectedId) || allQueue[0]
  }, [allQueue, selectedId])

  const selectTopic = (id: string) => {
    setSelectedId(id)
    setQuestionIndex(0)
    setShowAnswer(false)
  }

  const openSessionKey = (sessionKey?: string) => {
    const parsed = splitSessionKey(sessionKey)
    if (!parsed) return
    navigate(`/chat/${encodeURIComponent(parsed.sessionId)}?agent_id=${encodeURIComponent(parsed.agentId)}`)
  }

  if (allQueue.length === 0) {
    return (
      <div className="learning-empty-box">
        <Stack align="center" gap="xs">
          <IconHistory size={30} color="var(--astr-muted)" />
          <Text fw={600} size="sm">间隔复习队列暂时为空</Text>
          <Text size="xs" c="dimmed" maw={440}>
            在「盲区雷达」中点击任意主题的「用我的代码讲给我听」或「加入间隔复习」，星序就会按 1 / 3 / 7 / 21 天节奏安排定期自测抽查。
          </Text>
          <Button size="xs" color="dark" mt="xs" onClick={onSwitchToRadar}>
            返回盲区雷达
          </Button>
        </Stack>
      </div>
    )
  }

  const currentStage = Math.max(1, activeTopic?.review.stage || 1)
  const nextStageInfo = LEITNER_ROUNDS.find((r) => r.stage === currentStage + 1)
  const questions = activeTopic?.quiz_questions || []
  const safeQIdx = questions.length > 0 ? Math.min(questionIndex, questions.length - 1) : 0
  const activeQuestion = questions[safeQIdx]
  const firstFile = activeTopic?.evidence.find((e) => e.file_path)?.file_path

  return (
    <div className="learning-review-grid">
      {/* 左侧队列 */}
      <div className="learning-panel-card">
        <Group justify="space-between">
          <Text fw={600} size="sm">复习队列</Text>
          <span className="learning-mono-chip">{dueItems.length} 到期</span>
        </Group>

        {dueItems.length > 0 && (
          <Stack gap={6}>
            <Group gap={6}>
              <span className="learning-dot" style={{ backgroundColor: '#c92a2a' }} />
              <Text size="xs" fw={600}>今日到期 ({dueItems.length})</Text>
            </Group>
            {dueItems.map((item) => {
              const selected = activeTopic?.id === item.id
              return (
                <button
                  key={item.id}
                  type="button"
                  className={`learning-topic-card${selected ? ' is-selected' : ''}`}
                  style={{ padding: '10px 12px', textAlign: 'left', gap: 5 }}
                  onClick={() => selectTopic(item.id)}
                >
                  <Group justify="space-between" gap={4}>
                    <span className={`learning-chip ${categoryMeta(item.category).chipClass}`}>
                      {categoryMeta(item.category).shortLabel}
                    </span>
                    <Text size="xs" c="red" fw={600}>到期</Text>
                  </Group>
                  <Text size="xs" fw={600} lineClamp={2}>{item.title}</Text>
                  <Text size="xs" c="dimmed">
                    第 {item.review.stage || 1} 轮 · {item.review.interval_days || 1} 天间隔
                  </Text>
                </button>
              )
            })}
          </Stack>
        )}

        {upcomingItems.length > 0 && (
          <Stack gap={6}>
            <Text size="xs" c="dimmed" fw={600}>即将到来 ({upcomingItems.length})</Text>
            {upcomingItems.map((item) => {
              const selected = activeTopic?.id === item.id
              return (
                <button
                  key={item.id}
                  type="button"
                  className={`learning-topic-card${selected ? ' is-selected' : ''}`}
                  style={{ padding: '9px 12px', textAlign: 'left', gap: 4, opacity: selected ? 1 : 0.82 }}
                  onClick={() => selectTopic(item.id)}
                >
                  <Group justify="space-between" gap={4}>
                    <Text size="xs" c="dimmed">{item.project_name || '通用'}</Text>
                    <Text size="xs" c="dimmed">{formatDaysUntil(item.review.next_review_at)}</Text>
                  </Group>
                  <Text size="xs" fw={550} lineClamp={2}>{item.title}</Text>
                  <Text size="xs" c="dimmed">第 {item.review.stage} 轮 · {item.review.interval_days} 天间隔</Text>
                </button>
              )
            })}
          </Stack>
        )}

        <Text size="xs" c="dimmed" pt={6} style={{ borderTop: '1px solid var(--astr-border)' }}>
          连续通过 4 轮抽查的主题将自动归档至「已掌握档案」，并从后续盲区扫描中排除。
        </Text>
      </div>

      {/* 右侧高专注自测卡 */}
      {activeTopic && (
        <div className="learning-panel-card">
          <Group justify="space-between" align="center" wrap="wrap" gap="xs">
            <Group gap={6}>
              <span className={`learning-chip ${categoryMeta(activeTopic.category).chipClass}`}>
                {categoryMeta(activeTopic.category).label}
              </span>
              {activeTopic.project_name && <span className="learning-mono-chip">{activeTopic.project_name}</span>}
            </Group>

            {/* 4 轮 Leitner 步进器 */}
            <div className="learning-stepper">
              {LEITNER_ROUNDS.map((r) => {
                const isDone = r.stage < currentStage
                const isCurrent = r.stage === currentStage
                return (
                  <span
                    key={r.stage}
                    className={`learning-step-pill${isCurrent ? ' is-current' : isDone ? ' is-done' : ''}`}
                  >
                    {isDone && <IconCheck size={11} />}
                    {r.label}
                  </span>
                )
              })}
            </div>
          </Group>

          <Text fw={650} size="lg" lh={1.35}>{activeTopic.title}</Text>

          {/* 关联源码与伴学会话锚点 */}
          <div className="learning-evidence-row" style={{ flexWrap: 'wrap' }}>
            <Group gap={6} style={{ minWidth: 0, flex: 1 }}>
              <IconCode size={14} />
              <Text size="xs" c="dimmed" truncate>
                {firstFile ? `关联文件：${firstFile}` : activeTopic.workspace || '基于近期工作现场提炼'}
              </Text>
            </Group>
            <Group gap={6}>
              {activeTopic.study_session_key && (
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="indigo"
                  rightSection={<IconArrowUpRight size={12} />}
                  onClick={() => openSessionKey(activeTopic.study_session_key)}
                >
                  跳回伴学会话
                </Button>
              )}
              <Button
                size="compact-xs"
                variant="default"
                leftSection={<IconSparkles size={12} />}
                loading={startingStudyId === activeTopic.id}
                onClick={() => onStartReviewDrill(activeTopic)}
              >
                让导师 Bot 抽查
              </Button>
            </Group>
          </div>

          {/* 活跃自测题 */}
          {activeQuestion ? (
            <div className="learning-quiz-item" style={{ padding: '14px 16px' }}>
              <Group justify="space-between" mb={8}>
                <Text size="xs" c="dimmed" fw={500}>
                  自测题 {safeQIdx + 1} / {questions.length}（核心原理解构）
                </Text>
                {questions.length > 1 && (
                  <Group gap={4}>
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      disabled={safeQIdx === 0}
                      onClick={() => {
                        setQuestionIndex((i) => Math.max(0, i - 1))
                        setShowAnswer(false)
                      }}
                    >
                      上一题
                    </Button>
                    <Button
                      size="compact-xs"
                      variant="subtle"
                      color="gray"
                      disabled={safeQIdx >= questions.length - 1}
                      onClick={() => {
                        setQuestionIndex((i) => Math.min(questions.length - 1, i + 1))
                        setShowAnswer(false)
                      }}
                    >
                      下一题
                    </Button>
                  </Group>
                )}
              </Group>

              <Text fw={600} size="sm" lh={1.6}>{activeQuestion.question}</Text>

              <Group justify="space-between" align="center" mt={10}>
                <Button
                  size="compact-xs"
                  variant="subtle"
                  color="indigo"
                  rightSection={showAnswer ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
                  onClick={() => setShowAnswer((v) => !v)}
                >
                  {showAnswer ? '隐藏参考要点' : '展开参考要点'}
                </Button>
              </Group>

              {showAnswer && (
                <div className="learning-quiz-answer">
                  {activeQuestion.hint && (
                    <Text size="xs" c="dimmed" mb={6}>切入提示：{activeQuestion.hint}</Text>
                  )}
                  <Text size="xs" lh={1.6}>
                    {activeQuestion.answer || activeTopic.key_points.join('；') || '结合关联文件回顾关键设计决策。'}
                  </Text>
                </div>
              )}
            </div>
          ) : (
            <div className="learning-quiz-item" style={{ padding: '14px 16px' }}>
              <Text size="xs" fw={600} mb={6}>核心要点回顾</Text>
              <Stack gap={4}>
                {activeTopic.key_points.map((pt, i) => (
                  <Text key={i} size="xs" lh={1.55}>{i + 1}. {pt}</Text>
                ))}
              </Stack>
            </div>
          )}

          {activeTopic.study_notes && (
            <div className="learning-evidence-row" style={{ flexDirection: 'column', alignItems: 'flex-start', gap: 4 }}>
              <Text size="xs" fw={600} c="dimmed">原伴学会话记录笔记</Text>
              <Text size="xs" lh={1.55}>{activeTopic.study_notes}</Text>
            </div>
          )}

          {/* 3 档复习自评操作栏 */}
          <div>
            <Text size="xs" c="dimmed" mb={8}>本次抽查自测反馈（选定后立即生效并调整下次复习周期）</Text>
            <div className="learning-rating-grid">
              <Button
                variant="default"
                size="sm"
                loading={busyTopicId === activeTopic.id}
                onClick={() => onSubmitReview(activeTopic, 'fuzzy', syncSchedule)}
              >
                有点模糊 · 明天重测
              </Button>
              <Button
                color="dark"
                size="sm"
                loading={busyTopicId === activeTopic.id}
                onClick={() => onSubmitReview(activeTopic, 'remembered', syncSchedule)}
              >
                {nextStageInfo ? `记住了 · ${nextStageInfo.days} 天后下一轮` : '记住了 · 完成全部复习'}
              </Button>
              <Button
                variant="default"
                size="sm"
                loading={busyTopicId === activeTopic.id}
                onClick={() => onSubmitReview(activeTopic, 'mastered', syncSchedule)}
              >
                彻底掌握 · 移入档案
              </Button>
            </div>
          </div>

          <Checkbox
            size="xs"
            label="同时在原伴学会话保持到期提醒（复用星序会话定时任务）"
            checked={syncSchedule}
            onChange={(e) => setSyncSchedule(e.currentTarget.checked)}
          />
        </div>
      )}
    </div>
  )
}
