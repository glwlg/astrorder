import { useEffect, useState } from 'react'
import { Button, Group, Modal, NativeSelect, Stack, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import type { Agent, Session } from '../domain/types'
import { REASONING_EFFORTS } from '../features/chat/composerMedia'
import { agentLabel } from './NewSessionDialog'
import { useBackgroundTasks } from '../state/backgroundTasks'

type ModelChoice = { provider: string; model: string; label: string }

export function HandoffDialog({
  source,
  targets,
  sessions,
  onClose,
  onTransferred,
  onOpenTransferred,
}: {
  source: Session
  targets: Agent[]
  sessions: Session[]
  onClose: () => void
  onTransferred: (session: Session, target: Agent) => void
  onOpenTransferred: (session: Session) => void
}) {
  const initialAgentId = targets.length === 1 ? targets[0].id : ''
  const [agentId, setAgentId] = useState(initialAgentId)
  const [models, setModels] = useState<ModelChoice[]>([])
  const [modelKey, setModelKey] = useState('')
  const [effort, setEffort] = useState('')
  const [loadingModels, setLoadingModels] = useState(Boolean(initialAgentId))
  const [modelError, setModelError] = useState('')
  const probe = sessions.find((session) => session.agent_id === agentId)

  useEffect(() => {
    if (!agentId || !probe) return
    let active = true
    void api.getSessionModels(probe.id, agentId)
      .then(({ items }) => {
        if (active) setModels(items)
      })
      .catch((error) => {
        if (active) setModelError(error instanceof Error ? error.message : '读取可用模型失败')
      })
      .finally(() => {
        if (active) setLoadingModels(false)
      })
    return () => { active = false }
  }, [agentId, probe])

  const changeAgent = (value: string) => {
    if (value === agentId) return
    setAgentId(value)
    setModels([])
    setModelKey('')
    setModelError('')
    setLoadingModels(Boolean(value && sessions.some((session) => session.agent_id === value)))
  }

  const submit = () => {
    const target = targets.find((agent) => agent.id === agentId)
    const model = models.find((item) => JSON.stringify([item.provider, item.model]) === modelKey)
    if (!target || !model || !effort) return
    onClose()
    const operationId = crypto.randomUUID()
    const taskId = useBackgroundTasks.getState().add({
      title: '转交会话',
      detail: `${source.title} → ${agentLabel(target)} · ${model.label}`,
      cancel: () => api.cancelHandoff(operationId),
    })
    void api.handoffSession(source.id, source.agent_id, target.id, operationId, {
        provider: model.provider,
        model: model.model,
        effort,
      })
      .then((created) => {
        onTransferred(created, target)
        useBackgroundTasks.getState().complete(taskId, `已转交给 ${agentLabel(target)}`, {
          actionLabel: '打开',
          action: () => onOpenTransferred(created),
        })
      })
      .catch((error) => {
        if (useBackgroundTasks.getState().tasks[taskId]?.status === 'cancelled') return
        const message = error instanceof Error ? error.message : '会话转交失败，请重试'
        useBackgroundTasks.getState().fail(taskId, message)
        notifications.show({ color: 'red', message })
      })
  }

  return <Modal opened onClose={onClose} title="转交会话" centered size="sm">
    <Stack>
      <NativeSelect
        label="目标 Agent"
        value={agentId}
        onChange={(event) => changeAgent(event.currentTarget.value)}
        data={[{ value: '', label: '请选择 Agent' }, ...targets.map((agent) => ({ value: agent.id, label: agentLabel(agent) }))]}
      />
      <NativeSelect
        label="模型"
        value={modelKey}
        onChange={(event) => setModelKey(event.currentTarget.value)}
        data={[{ value: '', label: loadingModels ? '正在读取模型…' : '请选择模型' }, ...models.map((item) => ({ value: JSON.stringify([item.provider, item.model]), label: item.label }))]}
        disabled={!agentId || loadingModels}
      />
      {(modelError || (agentId && !probe)) && <Text size="sm" c="red">{modelError || '目标 Agent 尚无会话，无法读取可用模型。'}</Text>}
      <NativeSelect
        label="思考程度"
        value={effort}
        onChange={(event) => setEffort(event.currentTarget.value)}
        data={[{ value: '', label: '请选择思考程度' }, ...REASONING_EFFORTS]}
      />
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>取消</Button>
        <Button onClick={submit} disabled={!agentId || !modelKey || !effort}>转交</Button>
      </Group>
    </Stack>
  </Modal>
}
