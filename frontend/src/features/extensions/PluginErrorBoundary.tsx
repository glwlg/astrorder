import { Component, Fragment, type ErrorInfo, type ReactNode } from 'react'
import { Button, Center, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconAlertTriangle, IconPower, IconRefresh } from '@tabler/icons-react'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { useExtensionStore } from './extensionStore'

export interface PluginErrorBoundaryProps {
  pluginId: string
  pluginName?: string
  /** 变更时自动重置错误状态（例如切换文档、路由或插件热升级） */
  resetKey?: string | number
  /** 自定义重试逻辑（除重新挂载子树外触发） */
  onRetry?: () => void
  /** 自定义停用逻辑（缺省自动停用对应扩展或内置插件开关） */
  onDisable?: (pluginId: string) => void | Promise<void>
  children: ReactNode
}

interface PluginErrorBoundaryState {
  hasError: boolean
  error: Error | null
  retryCount: number
  disabling: boolean
}

export class PluginErrorBoundary extends Component<
  PluginErrorBoundaryProps,
  PluginErrorBoundaryState
> {
  state: PluginErrorBoundaryState = {
    hasError: false,
    error: null,
    retryCount: 0,
    disabling: false,
  }

  static getDerivedStateFromError(error: Error): Partial<PluginErrorBoundaryState> {
    return {
      hasError: true,
      error,
    }
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error(`[PluginErrorBoundary] plugin="${this.props.pluginId}" crashed:`, error, info)
  }

  componentDidUpdate(prevProps: PluginErrorBoundaryProps): void {
    if (
      this.state.hasError &&
      (prevProps.resetKey !== this.props.resetKey || prevProps.pluginId !== this.props.pluginId)
    ) {
      this.setState({ hasError: false, error: null, disabling: false })
    }
  }

  private handleRetry = () => {
    this.setState((prev) => ({
      hasError: false,
      error: null,
      retryCount: prev.retryCount + 1,
      disabling: false,
    }))
    this.props.onRetry?.()
  }

  private handleDisable = async () => {
    const { pluginId, pluginName, onDisable } = this.props
    if (!pluginId || this.state.disabling) return
    this.setState({ disabling: true })
    try {
      if (onDisable) {
        await onDisable(pluginId)
      } else {
        const extStore = useExtensionStore.getState()
        const ext = extStore.getById(pluginId)
        if (ext) {
          await extStore.setEnabled(pluginId, false)
        } else {
          usePluginSettingsStore.getState().togglePlugin(pluginId, false)
        }
      }
      notifications.show({
        color: 'yellow',
        message: `已停用插件「${pluginName || pluginId}」`,
      })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : `停用插件「${pluginName || pluginId}」失败`,
      })
      this.setState({ disabling: false })
    }
  }

  render() {
    if (!this.state.hasError) {
      return <Fragment key={this.state.retryCount}>{this.props.children}</Fragment>
    }

    const label = this.props.pluginName || this.props.pluginId
    const detail = this.state.error?.message || '组件运行时发生未捕获错误'

    return (
      <Center style={{ height: '100%', minHeight: 200, padding: 20 }} role="alert">
        <Paper withBorder radius="md" p="lg" maw={460} w="100%">
          <Stack align="center" gap="sm" ta="center">
            <IconAlertTriangle size={28} color="var(--mantine-color-red-6, #fa5252)" />
            <Title order={4} size="h5">
              {label} 渲染异常
            </Title>
            <Text size="xs" c="dimmed" style={{ wordBreak: 'break-word' }}>
              {detail}
            </Text>
            <Text size="xs" c="dimmed">
              该错误已隔离在当前扩展内，不影响星序工作台其余功能。
            </Text>
            <Group gap="xs" mt={4}>
              <Button
                size="xs"
                variant="light"
                leftSection={<IconRefresh size={14} />}
                onClick={this.handleRetry}
              >
                重试
              </Button>
              <Button
                size="xs"
                variant="light"
                color="red"
                loading={this.state.disabling}
                leftSection={<IconPower size={14} />}
                onClick={() => void this.handleDisable()}
              >
                停用该插件
              </Button>
            </Group>
          </Stack>
        </Paper>
      </Center>
    )
  }
}
