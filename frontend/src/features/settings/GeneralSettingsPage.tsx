import { useEffect, useState } from 'react'
import { SegmentedControl, Switch, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { AppearanceSettingsPage } from '../appearance/AppearanceSettingsPage'
import { setAppProfile, useAppProfile, type AppProfile } from '../office/appProfile'
import { OfficePersonaCard } from '../office/OfficePersonaCard'

type StartupSettings = { available: boolean; enabled: boolean }
type DesktopBridge = {
  getStartupSettings?: () => Promise<StartupSettings>
  setStartupEnabled?: (enabled: boolean) => Promise<StartupSettings>
}

function getDesktopBridge(): DesktopBridge | undefined {
  if (typeof window === 'undefined') return undefined
  return (window as unknown as { astrorderDesktop?: DesktopBridge }).astrorderDesktop
}

export function GeneralSettingsPage() {
  const appProfile = useAppProfile()
  const [startup, setStartup] = useState<StartupSettings>({ available: false, enabled: false })
  const [loadingStartup, setLoadingStartup] = useState(() => Boolean(getDesktopBridge()?.getStartupSettings))

  useEffect(() => {
    const desktop = getDesktopBridge()
    if (!desktop?.getStartupSettings) return
    void desktop
      .getStartupSettings()
      .then(setStartup)
      .catch((error) =>
        notifications.show({
          color: 'red',
          message: error instanceof Error ? error.message : '读取开机启动设置失败',
        }),
      )
      .finally(() => setLoadingStartup(false))
  }, [])

  const updateStartup = async (enabled: boolean) => {
    const desktop = getDesktopBridge()
    if (!desktop?.setStartupEnabled) return
    setLoadingStartup(true)
    try {
      setStartup(await desktop.setStartupEnabled(enabled))
      notifications.show({
        color: 'teal',
        message: enabled ? '已设置开机自动启动' : '已关闭开机自动启动',
      })
    } catch (error) {
      notifications.show({
        color: 'red',
        message: error instanceof Error ? error.message : '更新开机启动设置失败',
      })
    } finally {
      setLoadingStartup(false)
    }
  }

  return (
    <div className="general-settings-page">
      <div className="settings-page-heading">
        <Title order={2}>常规与外观</Title>
        <Text c="dimmed" size="sm">
          管理工作模式、桌面启动行为、明暗外观与界面主题配色
        </Text>
      </div>

      <div className="st-setting-row">
        <div className="st-setting-row-copy">
          <div className="st-setting-row-title">工作模式</div>
          <div className="st-setting-row-desc">
            {appProfile === 'office'
              ? '当前为办公模式：聚焦对话、文档、会议、周报与待办，隐藏终端与代码工具'
              : '当前为研发模式：保留全部代码审查、Git、终端、数据库与多会话矩阵'}
          </div>
        </div>
        <SegmentedControl
          size="xs"
          aria-label="全局工作模式"
          value={appProfile}
          onChange={(val) => {
            const next = val as AppProfile
            setAppProfile(next)
            notifications.show({
              color: 'teal',
              message: next === 'office' ? '已切换至办公模式' : '已切换至研发模式',
            })
          }}
          data={[
            { value: 'office', label: '办公模式' },
            { value: 'developer', label: '研发模式' },
          ]}
        />
      </div>

      <div className="st-setting-row">
        <div className="st-setting-row-copy">
          <div className="st-setting-row-title">开机自动启动</div>
          <div className="st-setting-row-desc">
            {startup.available
              ? '登录 Windows 后自动启动桌面客户端'
              : '仅在已安装的 Windows 桌面客户端中支持设置'}
          </div>
        </div>
        <Switch
          aria-label="开机启动"
          checked={startup.enabled}
          disabled={loadingStartup || !startup.available}
          onChange={(event) => void updateStartup(event.currentTarget.checked)}
        />
      </div>

      <OfficePersonaCard />

      <AppearanceSettingsPage embedded />
    </div>
  )
}
