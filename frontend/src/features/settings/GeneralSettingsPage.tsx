import { useEffect, useState } from 'react'
import { Button, Group, SegmentedControl, Switch, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { useNotificationPermissionControl } from '../../components/NotificationPermissionControl'
import { AppearanceSettingsPage } from '../appearance/AppearanceSettingsPage'
import { setAppProfile, useAppProfile, type AppProfile } from '../office/appProfile'
import { OfficePersonaCard } from '../office/OfficePersonaCard'
import { openOnboardingWizard } from '../office/OnboardingWizard'

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
  const {
    permission: notifyPermission,
    busy: notifyBusy,
    enable: enableNotifications,
    disable: disableNotifications,
    sendTest: sendTestNotification,
    showDeniedHelp,
  } = useNotificationPermissionControl()
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
          <div className="st-setting-row-title">初始化配置向导</div>
          <div className="st-setting-row-desc">
            重新选择或安装本机 AI 助手引擎、配置模型、注入星序 CLI 与官方技能，并启动配置助手对话
          </div>
        </div>
        <Button
          size="xs"
          variant="default"
          onClick={() => openOnboardingWizard()}
        >
          重新运行向导
        </Button>
      </div>

      <div className="st-setting-row">
        <div className="st-setting-row-copy">
          <div className="st-setting-row-title">桌面与后台通知</div>
          <div className="st-setting-row-desc">
            {notifyPermission === 'granted'
              ? '已开启：任务完成、待回答问题与审批请求将发送系统通知'
              : notifyPermission === 'denied'
                ? '通知权限已被拒绝，请在系统或浏览器设置中允许星序通知后重试'
                : notifyPermission === 'unsupported'
                  ? '当前浏览器环境不支持后台推送通知'
                  : '开启后，AI 助手完成长任务或等待确认时将通过系统通知栏提醒你'}
          </div>
        </div>
        <Group gap="xs" wrap="nowrap">
          {notifyPermission === 'granted' && (
            <Button
              size="xs"
              variant="default"
              loading={notifyBusy}
              onClick={() => void sendTestNotification()}
            >
              测试通知
            </Button>
          )}
          {notifyPermission === 'denied' ? (
            <Button size="xs" variant="default" onClick={showDeniedHelp}>
              查看说明
            </Button>
          ) : (
            <Switch
              aria-label="桌面与后台通知"
              checked={notifyPermission === 'granted'}
              disabled={notifyBusy || notifyPermission === 'unsupported'}
              onChange={(event) => {
                if (event.currentTarget.checked) {
                  void enableNotifications()
                } else {
                  void disableNotifications()
                }
              }}
            />
          )}
        </Group>
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
