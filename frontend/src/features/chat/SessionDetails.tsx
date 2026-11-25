import { useContext, useState } from 'react'
import { QueryClientContext } from '@tanstack/react-query'
import { Group, Stack, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAlertTriangle,
  IconCheck,
  IconCopy,
  IconFolder,
  IconFolderCode,
  IconGitBranch,
  IconPin,
  IconPinnedOff,
  IconShieldCheck,
  IconTerminal2,
  IconTransfer,
  IconX,
} from '@tabler/icons-react'
import { UserInputCard, isUserInput } from '../../components/UserInputCard'
import { MarkdownContent } from '../../components/MarkdownContent'
import { SessionRuntimeFacts } from '../../components/SessionRuntimeFacts'
import { NativeObservationPanel } from '../../components/NativeObservationPanel'
import { sessionActivityStatus } from '../../components/sessionRailModel'
import { useGitStatus } from '../../hooks/useGitStatus'
import { useSessionModel } from '../../hooks/useSessionModel'
import { useAstrorderStore } from '../../state/store'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { FILETREE_VIEWER_PLUGIN_ID, XTERM_VIEWER_PLUGIN_ID } from '../plugins/builtinPlugins'
import { useIsOfficeExperience } from '../office/appProfile'
import type { Agent, Approval, Command, Message, Session } from '../../domain/types'
import './sessionDetails.css'

const STATUS_LABELS = {
  idle: '空闲',
  running: '运行中',
  waiting_approval: '等待审批',
  error: '异常',
} as const

export interface SessionDetailsProps {
  session: Session
  agent?: Agent
  commands: Command[]
  messages: Message[]
  approvals: Approval[]
  onApproval: (approval: Approval, action: 'approve' | 'cancel') => void
  handoffLink?: { label: string; title?: string; onOpen: () => void } | null
  mode?: 'drawer' | 'pinned' | 'sidecar'
  onPin?: () => void
  onUnpin?: () => void
  onClose?: () => void
}

function SessionBranchBadgeInner({ session }: { session: Session }) {
  const git = useGitStatus(session)
  const model = useSessionModel(session)
  const branchLabel = session.workspace
    ? git.isPending
      ? '…'
      : git.isError
        ? null
        : git.data?.branch || null
    : model.data?.branch || null
  if (!branchLabel) return null
  return (
    <span className="si-branch-pill" title={`Git 分支：${branchLabel}`}>
      <IconGitBranch size={11} stroke={1.8} aria-hidden="true" />
      <span>{branchLabel}</span>
    </span>
  )
}

function SessionBranchBadge({ session }: { session: Session }) {
  const queryClient = useContext(QueryClientContext)
  if (!queryClient) return null
  return <SessionBranchBadgeInner session={session} />
}

