import { useEffect, useState } from 'react'
import { Alert, Button, Checkbox, Group, Modal, Stack, Text } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'
import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import { lookupAgentEngine, type ManagedAgentKind } from '../plugins/builtinPlugins'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'

interface AgentDisableGuardModalProps {
  opened: boolean
  kind: ManagedAgentKind | null
  runningSessionCount: number
  onClose: () => void
  onConfirm: (disableRelatedPluginIds: string[]) => void
}

export function AgentDisableGuardModal({
  opened,
  kind,
  runningSessionCount,
  onClose,
  onConfirm,
}: AgentDisableGuardModalProps) {
  const isPluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled)
  const engine = lookupAgentEngine(kind)
  const activeRelated = (engine?.relatedPlugins || []).filter((p) => isPluginEnabled(p.id))
  const [selectedRelated, setSelectedRelated] = useState<Record<string, boolean>>({})

  useEffect(() => {
    if (!opened) return
    const next: Record<string, boolean> = {}
    for (const rel of activeRelated) next[rel.id] = false
    setSelectedRelated(next)
  }, [opened, kind])

  if (!kind) return null
  const label = agentKindLabel(kind)

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      centered
      size="md"
      radius="md"
      zIndex={430}
      title={
        <Group gap="xs">
          <AgentBrandIcon kind={kind} size={20} />
          <Text fw={600} size="sm">
            停用 {label} 插件
          </Text>
        </Group>
      }
    >
      <Stack gap="md">
        {runningSessionCount > 0 && (
          <Alert color="orange" variant="light" icon={<IconAlertTriangle size={16} />}>
            当前检测到 {runningSessionCount} 个正在运行的 {label} 会话。停用插件不会强杀小内核中正在执行的任务，但会隐藏新建入口与自动重连。
          </Alert>
        )}
        <Text size="xs" c="dimmed">
          停用后：
        </Text>
        <Stack gap={4} pl="xs">
          <Text size="xs">• {label} 将从 Agent 列表、新建会话选择、模型同步、能力中心与批量升级中隐藏；</Text>
          <Text size="xs">• 星序不再对该类型 Agent 发起后台自动连接；</Text>
          <Text size="xs">• 所有历史会话与本地数据完整保留，仍可只读查看，随时可在插件中心一键重新启用。</Text>
        </Stack>
        {activeRelated.length > 0 && (
          <Stack gap={6} pt={4}>
            <Text size="xs" fw={600} c="dimmed">关联插件联动（可选）</Text>
            {activeRelated.map((rel) => (
              <Checkbox
                key={rel.id}
                size="xs"
                checked={Boolean(selectedRelated[rel.id])}
                onChange={(e) => setSelectedRelated((prev) => ({ ...prev, [rel.id]: e.currentTarget.checked }))}
                label={`同时停用关联插件「${rel.label}」`}
              />
            ))}
          </Stack>
        )}
        <Group justify="flex-end" gap="xs">
          <Button size="xs" variant="default" onClick={onClose}>
            取消
          </Button>
          <Button
            size="xs"
            color="orange"
            onClick={() =>
              onConfirm(
                Object.entries(selectedRelated)
                  .filter(([, checked]) => checked)
                  .map(([id]) => id),
              )
            }
          >
            确认停用
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
