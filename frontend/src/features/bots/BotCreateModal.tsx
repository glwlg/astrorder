import {
  IconCheck,
  IconFolder,
  IconRobot,
  IconSparkles,
  IconWand,
  IconX,
} from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useShallow } from 'zustand/react/shallow'
import {
  api,
  type BotApprovalMode,
  type BotSummary,
  type DistillBotResponse,
} from '../../api/client'
import type { Agent } from '../../domain/types'
import { scopeKey } from '../../domain/semantics'
import { selectProjects, selectSessions, useAstrorderStore } from '../../state/store'
import { connectionName } from '../../components/AgentSessionFilter'
import { displaySessionTitle } from '../../components/sessionRailModel'
import { setProjectOfficeMode } from '../office/officeWorkspaceMode'
import {
  BOT_APPROVAL_MODE_OPTIONS,
  effortOptionsForBotSelection,
  normalizeBotApprovalMode,
  resolveBotModelSelection,
  useBotModelCatalog,
} from './useBotModelCatalog'
import {
  buildBotEnvironmentOptions,
  normalizeBotConnection,
  useInvalidateBots,
  type DistillBotIntent,
} from './useBots'

function formatEnvLabel(connectionId: string | undefined, agent: Agent | undefined): string {
  if (agent) return connectionName(agent)
  if (!connectionId || connectionId === 'local') return '本机'
  if (connectionId.startsWith('wsl:')) return connectionId.slice(4) || 'WSL'
  if (connectionId.startsWith('ssh-')) return 'SSH'
  return connectionId
}

function formatAgentLabel(agentId: string, agent: Agent | undefined): string {
  const kindMap: Record<string, string> = {
    antigravity: 'Antigravity',
    'antigravity-ssh': 'Antigravity',
    codex: 'Codex',
    'codex-ssh': 'Codex',
    hermes: 'Hermes',
    ssh: 'Hermes',
    grok: 'Grok',
    'grok-ssh': 'Grok',
  }
  if (agent) return kindMap[agent.kind] || agent.name || agent.id
  const low = agentId.toLowerCase()
  if (low.includes('antigravity')) return 'Antigravity'
  if (low.includes('codex')) return 'Codex'
  if (low.includes('hermes')) return 'Hermes'
  if (low.includes('grok')) return 'Grok'
  return agentId
}

export type BotModalMode = 'create' | 'distill' | 'feedback'

const ICON_OPTIONS = [
  { value: 'robot', label: '通用 Bot' },
  { value: 'video', label: '视频制作' },
  { value: 'doc', label: '文档写作' },
  { value: 'code', label: '工程研发' },
  { value: 'design', label: '视觉设计' },
  { value: 'data', label: '数据分析' },
  { value: 'ops', label: '运维巡检' },
  { value: 'briefcase', label: '办公助理' },
  { value: 'spark', label: '创意策划' },
]

interface BotCreateModalProps {
  opened: boolean
  initialMode?: BotModalMode
  initialDistill?: DistillBotIntent | null
  feedbackBot?: BotSummary | null
  onClose: () => void
  onSaved: (botId: string) => void
}

