import { useState } from 'react'
import { Button, Group, Modal, NativeSelect, Stack, Text, TextInput } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import type { Agent, Session } from '../domain/types'
import { scopeKey } from '../domain/semantics'
import { useAstrorderStore } from '../state/store'
import type { ProjectGroup } from './sessionRailModel'

export function agentEnvironment(agent: Agent): string {
  if (agent.connection_id) return agent.connection_id
  const source = agent.source_id || agent.id
  if (source === 'local-codex' || source.startsWith('hermes-local-') || source.startsWith('local-hermes-')) return 'local'
  return source
}
export function agentLabel(agent: Agent): string {
  return `${agent.kind === 'codex' ? 'Codex' : 'Hermes'} · ${agent.name || agent.id} · ${agent.connection_id || '本机'}`
}
export function NewSessionDialog({ agents, project, onClose, onCreated }: { agents: Record<string, Agent>; project: ProjectGroup | null; onClose: () => void; onCreated: (session: Session) => void }) {
  const [agentId, setAgentId] = useState('')
  const [workspace, setWorkspace] = useState(project?.workspace || '')
  const [title, setTitle] = useState('新会话')
  const [busy, setBusy] = useState(false)
  const owner = project?.agentId ? agents[project.agentId] : null
  const choices = Object.values(agents).filter(agent => agent.status === 'ready' && (!project || (owner && agentEnvironment(owner) === agentEnvironment(agent))))
  const create = async () => {
    if (busy || !choices.some(a => a.id === agentId)) return
    setBusy(true)
    try {
      const session = await api.createSession({ agent_id: agentId, workspace: workspace.trim() || null, title: title.trim() || '新会话', project_name: project?.label })
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
      <TextInput label="会话名称" value={title} onChange={event => setTitle(event.currentTarget.value)} disabled={busy} />
      <TextInput label="工作区" value={workspace} onChange={event => setWorkspace(event.currentTarget.value)} placeholder="使用所选 Agent 环境的路径" disabled={busy || !!project?.workspace} />
      <Group justify="flex-end"><Button variant="default" onClick={onClose} disabled={busy}>取消</Button><Button onClick={() => void create()} loading={busy} disabled={!agentId}>创建会话</Button></Group>
    </Stack>
  </Modal>
}
