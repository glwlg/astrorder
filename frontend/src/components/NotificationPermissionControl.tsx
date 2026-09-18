import { IconBell, IconBellOff, IconCheck } from '@tabler/icons-react'
import { ActionIcon, Menu, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useState } from 'react'
import {
  deliverBrowserNotification,
  notificationPermission,
  requestNotificationPermission,
  setSystemNotificationEnabled,
  type NotificationPermissionState,
} from '../domain/notifications'

const labels: Record<NotificationPermissionState, string> = {
  granted: '系统通知已开启（点击管理或测试）',
  denied: '后台通知已拒绝',
  default: '启用后台通知',
  unsupported: '当前环境不支持系统通知',
}

export function NotificationPermissionControl() {
  const [permission, setPermission] = useState<NotificationPermissionState>(() => notificationPermission())

  const enable = async () => {
    setSystemNotificationEnabled(true)
    const next = await requestNotificationPermission()
    setPermission(next)
    if (next === 'granted') {
      notifications.show({ color: 'teal', message: '系统通知已开启' })
      deliverBrowserNotification({
        key: 'test-notification-' + Date.now(),
        kind: 'task_completed',
        title: '系统通知已开启',
        message: '会话完成与待审批将通过系统通知实时提醒。',
        agent_id: 'system',
        session_id: 'system',
        created_at: new Date().toISOString(),
      })
    }
  }

  const disable = () => {
    setSystemNotificationEnabled(false)
    setPermission('default')
    notifications.show({ color: 'gray', message: '系统通知已停用' })
  }

  const sendTest = () => {
    notifications.show({ color: 'teal', message: '已发送测试系统通知' })
    deliverBrowserNotification({
      key: 'test-notification-' + Date.now(),
      kind: 'task_completed',
      title: '星序 · 测试通知',
      message: '系统通知已重新注册并测试成功。',
      agent_id: 'system',
      session_id: 'system',
      created_at: new Date().toISOString(),
    })
  }

  const isGranted = permission === 'granted'
  const isDenied = permission === 'denied'

  if (isDenied) {
    return (
      <Tooltip label={labels.denied}>
        <ActionIcon
          variant="subtle"
          size="md"
          radius="md"
          disabled
          aria-live="polite"
          aria-label={labels.denied}
        >
          <IconBellOff size={18} />
        </ActionIcon>
      </Tooltip>
    )
  }

  if (!isGranted) {
    return (
      <Tooltip label={labels[permission]}>
        <ActionIcon
          variant="subtle"
          size="md"
          radius="md"
          onClick={() => void enable()}
          aria-live="polite"
          aria-label={labels[permission]}
        >
          <IconBell size={18} />
        </ActionIcon>
      </Tooltip>
    )
  }

  return (
    <Menu shadow="md" width={180} position="bottom-end" withinPortal>
      <Menu.Target>
        <Tooltip label={labels.granted}>
          <ActionIcon
            variant="subtle"
            size="md"
            radius="md"
            color="teal"
            aria-live="polite"
            aria-label="后台通知已启用"
          >
            <IconBell size={18} />
          </ActionIcon>
        </Tooltip>
      </Menu.Target>
      <Menu.Dropdown>
        <Menu.Item leftSection={<IconCheck size={14} color="var(--mantine-color-teal-6)" />} onClick={sendTest}>
          测试系统通知
        </Menu.Item>
        <Menu.Item leftSection={<IconBellOff size={14} />} color="red" onClick={disable}>
          停用系统通知
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  )
}
