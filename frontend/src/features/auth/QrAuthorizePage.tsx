import { Alert, Button, Center, Group, Loader, Paper, PasswordInput, Stack, Text, Title } from '@mantine/core'
import { IconAlertCircle, IconCheck, IconDeviceDesktop, IconKey, IconLock, IconShieldCheck, IconX } from '@tabler/icons-react'
import { type FormEvent, useEffect, useRef, useState } from 'react'
import { api, ApiError, type QrInspectResponse } from '../../api/client'
import { clearQrFragmentFromHistory, extractQrFragmentParams } from './qrUtils'

export function QrAuthorizePage({
  authenticated,
  onAuthenticated,
  onDone,
}: {
  authenticated: boolean
  onAuthenticated?: () => void
  onDone?: () => void
}) {
  const [paramState] = useState(() => extractQrFragmentParams())
  const [loginToken, setLoginToken] = useState('')
  const [loggingIn, setLoggingIn] = useState(false)
  const [loginError, setLoginError] = useState<string | null>(null)

  const [inspecting, setInspecting] = useState(false)
  const [inspectData, setInspectData] = useState<QrInspectResponse | null>(null)
  const [inspectError, setInspectError] = useState<string | null>(null)

  const [actionLoading, setActionLoading] = useState(false)
  const [decisionResult, setDecisionResult] = useState<'approved' | 'rejected' | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)

  const hasClearedHistory = useRef(false)

  // Remove fragment from browser history immediately on mount while preserving state
  useEffect(() => {
    if (!hasClearedHistory.current) {
      hasClearedHistory.current = true
      clearQrFragmentFromHistory()
    }
  }, [])

  const id = paramState.id
  const scanSecret = paramState.scan_secret
  const isParamsValid = Boolean(id && scanSecret)

  // Inspect QR session when authenticated and params valid
  useEffect(() => {
    if (!authenticated || !isParamsValid || !id || !scanSecret || decisionResult) return

    let active = true
    setInspecting(true)
    setInspectError(null)

    api.inspectQrSession(id, scanSecret)
      .then((data) => {
        if (!active) return
        setInspectData(data)
      })
      .catch((err) => {
        if (!active) return
        if (err instanceof ApiError && err.status === 410) {
          setInspectError('此二维码已过期，请在电脑端刷新后重试')
        } else {
          setInspectError(err instanceof ApiError ? err.detail : '获取授权会话失败，请重试')
        }
      })
      .finally(() => {
        if (active) setInspecting(false)
      })

    return () => {
      active = false
    }
  }, [authenticated, isParamsValid, id, scanSecret, decisionResult])

  const handleLogin = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!loginToken.trim() || loggingIn) return
    setLoggingIn(true)
    setLoginError(null)
    try {
      await api.login(loginToken)
      try {
        localStorage.setItem('astrorder:token', loginToken)
      } catch {}
      setLoginToken('')
      onAuthenticated?.()
    } catch (err) {
      setLoginError(err instanceof ApiError ? err.detail : '登录失败，请检查访问令牌')
    } finally {
      setLoggingIn(false)
    }
  }

  const handleDecision = async (decision: 'approve' | 'reject') => {
    if (!id || !scanSecret || actionLoading) return
    setActionLoading(true)
    setActionError(null)
    try {
      const res = await api.approveQrSession(id, scanSecret, decision)
      if (res.status === 'approved' || res.status === 'rejected') {
        setDecisionResult(res.status)
      } else if (res.status === 'expired') {
        setInspectError('该二维码已过期，请在电脑端重新生成并重新扫码。')
      } else {
        setActionError('授权状态异常，请重试')
      }
    } catch (err) {
      setActionError(err instanceof ApiError ? err.detail : '操作未完成，请重试')
    } finally {
      setActionLoading(false)
    }
  }

  if (!isParamsValid) {
    return (
      <Center style={{ minHeight: '100vh', padding: 16 }}>
        <Paper withBorder radius="lg" p="xl" maw={440} w="100%">
          <Stack align="center" gap="md">
            <IconAlertCircle size={36} color="#fa5252" />
            <Title order={2} size="h4">二维码凭据无效</Title>
            <Text size="sm" c="dimmed" ta="center">
              {paramState.error || '未找到有效的授权凭据或参数格式不正确，请重新扫描电脑端二维码。'}
            </Text>
            {onDone && (
              <Button onClick={onDone} variant="light">
                返回工作台
              </Button>
            )}
          </Stack>
        </Paper>
      </Center>
    )
  }

  // Not authenticated: explicitly require login first, do not auto-authorize
  if (!authenticated) {
    return (
      <Center style={{ minHeight: '100vh', padding: 16 }}>
        <Paper withBorder radius="lg" p="xl" maw={440} w="100%">
          <Stack gap="lg">
            <Group gap="xs" align="center">
              <IconLock size={24} color="#228be6" />
              <Title order={2} size="h3">扫码授权登录</Title>
            </Group>
            <Alert color="blue" icon={<IconAlertCircle size={16} />}>
              当前手机端尚未登录，请先登录此手机端，然后再确认授权电脑登录。
            </Alert>
            {loginError && (
              <Alert color="red" icon={<IconAlertCircle size={16} />}>
                {loginError}
              </Alert>
            )}
            <form onSubmit={handleLogin}>
              <Stack gap="md">
                <PasswordInput
                  label="访问令牌"
                  placeholder="输入访问令牌"
                  value={loginToken}
                  onChange={(e) => setLoginToken(e.currentTarget.value)}
                  leftSection={<IconKey size={16} />}
                  required
                />
                <Button type="submit" loading={loggingIn} disabled={!loginToken.trim()}>
                  登录并继续授权
                </Button>
              </Stack>
            </form>
          </Stack>
        </Paper>
      </Center>
    )
  }

  // Authenticated state: show inspect result and decision
  return (
    <Center style={{ minHeight: '100vh', padding: 16 }}>
      <Paper withBorder radius="lg" p="xl" maw={440} w="100%">
        <Stack gap="lg" align="center">
          <Group gap="xs" align="center">
            <IconDeviceDesktop size={26} color="#228be6" />
            <Title order={2} size="h3">电脑登录确认</Title>
          </Group>

          {inspecting && (
            <Stack align="center" gap="sm" py="md">
              <Loader size="md" />
              <Text size="sm" c="dimmed">正在核对电脑端凭据…</Text>
            </Stack>
          )}

          {inspectError && (
            <Stack align="center" gap="md" w="100%">
              <Alert color="yellow" icon={<IconAlertCircle size={16} />} w="100%">
                {inspectError}
              </Alert>
              {onDone && (
                <Button onClick={onDone} variant="light">
                  返回工作台
                </Button>
              )}
            </Stack>
          )}

          {decisionResult === 'approved' && (
            <Stack align="center" gap="md" w="100%">
              <IconCheck size={44} color="#40c057" />
              <Text size="md" fw={600}>已授权电脑端登录</Text>
              <Text size="sm" c="dimmed" ta="center">电脑端将自动完成登录并进入工作台。</Text>
              {onDone && (
                <Button onClick={onDone} variant="light" fullWidth>
                  返回工作台
                </Button>
              )}
            </Stack>
          )}

          {decisionResult === 'rejected' && (
            <Stack align="center" gap="md" w="100%">
              <IconX size={44} color="#868e96" />
              <Text size="md" fw={600}>已取消授权</Text>
              <Text size="sm" c="dimmed" ta="center">本次扫码登录请求已取消。</Text>
              {onDone && (
                <Button onClick={onDone} variant="light" fullWidth>
                  返回工作台
                </Button>
              )}
            </Stack>
          )}

          {!inspecting && !inspectError && !decisionResult && inspectData && (
            <Stack gap="md" w="100%">
              {inspectData.status === 'pending' ? (
                <>
                  <Paper withBorder p="md" radius="md" style={{ background: 'var(--mantine-color-gray-light)' }}>
                    <Stack gap="xs" align="center">
                      <Text size="xs" c="dimmed">确认码</Text>
                      <Text size="xl" fw={700} style={{ letterSpacing: 3 }}>
                        {inspectData.confirmation_code}
                      </Text>
                      <Text size="xs" c="dimmed">
                        申请设备：{inspectData.device || '星序工作台 (PC)'}
                      </Text>
                    </Stack>
                  </Paper>
                  <Text size="xs" c="dimmed" ta="center">
                    请仔细核对电脑网页上显示的确认码是否与上方一致。
                  </Text>
                  {actionError && (
                    <Alert color="red" icon={<IconAlertCircle size={16} />}>
                      {actionError}
                    </Alert>
                  )}
                  <Group grow mt="xs">
                    <Button
                      variant="light"
                      color="gray"
                      onClick={() => void handleDecision('reject')}
                      loading={actionLoading}
                    >
                      取消
                    </Button>
                    <Button
                      color="blue"
                      leftSection={<IconShieldCheck size={17} />}
                      onClick={() => void handleDecision('approve')}
                      loading={actionLoading}
                    >
                      确认授权
                    </Button>
                  </Group>
                </>
              ) : inspectData.status === 'expired' ? (
                <Stack align="center" gap="md">
                  <Alert color="yellow" icon={<IconAlertCircle size={16} />} w="100%">
                    该二维码已过期，请在电脑端重新生成。
                  </Alert>
                  {onDone && (
                    <Button onClick={onDone} variant="light" fullWidth>
                      返回工作台
                    </Button>
                  )}
                </Stack>
              ) : (
                <Stack align="center" gap="md">
                  <Alert color="yellow" icon={<IconAlertCircle size={16} />} w="100%">
                    该二维码已失效或已被处理。
                  </Alert>
                  {onDone && (
                    <Button onClick={onDone} variant="light" fullWidth>
                      返回工作台
                    </Button>
                  )}
                </Stack>
              )}
            </Stack>
          )}
        </Stack>
      </Paper>
    </Center>
  )
}