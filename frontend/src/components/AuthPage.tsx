import { IconKey, IconLock, IconRefresh } from '@tabler/icons-react'
import { Alert, Button, Group, PasswordInput, Paper, Stack, Text, Title } from '@mantine/core'
import { type FormEvent, useState } from 'react'
import { ApiError, api } from '../api/client'
import { BrandMark } from './BrandMark'

function errorDetail(error: unknown): string {
  if (error instanceof ApiError) return error.detail
  if (error instanceof Error) return error.message
  return '服务暂时不可用，请检查 Astrorder 后端是否已启动。'
}

export function AuthPage({
  initialError,
  onAuthenticated,
}: {
  initialError?: unknown
  onAuthenticated: () => void
}) {
  const [token, setToken] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<unknown>(initialError)

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!token.trim() || submitting) return
    setSubmitting(true)
    setError(undefined)
    try {
      await api.login(token)
      try {
        localStorage.setItem('astrorder:token', token)
      } catch {}
      // Keep the credential only in this event handler; the server owns the HttpOnly cookie.
      setToken('')
      onAuthenticated()
    } catch (nextError) {
      setError(nextError)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-page">
      <Paper className="auth-card" withBorder radius="xl" p={{ base: 'lg', sm: 'xl' }}>
        <Stack gap="lg">
          <Group gap="sm" align="center">
            <BrandMark className="brand-mark brand-mark-lg" size={46} alt="" />
            <div>
              <Title order={1} size="h2">星序 · Astrorder</Title>
              <Text size="sm" c="dimmed">群星各有所长，协作自有秩序</Text>
            </div>
          </Group>
          <div>
            <Title order={2} size="h3">登录本地工作台</Title>
            <Text c="dimmed" mt={6} size="sm">
              会话凭证只提交到当前 Astrorder 服务端，不会写入 URL 或浏览器存储。
            </Text>
          </div>
          {Boolean(error) && (
            <Alert color={error instanceof ApiError && error.status === 401 ? 'red' : 'yellow'} icon={<IconLock size={18} />}>
              {errorDetail(error)}
            </Alert>
          )}
          <form onSubmit={submit}>
            <Stack gap="md">
              <PasswordInput
                label="访问令牌"
                description="由本地服务配置提供"
                placeholder="输入令牌"
                value={token}
                onChange={(event) => setToken(event.currentTarget.value)}
                leftSection={<IconKey size={17} />}
                autoComplete="off"
                aria-label="访问令牌"
                required
              />
              <Button type="submit" loading={submitting} disabled={!token.trim()} leftSection={<IconRefresh size={17} />}>
                建立会话
              </Button>
            </Stack>
          </form>
          <Text size="xs" c="dimmed">
            未连接或未配置认证时，星序不会展示模拟 Agent、会话或消息。
          </Text>
        </Stack>
      </Paper>
    </main>
  )
}
