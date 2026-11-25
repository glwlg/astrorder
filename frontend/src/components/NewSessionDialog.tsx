import { useMemo, useState } from 'react'
import { Button, Group, Modal, Stack, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import type { Agent, GitWorktreesResponse, Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { useAstrorderStore } from '../state/store'
import type { ProjectGroup } from './sessionRailModel'
import { AgentBrandIcon, agentKindLabel } from './AgentBrandIcon'
import { BrandedSelect } from './BrandedSelect'
import { WORKFLOW_GIT_WORKTREE_PLUGIN_ID } from '../features/plugins/builtinPlugins'
import { usePluginSettingsStore } from '../features/plugins/pluginSettingsStore'
import {
  WorktreeConfigSection,
  buildWorktreePayload,
  createInitialWorktreeState,
  saveWorktreePrefs,
} from './WorktreeConfigSection'
import { useIsOfficeProfile } from '../features/office/appProfile'

export function agentEnvironment(agent: Agent): string {
  return agent.connection_id || 'local'
}
export function agentLabel(agent: Agent): string {
  return `${agentKindLabel(agent.kind)} · ${agent.name || agent.id} · ${agent.connection_id || '本机'}`
}
export function NewSessionDialog({
  agents,
  project,
  initialAgentId,
  initialWorkspace,
  initialWorktreeMode = 'none',
  initialWorktreePath,
  officeMode = false,
  onClose,
  onCreated,
}: {
  agents: Record<string, Agent>
  project: ProjectGroup | null
  initialAgentId?: string
  initialWorkspace?: string
  initialWorktreeMode?: 'none' | 'create' | 'existing'
  initialWorktreePath?: string
  officeMode?: boolean
  onClose: () => void
  onCreated: (session: Session) => void
}) {
  const isOfficeProfile = useIsOfficeProfile()
  const isOffice = officeMode || isOfficeProfile
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const isAgentEnabled = usePluginSettingsStore((s) => s.isAgentEnabled)
  const owner = project?.agentId ? agents[project.agentId] : null
  const choices = Object.values(agents).filter(agent =>
    agent.status === 'ready'
    && isAgentEnabled(agent.kind)
    && (!project || (owner && agentEnvironment(owner) === agentEnvironment(agent))),
  )
  void enabledPlugins
  const [agentId, setAgentId] = useState(() => {
    if (initialAgentId && choices.some(a => a.id === initialAgentId)) return initialAgentId
    const match = choices.find(a => a.kind === initialAgentId)
    if (match) return match.id
    return choices.length >= 1 ? choices[0].id : ''
  })
  const projectWorkspace = initialWorkspace || project?.workspace || project?.sessions?.find(s => s.workspace)?.workspace || ''
  const [workspace, setWorkspace] = useState(projectWorkspace)
  const [title, setTitle] = useState('')
  const [busy, setBusy] = useState(false)
  const worktreePluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKFLOW_GIT_WORKTREE_PLUGIN_ID)) && !isOffice
  const allSessionsMap = useAstrorderStore((s) => s.sessions)
  const allSessions = useMemo(() => Object.values(allSessionsMap), [allSessionsMap])
  const [wtState, setWtState] = useState(() =>
    createInitialWorktreeState(projectWorkspace, initialWorktreeMode, undefined, initialWorktreePath),
  )
  const [repoInfo, setRepoInfo] = useState<GitWorktreesResponse | null>(null)
  const selectedAgent = agents[agentId]
  const connectionId = selectedAgent?.connection_id || owner?.connection_id || undefined

  const wtInvalid = worktreePluginEnabled && (
    (wtState.mode === 'create' && !wtState.branch.trim())
    || (wtState.mode === 'existing' && !wtState.existingPath.trim())
  )

  const create = async () => {
    if (busy || !choices.some(a => a.id === agentId) || wtInvalid) return
    setBusy(true)
    try {
      const wtPayload = worktreePluginEnabled ? buildWorktreePayload(wtState) : undefined
      const session = await api.createSession({
        agent_id: agentId,
        workspace: workspace.trim() || null,
        title: title.trim() || null,
        project_name: project?.label,
        ...(wtPayload ? { worktree: wtPayload } : {}),
      })
      if (wtPayload?.mode === 'create') {
        saveWorktreePrefs(repoInfo?.main_workspace || workspace.trim(), wtState)
        const copiedNote = wtState.copyFiles.length > 0 ? `（已同步 ${wtState.copyFiles.length} 个本地配置）` : ''
        notifications.show({
          color: 'teal',
          message: `已在 Worktree 分支 ${session.worktree_branch || wtState.branch.trim()} 中创建会话${copiedNote}`,
        })
      }
      useAstrorderStore.setState(state => ({ sessions: { ...state.sessions, [scopeKey(session.agent_id, session.id)]: session } }))
      onCreated(session)
      onClose()
    } catch (error) { notifications.show({ color: 'red', message: error instanceof Error ? error.message : '新建会话未确认' }) }
    finally { setBusy(false) }
  }
  return <Modal opened onClose={() => { if (!busy) onClose() }} title={isOffice ? '新建对话' : '新建会话'} centered size="md" zIndex={400}>
    <Stack>
      {project && <Text size="sm">{isOffice ? '工作空间' : '项目'}：{project.label}</Text>}
      {(!isOffice || choices.length > 1) && (
        <BrandedSelect
          label={isOffice ? 'AI 助手' : '选择 Agent'}
          placeholder={isOffice ? '请选择 AI 助手' : '请选择 Agent'}
          value={agentId}
          onChange={setAgentId}
          disabled={busy}
          options={choices.map(agent => ({
            value: agent.id,
            label: agentLabel(agent),
            icon: <AgentBrandIcon kind={agent.kind} size={15} />,
          }))}
        />
      )}
      {!choices.length && <Text size="sm" c="dimmed">{isOffice ? '当前暂无可用 AI 助手，请先在设置中检查连接状态。' : '该环境暂无已接入 Agent，请先在连接管理中接入。'}</Text>}
      <TextInput label={isOffice ? '对话标题（选填，留空自动命名）' : '会话名称（选填，留空由 Agent 自动命名）'} placeholder={isOffice ? '留空自动命名' : '留空由 Agent 自动命名'} value={title} onChange={event => setTitle(event.currentTarget.value)} disabled={busy} />
      {(!isOffice || !projectWorkspace) && (
        <TextInput label={isOffice ? '工作文件夹' : '工作区'} value={workspace} onChange={event => setWorkspace(event.currentTarget.value)} placeholder={isOffice ? '选择本机工作目录路径' : '使用所选 Agent 环境的路径'} disabled={busy || !!projectWorkspace} />
      )}
      {worktreePluginEnabled && workspace.trim() && (
        <WorktreeConfigSection
          workspace={workspace}
          connectionId={connectionId}
          value={wtState}
          onChange={setWtState}
          suggestedTitle={title}
          disabled={busy}
          sessions={allSessions}
          onRepoLoaded={setRepoInfo}
        />
      )}
      <Group justify="flex-end"><Button variant="default" onClick={onClose} disabled={busy}>取消</Button><Button onClick={() => void create()} loading={busy} disabled={!agentId || wtInvalid}>{wtState.mode === 'create' ? '创建 Worktree 并新建会话' : isOffice ? '开始对话' : '创建会话'}</Button></Group>
    </Stack>
  </Modal>
}
