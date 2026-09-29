import { describe, expect, it } from 'vitest'
import type { DraftState } from '../../domain/types'
import { appendReviewOnce, appendReviewToDraft, buildUncommittedReviewCommand, extractNativeReviewComments, isRecoverableReviewRun, reviewOutputPending, reviewRunTimedOut } from './reviewRelay'

const draft = (text: string): DraftState => ({ text, attachments: [], sessionRefs: [] })

const first = '::code-comment{title="[P1] 缺少校验" body="需要校验失败分支。" file="src/a.ts" start=12 end=14 priority=1}'
const second = '::code-comment{title="[P2] 缺少测试" body="补充回归测试。" file="src/a.test.ts" start=8 end=9 priority=2}'

describe('review relay', () => {
  it('uses the existing Astrorder uncommitted-review selection for the review session', () => {
    expect(buildUncommittedReviewCommand({ id: 'run-1', agent_id: 'codex', session_id: 'review-session' })).toEqual({
      id: 'run-1',
      agent_id: 'codex',
      session_id: 'review-session',
      action: 'send',
      text: '请检查我未提交的更改',
      attachment_ids: [],
      target_id: null,
    })
  })

  it('recovers only the latest timed-out run without re-dispatching a review', () => {
    expect(isRecoverableReviewRun({ status: 'failed', error: 'Codex 审查超过 10 分钟未完成。', comment_text: null })).toBe(true)
    expect(isRecoverableReviewRun({ status: 'failed', error: '发送失败', comment_text: null })).toBe(false)
    expect(isRecoverableReviewRun({ status: 'failed', error: 'Codex 审查超过 10 分钟未完成。', comment_text: first })).toBe(false)
    expect(isRecoverableReviewRun({ status: 'reviewing', error: null, comment_text: null })).toBe(true)
    expect(isRecoverableReviewRun({ status: 'empty', error: null, comment_text: null })).toBe(true)
    expect(isRecoverableReviewRun({ status: 'draft_ready', error: null, comment_text: first })).toBe(false)
  })

  it('waits for delayed review messages after command completion before declaring no comments', () => {
    const completedAt = 1000
    expect(reviewOutputPending(completedAt, completedAt + 1000)).toBe(true)
    expect(reviewOutputPending(completedAt, completedAt + 2 * 60 * 1000)).toBe(true)
    expect(reviewOutputPending(completedAt, completedAt + 3 * 60 * 1000)).toBe(false)
  })

  it('checks terminal command state before declaring a resumed review timed out', () => {
    const started = 1000
    const later = started + 11 * 60 * 1000
    expect(reviewRunTimedOut('completed', started, later)).toBe(false)
    expect(reviewRunTimedOut('running', started, later)).toBe(true)
    expect(reviewRunTimedOut(undefined, started, later)).toBe(true)
  })

  it('keeps only native code-comment markers and preserves their order', () => {
    expect(extractNativeReviewComments(`普通审查说明\n${first}\n工具日志\n${second}`)).toEqual({
      rawText: `${first}\n\n${second}`,
      comments: 2,
    })
  })

  it('returns an empty batch when review output has no code comments', () => {
    expect(extractNativeReviewComments('没有需要修改的问题。')).toEqual({ rawText: '', comments: 0 })
  })

  it('appends to an existing draft without overwriting it', () => {
    expect(appendReviewToDraft(draft('继续修复'), first).text).toBe(`继续修复\n\n${first}`)
    expect(appendReviewToDraft(draft(''), first).text).toBe(first)
  })

  it('does not append a replayed native result twice', () => {
    expect(appendReviewOnce(draft(`继续修复\n\n${first}`), first).text).toBe(`继续修复\n\n${first}`)
    expect(appendReviewOnce(draft('继续修复'), '').text).toBe('继续修复')
  })
})
