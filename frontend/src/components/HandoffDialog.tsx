import { useEffect, useMemo, useState } from 'react'
import { Button, Group, Modal, Stack, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconBolt, IconCpu } from '@tabler/icons-react'
import { api } from '../api/client'
import type { Agent, Session } from '../domain/types'
import { REASONING_EFFORTS } from '../features/chat/composerMedia'
import {
  groupModelChoices,
  parseAntigravityModelId,
  resolveGroupVariant,
  type ModelChoice,
} from '../features/chat/antigravityModels'
import { AgentBrandIcon } from './AgentBrandIcon'
import { BrandedSelect } from './BrandedSelect'
import { agentLabel } from './NewSessionDialog'
import { useBackgroundTasks } from '../state/backgroundTasks'

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
  const selectedTarget = targets.find((agent) => agent.id === agentId)
  const isAntigravityTarget = selectedTarget?.kind === 'antigravity' || selectedTarget?.kind === 'antigravity-ssh'
  const canProbeWithoutSession = isAntigravityTarget || selectedTarget?.kind === 'codex'
  const probe = sessions.find((session) => session.agent_id === agentId)
  const modelSessionId = probe?.id ?? (canProbeWithoutSession ? '__probe__' : '')
  const initialHasModelSource = Boolean(
    initialAgentId &&
      (sessions.some((session) => session.agent_id === initialAgentId) ||
        targets[0]?.kind === 'antigravity' ||
        targets[0]?.kind === 'antigravity-ssh' ||
        targets[0]?.kind === 'codex'),
  )
  const [loadingModels, setLoadingModels] = useState(initialHasModelSource)
  const [modelError, setModelError] = useState('')

  useEffect(() => {
    if (!agentId || !modelSessionId) return
    let active = true
    void api.getSessionModels(modelSessionId, agentId)
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
  }, [agentId, modelSessionId])

  const groupedModels = useMemo(
    () => groupModelChoices(models, { treatAllAsAntigravity: isAntigravityTarget }),
    [isAntigravityTarget, models],
  )
  const selectedGroup = groupedModels.find((item) => item.key === modelKey) ?? null
  const agyNeedsEffort = isAntigravityTarget && Boolean(selectedGroup?.isGrouped && selectedGroup.efforts.length > 1)
  const showEffortSelect = !isAntigravityTarget || agyNeedsEffort
  const effortOptions = agyNeedsEffort
    ? selectedGroup!.efforts.map((item) => ({ value: item.effort, label: item.label }))
    : REASONING_EFFORTS

  const changeAgent = (value: string) => {
    if (value === agentId) return
    const nextTarget = targets.find((agent) => agent.id === value)
    const nextCanProbe = nextTarget?.kind === 'antigravity' || nextTarget?.kind === 'antigravity-ssh' || nextTarget?.kind === 'codex'
    setAgentId(value)
    setModels([])
    setModelKey('')
    setEffort('')
    setModelError('')
    setLoadingModels(Boolean(value && (sessions.some((session) => session.agent_id === value) || nextCanProbe)))
  }

  const changeModel = (nextKey: string) => {
    setModelKey(nextKey)
    if (!isAntigravityTarget) return
    const nextGroup = groupedModels.find((item) => item.key === nextKey)
    if (nextGroup?.isGrouped && nextGroup.efforts.length > 1) {
      const resolved = resolveGroupVariant(nextGroup, effort)
      setEffort(parseAntigravityModelId(resolved.model).effort || nextGroup.efforts[0].effort)
    } else {
      setEffort('')
    }
  }

  const submit = () => {
    const target = targets.find((agent) => agent.id === agentId)
    if (!target || !selectedGroup || (showEffortSelect && !effort)) return
    const resolvedModel = resolveGroupVariant(selectedGroup, effort)
    onClose()
    const operationId = crypto.randomUUID()
    const taskId = useBackgroundTasks.getState().add({
      title: '转交会话',
      detail: `${source.title} → ${agentLabel(target)} · ${resolvedModel.label}`,
      cancel: () => api.cancelHandoff(operationId),
    })
    void api.handoffSession(source.id, source.agent_id, target.id, operationId, {
        provider: resolvedModel.provider,
        model: resolvedModel.model,
        effort: isAntigravityTarget ? '' : effort,
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
      <BrandedSelect
        label="目标 Agent"
        placeholder="请选择 Agent"
        value={agentId}
        onChange={changeAgent}
        options={targets.map((agent) => ({
          value: agent.id,
          label: agentLabel(agent),
          icon: <AgentBrandIcon kind={agent.kind} size={15} />,
        }))}
      />
      <BrandedSelect
        label="模型"
        placeholder={loadingModels ? '正在读取模型…' : '请选择模型'}
        value={modelKey}
        onChange={changeModel}
        disabled={!agentId || loadingModels}
        options={groupedModels.map((item) => ({
          value: item.key,
          label: item.label,
          description: item.provider,
          icon: <IconCpu size={14} color="var(--astr-indigo)" />,
        }))}
      />
      {(modelError || (agentId && !modelSessionId)) && <Text size="sm" c="red">{modelError || '目标 Agent 尚无会话，无法读取可用模型。'}</Text>}
      {showEffortSelect && (
        <BrandedSelect
          label="思考程度"
          placeholder="请选择思考程度"
          value={effort}
          onChange={setEffort}
          options={effortOptions.map((item) => ({
            value: item.value,
            label: item.label,
            icon: <IconBolt size={14} color="var(--astr-indigo)" />,
          }))}
        />
      )}
      <Group justify="flex-end">
        <Button variant="default" onClick={onClose}>取消</Button>
        <Button onClick={submit} disabled={!agentId || !modelKey || (showEffortSelect && !effort)}>转交</Button>
      </Group>
    </Stack>
  </Modal>
}
