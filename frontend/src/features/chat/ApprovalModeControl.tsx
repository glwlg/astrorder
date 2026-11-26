import { useState } from 'react'
import { Group, Popover, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { IconAlertTriangle, IconCheck, IconClockCheck, IconHandStop } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { ApprovalMode, Session } from '../../domain/types'
import { useIsOfficeExperience } from '../office/appProfile'

export interface ApprovalModeItem {
  key: ApprovalMode
  label: string
  capsuleLabel: string
  description: string
  icon: typeof IconClockCheck
  color: string
}

export const APPROVAL_MODES: ApprovalModeItem[] = [
  {
    key: 'manual',
    label: '请求批准',
    capsuleLabel: '请求批准',
    description: '编辑外部文件和使用互联网时始终询问',
    icon: IconHandStop,
    color: 'var(--astr-blue, #228be6)',
  },
  {
    key: 'auto',
    label: '帮我批准',
    capsuleLabel: '帮我批准',
    description: '仅对检测到的风险操作请求批准',
    icon: IconClockCheck,
    color: 'var(--astr-teal, #12b886)',
  },
  {
    key: 'full_access',
    label: '完全访问权限',
    capsuleLabel: '完全访问',
    description: '可不受限制地访问互联网和你电脑上的任何文件',
    icon: IconAlertTriangle,
    color: '#e8590c',
  },
]

export function ApprovalModeControl({
  session,
  compact = false,
  iconOnly = false,
  variant = 'capsule',
  disabled = false,
  supported = true,
  enabled = true,
}: {
  session: Session
  agentKind?: string
  compact?: boolean
  iconOnly?: boolean
  variant?: 'capsule' | 'panel'
  disabled?: boolean
  supported?: boolean
  enabled?: boolean
}) {
  const modes = APPROVAL_MODES
  const isOfficeMode = useIsOfficeExperience(session)
  const queryClient = useQueryClient()
  const [opened, setOpened] = useState(false)
  const { data, isError } = useQuery({
    queryKey: ['astrorder', 'approval-mode', session.agent_id, session.id],
    queryFn: async () => {
      const res = await api.getSessionApprovalMode(session.id, session.agent_id)
      if (!modes.some(item => item.key === res.mode)) throw new Error('原生审批模式未确认')
      return res.mode
    },
    enabled: supported && (enabled || opened),
    retry: false,
    staleTime: 60000,
    refetchOnWindowFocus: false,
  })

  const currentMode = data
  const activeItem = modes.find((item) => item.key === currentMode) || modes[0]
  const isFull = currentMode === 'full_access'

  const mutation = useMutation({
    mutationFn: async (mode: ApprovalMode) => {
      const res = await api.setSessionApprovalMode(session.id, session.agent_id, mode)
      if (res.mode !== mode) throw new Error('原生审批模式未确认')
      return res
    },
    onSuccess: (res) => {
      queryClient.setQueryData(['astrorder', 'approval-mode', session.agent_id, session.id], res.mode)
      setOpened(false)
      notifications.show({
        message: `已切换为“${modes.find((item) => item.key === res.mode)?.label}”模式`,
        color: res.mode === 'full_access' ? 'orange' : 'teal',
      })
    },
    onError: (error) => {
      notifications.show({
        message: error instanceof Error ? error.message : '切换审批模式失败',
        color: 'red',
      })
    },
  })

  if (!supported) return null
  if (!currentMode || isError) {
    const label = isError ? '审批模式读取失败' : '正在读取审批模式'
    if (iconOnly) {
      const FallbackIcon = isError ? IconAlertTriangle : IconClockCheck
      return (
        <Tooltip label={label} position="top" withArrow>
          <button
            type="button"
            className="approval-mode-capsule is-icon-only"
            disabled
            aria-label={label}
          >
            <FallbackIcon size={18} className="approval-mode-capsule-icon" aria-hidden="true" />
          </button>
        </Tooltip>
      )
    }
    return <button type="button" className="approval-mode-capsule" disabled aria-label={label}>{label}</button>
  }

  const ActiveIcon = activeItem.icon

  const options = (
    <Stack gap="xs">
      <div className="approval-mode-header">
        <Text size="sm" fw={650}>
          应如何批准操作？
        </Text>
        <Text size="xs" c="dimmed" mt={2}>
          {isOfficeMode ? '控制 AI 助手修改文件或联网查询时的确认方式' : '控制执行系统命令与编辑文件时的权限等级'}
        </Text>
      </div>

      <Stack gap={4} className="approval-mode-options">
        {modes.map((item) => {
          const isSelected = item.key === currentMode
          const ItemIcon = item.icon
          const itemIsFull = item.key === 'full_access'

          return (
            <UnstyledButton
              key={item.key}
              className={`approval-mode-option ${isSelected ? 'is-selected' : ''} ${itemIsFull ? 'is-full-option' : ''}`}
              onClick={() => { if (!disabled && !mutation.isPending) mutation.mutate(item.key) }}
              disabled={disabled || mutation.isPending}
              role="button"
              aria-pressed={isSelected}
            >
              <Group wrap="nowrap" align="flex-start" gap="sm" style={{ width: '100%' }}>
                <div
                  className="approval-mode-option-icon"
                  style={{ color: itemIsFull ? '#e8590c' : item.color }}
                >
                  <ItemIcon size={18} />
                </div>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <Text
                    size="xs"
                    fw={600}
                    style={{
                      color: isSelected && itemIsFull ? '#e8590c' : undefined,
                    }}
                  >
                    {item.label}
                  </Text>
                  <Text size="xs" c="dimmed" mt={2} style={{ lineHeight: 1.35 }}>
                    {item.description}
                  </Text>
                </div>
                {isSelected && (
                  <IconCheck
                    size={16}
                    className="approval-mode-option-check"
                    style={{ color: itemIsFull ? '#e8590c' : 'var(--astr-teal)' }}
                  />
                )}
              </Group>
            </UnstyledButton>
          )
        })}
      </Stack>
    </Stack>
  )

  if (variant === 'panel') {
    return <div className="approval-mode-panel">{options}</div>
  }

  const triggerButton = (
    <button
      type="button"
      className={[
        'approval-mode-capsule',
        `is-mode-${activeItem.key}`,
        isFull ? 'is-full-access' : '',
        compact ? 'is-compact' : '',
        iconOnly ? 'is-icon-only' : '',
      ].filter(Boolean).join(' ')}
      onClick={() => setOpened((v) => !v)}
      disabled={disabled || mutation.isPending}
      aria-label={`当前审批模式：${activeItem.label}`}
      title={iconOnly ? undefined : `审批模式：${activeItem.label}（点击切换）`}
    >
      <ActiveIcon
        size={iconOnly ? 18 : 14}
        className="approval-mode-capsule-icon"
        style={iconOnly ? undefined : { color: activeItem.color }}
        aria-hidden="true"
      />
      {!iconOnly && <span className="approval-mode-capsule-label">{activeItem.capsuleLabel}</span>}
    </button>
  )

  return (
    <Popover
      opened={opened}
      onChange={setOpened}
      position="top-start"
      withArrow={false}
      shadow="md"
      radius="lg"
      offset={8}
    >
      <Popover.Target>
        {iconOnly ? (
          <Tooltip label={`${activeItem.label} · ${activeItem.description}`} position="top" withArrow disabled={opened}>
            {triggerButton}
          </Tooltip>
        ) : (
          triggerButton
        )}
      </Popover.Target>

      <Popover.Dropdown className="approval-mode-dropdown">
        {options}
      </Popover.Dropdown>
    </Popover>
  )
}
