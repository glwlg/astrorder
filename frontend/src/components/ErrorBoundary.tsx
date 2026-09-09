import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Alert, Button, Group, Paper, Stack, Text, Title } from '@mantine/core'
import { IconAlertTriangle, IconRefresh } from '@tabler/icons-react'

interface Props {
  children: ReactNode
  fallbackTitle?: string
}

interface State {
  hasError: boolean
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  public override state: State = {
    hasError: false,
    error: null,
  }

  public static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error }
  }

  public override componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('[ErrorBoundary caught error]', error, errorInfo)
  }

  private handleReset = () => {
    this.setState({ hasError: false, error: null })
  }

  public override render() {
    if (this.state.hasError) {
      return (
        <div style={{ padding: '32px', display: 'grid', placeItems: 'center', minHeight: '300px' }}>
          <Paper withBorder radius="md" p="xl" style={{ maxWidth: 540, width: '100%', background: 'var(--astr-surface)' }}>
            <Stack gap="md">
              <Group gap="sm">
                <IconAlertTriangle color="var(--astr-yellow)" size={24} />
                <Title order={4}>{this.props.fallbackTitle || '页面渲染遇到异常'}</Title>
              </Group>
              <Text size="sm" c="dimmed">
                当前视图在渲染时遇到了未预期的错误，已自动拦截保护工作台状态。
              </Text>
              {this.state.error && (
                <Alert color="red" variant="light">
                  {this.state.error.message || String(this.state.error)}
                </Alert>
              )}
              <Group justify="flex-end">
                <Button size="xs" variant="light" leftSection={<IconRefresh size={14} />} onClick={this.handleReset}>
                  重试渲染
                </Button>
              </Group>
            </Stack>
          </Paper>
        </div>
      )
    }

    return this.props.children
  }
}
