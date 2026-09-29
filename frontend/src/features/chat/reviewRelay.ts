import type { CommandPayload, DraftState } from '../../domain/types'

// Exactly the message sent by the existing /review → 审查未提交的更改 menu action.
export const UNCOMMITTED_REVIEW_MESSAGE = '请检查我未提交的更改'

export const REVIEW_TIMEOUT_ERROR = 'Codex 审查超过 10 分钟未完成。'

/** A completed command may be projected before its final assistant message arrives. */
export function reviewOutputPending(completedObservedAt: number, now: number): boolean {
  return now - completedObservedAt < 3 * 60 * 1000
}

export function isRecoverableReviewRun(run: { status: string; error: string | null; comment_text: string | null }): boolean {
  return run.status === 'reviewing' || run.status === 'forwarding'
    || (run.status === 'empty' && !run.comment_text)
    || (run.status === 'failed' && run.error === REVIEW_TIMEOUT_ERROR && !run.comment_text)
}

export function reviewRunTimedOut(commandState: string | undefined, startedAt: number, now: number): boolean {
  return !['completed', 'failed', 'cancelled'].includes(commandState || '') && now - startedAt > 10 * 60 * 1000
}

export type ReviewRelayStatus =
  | 'validating'
  | 'reviewing'
  | 'forwarding'
  | 'draft_ready'
  | 'empty'
  | 'failed'

export interface ReviewRelayRun {
  id: string
  sourceAgentId: string
  sourceSessionId: string
  reviewAgentId: string
  reviewSessionId: string
  messageCursor: string | null
  status: ReviewRelayStatus
  commentCount: number
  forwardedText?: string
  error?: string
}

export interface ReviewCommentBatch {
  rawText: string
  comments: number
}

const CODE_COMMENT = /::code-comment\{((?:[^}"']|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')*)\}/g

export function buildUncommittedReviewCommand(input: Pick<CommandPayload, 'id' | 'agent_id' | 'session_id'>): CommandPayload {
  return {
    ...input,
    action: 'send',
    text: UNCOMMITTED_REVIEW_MESSAGE,
    attachment_ids: [],
    target_id: null,
  }
}

export function extractNativeReviewComments(text: string): ReviewCommentBatch {
  const comments = [...text.matchAll(CODE_COMMENT)]
  return {
    rawText: comments.map(match => match[0]).join('\n\n'),
    comments: comments.length,
  }
}

/** Append a review result without replacing text the user already typed. */
export function appendReviewToDraft(draft: DraftState, reviewText: string): DraftState {
  const addition = reviewText.trim()
  if (!addition) return draft
  const current = draft.text.trimEnd()
  return {
    ...draft,
    text: current ? `${current}\n\n${addition}` : addition,
  }
}

/** Prevent a replayed event from appending the same native result twice. */
export function appendReviewOnce(draft: DraftState, reviewText: string): DraftState {
  const addition = reviewText.trim()
  if (!addition || draft.text.includes(addition)) return draft
  return appendReviewToDraft(draft, addition)
}
