import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { Tooltip, UnstyledButton } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAlertTriangle,
  IconBrandGitlab,
  IconExternalLink,
  IconListDetails,
  IconMessageForward,
} from '@tabler/icons-react'
import {
  gitlabApi,
  type GitLabJobSummary,
  type GitLabPipelineDetailResponse,
  type GitLabPipelinesResponse,
} from '../../api/gitlab'
import type { Session } from '../../domain/types'
import {
  formatPipelineDuration,
  gitLabStatusMeta,
  useGitLabPipelines,
} from '../../hooks/useGitLabCI'
import { extensionRegistry } from '../extensions/extensionRegistry'
import type { PluginPanelContext, StatusBarSlotContext } from '../extensions/types'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { GitLabCIViewer } from '../sidecar/viewers/gitlabci/GitLabCIViewer'
import { GitLabSettingsSection } from './GitLabSettingsSection'

export const GITLAB_CI_PLUGIN_ID = 'gitlab-ci-viewer'
export const GITLAB_CI_PANEL_ID = 'ci-panel'

export function openGitLabCIPanel(session: Session, pipelineId?: number): void {
  if (!session.workspace) {
    notifications.show({ color: 'yellow', message: '该会话没有可确认的工作区目录。' })
    return
  }
  useSidecarStore.getState().openPluginPanel(GITLAB_CI_PLUGIN_ID, GITLAB_CI_PANEL_ID, {
    title: 'GitLab CI',
    mediaType: 'application/x-gitlab-ci',
    viewerId: GITLAB_CI_PLUGIN_ID,
    sessionId: session.id,
    agentId: session.agent_id,
    workspacePath: session.workspace || undefined,
    connectionId: session.connection_id || undefined,
    params: pipelineId && pipelineId > 0 ? { pipeline_id: pipelineId } : undefined,
  })
}

export function formatGitLabStatusQuote(data: GitLabPipelinesResponse): string {
  const repoPath = data.repo?.project_path || 'GitLab 仓库'
  const pipeline = data.latest_pipeline
  if (!pipeline) {
    const branchPart = data.repo?.branch ? `（分支 \`${data.repo.branch}\`）` : ''
    return `[GitLab CI 状态] ${repoPath} · 暂无流水线记录${branchPart}\n`
  }
  const meta = gitLabStatusMeta(pipeline.status)
  const parts = [
    `[GitLab CI 状态] ${repoPath}`,
    `Pipeline #${pipeline.id}`,
    `${meta.label} (${pipeline.status})`,
    `分支 \`${pipeline.ref}\` @ \`${pipeline.short_sha}\``,
  ]
  if (pipeline.duration) {
    parts.push(`耗时 ${formatPipelineDuration(pipeline.duration)}`)
  }
  if (pipeline.matches_head) {
    parts.push('当前 HEAD')
  }
  const urlLine = pipeline.web_url ? `\n链接：${pipeline.web_url}` : ''
  return `${parts.join(' · ')}${urlLine}\n`
}

export function formatGitLabPipelineJobsQuote(
  data: GitLabPipelinesResponse,
  detail: GitLabPipelineDetailResponse,
): string {
  const repoPath = detail.repo?.project_path || data.repo?.project_path || 'GitLab 仓库'
  const pipeline = detail.pipeline
  const meta = gitLabStatusMeta(pipeline.status)
  const failedJobs = (detail.jobs || []).filter((j) => j.status === 'failed' && !j.is_bridge)
  const targetJobs: GitLabJobSummary[] = failedJobs.length > 0 ? failedJobs : (detail.jobs || []).filter((j) => !j.is_bridge).slice(0, 8)
  const tag = failedJobs.length > 0 ? '[GitLab CI 失败作业]' : '[GitLab CI 作业概况]'
  const header = `${tag} ${repoPath} · Pipeline #${pipeline.id} · ${meta.label} (${pipeline.status}) · 分支 \`${pipeline.ref}\` @ \`${pipeline.short_sha}\``
  if (targetJobs.length === 0) {
    return `${header}\n`
  }
  const lines = targetJobs.map((job) => {
    const jMeta = gitLabStatusMeta(job.status)
    const attrs = [`#${job.id}`, `stage: \`${job.stage}\``, jMeta.label]
    if (job.failure_reason) attrs.push(`失败原因: \`${job.failure_reason}\``)
    if (job.duration) attrs.push(`耗时 ${formatPipelineDuration(job.duration)}`)
    return `- \`${job.name}\` (${attrs.join(', ')})`
  })
  return `${header}\n${lines.join('\n')}\n`
}

