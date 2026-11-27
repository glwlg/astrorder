import { useState, type ReactNode } from 'react'
import {
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconClock,
  IconCode,
  IconDots,
  IconEdit,
  IconFileCode,
  IconHelpCircle,
  IconMessageChatbot,
  IconRepeat,
} from '@tabler/icons-react'
import type { LearningTopic } from '../../../api/learning'
import { categoryMeta, LEITNER_ROUNDS } from '../../learning/useLearningRadar'
import type { MobileLearningSheetTab } from './MobileLearningSheet'

function renderInlineCode(text: string): ReactNode {
  const parts = text.split(/(`[^`]+`)/g)
  if (parts.length === 1) return text
  return parts.map((part, idx) =>
    part.startsWith('`') && part.endsWith('`') && part.length > 2 ? (
      <code key={idx} className="m-learning-inline-code">
        {part.slice(1, -1)}
      </code>
    ) : (
      <span key={idx}>{part}</span>
    ),
  )
}

interface MobileLearningCardProps {
  topic: LearningTopic
  isLast: boolean
  busy: boolean
  startingStudy: boolean
  onMarkMastered: (topic: LearningTopic) => void
  onReviewAction: (topic: LearningTopic, outcome: 'enroll' | 'fuzzy' | 'remembered' | 'mastered') => void
  onStartStudy: (topic: LearningTopic) => void
  onOpenSheet: (topic: LearningTopic, tab: MobileLearningSheetTab) => void
  onOpenMore: (topic: LearningTopic) => void
}

export function MobileLearningCard({
  topic,
  isLast,
  busy,
  startingStudy,
  onMarkMastered,
  onReviewAction,
  onStartStudy,
  onOpenSheet,
  onOpenMore,
}: MobileLearningCardProps) {
  const [quizIndex, setQuizIndex] = useState(0)
  const [answerRevealed, setAnswerRevealed] = useState(false)

  const cat = categoryMeta(topic.category)
  const quizzes = topic.quiz_questions || []
  const currentQuiz = quizzes[quizIndex] || quizzes[0]
  const isReviewMode = topic.status === 'review_due' || topic.review.stage > 0
  const nextStageIdx = Math.min(LEITNER_ROUNDS.length - 1, Math.max(0, topic.review.stage))
  const nextDays = LEITNER_ROUNDS[nextStageIdx]?.days ?? 7
  const firstFileEvidence = topic.evidence.find((e) => e.file_path)?.file_path || topic.evidence[0]?.label

  const handleNextQuiz = () => {
    if (quizzes.length <= 1) return
    setQuizIndex((prev) => (prev + 1) % quizzes.length)
    setAnswerRevealed(false)
  }

  return (
    <article className="m-learning-slide" data-topic-id={topic.id}>
      <div className="m-learning-slide-stage">
        {/* Main Scannable Knowledge Card (3-zone: fixed head | scrollable body | pinned quiz) */}
        <div className="m-learning-card">
          <div className="m-learning-card-head">
            <div className="m-learning-card-meta">
              <div className="m-learning-chip-row">
                <span className={`m-learning-cat-chip ${cat.chipClass}`}>{cat.label}</span>
                {topic.project_name && (
                  <span className="m-learning-mono-chip" title={topic.project_name}>
                    {topic.project_name}
                  </span>
                )}
                {topic.status === 'review_due' && (
                  <span className="m-learning-mono-chip">
                    第 {Math.max(1, topic.review.stage)} 轮复习
                  </span>
                )}
              </div>
              <span className="m-learning-time-meta">
                <IconClock size={11} />约 {Math.max(1, topic.estimated_minutes || 3)} 分钟
              </span>
            </div>

            <h2 className="m-learning-card-title">{renderInlineCode(topic.title)}</h2>
          </div>

          <div className="m-learning-card-body">
            {topic.why_now && (
              <div className="m-learning-why-callout">
                {renderInlineCode(topic.why_now)}
              </div>
            )}

            {topic.key_points.length > 0 && (
              <>
                <div className="m-learning-section-label">
                  <span className="m-learning-section-dot" aria-hidden="true" />
                  <span>核心原理（30 秒速懂）</span>
                </div>
                <ol className="m-learning-points">
                  {topic.key_points.slice(0, 4).map((pt, idx) => (
                    <li key={idx} className="m-learning-point">
                      <span className="m-learning-point-num">{idx + 1}</span>
                      <span className="m-learning-point-text">{renderInlineCode(pt)}</span>
                    </li>
                  ))}
                </ol>
              </>
            )}
          </div>

          {currentQuiz && (
            <div className="m-learning-quiz-box">
              <div className="m-learning-quiz-head">
                <span className="m-learning-quiz-title">
                  <IconHelpCircle size={13} />
                  <span>30 秒自测 · 点击揭晓</span>
                </span>
                <span className="m-learning-quiz-pager">
                  Q{quizIndex + 1} / {quizzes.length}
                  {quizzes.length > 1 && (
                    <button type="button" onClick={handleNextQuiz}>
                      换一题
                    </button>
                  )}
                </span>
              </div>

              <p className="m-learning-quiz-question">{renderInlineCode(currentQuiz.question)}</p>

              {!answerRevealed ? (
                <button
                  type="button"
                  className="m-learning-quiz-reveal-btn"
                  onClick={() => setAnswerRevealed(true)}
                >
                  <span>轻点展开参考要点</span>
                  <IconChevronDown size={14} />
                </button>
              ) : (
                <div className="m-learning-quiz-answer">
                  <div>
                    {renderInlineCode(
                      currentQuiz.answer || currentQuiz.hint || '建议结合上方核心原理与原项目代码复述一遍。',
                    )}
                  </div>
                  {currentQuiz.hint && currentQuiz.answer && (
                    <div className="m-learning-quiz-hint">提示：{currentQuiz.hint}</div>
                  )}
                  <div className="m-learning-review-bar">
                    <button
                      type="button"
                      className="m-learning-review-pill"
                      disabled={busy}
                      onClick={() => onReviewAction(topic, 'fuzzy')}
                    >
                      有点模糊
                    </button>
                    <button
                      type="button"
                      className="m-learning-review-pill is-primary"
                      disabled={busy}
                      onClick={() => onReviewAction(topic, isReviewMode ? 'remembered' : 'enroll')}
                    >
                      {isReviewMode ? `记住了 · ${nextDays}天后` : '加入复习'}
                    </button>
                    <button
                      type="button"
                      className="m-learning-review-pill"
                      disabled={busy}
                      onClick={() => onMarkMastered(topic)}
                    >
                      彻底掌握
                    </button>
                  </div>
                </div>
              )}
            </div>
          )}
        </div>

        {/* Right Floating Thumb Action Rail */}
        <aside className="m-learning-rail" aria-label="快捷交互栏">
          <button
            type="button"
            className="m-learning-rail-item is-obsidian"
            aria-label="标记已掌握"
            disabled={busy}
            onClick={() => onMarkMastered(topic)}
          >
            <span className="m-learning-rail-circle">
              <IconCheck size={19} stroke={2.4} />
            </span>
            <span className="m-learning-rail-label">已掌握</span>
          </button>

          <button
            type="button"
            className="m-learning-rail-item"
            aria-label={isReviewMode ? '复习记住了' : '加入间隔复习'}
            disabled={busy}
            onClick={() => onReviewAction(topic, isReviewMode ? 'remembered' : 'enroll')}
          >
            <span className="m-learning-rail-circle">
              <IconRepeat size={18} />
            </span>
            <span className="m-learning-rail-label">{isReviewMode ? '记住了' : '加入复习'}</span>
          </button>

          <button
            type="button"
            className="m-learning-rail-item is-accent"
            aria-label="问成长导师 Bot"
            disabled={startingStudy}
            onClick={() => onStartStudy(topic)}
          >
            <span className="m-learning-rail-circle">
              <IconMessageChatbot size={18} />
            </span>
            <span className="m-learning-rail-label">{startingStudy ? '派发中' : '问导师'}</span>
          </button>

          <button
            type="button"
            className="m-learning-rail-item"
            aria-label={`查看溯源证据，共 ${topic.evidence.length} 条`}
            onClick={() => onOpenSheet(topic, 'evidence')}
          >
            <span className="m-learning-rail-circle">
              <IconCode size={18} />
              {topic.evidence.length > 0 && (
                <span className="m-learning-rail-badge">{topic.evidence.length}</span>
              )}
            </span>
            <span className="m-learning-rail-label">溯源</span>
          </button>

          <button
            type="button"
            className="m-learning-rail-item"
            aria-label="记录速记与语音笔记"
            onClick={() => onOpenSheet(topic, 'notes')}
          >
            <span className="m-learning-rail-circle">
              <IconEdit size={18} />
              {Boolean(topic.study_notes?.trim()) && <span className="m-learning-rail-dot" />}
            </span>
            <span className="m-learning-rail-label">速记</span>
          </button>

          <button
            type="button"
            className="m-learning-rail-item"
            aria-label="稍后或忽略"
            disabled={busy}
            onClick={() => onOpenMore(topic)}
          >
            <span className="m-learning-rail-circle">
              <IconDots size={18} />
            </span>
            <span className="m-learning-rail-label">稍后</span>
          </button>
        </aside>
      </div>

      {/* Bottom File Anchor + Swipe Hint */}
      <footer className="m-learning-slide-foot">
        {firstFileEvidence ? (
          <button
            type="button"
            className="m-learning-file-anchor"
            onClick={() => onOpenSheet(topic, 'evidence')}
          >
            <IconFileCode size={13} />
            <span>{firstFileEvidence}</span>
          </button>
        ) : (
          <span />
        )}
        {!isLast && (
          <span className="m-learning-swipe-hint">
            <span>上滑切换下一条</span>
            <IconChevronUp size={13} />
          </span>
        )}
      </footer>
    </article>
  )
}
