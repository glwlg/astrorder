import { IconBell } from '@tabler/icons-react'
import { Button } from '@mantine/core'
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
    <Button
      variant="subtle"
      size="compact-sm"
      leftSection={<IconBell size={16} />}
      onClick={() => void enable()}
      disabled={disabled}
      aria-live="polite"
      aria-label={labels[permission]}
      style={{ minHeight: 44 }}
    >
      {labels[permission]}
    </Button>
  )
}
