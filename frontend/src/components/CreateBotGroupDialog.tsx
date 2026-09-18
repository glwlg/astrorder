import { useState } from 'react'
import {
  Badge,
  Button,
  Checkbox,
  Group,
  Modal,
  NumberInput,
  Paper,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { api } from '../api/client'
import type { Agent, BotGroup, BotGroupMember } from '../domain/types'
import { AgentBrandIcon } from './AgentBrandIcon'

function machineLabel(agent: Agent): string {
  if (agent.connection_id) {
    return agent.connection_id.startsWith('ssh-') ? `SSH ${agent.connection_id.slice(4)}` : agent.connection_id
  }
  return '本机'
}

export function CreateBotGroupDialog({
  agents,
  opened,
  onClose,
  onCreated,
}: {
  agents: Record<string, Agent>
  opened: boolean
  onClose: () => void
  onCreated: (group: BotGroup) => void
}) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [maxHops, setMaxHops] = useState<number>(3)
  const [selectedAgentIds, setSelectedAgentIds] = useState<Set<string>>(new Set())
  const [memberRoles, setMemberRoles] = useState<Record<string, { alias: string; role: string }>>({})
  const [busy, setBusy] = useState(false)

  const activeAgents = Object.values(agents).filter(a => a.status === 'ready')

  const toggleSelect = (agentId: string) => {
    setSelectedAgentIds(prev => {
      const next = new Set(prev)
      if (next.has(agentId)) {
        next.delete(agentId)
      } else {
        next.add(agentId)
      }
      return next
    })
  }

  const updateRole = (agentId: string, field: 'alias' | 'role', val: string) => {
    setMemberRoles(prev => ({
      ...prev,
      [agentId]: {
        alias: field === 'alias' ? val : prev[agentId]?.alias || '',
        role: field === 'role' ? val : prev[agentId]?.role || '',
      },
    }))
  }

  const submit = async () => {
    if (busy || !name.trim() || selectedAgentIds.size === 0) return
    setBusy(true)
    try {
      const members: BotGroupMember[] = Array.from(selectedAgentIds).map(aid => {
        const ag = agents[aid]
        return {
          machine_id: ag?.connection_id || 'local',
          agent_id: aid,
          name: ag?.name || ag?.kind || aid,
          alias: memberRoles[aid]?.alias.trim() || undefined,
          system_role_prompt: memberRoles[aid]?.role.trim() || undefined,
        }
      })
      const res = await api.createBotGroup({
        name: name.trim(),
        description: description.trim() || undefined,
        members,
        max_hops: Number(maxHops) || 3,
      })
      notifications.show({ color: 'teal', message: `群聊「${res.group.name}」已创建` })
      onCreated(res.group)
      onClose()
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '创建群聊失败',
      })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      opened={opened}
      onClose={() => { if (!busy) onClose() }}
      title="新建多 Agent 群聊 (Bots Group)"
      size="lg"
      centered
      zIndex={400}
    >
      <Stack gap="md">
        <TextInput
          label="群聊名称"
          placeholder="如：全栈跨机发布组、代码审查突击队"
          value={name}
          onChange={(e) => setName(e.currentTarget.value)}
          required
          disabled={busy}
        />
        <TextInput
          label="职责说明（可选）"
          placeholder="简述该群的协作目标"
          value={description}
          onChange={(e) => setDescription(e.currentTarget.value)}
          disabled={busy}
        />
        <NumberInput
          label="单次提问最大自主接力跳数 (Max Hops)"
          description="防死循环硬限制：单次人类提问后，各 Agent 间自动接力移交的最大轮数（1~10）"
          min={1}
          max={10}
          value={maxHops}
          onChange={(val) => setMaxHops(typeof val === 'number' ? val : 3)}
          disabled={busy}
        />

        <div>
          <Text size="sm" fw={600} mb={6}>
            选择群成员 Agent ({selectedAgentIds.size}/{activeAgents.length})
          </Text>
          <Stack gap={8} style={{ maxHeight: 280, overflowY: 'auto' }}>
            {activeAgents.map(ag => {
              const checked = selectedAgentIds.has(ag.id)
              const roleInfo = memberRoles[ag.id] || { alias: '', role: '' }
              return (
                <Paper
                  key={ag.id}
                  withBorder
                  p="xs"
                  radius="md"
                  style={{
                    borderColor: checked ? 'var(--mantine-color-indigo-6)' : undefined,
                    background: checked ? 'color-mix(in srgb, var(--mantine-color-indigo-6) 6%, transparent)' : undefined,
                  }}
                >
                  <Group justify="space-between" align="center" wrap="nowrap">
                    <Group gap="xs" wrap="nowrap">
                      <Checkbox
                        checked={checked}
                        onChange={() => toggleSelect(ag.id)}
                        disabled={busy}
                      />
                      <AgentBrandIcon kind={ag.kind} size={18} />
                      <div>
                        <Group gap={6} align="center">
                          <Text size="sm" fw={500}>{ag.name || ag.id}</Text>
                          <Badge size="xs" variant="outline" color="gray">
                            {machineLabel(ag)}
                          </Badge>
                        </Group>
                      </div>
                    </Group>
                  </Group>
                  {checked && (
                    <Group grow gap="xs" mt={8}>
                      <TextInput
                        size="xs"
                        placeholder="群内别名（如：架构师）"
                        value={roleInfo.alias}
                        onChange={(e) => updateRole(ag.id, 'alias', e.currentTarget.value)}
                        disabled={busy}
                      />
                      <TextInput
                        size="xs"
                        placeholder="定制角色 Prompt（可选）"
                        value={roleInfo.role}
                        onChange={(e) => updateRole(ag.id, 'role', e.currentTarget.value)}
                        disabled={busy}
                      />
                    </Group>
                  )}
                </Paper>
              )
            })}
          </Stack>
        </div>

        <Group justify="flex-end" mt="sm">
          <Button variant="default" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button
            onClick={() => void submit()}
            loading={busy}
            disabled={!name.trim() || selectedAgentIds.size === 0}
          >
            创建群聊
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
