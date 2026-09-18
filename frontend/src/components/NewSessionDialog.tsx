import { useState } from 'react'
import { Button, Group, Modal, NativeSelect, Stack, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import type { Agent, Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { useAstrorderStore } from '../state/store'
import type { ProjectGroup } from './sessionRailModel'
import { agentKindLabel } from './AgentBrandIcon'

export function agentEnvironment(agent: Agent): string {
  return agent.connection_id || 'local'
}
export function agentLabel(agent: Agent): string {
  return `${agentKindLabel(agent.kind)} · ${agent.name || agent.id} · ${agent.connection_id || '本机'}`
}
export function NewSessionDialog({ agents, project, initialAgentId, onClose, onCreated }: { agents: Record<string, Agent>; project: ProjectGroup | null; initialAgentId?: string; onClose: () => void; onCreated: (session: Session) => void }) {
  const owner = project?.agentId ? agents[project.agentId] : null
  const choices = Object.values(agents).filter(agent => agent.status === 'ready' && (!project || (owner && agentEnvironment(owner) === agentEnvironment(agent))))
  const [agentId, setAgentId] = useState(() => {
    if (initialAgentId && choices.some(a => a.id === initialAgentId)) return initialAgentId
    const match = choices.find(a => a.kind === initialAgentId)
    if (match) return match.id
    return choices.length === 1 ? choices[0].id : ''
  })
    const projectWorkspace = project?.workspace || project?.sessions?.find(s => s.workspace)?.workspace || ''
  const [workspace, setWorkspace] = useState(projectWorkspace)
  const [title, setTitle] = useState('')
  const [busy, setBusy] = useState(false)
  const create = async () => {
    if (busy || !choices.some(a => a.id === agentId)) return
    setBusy(true)
    try {
      const session = await api.createSession({ agent_id: agentId, workspace: workspace.trim() || null, title: title.trim() || null, project_name: project?.label })
      useAstrorderStore.setState(state => ({ sessions: { ...state.sessions, [scopeKey(session.agent_id, session.id)]: session } }))
      onCreated(session)
      onClose()
    } catch (error) { notifications.show({ color: 'red', message: error instanceof Error ? error.message : '新建会话未确认' }) }
    finally { setBusy(false) }
  }
  return <Modal opened onClose={() => { if (!busy) onClose() }} title="新建会话" centered size="md" zIndex={400}>
    <Stack>
      {project && <Text size="sm">项目：{project.label}</Text>}
      <NativeSelect label="选择 Agent" value={agentId} onChange={event => setAgentId(event.currentTarget.value)} data={[{ value: '', label: '请选择 Agent' }, ...choices.map(agent => ({ value: agent.id, label: agentLabel(agent) }))]} disabled={busy} />
      {!choices.length && <Text size="sm" c="dimmed">该环境暂无已接入 Agent，请先在连接管理中接入。</Text>}
      <TextInput label="会话名称（选填，留空由 Agent 自动命名）" placeholder="留空由 Agent 自动命名" value={title} onChange={event => setTitle(event.currentTarget.value)} disabled={busy} />
      <TextInput label="工作区" value={workspace} onChange={event => setWorkspace(event.currentTarget.value)} placeholder="使用所选 Agent 环境的路径" disabled={busy || !!projectWorkspace} />
      <Group justify="flex-end"><Button variant="default" onClick={onClose} disabled={busy}>取消</Button><Button onClick={() => void create()} loading={busy} disabled={!agentId}>创建会话</Button></Group>
    </Stack>
  </Modal>
}
