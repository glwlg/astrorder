import { IconBell } from '@tabler/icons-react'
import { ActionIcon, Tooltip } from '@mantine/core'
import { useState } from 'react'
import { notificationPermission, requestNotificationPermission, type NotificationPermissionState } from '../domain/notifications'

const labels: Record<NotificationPermissionState, string> = {
  granted: '后台通知已启用',
  denied: '后台通知已拒绝',
  default: '启用后台通知',
  unsupported: '后台通知不可用',
}

export function NotificationPermissionControl() {
  const [permission, setPermission] = useState<NotificationPermissionState>(() => notificationPermission())
  const enable = async () => {
    setPermission(await requestNotificationPermission())
  }
  const disabled = permission !== 'default'
  return (
    <Tooltip label={labels[permission]}>
      <ActionIcon
        variant="subtle"
        size="md"
        radius="md"
        onClick={() => void enable()}
        disabled={disabled}
        aria-live="polite"
        aria-label={labels[permission]}
        color={permission === 'granted' ? 'teal' : undefined}
      >
        <IconBell size={18} />
      </ActionIcon>
    </Tooltip>
  )
}
