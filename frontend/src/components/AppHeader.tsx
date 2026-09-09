import { IconMenu2, IconMoon, IconPower, IconSun } from '@tabler/icons-react'
import { ActionIcon, Group, Title, Tooltip } from '@mantine/core'
import { useComputedColorScheme, useMantineColorScheme } from '@mantine/core'
import { useState } from 'react'
import { ConnectionBadge } from './Status'
import { NotificationPermissionControl } from './NotificationPermissionControl'

export function AppHeader({
  connection,
  onMenu,
  onLogout,
}: {
  connection: Parameters<typeof ConnectionBadge>[0]['status']
  onMenu: () => void
  onLogout: () => Promise<void>
}) {
  const { setColorScheme } = useMantineColorScheme()
  const computed = useComputedColorScheme('light', { getInitialValueInEffect: true })
  const [loggingOut, setLoggingOut] = useState(false)
  const toggleTheme = () => setColorScheme(computed === 'dark' ? 'light' : 'dark')
  const logout = async () => {
    if (loggingOut) return
    setLoggingOut(true)
    try {
      await onLogout()
    } finally {
      setLoggingOut(false)
    }
  }

  return (
    <div className="app-header">
      <Group h="100%" justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap">
          <ActionIcon className="mobile-menu-button" hiddenFrom="md" variant="subtle" onClick={onMenu} aria-label="打开导航">
            <IconMenu2 size={21} />
          </ActionIcon>
          <Title className="mobile-header-title" order={2} size="h4">星序 · Astrorder</Title>
        </Group>
        <Group gap="xs" wrap="nowrap">
          <NotificationPermissionControl />
          <ConnectionBadge status={connection} />
          <Tooltip label={computed === 'dark' ? '切换浅色' : '切换深色'}>
            <ActionIcon variant="subtle" onClick={toggleTheme} aria-label="切换主题">
              {computed === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
            </ActionIcon>
          </Tooltip>
          <Tooltip label="退出会话">
            <ActionIcon variant="subtle" color="red" onClick={() => void logout()} loading={loggingOut} aria-label="退出会话">
              <IconPower size={18} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
    </div>
  )
}
