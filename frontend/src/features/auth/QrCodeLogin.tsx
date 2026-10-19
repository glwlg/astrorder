import { Alert, Button, Group, Loader, Paper, Stack, Text } from '@mantine/core'
import { IconAlertCircle, IconInfoCircle, IconRefresh } from '@tabler/icons-react'
import { QRCodeSVG } from 'qrcode.react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { api, ApiError, type QrCreateResponse } from '../../api/client'
import { buildQrCodeUrl } from './qrUtils'

export function QrCodeLogin({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [session, setSession] = useState<QrCreateResponse | null>(null)
  const [status, setStatus] = useState<'loading' | 'pending' | 'approved' | 'rejected' | 'expired' | 'error'>('loading')
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  const [pollError, setPollError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const isMountedRef = useRef(true)
  const pollTimerRef = useRef<number | null>(null)
  const expireTimerRef = useRef<number | null>(null)

  const isLocalHost = typeof window !== 'undefined' && (window.location.hostname === 'localhost' || window.location.hostname === '127.0.0.1')

  const clearTimers = () => {
    if (pollTimerRef.current !== null) {
      clearTimeout(pollTimerRef.current)
      pollTimerRef.current = null
    }
    if (expireTimerRef.current !== null) {
      clearTimeout(expireTimerRef.current)
      expireTimerRef.current = null
    }
  }

  const createSession = useCallback(async () => {
    clearTimers()
    setRefreshing(true)
    setErrorMessage(null)
    setPollError(null)
    setStatus('loading')
    try {
      const data = await api.createQrSession()
      if (!isMountedRef.current) return
      setSession(data)
      setStatus('pending')

      // Schedule local expiration if expires_at provided
      if (data.expires_at) {
        const msUntilExpire = new Date(data.expires_at).getTime() - Date.now()
        if (msUntilExpire <= 0) {
          setStatus('expired')
        } else {
          expireTimerRef.current = window.setTimeout(() => {
            if (isMountedRef.current) {
              clearTimers()
              setStatus('expired')
            }
          }, msUntilExpire)
        }
      }
    } catch (err) {
      if (!isMountedRef.current) return
      setStatus('error')
      setErrorMessage(err instanceof ApiError ? err.detail : '获取登录二维码失败，请检查网络或服务状态')
    } finally {
      if (isMountedRef.current) {
        setRefreshing(false)
      }
    }
  }, [])

  // StrictMode protection: defer initial create with setTimeout(0) so cleanup can cancel
  useEffect(() => {
    isMountedRef.current = true
    const initTimer = setTimeout(() => {
      void createSession()
    }, 0)

    return () => {
      isMountedRef.current = false
      clearTimeout(initTimer)
      clearTimers()
    }
  }, [createSession])

  // Polling loop
  useEffect(() => {
    if (status !== 'pending' || !session?.id) {
      if (pollTimerRef.current !== null) {
        clearTimeout(pollTimerRef.current)
        pollTimerRef.current = null
      }
      return
    }

    let active = true

    const runPoll = async () => {
      // Check local expiration before making poll request
      if (session.expires_at && Date.now() >= new Date(session.expires_at).getTime()) {
        if (active && isMountedRef.current) {
          setStatus('expired')
        }
        return
      }

      try {
        const res = await api.pollQrSession(session.id)
        if (!active || !isMountedRef.current) return

        setPollError(null)

        if (res.status === 'approved') {
          setStatus('approved')
          onAuthenticated()
          return
        }

        if (res.status === 'rejected') {
          setStatus('rejected')
          return
        }

        if (res.status === 'expired') {
          setStatus('expired')
          return
        }

        // Still pending: schedule next poll in 2000ms
        if (active && isMountedRef.current) {
          pollTimerRef.current = window.setTimeout(runPoll, 2000)
        }
      } catch (err) {
        if (!active || !isMountedRef.current) return

        if (err instanceof ApiError) {
          if (err.status === 410) {
            setStatus('expired')
            return
          }
          if (err.status === 401 || err.status === 403) {
            setStatus('rejected')
            return
          }
          setPollError(err.detail)
        } else {
          setPollError('轮询状态异常，请检查网络连接')
        }

        // Retry polling after slightly longer delay on non-fatal error
        if (active && isMountedRef.current) {
          pollTimerRef.current = window.setTimeout(runPoll, 3000)
        }
      }
    }

    pollTimerRef.current = window.setTimeout(runPoll, 2000)

    return () => {
      active = false
      if (pollTimerRef.current !== null) {
        clearTimeout(pollTimerRef.current)
        pollTimerRef.current = null
      }
    }
  }, [status, session?.id, session?.expires_at, onAuthenticated])

  const qrUrl = session ? buildQrCodeUrl(window.location.origin, session.id, session.scan_secret) : ''

  return (
    <Stack gap="md" align="center" style={{ width: '100%' }}>
      {isLocalHost && (
        <Alert
          color="blue"
          icon={<IconInfoCircle size={17} />}
          title="局域网访问提醒"
          styles={{ root: { width: '100%' } }}
        >
          请使用手机可访问的局域网地址或域名打开电脑网页
        </Alert>
      )}

      {status === 'loading' && (
        <Paper withBorder p="xl" radius="md" style={{ width: 220, height: 220, display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
          <Stack align="center" gap="sm">
            <Loader size="md" />
            <Text size="xs" c="dimmed">正在生成二维码…</Text>
          </Stack>
        </Paper>
      )}

      {status === 'pending' && session && (
        <Stack align="center" gap="xs">
          <Paper withBorder p="sm" radius="md" style={{ background: '#ffffff', lineHeight: 0 }}>
            <QRCodeSVG value={qrUrl} size={196} marginSize={2} />
          </Paper>
          <Group gap="xs" mt={4}>
            <Text size="sm" c="dimmed">确认码：</Text>
            <Text size="lg" fw={700} style={{ letterSpacing: 2 }}>{session.confirmation_code}</Text>
          </Group>
          <Text size="xs" c="dimmed" ta="center">
            请使用已登录的手机端扫描二维码授权登录
          </Text>
          {pollError && (
            <Alert color="yellow" variant="light" icon={<IconAlertCircle size={16} />}>
              {pollError}
            </Alert>
          )}
        </Stack>
      )}

      {status === 'expired' && (
        <Paper withBorder p="lg" radius="md" style={{ width: '100%' }}>
          <Stack align="center" gap="sm">
            <IconAlertCircle size={32} color="#f59f00" />
            <Text size="sm" fw={500}>二维码已过期</Text>
            <Button
              size="xs"
              variant="light"
              leftSection={<IconRefresh size={14} />}
              onClick={() => void createSession()}
              disabled={refreshing}
            >
              刷新二维码
            </Button>
          </Stack>
        </Paper>
      )}

      {status === 'rejected' && (
        <Paper withBorder p="lg" radius="md" style={{ width: '100%' }}>
          <Stack align="center" gap="sm">
            <IconAlertCircle size={32} color="#fa5252" />
            <Text size="sm" fw={500}>授权已失效或被手机端取消</Text>
            <Button
              size="xs"
              variant="light"
              leftSection={<IconRefresh size={14} />}
              onClick={() => void createSession()}
              disabled={refreshing}
            >
              重新获取二维码
            </Button>
          </Stack>
        </Paper>
      )}

      {status === 'error' && (
        <Paper withBorder p="lg" radius="md" style={{ width: '100%' }}>
          <Stack align="center" gap="sm">
            <IconAlertCircle size={32} color="#fa5252" />
            <Text size="sm" c="red">{errorMessage || '二维码获取失败'}</Text>
            <Button
              size="xs"
              variant="light"
              leftSection={<IconRefresh size={14} />}
              onClick={() => void createSession()}
              disabled={refreshing}
            >
              重试
            </Button>
          </Stack>
        </Paper>
      )}
    </Stack>
  )
}