import { IconMenu2, IconMoon, IconSun } from '@tabler/icons-react'
import { ActionIcon, Group, Title, Tooltip } from '@mantine/core'
import { useComputedColorScheme, useMantineColorScheme } from '@mantine/core'
import { AnimatePresence, motion } from 'motion/react'
import { useEffect } from 'react'
import { DecryptedText } from './animations/DecryptedText'
import { BrandMark } from './BrandMark'
import { ConnectionBadge } from './Status'
import { NotificationPermissionControl } from './NotificationPermissionControl'

export function AppHeader({
  connection,
  onMenu,
}: {
  connection: Parameters<typeof ConnectionBadge>[0]['status']
  onMenu: () => void
}) {
  const { setColorScheme } = useMantineColorScheme()
  const computed = useComputedColorScheme('light', { getInitialValueInEffect: true })
  const toggleTheme = () => setColorScheme(computed === 'dark' ? 'light' : 'dark')

  useEffect(() => {
    const themeColor = computed === 'dark' ? '#171e2a' : '#f9fafc'
    const meta = document.querySelector('meta[name="theme-color"]')
    if (meta) {
      meta.setAttribute('content', themeColor)
    }
    const desktop = (window as unknown as { astrorderDesktop?: { setTheme?: (t: string) => Promise<void> } }).astrorderDesktop
    if (desktop?.setTheme) {
      void desktop.setTheme(computed)
    }
  }, [computed])

  return (
    <div className="app-header">
      <Group h="100%" justify="space-between" wrap="nowrap">
        <Group gap="sm" wrap="nowrap" className="header-brand-group">
          <ActionIcon className="mobile-menu-button" hiddenFrom="md" variant="subtle" onClick={onMenu} aria-label="打开导航">
            <IconMenu2 size={21} />
          </ActionIcon>
          <Group gap={8} wrap="nowrap" className="header-brand" align="center">
            <BrandMark className="brand-mark" size={20} />
            <Title order={1} size="h4" style={{ fontSize: '15px', fontWeight: 600, letterSpacing: '-0.01em', margin: 0, whiteSpace: 'nowrap' }}>
              <DecryptedText text="星序 · Astrorder" speed={30} maxIterations={8} animateOn="hover" />
            </Title>
          </Group>
        </Group>
        <Group gap={4} wrap="nowrap" className="header-actions">
          <NotificationPermissionControl />
          <ConnectionBadge status={connection} />
          <Tooltip label={computed === 'dark' ? '切换浅色' : '切换深色'}>
            <ActionIcon variant="subtle" onClick={toggleTheme} aria-label="切换主题">
              <AnimatePresence mode="wait" initial={false}>
                <motion.span
                  key={computed}
                  initial={{ opacity: 0, rotate: -90, scale: 0.75 }}
                  animate={{ opacity: 1, rotate: 0, scale: 1 }}
                  exit={{ opacity: 0, rotate: 90, scale: 0.75 }}
                  transition={{ duration: 0.22, ease: [0.16, 1, 0.3, 1] }}
                  style={{ display: 'inline-flex' }}
                >
                  {computed === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
                </motion.span>
              </AnimatePresence>
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
    </div>
  )
}
