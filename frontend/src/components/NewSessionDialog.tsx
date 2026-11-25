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
  onClose,
  onCreated,
}: {
  agents: Record<string, Agent>
  project: ProjectGroup | null
  initialAgentId?: string
  initialWorkspace?: string
  initialWorktreeMode?: 'none' | 'create' | 'existing'
  initialWorktreePath?: string
  onClose: () => void
  onCreated: (session: Session) => void
}) {
  const owner = project?.agentId ? agents[project.agentId] : null
  const choices = Object.values(agents).filter(agent => agent.status === 'ready' && (!project || (owner && agentEnvironment(owner) === agentEnvironment(agent))))
  const [agentId, setAgentId] = useState(() => {
    if (initialAgentId && choices.some(a => a.id === initialAgentId)) return initialAgentId
    const match = choices.find(a => a.kind === initialAgentId)
    if (match) return match.id
    return choices.length === 1 ? choices[0].id : ''
  })
  const projectWorkspace = initialWorkspace || project?.workspace || project?.sessions?.find(s => s.workspace)?.workspace || ''
  const [workspace, setWorkspace] = useState(projectWorkspace)
  const [title, setTitle] = useState('')
  const [busy, setBusy] = useState(false)
  const worktreePluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(WORKFLOW_GIT_WORKTREE_PLUGIN_ID))
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
  return <Modal opened onClose={() => { if (!busy) onClose() }} title="新建会话" centered size="md" zIndex={400}>
    <Stack>
      {project && <Text size="sm">项目：{project.label}</Text>}
      <BrandedSelect
        label="选择 Agent"
        placeholder="请选择 Agent"
        value={agentId}
        onChange={setAgentId}
        disabled={busy}
        options={choices.map(agent => ({
          value: agent.id,
          label: agentLabel(agent),
          icon: <AgentBrandIcon kind={agent.kind} size={15} />,
        }))}
      />
      {!choices.length && <Text size="sm" c="dimmed">该环境暂无已接入 Agent，请先在连接管理中接入。</Text>}
      <TextInput label="会话名称（选填，留空由 Agent 自动命名）" placeholder="留空由 Agent 自动命名" value={title} onChange={event => setTitle(event.currentTarget.value)} disabled={busy} />
      <TextInput label="工作区" value={workspace} onChange={event => setWorkspace(event.currentTarget.value)} placeholder="使用所选 Agent 环境的路径" disabled={busy || !!projectWorkspace} />
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
      <Group justify="flex-end"><Button variant="default" onClick={onClose} disabled={busy}>取消</Button><Button onClick={() => void create()} loading={busy} disabled={!agentId || wtInvalid}>{wtState.mode === 'create' ? '创建 Worktree 并新建会话' : '创建会话'}</Button></Group>
    </Stack>
  </Modal>
}