function dispatchComposerQuote(session: Session, text: string): void {
  window.dispatchEvent(
    new CustomEvent('astrorder:quote-to-composer', {
      detail: {
        agentId: session.agent_id,
        sessionId: session.id,
        text,
      },
    }),
  )
}

export function GitLabComposerStatusPill({
  session,
  statusUnavailable,
  statusLoading,
  branch,
}: StatusBarSlotContext) {
  const gitlab = useGitLabPipelines(session, {
    enabled: !statusUnavailable && !statusLoading && Boolean(branch),
    limit: 6,
  })
  const rawId = useId()
  const menuId = `gitlab-ci-ctx-${rawId.replace(/[^a-zA-Z0-9_-]/g, '')}`
  const anchorRef = useRef<HTMLSpanElement | null>(null)
  const popoverRef = useRef<HTMLDivElement | null>(null)
  const menuListRef = useRef<HTMLDivElement | null>(null)
  const [fallbackOpen, setFallbackOpen] = useState(false)
  const [fallbackPos, setFallbackPos] = useState<{ left: number; bottom: number } | null>(null)
  const [quotingJobs, setQuotingJobs] = useState(false)

  const closeMenu = useCallback(() => {
    setFallbackOpen(false)
    const el = popoverRef.current
    if (el && typeof (el as HTMLElement & { hidePopover?: () => void }).hidePopover === 'function') {
      try {
        if (el.matches(':popover-open')) {
          ;(el as HTMLElement & { hidePopover: () => void }).hidePopover()
        }
      } catch {
        // Ignore if already closed or unsupported
      }
    }
  }, [])

  const focusFirstMenuItem = useCallback(() => {
    const first = menuListRef.current?.querySelector<HTMLButtonElement>('button:not(:disabled)')
    first?.focus()
  }, [])

  useEffect(() => {
    const el = popoverRef.current
    if (!el) return
    const onToggle = (ev: Event) => {
      const toggleEv = ev as Event & { newState?: string }
      if (toggleEv.newState === 'open') {
        requestAnimationFrame(focusFirstMenuItem)
      } else if (toggleEv.newState === 'closed') {
        setFallbackOpen(false)
      }
    }
    el.addEventListener('toggle', onToggle)
    return () => el.removeEventListener('toggle', onToggle)
  }, [focusFirstMenuItem])

  useEffect(() => {
    if (!fallbackOpen) return
    requestAnimationFrame(focusFirstMenuItem)
    const onPointerDown = (ev: MouseEvent) => {
      if (popoverRef.current?.contains(ev.target as Node)) return
      setFallbackOpen(false)
    }
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') {
        ev.preventDefault()
        setFallbackOpen(false)
      }
    }
    document.addEventListener('mousedown', onPointerDown, true)
    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      document.removeEventListener('mousedown', onPointerDown, true)
      document.removeEventListener('keydown', onKeyDown, true)
    }
  }, [fallbackOpen, focusFirstMenuItem])

  const matchedGitLab = Boolean(!statusUnavailable && gitlab.data?.matched)
  if (!matchedGitLab || !gitlab.data) {
    return null
  }
  const gitlabData = gitlab.data
  const latestPipeline = gitlabData.latest_pipeline || null
  const latestPipelineMeta = latestPipeline ? gitLabStatusMeta(latestPipeline.status) : null

  const openContextMenu = (e: React.MouseEvent<HTMLButtonElement>) => {
    e.preventDefault()
    e.stopPropagation()
    const x = e.clientX
    const y = e.clientY
    const popoverEl = popoverRef.current
    const anchorEl = anchorRef.current
    if (!popoverEl || !anchorEl) return

    anchorEl.style.left = `${x}px`
    anchorEl.style.top = `${y}px`

    const menuWidth = 224
    const clampedLeft = Math.max(8, Math.min(window.innerWidth - menuWidth - 8, x))
    const bottomOffset = Math.max(8, window.innerHeight - y + 6)
    const supportsPopover = typeof (popoverEl as HTMLElement & { showPopover?: unknown }).showPopover === 'function'
    const supportsAnchor =
      typeof CSS !== 'undefined' &&
      typeof CSS.supports === 'function' &&
      CSS.supports('position-area: block-start')

    if (supportsPopover) {
      if (!supportsAnchor) {
        popoverEl.style.left = `${clampedLeft}px`
        popoverEl.style.bottom = `${bottomOffset}px`
        popoverEl.style.top = 'auto'
      } else {
        popoverEl.style.removeProperty('left')
        popoverEl.style.removeProperty('bottom')
        popoverEl.style.removeProperty('top')
      }
      try {
        if (popoverEl.matches(':popover-open')) {
          ;(popoverEl as HTMLElement & { hidePopover: () => void }).hidePopover()
        }
      } catch {
        // Ignore if not currently open
      }
      try {
        ;(popoverEl as HTMLElement & { showPopover: (opts?: { source?: HTMLElement }) => void }).showPopover({
          source: anchorEl,
        })
      } catch {
        try {
          ;(popoverEl as HTMLElement & { showPopover: () => void }).showPopover()
        } catch {
          setFallbackPos({ left: clampedLeft, bottom: bottomOffset })
          setFallbackOpen(true)
        }
      }
    } else {
      setFallbackPos({ left: clampedLeft, bottom: bottomOffset })
      setFallbackOpen(true)
    }
  }

  const handleMenuKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      closeMenu()
      return
    }
    if ('focusGroup' in HTMLElement.prototype) return
    const items = Array.from(menuListRef.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') || [])
    if (items.length === 0) return
    const currentIdx = items.indexOf(document.activeElement as HTMLButtonElement)
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      items[(currentIdx + 1 + items.length) % items.length]?.focus()
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      items[(currentIdx - 1 + items.length) % items.length]?.focus()
    } else if (e.key === 'Home') {
      e.preventDefault()
      items[0]?.focus()
    } else if (e.key === 'End') {
      e.preventDefault()
      items[items.length - 1]?.focus()
    }
  }

  const handleQuoteStatus = () => {
    closeMenu()
    dispatchComposerQuote(session, formatGitLabStatusQuote(gitlabData))
    notifications.show({ color: 'indigo', message: '已引用 GitLab CI 状态到对话框' })
  }

  const handleQuotePipelineJobs = async () => {
    closeMenu()
    if (!latestPipeline) {
      handleQuoteStatus()
      return
    }
    setQuotingJobs(true)
    try {
      const detail = await gitlabApi.getPipelineDetail(latestPipeline.id, {
        workspace: session.workspace || undefined,
        connection_id: session.connection_id || undefined,
        session_id: session.id,
      })
      dispatchComposerQuote(session, formatGitLabPipelineJobsQuote(gitlabData, detail))
      notifications.show({
        color: 'indigo',
        message:
          latestPipeline.status === 'failed'
            ? `已引用 Pipeline #${latestPipeline.id} 失败作业详情到对话框`
            : `已引用 Pipeline #${latestPipeline.id} 作业概况到对话框`,
      })
    } catch {
      dispatchComposerQuote(session, formatGitLabStatusQuote(gitlabData))
      notifications.show({ color: 'blue', message: '拉取作业明细失败，已降级引用 CI 状态摘要' })
    } finally {
      setQuotingJobs(false)
    }
  }

  return (
    <span className="composer-gitlab-pill-wrap">
      <Tooltip
        label={
          latestPipeline
            ? `${gitlabData.repo?.project_path || 'GitLab'} · #${latestPipeline.id} (${latestPipeline.ref} @ ${latestPipeline.short_sha}) — 左键查看流水线与日志，右键引用到对话框`
            : `${gitlabData.repo?.project_path || 'GitLab'} — 左键查看流水线，右键引用到对话框`
        }
      >
        <UnstyledButton
          className={`composer-gitlab-pill${latestPipelineMeta ? ` is-${latestPipelineMeta.tone}` : ''}`}
          aria-label="查看 GitLab CI 流水线"
          aria-haspopup="menu"
          onClick={() => openGitLabCIPanel(session, latestPipeline?.id)}
          onContextMenu={openContextMenu}
        >
          <IconBrandGitlab size={13} color="#e24329" aria-hidden="true" />
          {latestPipeline && latestPipelineMeta ? (
            <>
              <span
                className={`composer-gitlab-dot${latestPipelineMeta.spinning ? ' is-pulsing' : ''}`}
                style={{ backgroundColor: latestPipelineMeta.dotColor }}
              />
              <span className="composer-gitlab-id">#{latestPipeline.id}</span>
              <span>{latestPipelineMeta.label}</span>
            </>
          ) : (
            <span>GitLab CI</span>
          )}
        </UnstyledButton>
      </Tooltip>

      <span ref={anchorRef} className="composer-gitlab-menu-anchor" aria-hidden="true" />
      <div
        ref={popoverRef}
        id={menuId}
        {...({ popover: 'auto' } as Record<string, string>)}
        className="composer-gitlab-context-menu"
        data-open={fallbackOpen ? 'true' : undefined}
        style={
          fallbackOpen && fallbackPos
            ? { display: 'block', left: `${fallbackPos.left}px`, bottom: `${fallbackPos.bottom}px` }
            : undefined
        }
      >
        <div
          ref={menuListRef}
          role="menu"
          aria-label="GitLab CI 状态操作"
          {...({ focusgroup: 'menu nomemory' } as Record<string, string>)}
          className="composer-gitlab-context-list"
          onKeyDown={handleMenuKeyDown}
        >
          <button
            type="button"
            role="menuitem"
            className="composer-gitlab-context-item"
            onClick={handleQuoteStatus}
          >
            <IconMessageForward size={14} aria-hidden="true" />
            <span>引用 CI 状态到对话框</span>
          </button>

          {latestPipeline && (
            <button
              type="button"
              role="menuitem"
              className="composer-gitlab-context-item"
              disabled={quotingJobs}
              onClick={() => { void handleQuotePipelineJobs() }}
            >
              {latestPipeline.status === 'failed' ? (
                <IconAlertTriangle size={14} color="var(--astr-red, #e03131)" aria-hidden="true" />
              ) : (
                <IconListDetails size={14} aria-hidden="true" />
              )}
              <span>
                {latestPipeline.status === 'failed'
                  ? '引用失败作业详情到对话框'
                  : '引用流水线作业概况到对话框'}
              </span>
            </button>
          )}

          <div role="separator" className="composer-gitlab-context-sep" />

          <button
            type="button"
            role="menuitem"
            className="composer-gitlab-context-item"
            onClick={() => {
              closeMenu()
              openGitLabCIPanel(session, latestPipeline?.id)
            }}
          >
            <IconBrandGitlab size={14} color="#e24329" aria-hidden="true" />
            <span>打开 GitLab CI 面板</span>
          </button>

          {latestPipeline?.web_url && (
            <button
              type="button"
              role="menuitem"
              className="composer-gitlab-context-item"
              onClick={() => {
                closeMenu()
                window.open(latestPipeline.web_url, '_blank', 'noopener,noreferrer')
              }}
            >
              <IconExternalLink size={14} aria-hidden="true" />
              <span>在 GitLab 网页中打开</span>
            </button>
          )}
        </div>
      </div>
    </span>
  )
}

