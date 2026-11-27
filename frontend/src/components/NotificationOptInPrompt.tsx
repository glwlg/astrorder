import { useEffect, useState } from 'react'
import { Button, Group, Paper, Text } from '@mantine/core'
import { IconX } from '@tabler/icons-react'
import { BellSimpleIcon } from './workbenchIcons'
import { notificationPermission } from '../domain/notifications'
import { hasExplicitAppProfile } from '../features/office/appProfile'
import { useNotificationPermissionControl } from './NotificationPermissionControl'

export const NOTIFICATION_PROMPTED_STORAGE_KEY = 'astrorder:notification-prompted'

export interface NotificationOptInPromptProps {
  ready: boolean
  forceOpen?: boolean
}

function wasAlreadyPrompted(): boolean {
  if (typeof localStorage === 'undefined') return true
  try {
    return localStorage.getItem(NOTIFICATION_PROMPTED_STORAGE_KEY) === '1'
  } catch {
    return true
  }
}

function markPrompted(): void {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(NOTIFICATION_PROMPTED_STORAGE_KEY, '1')
  } catch {
    // ignore storage errors
  }
}

export function NotificationOptInPrompt({ ready, forceOpen = false }: NotificationOptInPromptProps) {
  const { permission, busy, enable } = useNotificationPermissionControl()
  const [visible, setVisible] = useState(forceOpen)

  useEffect(() => {
    if (forceOpen) {
      setVisible(true)
      return
    }
    if (!ready) return
    if (import.meta.env.MODE === 'test') return
    if (wasAlreadyPrompted()) return

    const current = notificationPermission()
    if (current !== 'default') {
      markPrompted()
      return
    }

    let timer: number | null = null
    const trySchedule = () => {
      if (wasAlreadyPrompted() || notificationPermission() !== 'default') return
      if (!hasExplicitAppProfile()) return
      if (timer !== null) window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        if (!wasAlreadyPrompted() && notificationPermission() === 'default') {
          setVisible(true)
        }
      }, 900)
    }

    trySchedule()
    const onStorage = () => trySchedule()
    window.addEventListener('storage', onStorage)
    // Poll briefly in case profile is chosen in the same tab during first-launch onboarding
    const poll = window.setInterval(() => {
      if (hasExplicitAppProfile()) {
        window.clearInterval(poll)
        trySchedule()
      }
    }, 1500)

    return () => {
      if (timer !== null) window.clearTimeout(timer)
      window.clearInterval(poll)
      window.removeEventListener('storage', onStorage)
    }
  }, [forceOpen, ready])

  useEffect(() => {
    if (permission !== 'default' && visible && !forceOpen) {
      markPrompted()
      setVisible(false)
    }
  }, [forceOpen, permission, visible])

  if (!visible) return null

  const dismiss = () => {
    markPrompted()
    setVisible(false)
  }

  const handleEnable = async () => {
    markPrompted()
    await enable()
    setVisible(false)
  }

  return (
    <Paper
      role="dialog"
      aria-label="首次启动通知提醒设置"
      withBorder
      radius="md"
      p="md"
      style={{
        position: 'fixed',
        right: 20,
        bottom: 20,
        zIndex: 260,
        width: 'min(360px, calc(100vw - 32px))',
        background: 'var(--astr-surface, #ffffff)',
        borderColor: 'var(--astr-border, #e4e4e7)',
        boxShadow: '0 14px 36px rgba(15, 23, 42, 0.16)',
      }}
    >
      <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
        <Group align="flex-start" wrap="nowrap" gap="sm" style={{ minWidth: 0 }}>
          <span
            style={{
              width: 32,
              height: 32,
              borderRadius: 9,
              flexShrink: 0,
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              background: 'var(--astr-surface-muted, #f4f4f5)',
              color: 'var(--astr-text, #18181b)',
              border: '1px solid var(--astr-border, #e4e4e7)',
            }}
            aria-hidden="true"
          >
            <BellSimpleIcon size={18} />
          </span>
          <div style={{ minWidth: 0 }}>
            <Text size="sm" fw={650} lh={1.35}>
              是否开启任务完成与审批通知？
            </Text>
            <Text size="xs" c="dimmed" mt={4} lh={1.5}>
              AI 助手完成长任务或需要你确认时及时提醒，之后随时可在「设置 → 常规与外观」中管理。
            </Text>
          </div>
        </Group>
        <button
          type="button"
          aria-label="关闭通知提醒询问"
          onClick={dismiss}
          style={{
            border: 'none',
            background: 'transparent',
            color: 'var(--astr-muted, #71717a)',
            cursor: 'pointer',
            padding: 2,
            borderRadius: 6,
            display: 'inline-flex',
          }}
        >
          <IconX size={15} aria-hidden="true" />
        </button>
      </Group>

      <Group justify="flex-end" gap="xs" mt="sm">
        <Button size="xs" variant="subtle" color="gray" disabled={busy} onClick={dismiss}>
          暂不开启
        </Button>
        <Button size="xs" color="dark" loading={busy} onClick={() => void handleEnable()}>
          开启通知
        </Button>
      </Group>
    </Paper>
  )
}
