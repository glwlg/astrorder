import { useRef, useState } from 'react'
import {
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconClipboardCheck,
  IconExternalLink,
  IconFileDescription,
  IconLoader2,
  IconPlayerPlay,
} from '@tabler/icons-react'
import { api } from '../api/client'
import {
  isArtifactProceedCommand,
  type ArtifactReviewItem,
} from '../domain/artifactReview'
import type { Command, CommandPayload, Session } from '../domain/types'
import { artifactViewerRegistry } from '../features/sidecar/registry'
import { resolveArtifactFromPath } from '../features/sidecar/resolver'
import { useSidecarStore } from '../features/sidecar/sidecarStore'
import { useAstrorderStore } from '../state/store'
import { MarkdownLinkMenu, type MarkdownLinkMenuTarget } from './MarkdownLinkMenu'

const SUMMARY_COLLAPSE_CHARS = 240
const SUMMARY_COLLAPSE_LINES = 4

export interface ArtifactReviewCardProps {
  artifact: ArtifactReviewItem
  session?: Session | null
  commands?: readonly Command[]
  continued?: boolean
  turnRunning?: boolean
  readOnly?: boolean
  turnEndAt?: string
  compact?: boolean
  onOpenFile?: (path: string) => void
}

export function ArtifactReviewCard({
  artifact,
  session,
  commands = [],
  continued = false,
  turnRunning = false,
  readOnly = false,
  turnEndAt,
  compact = false,
  onOpenFile,
}: ArtifactReviewCardProps) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [receipt, setReceipt] = useState<Command | null>(null)
  const [expanded, setExpanded] = useState(false)
  const [linkMenu, setLinkMenu] = useState<MarkdownLinkMenuTarget | null>(null)
  const submission = useRef<CommandPayload | null>(null)

  const saved =
    receipt ||
    commands.find(
      (c) =>
        isArtifactProceedCommand(c, artifact, turnEndAt) ||
        (c.agent_id === artifact.agentId &&
          c.session_id === artifact.sessionId &&
          c.action === 'send' &&
          (c.target_id === artifact.messageId ||
            c.id === `artifact-proceed:${artifact.messageId}`) &&
          c.state !== 'failed' &&
          c.state !== 'cancelled'),
    )

  const sent = Boolean(
    saved && ['accepted', 'running', 'completed'].includes(saved.state),
  )
  const settled = continued || sent
  const uncertain = Boolean(
    submission.current ||
      (saved && !['failed', 'cancelled'].includes(saved.state) && !sent),
  )

  const canOpen = Boolean(artifact.path)
  const handleOpenArtifact = () => {
    if (!artifact.path) return
    if (onOpenFile) {
      onOpenFile(artifact.path)
      return
    }
    if (session) {
      const resolved = resolveArtifactFromPath(
        artifact.path.replace(/%/g, '%25'),
        session,
        { name: artifact.fileName },
      )
      const viewer = artifactViewerRegistry.findViewer(resolved)
      if (viewer) {
        useSidecarStore.getState().openArtifact(resolved, viewer.id)
        return
      }
      useSidecarStore.getState().revealFileInTree(
        session.id,
        session.agent_id,
        resolved.path || artifact.path,
        session.workspace || undefined,
        session.project_name || session.title,
        session.connection_id || undefined,
      )
      return
    }
    const query = new URLSearchParams({ path: artifact.path, download: '1' })
    window.open(`/api/v1/files/raw?${query.toString()}`, '_blank')
  }

  const handleProceed = async () => {
    if (
      !artifact.requestFeedback ||
      busy ||
      sent ||
      continued ||
      readOnly ||
      turnRunning ||
      artifact.isToolRunning
    ) {
      return
    }
    setBusy(true)
    setError('')
    try {
      if (uncertain) {
        const result = await api.getCommands(artifact.sessionId, artifact.agentId)
        useAstrorderStore.getState().mergeCommands(result.items)
        const found = result.items.find(
          (c) => c.id === (submission.current?.id || saved?.id),
        )
        if (found) {
          setReceipt(found)
          if (['failed', 'cancelled'].includes(found.state)) {
            submission.current = null
            throw new Error(found.error || 'Proceed 未发送成功，请重试。')
          }
          if (['accepted', 'running', 'completed'].includes(found.state)) return
          throw new Error('Proceed 发送结果尚未确认，请稍后重试。')
        }
        throw new Error('Proceed 回执暂不可读，请稍后重试。')
      }

      if (!submission.current) {
        const priorFailure =
          receipt ||
          commands.find(
            (c) =>
              c.target_id === artifact.messageId &&
              c.agent_id === artifact.agentId &&
              c.session_id === artifact.sessionId &&
              ['failed', 'cancelled'].includes(c.state),
          )
        submission.current = {
          id: priorFailure
            ? crypto.randomUUID()
            : `artifact-proceed:${artifact.messageId}`,
          agent_id: artifact.agentId,
          session_id: artifact.sessionId,
          action: 'send',
          text: 'Proceed',
          attachment_ids: [],
          target_id: artifact.messageId,
        }
      }

      const command = await api.createCommand(submission.current)
      setReceipt(command)
      useAstrorderStore.getState().mergeCommands([command])
      if (['failed', 'cancelled'].includes(command.state)) {
        submission.current = null
        throw new Error(command.error || 'Proceed 未发送成功，请重试。')
      }
      if (!['accepted', 'running', 'completed'].includes(command.state)) {
        throw new Error('Proceed 发送结果尚未确认，请稍后检查。')
      }

      const history = await api
        .getMessages(artifact.sessionId, artifact.agentId)
        .catch(() => null)
      if (history) {
        useAstrorderStore
          .getState()
          .mergeMessages(artifact.agentId, artifact.sessionId, history.items)
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '发送 Proceed 失败')
    } finally {
      setBusy(false)
    }
  }

  const summaryLines = artifact.summary ? artifact.summary.split('\n') : []
  const isLongSummary =
    artifact.summary.length > SUMMARY_COLLAPSE_CHARS ||
    summaryLines.length > SUMMARY_COLLAPSE_LINES

  const isPendingFeedback = artifact.requestFeedback && !settled
  const proceedDisabled =
    readOnly || busy || turnRunning || artifact.isToolRunning

  const proceedLabel = busy
    ? '发送中…'
    : uncertain
      ? '检查发送结果'
      : turnRunning || artifact.isToolRunning
        ? '本轮执行中…'
        : readOnly
          ? '只读会话'
          : 'Proceed'

  return (
    <section
      className={[
        'artifact-review-card',
        isPendingFeedback ? 'is-pending-feedback' : 'is-settled',
        compact ? 'is-compact' : '',
      ]
        .filter(Boolean)
        .join(' ')}
      aria-label={artifact.requestFeedback ? '计划产物确认' : '文档产物'}
      data-testid={`artifact-review-${artifact.messageId}`}
    >
      <div className="artifact-review-head">
        <div className="artifact-review-title-group">
          <span className="artifact-review-icon" aria-hidden="true">
            {artifact.requestFeedback ? (
              <IconClipboardCheck size={16} />
            ) : (
              <IconFileDescription size={16} />
            )}
          </span>
          <span
            className={`artifact-review-badge ${
              isPendingFeedback
                ? 'is-pending'
                : artifact.requestFeedback
                  ? 'is-done'
                  : 'is-doc'
            }`}
          >
            {isPendingFeedback
              ? '计划待确认'
              : artifact.requestFeedback
                ? '已确认继续'
                : '文档产物'}
          </span>
          {canOpen ? (
            <button
              type="button"
              className="artifact-review-file-btn"
              title={artifact.path}
              onClick={handleOpenArtifact}
              onContextMenu={(event) => {
                if (!session) return
                event.preventDefault()
                event.stopPropagation()
                setLinkMenu({
                  x: event.clientX,
                  y: event.clientY,
                  kind: 'file',
                  value: artifact.path,
                })
              }}
            >
              <span>{artifact.fileName}</span>
            </button>
          ) : (
            <span className="artifact-review-file-name">{artifact.fileName}</span>
          )}
        </div>
        {canOpen && (
          <button
            type="button"
            className="artifact-review-open-btn"
            onClick={handleOpenArtifact}
          >
            <IconExternalLink size={13} aria-hidden="true" />
            <span>查看文档</span>
          </button>
        )}
      </div>

      {artifact.summary && (
        <div className="artifact-review-summary-wrap">
          <p
            className={`artifact-review-summary${
              isLongSummary && !expanded ? ' is-clamped' : ''
            }`}
          >
            {artifact.summary}
          </p>
          {isLongSummary && (
            <button
              type="button"
              className="artifact-review-toggle"
              onClick={() => setExpanded((prev) => !prev)}
            >
              {expanded ? (
                <>
                  <IconChevronUp size={13} aria-hidden="true" />
                  <span>收起摘要</span>
                </>
              ) : (
                <>
                  <IconChevronDown size={13} aria-hidden="true" />
                  <span>展开全部摘要</span>
                </>
              )}
            </button>
          )}
        </div>
      )}

      {artifact.requestFeedback && (
        <div className="artifact-review-footer">
          {settled ? (
            <p className="artifact-review-status" role="status">
              <IconCheck size={14} aria-hidden="true" />
              <span>
                {sent && !continued
                  ? '已发送 Proceed，等待 Antigravity 开始执行。'
                  : sent
                    ? '已确认 Proceed，计划已继续执行。'
                    : '已有后续回复，此计划确认已结束。'}
              </span>
            </p>
          ) : (
            <>
              <span className="artifact-review-hint">
                {compact
                  ? '确认后点击 Proceed 或直接回复修改意见'
                  : '确认实施计划无误后可直接点击 Proceed 开始执行，或在下方回复修改意见。'}
              </span>
              <div className="artifact-review-actions">
                <button
                  type="button"
                  className="artifact-proceed-btn"
                  disabled={proceedDisabled}
                  onClick={() => void handleProceed()}
                >
                  {busy ? (
                    <IconLoader2
                      size={14}
                      className="artifact-spin"
                      aria-hidden="true"
                    />
                  ) : (
                    <IconPlayerPlay size={14} aria-hidden="true" />
                  )}
                  <span>{proceedLabel}</span>
                </button>
              </div>
            </>
          )}
        </div>
      )}

      {error && (
        <p className="artifact-review-error" role="alert">
          {error}
        </p>
      )}

      {session && (
        <MarkdownLinkMenu
          target={linkMenu}
          session={session}
          onClose={() => setLinkMenu(null)}
        />
      )}
    </section>
  )
}