function GitLabCIPanelAdapter({
  artifact,
  session,
  isActive,
  surface,
  onClose,
}: PluginPanelContext) {
  return (
    <GitLabCIViewer
      artifact={artifact}
      session={session}
      isActive={isActive}
      surface={surface}
      onClose={onClose}
    />
  )
}

export function registerGitLabExtension(): void {
  extensionRegistry.registerBuiltin({
    pluginId: GITLAB_CI_PLUGIN_ID,
    settingsTitle: 'GitLab CI / CD 实例配置',
    settingsButtonLabel: '配置实例',
    settingsComponent: GitLabSettingsSection,
    statusBarComponent: GitLabComposerStatusPill,
    gitMenuItems: [
      {
        id: 'open-gitlab-ci',
        groupLabel: 'CI / CD',
        label: '查看 GitLab CI 流水线...',
        icon: IconBrandGitlab,
        iconColor: '#e24329',
        requiresWorkspace: true,
        onClick: (session) => openGitLabCIPanel(session),
      },
    ],
    welcomeMenuItems: [
      {
        id: 'gitlab-ci',
        label: 'GitLab CI 流水线',
        shortcut: 'CI / CD',
        icon: IconBrandGitlab,
        iconColor: '#e24329',
        requiresWorkspace: true,
        hideInOfficeMode: true,
        onClick: (session) => openGitLabCIPanel(session),
      },
    ],
    panels: {
      [GITLAB_CI_PANEL_ID]: {
        title: 'GitLab CI',
        badgeLabel: '[GitLab CI]',
        tabColor: '#e24329',
        mediaType: 'application/x-gitlab-ci',
        component: GitLabCIPanelAdapter,
      },
    },
  })
}
