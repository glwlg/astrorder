import { ActionIcon, Tooltip, useComputedColorScheme, useMantineColorScheme } from '@mantine/core'
import { MoonIcon, SunIcon } from './workbenchIcons'

export function WorkspaceThemeToggle() {
  const scheme = useComputedColorScheme('light', { getInitialValueInEffect: true })
  const { setColorScheme } = useMantineColorScheme()
  return (
    <Tooltip label={scheme === 'dark' ? '切换浅色' : '切换深色'} position="right">
      <ActionIcon
        variant="subtle"
        aria-label="切换主题"
        onClick={() => setColorScheme(scheme === 'dark' ? 'light' : 'dark')}
      >
        {scheme === 'dark' ? <SunIcon size={24} aria-hidden="true" /> : <MoonIcon size={24} aria-hidden="true" />}
      </ActionIcon>
    </Tooltip>
  )
}
