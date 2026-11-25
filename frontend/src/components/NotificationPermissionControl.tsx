import { BellSimpleIcon, BellSimpleSlashIcon, CheckIcon } from './workbenchIcons'
import { ActionIcon, Menu, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useEffect, useState } from 'react'
import {
  disableSystemNotifications,
  notificationPermission,
  requestNotificationPermission,
  testSystemNotification,
  type NotificationPermissionState,
} from '../domain/notifications'

const labels: Record<NotificationPermissionState, string> = {
  granted: '系统通知已开启（点击管理或测试）',
  denied: '后台通知已拒绝',
  default: '启用后台通知',
  unsupported: '当前环境不支持系统通知',
}

export function useNotificationPermissionControl() {
  const [permission, setPermission] = useState<NotificationPermissionState>(() => notificationPermission())
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    const refresh = () => setPermission(notificationPermission())
    window.addEventListener('astrorder:notification-settings', refresh)
    window.addEventListener('focus', refresh)
    return () => {
      window.removeEventListener('astrorder:notification-settings', refresh)
      window.removeEventListener('focus', refresh)
    }
  }, [])

  const run = async (action: () => Promise<void>) => {
    setBusy(true)
    try { await action() } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '通知设置失败，请重试' })
    } finally {
      setPermission(notificationPermission())
      setBusy(false)
    }
  }
  const enable = () => run(async () => {
    const next = await requestNotificationPermission()
    if (next === 'granted') notifications.show({ color: 'teal', message: '后台通知已开启，任务完成和待回答问题将提醒你。' })
    else if (next === 'unsupported') notifications.show({ color: 'orange', message: '当前浏览器不支持后台通知；iPhone 请通过 HTTPS 打开星序并添加到主屏幕。' })
  })
  const disable = () => run(async () => {
    await disableSystemNotifications()
    notifications.show({ color: 'gray', message: '后台通知已停用' })
  })
  const sendTest = () => run(async () => {
    await testSystemNotification()
    notifications.show({ color: 'teal', message: '测试通知已提交，请检查系统通知栏。' })
  })
  const showDeniedHelp = () => {
    notifications.show({ color: 'orange', message: '通知权限已被拒绝，请在系统或浏览器的星序通知设置中允许通知后重试。' })
  }

  return { permission, busy, enable, disable, sendTest, showDeniedHelp }
}

export function NotificationPermissionMenuItems() {
  const { permission, busy, enable, disable, sendTest, showDeniedHelp } = useNotificationPermissionControl()

  if (permission === 'denied') {
    return (
      <Menu.Item leftSection={<BellSimpleSlashIcon size={17} />} onClick={showDeniedHelp}>
        后台通知已拒绝
      </Menu.Item>
    )
  }

  if (permission !== 'granted') {
    return (
      <Menu.Item leftSection={<BellSimpleIcon size={17} />} disabled={busy} onClick={() => void enable()}>
        {permission === 'unsupported' ? '系统通知不可用' : '启用后台通知'}
      </Menu.Item>
    )
  }

  return (
    <>
      <Menu.Item leftSection={<CheckIcon size={17} color="var(--astr-teal, #12b886)" />} disabled={busy} onClick={() => void sendTest()}>
        测试系统通知
      </Menu.Item>
      <Menu.Item leftSection={<BellSimpleSlashIcon size={17} />} color="red" disabled={busy} onClick={() => void disable()}>
        停用系统通知
      </Menu.Item>
    </>
  )
}

export function NotificationPermissionControl() {
  const { permission, busy, enable, disable, sendTest, showDeniedHelp } = useNotificationPermissionControl()

  const isGranted = permission === 'granted'
  const isDenied = permission === 'denied'

  if (isDenied) {
    return (
      <Tooltip label={labels.denied} position="right">
        <ActionIcon
          variant="subtle"
          size="md"
          radius="md"
          onClick={showDeniedHelp}
          aria-live="polite"
          aria-label={labels.denied}
        >
          <BellSimpleSlashIcon size={24} aria-hidden="true" />
        </ActionIcon>
      </Tooltip>
    )
  }

  if (!isGranted) {
    return (
      <Tooltip label={labels[permission]} position="right">
        <ActionIcon
          variant="subtle"
          size="md"
          radius="md"
          onClick={() => void enable()}
          loading={busy}
          aria-live="polite"
          aria-label={labels[permission]}
        >
          <BellSimpleIcon size={24} aria-hidden="true" />
        </ActionIcon>
      </Tooltip>
    )
  }

  return (
    <Menu shadow="md" width={180} position="right-end" withinPortal>
      <Menu.Target>
        <Tooltip label={labels.granted} position="right">
          <ActionIcon
            variant="subtle"
            size="md"
            radius="md"
            color="teal"
            loading={busy}
            aria-live="polite"
            aria-label="后台通知已启用"
          >
            <BellSimpleIcon size={24} weight="duotone" aria-hidden="true" />
          </ActionIcon>
        </Tooltip>
      </Menu.Target>
      <Menu.Dropdown>
        <Menu.Item leftSection={<CheckIcon size={16} color="var(--astr-muted)" />} onClick={sendTest}>
          测试系统通知
        </Menu.Item>
        <Menu.Item leftSection={<BellSimpleSlashIcon size={16} />} color="red" onClick={disable}>
          停用系统通知
        </Menu.Item>
      </Menu.Dropdown>
    </Menu>
  )
}
