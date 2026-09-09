import { IconAlertTriangle, IconLoader2 } from '@tabler/icons-react'
import { Alert, Button, Center, Loader, Paper, Stack, Text, Title } from '@mantine/core'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { useMediaQuery } from '@mantine/hooks'
import { MobileWorkspace } from '../features/mobile/MobileWorkspace'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError } from '../api/client'
import { AuthPage } from '../components/AuthPage'
import { AppShellLayout } from './AppShellLayout'
import { useAuthSession, useBootstrap } from '../hooks/useAstrorderData'
import { useEventStream } from '../hooks/useEventStream'
import { ChatPage } from '../features/chat/ChatPage'
import { MonitorPage } from '../features/monitor/MonitorPage'
import { AgentsPage } from '../features/agents/AgentsPage'

function LoadingPage({ label = '正在连接星序…' }: { label?: string }) {
  return <Center className="loading-page"><Stack align="center" gap="sm"><Loader size="md" color="indigo" /><Text c="dimmed">{label}</Text></Stack></Center>
}

function BootstrapError({ error, retry }: { error: unknown; retry: () => void }) {
  const detail = error instanceof ApiError ? error.detail : error instanceof Error ? error.message : '服务端未返回工作台数据。'
  return (
    <Center className="loading-page">
      <Paper withBorder radius="lg" p="xl" maw={520}>
        <Stack align="center" gap="sm">
          <IconAlertTriangle className="error-icon" size={34} />
          <Title order={2} size="h3">工作台暂不可用</Title>
          <Text ta="center" c="dimmed">{detail}</Text>
          <Text ta="center" size="sm" c="dimmed">星序不会用示例数据填充空白。请确认后端认证和 bootstrap 接口已配置。</Text>
          <Button onClick={retry} variant="light">重新读取</Button>
        </Stack>
      </Paper>
    </Center>
  )
}

function AuthenticatedApp() {
  const mobileViewport = useMediaQuery('(max-width: 767px)')
  const location = useLocation()
  const queryClient = useQueryClient()
  const bootstrap = useBootstrap(true)
  useEventStream(true, queryClient)

  if (bootstrap.isLoading) return <LoadingPage label="正在读取已认证工作台…" />
  if (bootstrap.error) return <BootstrapError error={bootstrap.error} retry={() => void bootstrap.refetch()} />
  if (mobileViewport || location.pathname.startsWith('/mobile')) return <MobileWorkspace />

  return (
    <Routes>
      <Route element={<AppShellLayout />}>
        <Route index element={<Navigate to="/chat" replace />} />
        <Route path="chat" element={<ChatPage />} />
        <Route path="chat/:sessionId" element={<ChatPage />} />
        <Route path="monitor" element={<MonitorPage />} />
        <Route path="agents" element={<AgentsPage />} />
        <Route path="*" element={<Navigate to="/chat" replace />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  const queryClient = useQueryClient()
  const auth = useAuthSession()
  if (auth.isLoading) return <LoadingPage label="正在检查登录状态…" />
  if (auth.data?.authenticated) return <AuthenticatedApp />

  return (
    <>
      {auth.error && !auth.data && (
        <Alert className="auth-service-alert" color="yellow" variant="light" icon={<IconLoader2 size={17} />}>
          登录接口当前返回错误；仍可尝试建立会话。
        </Alert>
      )}
      <AuthPage
        initialError={auth.error}
        onAuthenticated={() => {
          void queryClient.invalidateQueries({ queryKey: ['astrorder', 'auth-session'] })
        }}
      />
    </>
  )
}
