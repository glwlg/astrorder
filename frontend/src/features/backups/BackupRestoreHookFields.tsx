import type { Ref } from 'react'
import { Group, NativeSelect, Radio, Stack, Text, Textarea } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { api, type BackupRestoreAgentTarget } from '../../api/client'
import { agentKindLabel } from '../../components/AgentBrandIcon'

export type RestoreHookMode = 'none' | 'command' | 'restart_agent'

function agentKey(agent: BackupRestoreAgentTarget) {
  return JSON.stringify([agent.environment_id, agent.kind])
}

export function BackupRestoreHookFields({ opened, mode, onModeChange, command, onCommandChange, agent, onAgentChange, agentInput, agentError }: {
  opened: boolean
  mode: RestoreHookMode
  onModeChange: (mode: RestoreHookMode) => void
  command: string
  onCommandChange: (command: string) => void
  agent: BackupRestoreAgentTarget | null
  onAgentChange: (agent: BackupRestoreAgentTarget | null) => void
  agentInput: Ref<HTMLSelectElement>
  agentError: string | null
}) {
  const environments = useQuery({
    queryKey: ['astrorder', 'environments'],
    queryFn: api.getEnvironments,
    enabled: opened && mode === 'restart_agent',
    retry: false,
  })
  const choices = (environments.data?.items ?? []).flatMap((environment) =>
    environment.agents
      .filter((item) => item.available || item.state === 'connected')
      .map((item) => {
        const target = { environment_id: environment.id, kind: item.kind }
        return { value: agentKey(target), label: `${environment.name} · ${agentKindLabel(item.kind)}`, target }
      }),
  )
  const value = agent ? agentKey(agent) : ''
  if (agent && !choices.some((choice) => choice.value === value)) {
    const environment = environments.data?.items.find((item) => item.id === agent.environment_id)
    const name = environment?.name ?? (agent.environment_id === 'local' ? '本机' : agent.environment_id)
    choices.push({ value, label: `${name} · ${agentKindLabel(agent.kind)}`, target: agent })
  }

  return (
    <Stack gap={6}>
      <Radio.Group
        label="还原后操作"
        name="post_restore_mode"
        value={mode}
        onChange={(value) => onModeChange(value as RestoreHookMode)}
      >
        <Group gap="sm" mt={4}>
          <Radio value="none" label="不执行" />
          <Radio value="restart_agent" label="重启 Agent" />
          <Radio value="command" label="自定义命令" />
        </Group>
      </Radio.Group>
      {mode === 'restart_agent' && (
        <>
          <NativeSelect
            ref={agentInput}
            name="post_restore_restart_agent"
            label="要重启的 Agent"
            description="文件全部还原成功后执行。重启期间，该 Agent 的会话可能短暂中断。"
            value={value}
            data={[{ value: '', label: '请选择环境和 Agent' }, ...choices]}
            onChange={(event) => onAgentChange(choices.find((choice) => choice.value === event.currentTarget.value)?.target ?? null)}
            required
            error={agentError}
          />
          {environments.isPending && <Text size="xs" role="status">正在读取 Agent 列表…</Text>}
          {environments.isError && <Text size="xs" c="red" role="alert">无法读取 Agent 列表，请检查环境连接。</Text>}
          {environments.isSuccess && choices.length === 0 && <Text size="xs" c="dimmed">没有可重启的 Agent。</Text>}
        </>
      )}
      {mode === 'command' && (
        <Textarea
          label="还原后命令（可选）"
          name="post_restore_command"
          description="文件全部还原成功后，在目标服务器的还原目录或文件所在目录执行。Windows 本机使用 PowerShell，其他目标使用 /bin/sh。"
          value={command}
          onChange={(event) => onCommandChange(event.currentTarget.value)}
          autosize
          minRows={2}
          maxRows={6}
        />
      )}
    </Stack>
  )
}
