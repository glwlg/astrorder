import { useEffect, useState } from 'react'
import { Card, Group, Stack, Switch, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconDeviceDesktop } from '@tabler/icons-react'

type StartupSettings = { available: boolean; enabled: boolean }
type DesktopBridge = {
  getStartupSettings?: () => Promise<StartupSettings>
  setStartupEnabled?: (enabled: boolean) => Promise<StartupSettings>
}

function bridge(): DesktopBridge | undefined {
  return (window as unknown as { astrorderDesktop?: DesktopBridge }).astrorderDesktop
}

export function DesktopSettingsPage() {
  const [settings, setSettings] = useState<StartupSettings>({ available: false, enabled: false })
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const desktop = bridge()
    if (!desktop?.getStartupSettings) {
      setLoading(false)
      return
    }
    void desktop.getStartupSettings()
      .then(setSettings)
      .catch(error => notifications.show({ color: 'red', message: error instanceof Error ? error.message : '读取开机启动设置失败' }))
      .finally(() => setLoading(false))
  }, [])

  const update = async (enabled: boolean) => {
    const desktop = bridge()
    if (!desktop?.setStartupEnabled) return
    setLoading(true)
    try {
      setSettings(await desktop.setStartupEnabled(enabled))
      notifications.show({ color: 'teal', message: enabled ? '已设置开机启动' : '已关闭开机启动' })
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '更新开机启动设置失败' })
    } finally {
      setLoading(false)
    }
  }

  return (
    <Stack gap="lg" maw={760} mx="auto" py="md">
      <Group gap="sm">
        <IconDeviceDesktop size={24} />
        <div>
          <Title order={3} size="h4">桌面客户端</Title>
          <Text size="sm" c="dimmed">管理 Windows 登录后的客户端启动行为。</Text>
        </div>
      </Group>
      <Card withBorder radius="md" p="lg">
        <Group justify="space-between" align="center" wrap="nowrap">
          <div>
            <Text fw={600}>开机启动</Text>
            <Text size="sm" c="dimmed">
              {settings.available ? '登录 Windows 后自动启动星序客户端。' : '请在已安装的星序桌面客户端中设置。'}
            </Text>
          </div>
          <Switch
            aria-label="开机启动"
            checked={settings.enabled}
            disabled={loading || !settings.available}
            onChange={event => void update(event.currentTarget.checked)}
          />
        </Group>
      </Card>
    </Stack>
  )
}