export function BotCreateModal({
  opened,
  initialMode = 'create',
  initialDistill = null,
  feedbackBot = null,
  onClose,
  onSaved,
}: BotCreateModalProps) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  const tabCreateId = useId()
  const tabDistillId = useId()

  const invalidateBots = useInvalidateBots()
  const sessions = useAstrorderStore(useShallow(selectSessions))
  const projects = useAstrorderStore(useShallow(selectProjects))
  const agentsMap = useAstrorderStore((s) => s.agents)
  const agents = useMemo(() => Object.values(agentsMap), [agentsMap])

  const isFeedback = Boolean(feedbackBot && initialMode === 'feedback')
  const [mode, setMode] = useState<'create' | 'distill'>(initialMode === 'distill' ? 'distill' : 'create')

  // Shared / Create form state
  const [name, setName] = useState('')
  const [slug, setSlug] = useState('')
  const [description, setDescription] = useState('')
  const [icon, setIcon] = useState('robot')
  const [workspace, setWorkspace] = useState('')
  const [connectionId, setConnectionId] = useState('')
  const [defaultAgentId, setDefaultAgentId] = useState('')
  const [defaultModelKey, setDefaultModelKey] = useState('')
  const [defaultEffort, setDefaultEffort] = useState('')
  const [defaultApprovalMode, setDefaultApprovalMode] = useState<BotApprovalMode>('full_access')
  const [officeMode, setOfficeMode] = useState(false)
  const [tagsText, setTagsText] = useState('')
  const [instructions, setInstructions] = useState('')
  const [agentsMd, setAgentsMd] = useState('')
  const [writeAgentsMd, setWriteAgentsMd] = useState(true)

  // Distill / Feedback state
  const [selectedSessionKey, setSelectedSessionKey] = useState('')
  const [distillHint, setDistillHint] = useState('')
  const [generateSkill, setGenerateSkill] = useState(false)
  const [distillResult, setDistillResult] = useState<DistillBotResponse | null>(null)
  const [skillSlug, setSkillSlug] = useState('')
  const [skillMd, setSkillMd] = useState('')
  const [writeSkill, setWriteSkill] = useState(false)

  const [working, setWorking] = useState<'idle' | 'optimizing' | 'distilling' | 'saving'>('idle')
  const [statusMessage, setStatusMessage] = useState<string | null>(null)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)

  const defaultReadyAgent = useMemo(() => {
    const ready = agents.find((a) => a.status === 'ready')
    return ready?.id || agents[0]?.id || 'antigravity'
  }, [agents])

  const environmentOptions = useMemo(
    () => buildBotEnvironmentOptions(agents, connectionId),
    [agents, connectionId],
  )

  const modalModelCatalog = useBotModelCatalog(
    defaultAgentId,
    agentsMap[defaultAgentId],
    sessions,
    opened && !isFeedback,
  )

  // Sync native <dialog> showModal() / close() lifecycle (modern-web-guidance: dialog)
  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (opened) {
      if (!dialog.open) {
        if (typeof dialog.showModal === 'function') {
          dialog.showModal()
        } else {
          dialog.setAttribute('open', '')
        }
      }
    } else if (dialog.open) {
      if (typeof dialog.close === 'function') {
        dialog.close()
      } else {
        dialog.removeAttribute('open')
      }
    }
  }, [opened])

  // Reset state each time the modal opens
  useEffect(() => {
    if (!opened) return
    setErrorMessage(null)
    setStatusMessage(null)
    setWorking('idle')
    setDistillResult(null)
    setDistillHint('')
    setGenerateSkill(false)

    if (feedbackBot && initialMode === 'feedback') {
      setMode('distill')
      setName(feedbackBot.name)
      setSlug(feedbackBot.slug)
      setDescription(feedbackBot.description)
      setIcon(feedbackBot.icon || 'robot')
      setWorkspace(feedbackBot.workspace)
      setConnectionId(feedbackBot.connection_id || '')
      setDefaultAgentId(feedbackBot.default_agent_id)
      setOfficeMode(Boolean(feedbackBot.office_mode))
      setTagsText((feedbackBot.tags || []).join(', '))
      setAgentsMd('')
      setSkillSlug('')
      setSkillMd('')
      setWriteAgentsMd(true)
      setWriteSkill(false)
      const defaultKey = initialDistill?.sessionKey || ''
      setSelectedSessionKey(defaultKey)
      return
    }

    const nextMode = initialDistill ? 'distill' : initialMode === 'distill' ? 'distill' : 'create'
    setMode(nextMode)
    setName('')
    setSlug('')
    setDescription('')
    setIcon('robot')
    setWorkspace(initialDistill?.workspace || '')
    setConnectionId(initialDistill?.connectionId || '')
    setDefaultAgentId(initialDistill?.agentId || defaultReadyAgent)
    setDefaultModelKey('')
    setDefaultEffort('')
    setDefaultApprovalMode('full_access')
    setOfficeMode(false)
    setTagsText('')
    setInstructions('')
    setAgentsMd('')
    setWriteAgentsMd(true)
    setSkillSlug('')
    setSkillMd('')
    setWriteSkill(false)
    setSelectedSessionKey(initialDistill?.sessionKey || '')
  }, [opened, initialMode, initialDistill, feedbackBot, defaultReadyAgent])

  const sortedSessions = useMemo(() => {
    return [...sessions].sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))
  }, [sessions])

  const applyProjectChoice = (projectId: string) => {
    const project = projects.find((p) => p.id === projectId)
    if (!project) return
    if (project.workspace) setWorkspace(project.workspace)
    setConnectionId(project.connection_id || '')
    if (project.agent_id) {
      setDefaultAgentId(project.agent_id)
    }
    if (!name.trim()) {
      setName(project.project_name || project.workspace?.split(/[\\/]/).filter(Boolean).pop() || '')
    }
  }

  const handleSelectSession = (sessionKey: string) => {
    setSelectedSessionKey(sessionKey)
    const found = sessions.find((s) => scopeKey(s.agent_id, s.id) === sessionKey)
    if (found && !isFeedback) {
      if (found.workspace) setWorkspace(found.workspace)
      setConnectionId(found.connection_id || '')
      setDefaultAgentId(found.agent_id)
      if (!name.trim()) {
        setName(displaySessionTitle(found))
      }
    }
  }

  const handleOptimize = async () => {
    setErrorMessage(null)
    setStatusMessage(null)
    if (!name.trim() && !description.trim() && !instructions.trim()) {
      setErrorMessage('请先填写 Bot 名称或基础设定，再点击 AI 智能优化。')
      return
    }
    setWorking('optimizing')
    try {
      const res = await api.optimizeBotConfig({
        name: name.trim(),
        description: description.trim(),
        instructions: instructions.trim(),
        workspace: workspace.trim(),
        default_agent_id: defaultAgentId,
        existing_agents_md: agentsMd.trim() || undefined,
      })
      if (!name.trim() && res.suggested_name) setName(res.suggested_name)
      if (!slug.trim() && res.suggested_slug) setSlug(res.suggested_slug)
      if (!description.trim() && res.suggested_description) setDescription(res.suggested_description)
      if (!tagsText.trim() && res.suggested_tags?.length) setTagsText(res.suggested_tags.join(', '))
      setAgentsMd(res.agents_md)
      setWriteAgentsMd(true)
      setStatusMessage(
        res.message ||
          (res.mode === 'llm'
            ? `已通过辅助模型${res.model ? ` (${res.model})` : ''}优化生成 AGENTS.md，可直接审阅微调。`
            : '已按标准结构生成 AGENTS.md 草案，可直接审阅微调。'),
      )
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : '智能优化失败')
    } finally {
      setWorking('idle')
    }
  }

  const handleDistill = async () => {
    setErrorMessage(null)
    setStatusMessage(null)
    if (!selectedSessionKey) {
      setErrorMessage('请先选择一个源会话再进行经验总结。')
      return
    }
    setWorking('distilling')
    try {
      const res = await api.distillSessionToBot({
        session_key: selectedSessionKey,
        bot_id: isFeedback && feedbackBot ? feedbackBot.id : undefined,
        name: name.trim() || undefined,
        hint: distillHint.trim() || undefined,
        generate_skill: generateSkill,
      })
      setDistillResult(res)
      if (!isFeedback) {
        setName(res.suggested_name)
        setSlug(res.suggested_slug)
        setDescription(res.suggested_description)
        setWorkspace(res.workspace || workspace)
        setConnectionId(res.connection_id || '')
        setDefaultAgentId(res.default_agent_id || defaultReadyAgent)
        setTagsText((res.suggested_tags || []).join(', '))
        if (
          res.suggested_tags?.some((t) => t.includes('视频')) ||
          res.workspace.toLowerCase().includes('video')
        ) {
          setIcon('video')
          setOfficeMode(true)
        }
      }
      setAgentsMd(res.agents_md)
      const hasGeneratedSkill = Boolean(generateSkill && res.skill_md?.trim())
      setSkillSlug(hasGeneratedSkill ? (res.skill_slug || '') : '')
      setSkillMd(hasGeneratedSkill ? (res.skill_md || '') : '')
      // Safe default: if workspace already has AGENTS.md and this is new incubation, let user review before overwriting
      setWriteAgentsMd(isFeedback ? true : !res.has_existing_agents_md)
      setWriteSkill(hasGeneratedSkill)
      setStatusMessage(
        res.message ||
          (hasGeneratedSkill
            ? `已从会话「${res.source_session_title}」（读取 ${res.message_count} 条记录）总结出 AGENTS.md 与 Skill 草案。`
            : `已从会话「${res.source_session_title}」（读取 ${res.message_count} 条记录）总结出 AGENTS.md 草案（已纳入相关 Skill 调用指引）。`),
      )
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : '会话经验总结失败')
    } finally {
      setWorking('idle')
    }
  }

  const handleSubmit = async () => {
    setErrorMessage(null)
    if (isFeedback && feedbackBot) {
      if (!writeAgentsMd && !writeSkill) {
        setErrorMessage('请至少勾选「更新 AGENTS.md」或「写入 Skill」其中一项。')
        return
      }
      setWorking('saving')
      try {
        if (writeAgentsMd && agentsMd.trim()) {
          await api.saveBotAgentsMd(feedbackBot.id, agentsMd)
        }
        if (writeSkill && skillMd.trim()) {
          const targetSlug = skillSlug.trim() || `${feedbackBot.slug}-workflow`
          await api.saveBotSkill(feedbackBot.id, targetSlug, skillMd)
        }
        await invalidateBots()
        notifications.show({
          color: 'teal',
          message: `已将新经验反哺至「${feedbackBot.name}」工作区`,
        })
        onSaved(feedbackBot.id)
        onClose()
      } catch (err) {
        setErrorMessage(err instanceof Error ? err.message : '反哺保存失败')
      } finally {
        setWorking('idle')
      }
      return
    }

    if (!name.trim()) {
      setErrorMessage('请填写 Bot 名称。')
      return
    }
    if (!workspace.trim()) {
      setErrorMessage('请选择或填写 Bot 绑定的工作区目录。')
      return
    }
    if (!defaultAgentId.trim()) {
      setErrorMessage('请选择默认执行 Agent。')
      return
    }

    setWorking('saving')
    try {
      const tags = tagsText
        .split(/[,，、]/)
        .map((s) => s.trim())
        .filter(Boolean)
      const resolvedModel = resolveBotModelSelection(
        modalModelCatalog.groups,
        modalModelCatalog.isAntigravity,
        defaultModelKey,
        defaultEffort,
      )
      const res = await api.createBot({
        name: name.trim(),
        slug: slug.trim() || undefined,
        description: description.trim() || undefined,
        icon,
        workspace: workspace.trim(),
        connection_id: connectionId.trim() || undefined,
        default_agent_id: defaultAgentId.trim(),
        default_provider: resolvedModel.default_provider || undefined,
        default_model: resolvedModel.default_model || undefined,
        default_effort: resolvedModel.default_effort || undefined,
        default_approval_mode: defaultApprovalMode,
        office_mode: officeMode,
        tags,
        source_session_key: mode === 'distill' ? selectedSessionKey || undefined : undefined,
        source_session_title: mode === 'distill' ? distillResult?.source_session_title : undefined,
        write_agents_md: writeAgentsMd && Boolean(agentsMd.trim()),
        agents_md: writeAgentsMd ? agentsMd : undefined,
        write_skill: mode === 'distill' && writeSkill && Boolean(skillMd.trim()),
        skill_slug: skillSlug.trim() || undefined,
        skill_md: writeSkill ? skillMd : undefined,
      })

      if (officeMode && workspace.trim()) {
        setProjectOfficeMode({ projectKey: `bot:${res.bot.slug}`, workspace: workspace.trim() }, true)
      }

      await invalidateBots()
      if (res.warnings?.length) {
        notifications.show({
          color: 'yellow',
          title: `已创建 Bot「${res.bot.name}」（部分文件写入提醒）`,
          message: res.warnings.join('；'),
        })
      } else {
        notifications.show({
          color: 'teal',
          message: `Bot「${res.bot.name}」已就绪`,
        })
      }
      onSaved(res.bot.id)
      onClose()
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : '创建 Bot 失败')
    } finally {
      setWorking('idle')
    }
  }

  const handleTabKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'ArrowLeft' || event.key === 'ArrowRight' || event.key === 'Home' || event.key === 'End') {
      event.preventDefault()
      const next = mode === 'create' ? 'distill' : 'create'
      setMode(next)
      const targetId = next === 'create' ? tabCreateId : tabDistillId
      document.getElementById(targetId)?.focus()
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="bot-native-dialog"
      aria-labelledby={titleId}
      {...({ closedby: 'closerequest' } as Record<string, string>)}
      onCancel={(event) => {
        if (working !== 'idle') {
          event.preventDefault()
        }
      }}
      onClose={() => {
        if (dialogRef.current) {
          dialogRef.current.returnValue = ''
        }
        if (opened) onClose()
      }}
    >
      <div className="bot-dialog-header">
        <h1 id={titleId} className="bot-dialog-title">
          {isFeedback && feedbackBot
            ? `从任务反哺经验 · ${feedbackBot.name}`
            : mode === 'distill'
              ? '从已有会话孵化 Bot'
              : '新建 Bot'}
        </h1>
        <button
          type="button"
          className="bot-dialog-close"
          aria-label="关闭弹窗"
          disabled={working !== 'idle'}
          onClick={onClose}
        >
          <IconX size={18} aria-hidden="true" />
        </button>
      </div>

      {!isFeedback && (
        <div className="bot-dialog-tabs">
          <div
            className="bot-tablist"
            role="tablist"
            aria-label="Bot 创建方式"
            onKeyDown={handleTabKeyDown}
            {...({ focusgroup: 'tablist nomemory' } as Record<string, string>)}
          >
            <button
              type="button"
              id={tabCreateId}
              role="tab"
              className="bot-tab"
              aria-selected={mode === 'create'}
              {...(mode === 'create' ? ({ focusgroupstart: '' } as Record<string, string>) : {})}
              onClick={() => setMode('create')}
            >
              <IconRobot size={15} aria-hidden="true" />
              直接新建 Bot
            </button>
            <button
              type="button"
              id={tabDistillId}
              role="tab"
              className="bot-tab"
              aria-selected={mode === 'distill'}
              {...(mode === 'distill' ? ({ focusgroupstart: '' } as Record<string, string>) : {})}
              onClick={() => setMode('distill')}
            >
              <IconSparkles size={15} aria-hidden="true" />
              从已有会话孵化
            </button>
          </div>
        </div>
      )}

      <div className="bot-dialog-body">
        {errorMessage && (
          <div role="alert" className="bot-status-banner is-error">
            {errorMessage}
          </div>
        )}
        {statusMessage && (
          <div role="status" aria-live="polite" className="bot-status-banner">
            {statusMessage}
          </div>
        )}

        {(mode === 'distill' || isFeedback) && (
          <div className="bot-card">
            <div className="bot-field">
              <label htmlFor="bot-distill-session" className="bot-label">
                选择要提炼经验的源会话
              </label>
              <select
                id="bot-distill-session"
                className="bot-select"
                value={selectedSessionKey}
                onChange={(e) => handleSelectSession(e.target.value)}
              >
                <option value="">-- 请选择会话 --</option>
                {sortedSessions.map((s) => {
                  const key = scopeKey(s.agent_id, s.id)
                  const title = displaySessionTitle(s)
                  const agent = agentsMap[s.agent_id]
                  const subtitle = s.workspace || formatAgentLabel(s.agent_id, agent)
                  return (
                    <option key={key} value={key}>
                      {title} · {subtitle}
                    </option>
                  )
                })}
              </select>
            </div>
            <div className="bot-field">
              <label htmlFor="bot-distill-hint" className="bot-label">
                提炼侧重点（可选）
              </label>
              <input
                id="bot-distill-hint"
                className="bot-input"
                type="text"
                value={distillHint}
                onChange={(e) => setDistillHint(e.target.value)}
                placeholder="例如：重点沉淀配音、GPU 渲染与封面验收规范，剔除单个项目文案"
              />
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              <button
                type="button"
                className="bot-btn-secondary"
                disabled={working !== 'idle' || !selectedSessionKey}
                onClick={() => void handleDistill()}
              >
                <IconSparkles size={15} aria-hidden="true" />
                {working === 'distilling'
                  ? '正在读取会话并总结经验…'
                  : generateSkill
                    ? 'AI 总结为 AGENTS.md 与 Skill'
                    : 'AI 总结为 AGENTS.md'}
              </button>
              <label className="bot-checkbox-label">
                <input
                  type="checkbox"
                  checked={generateSkill}
                  onChange={(e) => {
                    const checked = e.target.checked
                    setGenerateSkill(checked)
                    setWriteSkill(checked)
                  }}
                />
                同时生成 / 更新 Skill 文件
              </label>
            </div>
            {distillResult && ((distillResult.used_skills?.length || 0) > 0 || (distillResult.existing_skills?.length || 0) > 0) && (
              <div className="bot-field-hint" style={{ display: 'flex', flexDirection: 'column', gap: 3 }}>
                {distillResult.used_skills && distillResult.used_skills.length > 0 && (
                  <span>
                    本会话用到的 Skill（已写入 AGENTS.md 指引）：{distillResult.used_skills.join('、')}
                  </span>
                )}
                {distillResult.existing_skills && distillResult.existing_skills.length > 0 && (
                  <span>
                    工作区已有 {distillResult.existing_skills.length} 个 Skill：
                    {distillResult.existing_skills.map((s) => s.slug).join('、')}
                  </span>
                )}
              </div>
            )}
            {distillResult?.has_existing_agents_md && !isFeedback && (
              <div className="bot-status-banner is-warn">
                检测到该工作区已存在 <code>AGENTS.md</code>。为保护原文件，默认未勾选覆盖写入；如确认使用下方草案替换，可手动勾选。
              </div>
            )}
          </div>
        )}

        {!isFeedback && (
          <>
            <div className="bot-field-row">
              <div className="bot-field">
                <label htmlFor="bot-name" className="bot-label">
                  Bot 名称
                </label>
                <input
                  id="bot-name"
                  className="bot-input"
                  type="text"
                  required
                  autoFocus={!isFeedback && mode === 'create'}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="例如：视频制作师"
                />
              </div>
              <div className="bot-field">
                <label htmlFor="bot-slug" className="bot-label">
                  调用标识（Slug，可选）
                </label>
                <input
                  id="bot-slug"
                  className="bot-input"
                  type="text"
                  value={slug}
                  onChange={(e) => setSlug(e.target.value)}
                  placeholder="例如：video-studio（留空自动生成）"
                />
              </div>
            </div>

            <div className="bot-field-row">
              <div className="bot-field">
                <label htmlFor="bot-project-pick" className="bot-label">
                  快速绑定已有项目工作区
                </label>
                <select
                  id="bot-project-pick"
                  className="bot-select"
                  value=""
                  onChange={(e) => applyProjectChoice(e.target.value)}
                >
                  <option value="">-- 从已有项目选择（可选） --</option>
                  {projects.map((p) => {
                    const matchingAgent = agents.find(
                      (a) => (a.connection_id || 'local') === (p.connection_id || 'local'),
                    )
                    const envLabel =
                      p.connection_id && p.connection_id !== 'local'
                        ? formatEnvLabel(p.connection_id, matchingAgent)
                        : ''
                    return (
                      <option key={p.id} value={p.id}>
                        {p.project_name || p.workspace || p.project_id}
                        {envLabel ? ` (${envLabel})` : ''}
                        {p.workspace ? ` · ${p.workspace}` : ''}
                      </option>
                    )
                  })}
                </select>
              </div>
              <div className="bot-field">
                <label htmlFor="bot-agent" className="bot-label">
                  默认执行 Agent
                </label>
                <select
                  id="bot-agent"
                  className="bot-select"
                  value={defaultAgentId}
                  onChange={(e) => {
                    setDefaultAgentId(e.target.value)
                    setDefaultModelKey('')
                    setDefaultEffort('')
                  }}
                >
                  {agents.map((a) => {
                    const agentLabel = formatAgentLabel(a.id, a)
                    const envLabel = formatEnvLabel(a.connection_id || undefined, a)
                    const display = a.name || `${agentLabel} · ${envLabel}`
                    const suffix = `${agentLabel} · ${envLabel}`
                    const redundant =
                      display.toLowerCase().includes(agentLabel.toLowerCase()) &&
                      display.toLowerCase().includes(envLabel.toLowerCase())
                    return (
                      <option key={a.id} value={a.id}>
                        {redundant ? display : `${display} (${suffix})`}
                      </option>
                    )
                  })}
                  {!agents.some((a) => a.id === defaultAgentId) && defaultAgentId && (
                    <option value={defaultAgentId}>{formatAgentLabel(defaultAgentId, undefined)}</option>
                  )}
                </select>
              </div>
            </div>

            {(() => {
              const effortMeta = effortOptionsForBotSelection(
                modalModelCatalog.groups,
                modalModelCatalog.isAntigravity,
                defaultModelKey,
              )
              return (
                <div className="bot-field-row-3">
                  <div className="bot-field">
                    <label htmlFor="bot-approval-mode" className="bot-label">
                      默认执行权限
                    </label>
                    <select
                      id="bot-approval-mode"
                      className="bot-select"
                      value={defaultApprovalMode}
                      onChange={(e) => setDefaultApprovalMode(normalizeBotApprovalMode(e.target.value))}
                    >
                      {BOT_APPROVAL_MODE_OPTIONS.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="bot-field">
                    <label htmlFor="bot-default-model" className="bot-label">
                      默认模型
                    </label>
                    <select
                      id="bot-default-model"
                      className="bot-select"
                      value={defaultModelKey}
                      onChange={(e) => {
                        const nextKey = e.target.value
                        setDefaultModelKey(nextKey)
                        const nextMeta = effortOptionsForBotSelection(
                          modalModelCatalog.groups,
                          modalModelCatalog.isAntigravity,
                          nextKey,
                        )
                        if (nextMeta.disabled) {
                          setDefaultEffort(nextMeta.options[0]?.value || '')
                        } else if (
                          defaultEffort &&
                          !nextMeta.options.some((opt) => opt.value === defaultEffort)
                        ) {
                          setDefaultEffort('')
                        }
                      }}
                    >
                      <option value="">
                        {modalModelCatalog.isLoading ? '正在加载模型…' : '沿用 Agent 默认模型'}
                      </option>
                      {modalModelCatalog.groups.map((g) => (
                        <option key={g.key} value={g.key}>
                          {g.label}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="bot-field">
                    <label htmlFor="bot-default-effort" className="bot-label">
                      默认思考强度
                    </label>
                    <select
                      id="bot-default-effort"
                      className="bot-select"
                      value={effortMeta.disabled ? (effortMeta.options[0]?.value || '') : defaultEffort}
                      disabled={effortMeta.disabled}
                      onChange={(e) => setDefaultEffort(e.target.value)}
                    >
                      <option value="">{effortMeta.placeholder}</option>
                      {effortMeta.options.map((opt) => (
                        <option key={opt.value} value={opt.value}>
                          {opt.label}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>
              )
            })()}

            <div className="bot-field-row">
              <div className="bot-field">
                <label htmlFor="bot-workspace" className="bot-label">
                  专属工作区路径（1 Bot = 1 项目目录）
                </label>
                <input
                  id="bot-workspace"
                  className="bot-input"
                  type="text"
                  required
                  value={workspace}
                  onChange={(e) => setWorkspace(e.target.value)}
                  placeholder="例如：~/Documents/my-project 或 D:\projects\my-bot"
                />
              </div>
              <div className="bot-field">
                <label htmlFor="bot-conn" className="bot-label">
                  运行环境
                </label>
                <select
                  id="bot-conn"
                  className="bot-select"
                  value={normalizeBotConnection(connectionId)}
                  onChange={(e) => setConnectionId(e.target.value)}
                >
                  {environmentOptions.map((opt) => (
                    <option key={opt.value || 'local'} value={opt.value}>
                      {opt.label}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="bot-field-row">
              <div className="bot-field">
                <label htmlFor="bot-desc" className="bot-label">
                  一句话能力简介（供 @Bot 检索）
                </label>
                <input
                  id="bot-desc"
                  className="bot-input"
                  type="text"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="例如：负责 2K 宣传片脚本、配音、渲染、混音与封面交付"
                />
              </div>
              <div className="bot-field">
                <label htmlFor="bot-icon" className="bot-label">
                  图标类型与标签
                </label>
                <div style={{ display: 'flex', gap: 8 }}>
                  <select
                    id="bot-icon"
                    className="bot-select"
                    style={{ width: 132, flex: 'none' }}
                    value={icon}
                    onChange={(e) => setIcon(e.target.value)}
                  >
                    {ICON_OPTIONS.map((opt) => (
                      <option key={opt.value} value={opt.value}>
                        {opt.label}
                      </option>
                    ))}
                  </select>
                  <input
                    aria-label="Bot 标签"
                    className="bot-input"
                    type="text"
                    value={tagsText}
                    onChange={(e) => setTagsText(e.target.value)}
                    placeholder="标签，逗号分隔"
                  />
                </div>
              </div>
            </div>

            <label className="bot-checkbox-label">
              <input
                type="checkbox"
                checked={officeMode}
                onChange={(e) => setOfficeMode(e.target.checked)}
              />
              将该工作区启用为办公空间模式（默认启用文档/音视频审阅工具，隐藏 Git/终端等开发面板）
            </label>
          </>
        )}

        {!isFeedback && mode === 'create' && (
          <div className="bot-field">
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
              <label htmlFor="bot-basic-instructions" className="bot-label">
                基础设定（用大白话描述角色职责、习惯与交付标准）
              </label>
              <button
                type="button"
                className="bot-btn-secondary"
                disabled={working !== 'idle'}
                onClick={() => void handleOptimize()}
              >
                <IconWand size={14} aria-hidden="true" />
                {working === 'optimizing' ? '正在优化…' : 'AI 智能优化为 AGENTS.md'}
              </button>
            </div>
            <textarea
              id="bot-basic-instructions"
              className="bot-textarea"
              rows={3}
              value={instructions}
              onChange={(e) => setInstructions(e.target.value)}
              placeholder="例如：你是一名专业宣传片导演，擅长将产品资料做成 90 秒 2K 宣传片，优先使用派单和 inbox 素材，如需了解更多细节可回溯源项目。"
            />
          </div>
        )}

        <div className="bot-field">
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
            <label htmlFor="bot-agents-md-draft" className="bot-label">
              工作区角色宪章（AGENTS.md）
            </label>
            <label className="bot-checkbox-label">
              <input
                type="checkbox"
                checked={writeAgentsMd}
                onChange={(e) => setWriteAgentsMd(e.target.checked)}
              />
              {isFeedback ? '写入更新 AGENTS.md' : '创建时写入工作区 AGENTS.md'}
            </label>
          </div>
          <textarea
            id="bot-agents-md-draft"
            className="bot-textarea is-mono"
            rows={7}
            value={agentsMd}
            onChange={(e) => setAgentsMd(e.target.value)}
            placeholder="可点击上方「AI 智能优化」或「AI 总结」自动生成，也可留空直接复用工作区已有 AGENTS.md"
          />
        </div>

        {(mode === 'distill' || isFeedback) && (generateSkill || Boolean(skillMd.trim())) && (
          <div className="bot-field">
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, flexWrap: 'wrap' }}>
              <label htmlFor="bot-skill-md-draft" className="bot-label">
                沉淀技能文件（.agents/skills/{skillSlug || 'workflow'}/SKILL.md）
              </label>
              <label className="bot-checkbox-label">
                <input
                  type="checkbox"
                  checked={writeSkill}
                  onChange={(e) => setWriteSkill(e.target.checked)}
                />
                保存此 Skill 到工作区
              </label>
            </div>
            <input
              aria-label="Skill 目录标识"
              className="bot-input"
              type="text"
              value={skillSlug}
              onChange={(e) => setSkillSlug(e.target.value)}
              placeholder="skill 目录名（英文 kebab-case，如 promo-video-production）"
            />
            <textarea
              id="bot-skill-md-draft"
              className="bot-textarea is-mono"
              rows={7}
              value={skillMd}
              onChange={(e) => setSkillMd(e.target.value)}
              placeholder="点击上方「AI 总结」自动生成标准 SKILL.md，或手动编写"
            />
          </div>
        )}
      </div>

      <div className="bot-dialog-footer">
        <span className="bot-field-hint">
          <IconFolder size={13} style={{ verticalAlign: '-2px', marginRight: 4 }} aria-hidden="true" />
          所有规范与技能均原生保存为工作区 Markdown 文件
        </span>
        <div className="bot-dialog-footer-actions">
          <button
            type="button"
            className="bot-btn-ghost"
            disabled={working !== 'idle'}
            onClick={onClose}
          >
            取消
          </button>
          <button
            type="button"
            className="bot-btn-primary"
            disabled={working !== 'idle'}
            onClick={() => void handleSubmit()}
          >
            <IconCheck size={15} aria-hidden="true" />
            {working === 'saving'
              ? '保存中…'
              : isFeedback
                ? '写入反哺经验'
                : '保存并创建 Bot'}
          </button>
        </div>
      </div>
    </dialog>
  )
}
