import { useEffect, useState } from 'react'
import { ActionIcon, Group, Text, Tooltip, useComputedColorScheme, useMantineColorScheme } from '@mantine/core'
import { IconMoon, IconSun } from '@tabler/icons-react'
import { AnimatePresence, motion } from 'motion/react'
import { BrandMark } from './BrandMark'
import { NotificationPermissionControl } from './NotificationPermissionControl'

export function DesktopTitlebar() {
  const [isDesktop, setIsDesktop] = useState(false)
  const { setColorScheme } = useMantineColorScheme()
  const computed = useComputedColorScheme('light', { getInitialValueInEffect: true })
  const toggleTheme = () => setColorScheme(computed === 'dark' ? 'light' : 'dark')

  const handleDoubleClick = () => {
    const desktop = (window as unknown as { astrorderDesktop?: { maximize?: () => Promise<boolean> } }).astrorderDesktop
    if (desktop?.maximize) {
      void desktop.maximize()
    }
  }

  useEffect(() => {
    if (typeof window !== 'undefined' && 'astrorderDesktop' in window) {
      setIsDesktop(true)
    }
  }, [])

  if (!isDesktop) return null

  return (
    <div className="desktop-window-titlebar" aria-label="窗口标题栏" onDoubleClick={handleDoubleClick}>
      <Group gap={6} wrap="nowrap" align="center" className="desktop-titlebar-brand">
        <BrandMark size={14} />
        <Text size="xs" fw={600} c="dimmed" style={{ letterSpacing: '0.02em' }}>
          星序 · Astrorder
        </Text>
      </Group>
      <div className="desktop-titlebar-drag-spacer" onDoubleClick={handleDoubleClick} />
      <div className="desktop-titlebar-actions">
        <NotificationPermissionControl />
        <Tooltip label={computed === 'dark' ? '切换浅色' : '切换深色'}>
          <ActionIcon
            variant="subtle"
            size={22}
            onClick={toggleTheme}
            aria-label="切换主题"
            style={{ color: 'var(--astr-muted)' }}
          >
            <AnimatePresence mode="wait" initial={false}>
              <motion.span
                key={computed}
                initial={{ opacity: 0, rotate: -90, scale: 0.75 }}
                animate={{ opacity: 1, rotate: 0, scale: 1 }}
                exit={{ opacity: 0, rotate: 90, scale: 0.75 }}
                transition={{ duration: 0.18, ease: [0.16, 1, 0.3, 1] }}
                style={{ display: 'inline-flex' }}
              >
                {computed === 'dark' ? <IconSun size={14} /> : <IconMoon size={14} />}
              </motion.span>
            </AnimatePresence>
          </ActionIcon>
        </Tooltip>
      </div>
    </div>
  )
}
