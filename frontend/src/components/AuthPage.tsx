import { IconKey, IconLock, IconRefresh } from '@tabler/icons-react'
import { Alert, Button, Group, PasswordInput, Paper, SegmentedControl, Stack, Text, Title } from '@mantine/core'
import { type FormEvent, useState } from 'react'
import { ApiError, api } from '../api/client'
import { BrandMark } from './BrandMark'
import { QrCodeLogin } from '../features/auth/QrCodeLogin'

function errorDetail(error: unknown): string {
  if (error instanceof ApiError) return error.detail
  if (error instanceof Error) return error.message
  return '服务暂时不可用，请确认星序服务已正常启动。'
}

export function AuthPage({
  initialError,
  onAuthenticated,
}: {
  initialError?: unknown
  onAuthenticated: () => void
}) {
  const [authMethod, setAuthMethod] = useState<'qr' | 'token'>('qr')
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
            <Title order={2} size="h3">登录星序工作台</Title>
            <Text c="dimmed" mt={6} size="sm">
              登录凭证仅用于连接当前设备上的星序服务，保障你的本地数据安全。
            </Text>
          </div>

          <SegmentedControl
            fullWidth
            value={authMethod}
            onChange={(val) => {
              setAuthMethod(val as 'qr' | 'token')
              setError(undefined)
            }}
            data={[
              { label: '扫码登录', value: 'qr' },
              { label: '口令登录', value: 'token' },
            ]}
          />

          {Boolean(error) && (
            <Alert color={error instanceof ApiError && error.status === 401 ? 'red' : 'yellow'} icon={<IconLock size={18} />}>
              {errorDetail(error)}
            </Alert>
          )}

          {authMethod === 'qr' ? (
            <QrCodeLogin onAuthenticated={onAuthenticated} />
          ) : (
            <form onSubmit={submit}>
              <Stack gap="md">
                <PasswordInput
                  label="登录口令"
                  description="可在本机星序桌面端或启动提示中查看"
                  placeholder="请输入登录口令"
                  value={token}
                  onChange={(event) => setToken(event.currentTarget.value)}
                  leftSection={<IconKey size={17} />}
                  autoComplete="off"
                  aria-label="登录口令"
                  required
                />
                <Button type="submit" loading={submitting} disabled={!token.trim()} leftSection={<IconRefresh size={17} />}>
                  立即登录
                </Button>
              </Stack>
            </form>
          )}

          <Text size="xs" c="dimmed">
            如需使用手机或平板访问，可点击上方「扫码登录」快速授权连接。
          </Text>
        </Stack>
      </Paper>
    </main>
  )
}