export function SessionDetails({
  session,
  agent,
  commands,
  approvals,
  onApproval,
  handoffLink,
  mode = 'sidecar',
  onPin,
  onUnpin,
  onClose,
}: SessionDetailsProps) {
  const canApprove = agent?.capabilities.includes('approvals') === true
  const activityStatus = sessionActivityStatus(session)
  const eventsConnected = useAstrorderStore((state) => state.connection === 'connected')
  const fileTreeEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(FILETREE_VIEWER_PLUGIN_ID))
  const terminalEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(XTERM_VIEWER_PLUGIN_ID))
  const isOfficeMode = useIsOfficeExperience(session)
  const [copiedPath, setCopiedPath] = useState(false)

  const failedCommands = commands.filter((cmd) => cmd.state === 'failed').slice(-3).reverse()

  const handleCopyWorkspace = async () => {
    if (!session.workspace) return
    try {
      await navigator.clipboard.writeText(session.workspace)
      setCopiedPath(true)
      window.setTimeout(() => setCopiedPath(false), 1600)
      notifications.show({ color: 'teal', message: '已复制工作区路径' })
    } catch {
      notifications.show({ color: 'red', message: '无法复制路径，请检查剪贴板权限。' })
    }
  }

  const handleOpenTerminal = () => {
    useSidecarStore
      .getState()
      .openTerminal(
        session.id,
        session.agent_id,
        session.project_name || session.title,
        session.connection_id || undefined,
      )
    if (mode === 'drawer') onClose?.()
  }

  const handleOpenFileTree = () => {
    if (!session.workspace) return
    useSidecarStore
      .getState()
      .openFileTree(
        session.id,
        session.agent_id,
        session.workspace,
        session.project_name || session.title,
        session.connection_id || undefined,
      )
    if (mode === 'drawer') onClose?.()
  }

  const showHeader = mode === 'drawer' || mode === 'pinned'

  return (
    <div className="session-inspector session-details">
      {showHeader && (
        <header className="si-header">
          <div className="si-header-left">
            <h2 className="si-header-title">{isOfficeMode ? '对话信息' : '会话详情'}</h2>
            {!isOfficeMode && <span className="si-header-tag">INSPECTOR</span>}
          </div>
          <div className="si-header-actions">
            {mode === 'drawer' && onPin && (
              <button
                type="button"
                className="si-header-btn"
                onClick={onPin}
                title="固定到右侧栏"
              >
                <IconPin size={14} stroke={1.7} aria-hidden="true" />
                <span>固定侧栏</span>
              </button>
            )}
            {mode === 'pinned' && onUnpin && (
              <button
                type="button"
                className="si-header-btn"
                onClick={onUnpin}
                title="取消固定"
              >
                <IconPinnedOff size={14} stroke={1.7} aria-hidden="true" />
                <span>取消固定</span>
              </button>
            )}
            {onClose && (
              <>
                {(onPin || onUnpin) && <span className="si-header-sep" aria-hidden="true" />}
                <button
                  type="button"
                  className="si-header-btn is-icon-only"
                  onClick={onClose}
                  aria-label="关闭会话详情"
                  title="关闭"
                >
                  <IconX size={15} stroke={1.8} aria-hidden="true" />
                </button>
              </>
            )}
          </div>
        </header>
      )}

      <div className="si-body">
        {/* Hero 识别区块 */}
        <section className="si-hero" aria-label="会话概览">
          <div className="si-hero-top">
            <span className="si-eyebrow">当前会话</span>
            <span className="si-status-pill" data-state={activityStatus}>
              <span className="si-dot" aria-hidden="true" />
              <span>{STATUS_LABELS[activityStatus]}</span>
            </span>
          </div>

          <h3 className="si-title">{session.title || '未命名会话'}</h3>

          {handoffLink && (
            <button
              type="button"
              className="si-handoff-chip"
              title={handoffLink.title}
              onClick={handoffLink.onOpen}
            >
              <IconTransfer size={13} stroke={1.8} aria-hidden="true" />
              <span>{handoffLink.label}</span>
            </button>
          )}

          {/* 工作区路径 + 分支内联条 */}
          <div className="si-workspace-bar" title={session.workspace || '未提供工作区'}>
            <IconFolder size={14} stroke={1.7} className="si-workspace-icon" aria-hidden="true" />
            <span className="si-workspace-path">{session.workspace || '未提供工作区'}</span>
            {!isOfficeMode && <SessionBranchBadge session={session} />}
            {session.workspace && (
              <button
                type="button"
                className="si-copy-btn"
                onClick={() => void handleCopyWorkspace()}
                aria-label="复制工作区路径"
                title="复制工作区路径"
              >
                {copiedPath ? <IconCheck size={13} aria-hidden="true" /> : <IconCopy size={13} aria-hidden="true" />}
              </button>
            )}
          </div>

          {/* 高频三连动作条 */}
          <div className="si-quick-actions" role="group" aria-label="会话快捷操作">
            {!isOfficeMode && terminalEnabled && (
              <button
                type="button"
                className="si-action-btn"
                onClick={handleOpenTerminal}
              >
                <IconTerminal2 size={13} stroke={1.8} aria-hidden="true" />
                <span>终端</span>
              </button>
            )}
            {fileTreeEnabled && (
              <button
                type="button"
                className="si-action-btn"
                onClick={handleOpenFileTree}
                disabled={!session.workspace}
              >
                <IconFolderCode size={13} stroke={1.8} aria-hidden="true" />
                <span>{isOfficeMode ? '工作区文件' : '文件树'}</span>
              </button>
            )}
            <button
              type="button"
              className="si-action-btn"
              onClick={() => void handleCopyWorkspace()}
              disabled={!session.workspace}
            >
              {copiedPath ? <IconCheck size={13} stroke={1.8} aria-hidden="true" /> : <IconCopy size={13} stroke={1.8} aria-hidden="true" />}
              <span>{copiedPath ? '已复制' : '复制路径'}</span>
            </button>
          </div>
        </section>

        {/* 条件高亮：待处理审批 */}
        {approvals.length > 0 && (
          <section className="si-alert-section approval-section" aria-label="待处理审批">
            <div className="si-section-kicker">
              <IconShieldCheck size={15} stroke={1.8} aria-hidden="true" />
              <span>待处理审批 ({approvals.length})</span>
            </div>
            <Stack gap="xs">
              {approvals.map((approval) =>
                isUserInput(approval) ? (
                  <UserInputCard key={approval.id} approval={approval} />
                ) : (
                  <div className="si-approval-card approval-card" key={approval.id}>
                    <div className="si-approval-title">{approval.title}</div>
                    {approval.detail && (
                      <div className="si-approval-detail">
                        <MarkdownContent value={approval.detail} session={session} />
                      </div>
                    )}
                    {canApprove ? (
                      <Group mt={2} gap="xs">
                        <button
                          className="approval-button approval-approve"
                          type="button"
                          onClick={() => onApproval(approval, 'approve')}
                        >
                          允许
                        </button>
                        <button
                          className="approval-button approval-cancel"
                          type="button"
                          onClick={() => onApproval(approval, 'cancel')}
                        >
                          取消
                        </button>
                      </Group>
                    ) : (
                      <Text size="xs" c="dimmed">Agent 未报告审批能力，操作已禁用。</Text>
                    )}
                  </div>
                ),
              )}
            </Stack>
          </section>
        )}

        {/* 条件高亮：仅当存在失败指令时呈现原生错误排查条 */}
        {failedCommands.length > 0 && (
          <section className="si-alert-section" aria-label="失败指令提示">
            {failedCommands.map((cmd) => (
              <div className="si-error-banner" key={cmd.id}>
                <div className="si-error-banner-head">
                  <IconAlertTriangle size={14} stroke={1.9} aria-hidden="true" />
                  <span>
                    {cmd.action === 'enqueue' ? '排队指令失败' : cmd.action === 'stop' ? '停止指令失败' : '最近指令执行失败'}
                  </span>
                </div>
                {cmd.error && <div className="si-error-banner-body">{cmd.error}</div>}
              </div>
            ))}
          </section>
        )}

        <hr className="si-divider" />

        {/* 上下文窗口 + 模型与额度 + 运行与标识（办公模式省略底层工程诊断） */}
        {!isOfficeMode && <SessionRuntimeFacts session={session} agent={agent} hideHeroDuplicates />}

        {/* Codex 原生桌面观察（条件保留，办公模式隐藏） */}
        {!isOfficeMode && agent?.kind === 'codex' && (
          <section aria-label="Codex 原生观察">
            <NativeObservationPanel agentId={agent.id} sessionId={session.id} />
          </section>
        )}

        <footer className="si-footer" aria-hidden="true">
          <span className="si-footer-status">
            <span className={`si-footer-dot${eventsConnected ? '' : ' is-reconnecting'}`} />
            <span>{isOfficeMode ? (eventsConnected ? '服务已就绪' : '正在重连…') : (eventsConnected ? 'SYNCED' : 'RECONNECTING')}</span>
          </span>
          {!isOfficeMode && <span>ASTRORDER INSPECTOR</span>}
        </footer>
      </div>
    </div>
  )
}
