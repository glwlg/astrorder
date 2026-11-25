import { useCallback, useId, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { ActionIcon, Badge, Group, Menu, Modal, Text, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import {
  IconArrowRight,
  IconCheck,
  IconChevronDown,
  IconClock,
  IconFileSpreadsheet,
  IconFileText,
  IconFileTypeDocx,
  IconFileTypePdf,
  IconFolder,
  IconFolderPlus,
  IconInbox,
  IconMicrophone,
  IconNotebook,
  IconPlus,
  IconPresentation,
  IconSend,
  IconSparkles,
  IconUserCog,
} from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { useShallow } from 'zustand/react/shallow'
import { api, type RecentOfficeDoc } from '../../api/client'
import { displaySessionTitle, formatRelativeTime } from '../../components/sessionRailModel'
import { newCommandId, scopeKey } from '../../domain/semantics'
import type { Agent, Project, Session } from '../../domain/types'
import { selectProjects, useAstrorderStore } from '../../state/store'
import { isOverdue, useActionInbox } from '../inbox/useActionInbox'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { resolveArtifactFromPath } from '../sidecar/resolver'
import { artifactViewerRegistry } from '../sidecar/registry'
import { useSidecarStore } from '../sidecar/sidecarStore'
import { OfficePersonaCard } from './OfficePersonaCard'
import { OfficeTemplateModal } from './OfficeTemplateModal'
import { buildPersonaPromptBlock, hasConfiguredOfficePersona, useOfficePersona, writingStyleLabel } from './officePersona'
import { OFFICE_DRAFT_TEMPLATES } from './officeTemplates'
import './officeStudio.css'

interface WorkspaceChoice {
  key: string
  label: string
  workspace: string
  projectName: string
}

function greetingByHour(): string {
  const h = new Date().getHours()
  if (h < 12) return '早上好'
  if (h < 18) return '下午好'
  return '晚上好'
}

function formattedTodayZh(): string {
  const d = new Date()
  const weekdays = ['星期日', '星期一', '星期二', '星期三', '星期四', '星期五', '星期六']
  return `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日 · ${weekdays[d.getDay()]}`
}

function docIcon(ext: string) {
  const e = ext.toLowerCase()
  if (e === 'docx' || e === 'doc' || e === 'wps') return <IconFileTypeDocx size={15} color="#2563eb" />
  if (e === 'xlsx' || e === 'xls' || e === 'csv') return <IconFileSpreadsheet size={15} color="#15803d" />
  if (e === 'pptx' || e === 'ppt') return <IconPresentation size={15} color="#ea580c" />
  if (e === 'pdf') return <IconFileTypePdf size={15} color="#dc2626" />
  return <IconFileText size={15} color="#52525b" />
}

export interface OfficeHomeDashboardProps {
  sessions: Session[]
  agents: Record<string, Agent>
  onNewSession?: () => void
}

export function OfficeHomeDashboard({ sessions, agents, onNewSession }: OfficeHomeDashboardProps) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const inputId = useId()
  const formRef = useRef<HTMLFormElement>(null)

  const projects = useAstrorderStore(useShallow(selectProjects))
  const isAgentEnabled = usePluginSettingsStore((s) => s.isAgentEnabled)
  const { profile } = useOfficePersona()
  const { openTodos, meetingActions } = useActionInbox()

  const recentDocsQuery = useQuery({
    queryKey: ['astrorder', 'office', 'recent-docs'],
    queryFn: async () => {
      if (typeof api.listRecentOfficeDocs !== 'function') return { items: [] }
      try {
        return await api.listRecentOfficeDocs({ limit: 8 })
      } catch {
        return { items: [] }
      }
    },
    staleTime: 30_000,
    refetchOnWindowFocus: false,
    retry: false,
  })

  const recentMeetingsQuery = useQuery({
    queryKey: ['astrorder', 'office', 'home-meetings'],
    queryFn: async () => {
      try {
        return await api.listMeetings()
      } catch {
        return { items: [] }
      }
    },
    staleTime: 45_000,
    refetchOnWindowFocus: false,
    retry: false,
  })

  const readyLocalAgents = useMemo(() => {
    const allReady = Object.values(agents).filter((a) => a.status === 'ready' && isAgentEnabled(a.kind))
    const localOnly = allReady.filter((a) => !a.connection_id || a.connection_id === 'local')
    return localOnly.length > 0 ? localOnly : allReady
  }, [agents, isAgentEnabled])

  const workspaceChoices = useMemo<WorkspaceChoice[]>(() => {
    const seen = new Set<string>()
    const list: WorkspaceChoice[] = []
    const push = (ws?: string | null, name?: string | null, conn?: string | null) => {
      if (!ws?.trim()) return
      if (conn && conn !== 'local') return
      const key = ws.trim().toLowerCase()
      if (seen.has(key)) return
      seen.add(key)
      const label = name?.trim() || ws.split(/[/\\]/).filter(Boolean).pop() || ws
      list.push({ key, label, workspace: ws.trim(), projectName: label })
    }
    for (const s of [...sessions].sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))) {
      push(s.workspace, s.project_name, s.connection_id)
    }
    for (const p of projects as Project[]) {
      push(p.workspace, p.project_name, p.connection_id)
    }
    return list
  }, [projects, sessions])

  const [prompt, setPrompt] = useState('')
  const [selectedWorkspaceKey, setSelectedWorkspaceKey] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [quickTodoText, setQuickTodoText] = useState('')
  const [addingTodo, setAddingTodo] = useState(false)
  const [personaModalOpen, setPersonaModalOpen] = useState(false)
  const [templateModalId, setTemplateModalId] = useState<string | null>(null)
  const [templateModalOpen, setTemplateModalOpen] = useState(false)

  const activeWorkspace = useMemo(
    () => workspaceChoices.find((w) => w.key === selectedWorkspaceKey) || workspaceChoices[0] || null,
    [selectedWorkspaceKey, workspaceChoices],
  )

  const effectiveAgent = useMemo(() => {
    const recentId = sessions.find((s) => readyLocalAgents.some((a) => a.id === s.agent_id))?.agent_id
    return readyLocalAgents.find((a) => a.id === recentId) || readyLocalAgents[0] || null
  }, [readyLocalAgents, sessions])

  const startConversationWithPrompt = useCallback(
    async (rawText: string, explicitTitle?: string) => {
      const trimmed = rawText.trim()
      if (!trimmed || !effectiveAgent || submitting) return
      setSubmitting(true)
      try {
        const personaPrefix = buildPersonaPromptBlock(profile)
        const outgoing = personaPrefix && !trimmed.includes('【个人办公偏好参考')
          ? `${personaPrefix}\n\n${trimmed}`
          : trimmed
        const created = await api.createSession({
          agent_id: effectiveAgent.id,
          workspace: activeWorkspace?.workspace || null,
          title: explicitTitle || null,
          project_name: activeWorkspace?.projectName || null,
        })
        useAstrorderStore.setState((state) => ({
          sessions: { ...state.sessions, [scopeKey(created.agent_id, created.id)]: created },
        }))
        const sent = await api.createCommand({
          id: newCommandId(),
          agent_id: created.agent_id,
          session_id: created.id,
          action: 'send',
          text: outgoing,
          attachment_ids: [],
          target_id: null,
        })
        useAstrorderStore.getState().mergeCommands([sent])
        setPrompt('')
        navigate(`/chat/${encodeURIComponent(created.id)}?agent_id=${encodeURIComponent(created.agent_id)}`)
      } catch (cause) {
        notifications.show({
          color: 'red',
          message: cause instanceof Error ? cause.message : '启动办公对话失败',
        })
      } finally {
        setSubmitting(false)
      }
    },
    [activeWorkspace, effectiveAgent, navigate, profile, submitting],
  )

  const handleOmniSubmit = (e?: FormEvent) => {
    e?.preventDefault()
    void startConversationWithPrompt(prompt)
  }

  const handleTextareaKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      if (e.nativeEvent.isComposing || e.keyCode === 229) return
      formRef.current?.requestSubmit()
    }
  }

  const handleQuickAddTodo = async (e: FormEvent) => {
    e.preventDefault()
    const text = quickTodoText.trim()
    if (!text || addingTodo) return
    setAddingTodo(true)
    try {
      await api.createTodo({ text, priority: 'medium' })
      setQuickTodoText('')
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'office-todos'] })
      notifications.show({ color: 'teal', message: '已添加到今日待办' })
    } catch (cause) {
      notifications.show({ color: 'red', message: cause instanceof Error ? cause.message : '添加待办失败' })
    } finally {
      setAddingTodo(false)
    }
  }

  const handleCompleteTodo = async (id: string) => {
    try {
      await api.patchTodo(id, { status: 'done' })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'office-todos'] })
    } catch {
      // ignore
    }
  }

  const handleCompleteMeetingAct = async (meetingId: string, itemId: string) => {
    try {
      await api.patchMeetingActionItem({ meeting_id: meetingId, item_id: itemId, status: 'done' })
      await queryClient.invalidateQueries({ queryKey: ['astrorder', 'meeting-action-items'] })
    } catch {
      // ignore
    }
  }

  const handleOpenRecentDoc = async (doc: RecentOfficeDoc) => {
    const matchedSession = sessions
      .filter((s) => s.workspace && doc.workspace && s.workspace.toLowerCase() === doc.workspace.toLowerCase())
      .sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))[0]

    let targetSession = matchedSession
    if (!targetSession && effectiveAgent) {
      try {
        targetSession = await api.createSession({
          agent_id: effectiveAgent.id,
          workspace: doc.workspace,
          title: doc.name.replace(/\.[^.]+$/, ''),
          project_name: doc.project_name || null,
        })
        useAstrorderStore.setState((state) => ({
          sessions: { ...state.sessions, [scopeKey(targetSession!.agent_id, targetSession!.id)]: targetSession! },
        }))
      } catch {
        notifications.show({ color: 'red', message: '无法打开该文档所属工作空间' })
        return
      }
    }
    if (!targetSession) return
    navigate(`/chat/${encodeURIComponent(targetSession.id)}?agent_id=${encodeURIComponent(targetSession.agent_id)}`)
    window.setTimeout(() => {
      const artifact = resolveArtifactFromPath(doc.path, targetSession!, { name: doc.name })
      const viewer = artifactViewerRegistry.findViewer(artifact)
      useSidecarStore.getState().openArtifact(
        { ...artifact, metadata: { ...artifact.metadata, preferRich: true } },
        viewer?.id || 'monaco-viewer',
      )
    }, 160)
  }

  const handleGeneratedDocument = async (doc: { title: string; markdown: string; savedPath?: string }) => {
    let targetSession = sessions
      .filter((s) => !activeWorkspace || (s.workspace || '').toLowerCase() === activeWorkspace.workspace.toLowerCase())
      .sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))[0]
    if (!targetSession && effectiveAgent) {
      try {
        targetSession = await api.createSession({
          agent_id: effectiveAgent.id,
          workspace: activeWorkspace?.workspace || null,
          title: doc.title,
          project_name: activeWorkspace?.projectName || null,
        })
        useAstrorderStore.setState((state) => ({
          sessions: { ...state.sessions, [scopeKey(targetSession!.agent_id, targetSession!.id)]: targetSession! },
        }))
      } catch {
        return
      }
    }
    if (!targetSession) return
    navigate(`/chat/${encodeURIComponent(targetSession.id)}?agent_id=${encodeURIComponent(targetSession.agent_id)}`)
    window.setTimeout(() => {
      if (doc.savedPath) {
        const artifact = resolveArtifactFromPath(doc.savedPath, targetSession!, { name: `${doc.title}.md` })
        useSidecarStore.getState().openArtifact(
          { ...artifact, metadata: { ...artifact.metadata, preferRich: true } },
          'monaco-viewer',
        )
      } else {
        useSidecarStore.getState().openArtifact(
          {
            id: `draft:${Date.now()}`,
            sessionId: targetSession!.id,
            agentId: targetSession!.agent_id,
            name: `${doc.title}.md`,
            kind: 'workspace_file',
            readUrl: '',
            mediaType: 'text/markdown',
            writable: false,
            metadata: { inlineContent: doc.markdown, inlineEditable: true, preferRich: true },
          },
          'monaco-viewer',
        )
      }
    }, 160)
  }

  const topTasks = useMemo(() => {
    const combined: Array<{
      id: string
      kind: 'todo' | 'meeting'
      meetingId?: string
      text: string
      meta: string
      overdue: boolean
      high: boolean
    }> = []
    for (const t of openTodos) {
      combined.push({
        id: t.id,
        kind: 'todo',
        text: t.text,
        meta: t.due_date || '个人待办',
        overdue: isOverdue(t.due_date),
        high: t.priority === 'high',
      })
    }
    for (const a of meetingActions) {
      combined.push({
        id: a.id,
        kind: 'meeting',
        meetingId: a.meeting_id,
        text: a.text,
        meta: a.meeting_title ? `会议 · ${a.meeting_title}` : '会议行动项',
        overdue: isOverdue(a.due_date),
        high: a.priority === 'high',
      })
    }
    combined.sort((a, b) => Number(b.overdue) - Number(a.overdue) || Number(b.high) - Number(a.high))
    return combined.slice(0, 5)
  }, [meetingActions, openTodos])

  const recentDocs = recentDocsQuery.data?.items || []
  const recentMeetings = (recentMeetingsQuery.data?.items || []).slice(0, 3)
  const recentSessions = useMemo(
    () =>
      [...sessions]
        .sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))
        .slice(0, 3),
    [sessions],
  )

  const personaConfigured = hasConfiguredOfficePersona(profile)

  return (
    <div className="office-home-stage" aria-label="今日办公工作台">
      <div className="office-home-inner">
        {/* 1. Morning Briefing Greeting + Persona Pill */}
        <header className="office-home-header">
          <div>
            <h1 className="office-home-greeting">
              {greetingByHour()}{profile.display_name ? `，${profile.display_name}` : ''}
            </h1>
            <div className="office-home-date">
              {formattedTodayZh()}
              {profile.role ? ` · ${profile.role}` : ''}
              {openTodos.length + meetingActions.length > 0
                ? ` · 当前有 ${openTodos.length + meetingActions.length} 项待办`
                : ' · 今日待办已清空'}
            </div>
          </div>

          <Group gap="xs">
            <button
              type="button"
              className="office-home-persona-pill"
              onClick={() => setPersonaModalOpen(true)}
            >
              <IconUserCog size={14} />
              <span>
                {personaConfigured
                  ? `写作风格：${writingStyleLabel(profile.writing_style).split('·')[0].trim()}`
                  : '设置个人办公画像'}
              </span>
            </button>
            <button
              type="button"
              className="office-home-persona-pill"
              onClick={() => navigate('/meetings')}
            >
              <IconMicrophone size={14} />
              <span>开始会议录音</span>
            </button>
            <button
              type="button"
              className="office-home-persona-pill"
              onClick={() => navigate('/digest')}
            >
              <IconNotebook size={14} />
              <span>工作周报</span>
            </button>
          </Group>
        </header>

        {/* 2. Conversation-First Office Omni-Composer */}
        <form ref={formRef} className="office-home-composer" onSubmit={handleOmniSubmit} aria-label="新建办公对话">
          <textarea
            id={inputId}
            className="office-home-textarea"
            placeholder={
              effectiveAgent
                ? '告诉我你想起草的方案、要分析的报表、或需要协助推进的工作，按 Enter 直接开始…'
                : '当前暂无就绪 AI 助手，请前往左下角「设置 → 高级配置 → 连接管理」检查工作环境…'
            }
            value={prompt}
            onChange={(e) => setPrompt(e.currentTarget.value)}
            onKeyDown={handleTextareaKeyDown}
            disabled={submitting || !effectiveAgent}
          />
          <div className="office-home-composer-bar">
            <Group gap={6}>
              <Menu withinPortal position="bottom-start" width={240}>
                <Menu.Target>
                  <button type="button" className="office-tool-chip" aria-label="选择目标工作空间">
                    <IconFolder size={12} />
                    <span>{activeWorkspace ? activeWorkspace.label : '选择工作空间'}</span>
                    <IconChevronDown size={11} />
                  </button>
                </Menu.Target>
                <Menu.Dropdown>
                  {workspaceChoices.map((w) => (
                    <Menu.Item
                      key={w.key}
                      leftSection={<IconFolder size={13} />}
                      rightSection={activeWorkspace?.key === w.key ? <IconCheck size={13} /> : null}
                      onClick={() => setSelectedWorkspaceKey(w.key)}
                    >
                      {w.label}
                    </Menu.Item>
                  ))}
                  {workspaceChoices.length === 0 && (
                    <Menu.Item disabled>暂无工作空间，可先新建</Menu.Item>
                  )}
                </Menu.Dropdown>
              </Menu>

              <button
                type="button"
                className="office-tool-chip"
                onClick={() => {
                  setTemplateModalId(null)
                  setTemplateModalOpen(true)
                }}
              >
                <IconSparkles size={12} />
                <span>套模板起草</span>
              </button>

              {onNewSession && (
                <button type="button" className="office-tool-chip" onClick={onNewSession}>
                  <IconFolderPlus size={12} />
                  <span>新建对话</span>
                </button>
              )}
            </Group>

            <button
              type="submit"
              className="office-home-submit"
              disabled={submitting || !prompt.trim() || !effectiveAgent}
            >
              <IconSend size={13} />
              <span>{submitting ? '正在开始…' : '开始对话'}</span>
            </button>
          </div>
        </form>

        {/* 3. 8-Scenario Office Drafting Template Strip */}
        <section aria-label="高频场景一键起草">
          <div className="office-home-section-head">
            <span className="office-home-section-title">高频办公模板 · 填要素一键成稿</span>
            <button
              type="button"
              className="office-home-link"
              onClick={() => {
                setTemplateModalId(null)
                setTemplateModalOpen(true)
              }}
            >
              <span>全部模板</span>
              <IconArrowRight size={12} />
            </button>
          </div>
          <div className="office-tpl-grid">
            {OFFICE_DRAFT_TEMPLATES.map((tpl) => (
              <button
                key={tpl.id}
                type="button"
                className="office-tpl-card"
                onClick={() => {
                  setTemplateModalId(tpl.id)
                  setTemplateModalOpen(true)
                }}
              >
                <div className="office-tpl-card-head">
                  <span className="office-tpl-card-name">{tpl.shortLabel}</span>
                  <span className="office-tpl-card-cat">{tpl.category}</span>
                </div>
                <span className="office-tpl-card-desc">{tpl.description}</span>
              </button>
            ))}
          </div>
        </section>

        {/* 4. 3-Column Living Office Bento Grid */}
        <section className="office-bento-grid" aria-label="今日办公概览">
          {/* Card 1: Today's Todos & Meeting Actions */}
          <div className="office-bento-card">
            <div className="office-home-section-head" style={{ marginBottom: 0 }}>
              <Group gap={6}>
                <IconInbox size={14} />
                <span className="office-home-section-title">今日待办与跟进</span>
                {openTodos.length + meetingActions.length > 0 && (
                  <Badge size="xs" variant="light" color="gray">
                    {openTodos.length + meetingActions.length}
                  </Badge>
                )}
              </Group>
              <button type="button" className="office-home-link" onClick={() => navigate('/inbox')}>
                <span>全部</span>
                <IconArrowRight size={12} />
              </button>
            </div>

            <form className="office-home-quick-todo" onSubmit={(e) => void handleQuickAddTodo(e)}>
              <IconPlus size={13} color="var(--oh-faint)" />
              <input
                type="text"
                aria-label="首页快捷添加待办"
                placeholder="记一条待办，按 Enter 添加…"
                value={quickTodoText}
                onChange={(e) => setQuickTodoText(e.currentTarget.value)}
                disabled={addingTodo}
              />
            </form>

            <div className="office-bento-list">
              {topTasks.length === 0 ? (
                <Text size="xs" c="dimmed" py="sm" ta="center">
                  当前没有待办事项，状态很清爽
                </Text>
              ) : (
                topTasks.map((task) => (
                  <div key={`${task.kind}-${task.id}`} className="office-bento-row" style={{ cursor: 'default' }}>
                    <Tooltip label="点击标为完成">
                      <ActionIcon
                        size="xs"
                        radius="xl"
                        variant="default"
                        aria-label={`完成 ${task.text}`}
                        onClick={() =>
                          task.kind === 'todo'
                            ? void handleCompleteTodo(task.id)
                            : void handleCompleteMeetingAct(task.meetingId || '', task.id)
                        }
                      >
                        <IconCheck size={10} />
                      </ActionIcon>
                    </Tooltip>
                    <div
                      className="office-bento-row-main"
                      style={{ cursor: 'pointer' }}
                      onClick={() =>
                        task.kind === 'meeting' && task.meetingId
                          ? navigate(`/meetings?id=${encodeURIComponent(task.meetingId)}`)
                          : navigate('/inbox')
                      }
                    >
                      <span className="office-bento-row-title">{task.text}</span>
                      <span
                        className="office-bento-row-sub"
                        style={task.overdue ? { color: '#e11d48', fontWeight: 550 } : undefined}
                      >
                        {task.overdue ? `已逾期 · ${task.meta}` : task.meta}
                      </span>
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>

          {/* Card 2: Recently Edited Office Documents */}
          <div className="office-bento-card">
            <div className="office-home-section-head" style={{ marginBottom: 0 }}>
              <Group gap={6}>
                <IconFileText size={14} />
                <span className="office-home-section-title">近期办公文档</span>
              </Group>
              <span style={{ fontSize: 11, color: 'var(--oh-faint)' }}>点击直达写作台</span>
            </div>

            <div className="office-bento-list">
              {recentDocs.length === 0 ? (
                <Text size="xs" c="dimmed" py="md" ta="center">
                  暂无扫描到的本机 Word / Excel / PPT / Markdown 文档
                </Text>
              ) : (
                recentDocs.slice(0, 6).map((doc) => (
                  <button
                    key={doc.path}
                    type="button"
                    className="office-bento-row"
                    title={doc.path}
                    onClick={() => void handleOpenRecentDoc(doc)}
                  >
                    {docIcon(doc.ext)}
                    <div className="office-bento-row-main">
                      <span className="office-bento-row-title">{doc.name}</span>
                      <span className="office-bento-row-sub">
                        {doc.project_name || '本机空间'}
                        {doc.mod_time ? ` · ${formatRelativeTime(doc.mod_time)}` : ''}
                      </span>
                    </div>
                  </button>
                ))
              )}
            </div>
          </div>

          {/* Card 3: Recent Meetings & Conversations */}
          <div className="office-bento-card">
            <div className="office-home-section-head" style={{ marginBottom: 0 }}>
              <Group gap={6}>
                <IconClock size={14} />
                <span className="office-home-section-title">近期会议与对话</span>
              </Group>
              <button type="button" className="office-home-link" onClick={() => navigate('/meetings')}>
                <span>会议室</span>
                <IconArrowRight size={12} />
              </button>
            </div>

            <div className="office-bento-list">
              {recentMeetings.map((m) => {
                const openAct = (m.action_items || []).filter((a) => a.status !== 'done').length
                return (
                  <button
                    key={`m-${m.id}`}
                    type="button"
                    className="office-bento-row"
                    onClick={() => navigate(`/meetings?id=${encodeURIComponent(m.id)}`)}
                  >
                    <IconMicrophone size={14} color="var(--oh-muted)" />
                    <div className="office-bento-row-main">
                      <span className="office-bento-row-title">{m.title}</span>
                      <span className="office-bento-row-sub">
                        会议 · {m.meeting_date}
                        {openAct > 0 ? ` · ${openAct} 项待办` : ''}
                      </span>
                    </div>
                  </button>
                )
              })}

              {recentSessions.map((s) => (
                <button
                  key={`s-${scopeKey(s.agent_id, s.id)}`}
                  type="button"
                  className="office-bento-row"
                  onClick={() =>
                    navigate(`/chat/${encodeURIComponent(s.id)}?agent_id=${encodeURIComponent(s.agent_id)}`)
                  }
                >
                  <IconFolder size={14} color="var(--oh-muted)" />
                  <div className="office-bento-row-main">
                    <span className="office-bento-row-title">{displaySessionTitle(s)}</span>
                    <span className="office-bento-row-sub">
                      {s.project_name || '办公对话'}
                      {s.updated_at ? ` · ${formatRelativeTime(s.updated_at)}` : ''}
                    </span>
                  </div>
                </button>
              ))}

              {recentMeetings.length === 0 && recentSessions.length === 0 && (
                <Text size="xs" c="dimmed" py="md" ta="center">
                  暂无近期会议或对话，可从上方模板一键开启
                </Text>
              )}
            </div>
          </div>
        </section>
      </div>

      {/* Personal Office Persona Quick Modal */}
      <Modal
        opened={personaModalOpen}
        onClose={() => setPersonaModalOpen(false)}
        title="个人办公画像与写作偏好"
        size="lg"
        withinPortal
      >
        <OfficePersonaCard compact onSaved={() => setPersonaModalOpen(false)} />
      </Modal>

      {/* 8-Scenario Office Template Drafting Modal */}
      <OfficeTemplateModal
        opened={templateModalOpen}
        initialTemplateId={templateModalId}
        workspace={activeWorkspace?.workspace || null}
        onClose={() => setTemplateModalOpen(false)}
        onSendToChat={(assembledPrompt, title) => startConversationWithPrompt(assembledPrompt, title)}
        onGeneratedDocument={(doc) => void handleGeneratedDocument(doc)}
      />
    </div>
  )
}
