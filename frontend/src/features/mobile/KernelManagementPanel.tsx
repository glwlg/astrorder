import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, Badge, Button, Group, Paper, Stack, Text } from '@mantine/core'
import { IconAlertCircle, IconCheck, IconCpu, IconRefresh, IconServer } from '@tabler/icons-react'
import {
  api,
  type KernelAction,
  type KernelOperationCreatedResponse,
  type KernelStatusResponse,
  type KernelTarget,
} from '../../api/client'
import { ConfirmPopover } from '../../components/ConfirmPopover'
import {
  confirmationCoordinatesFromEvent,
  type ConfirmationCoordinates,
} from '../../components/confirmationPosition'

function safeRemoveStorage(key: string) {
  try {
    sessionStorage.removeItem(key)
  } catch {
    // Ignore storage errors when restricted or unavailable
  }
}

function safeSetStorage(key: string, value: string) {
  try {
    sessionStorage.setItem(key, value)
  } catch {
    // Ignore storage errors so accepted operation continues
  }
}

const STORAGE_KEY = 'astrorder:kernel-active-operation'

function formatCreationTime(time?: number | string): string | null {
  if (time === undefined || time === null || time === '') return null
  try {
    const raw = typeof time === 'number' ? Math.floor(time) : String(time).trim()
    const bn = BigInt(raw)
    if (bn <= 0n) return null
    // Windows FILETIME: 100-nanosecond intervals since January 1, 1601 UTC
    if (bn > 10000000000000000n) {
      const ms = Number((bn - 116444736000000000n) / 10000n)
      const date = new Date(ms)
      if (!isNaN(date.getTime()) && date.getFullYear() >= 2020 && date.getFullYear() <= 2100) {
        return date.toLocaleTimeString()
      }
    }
    if (bn > 1000000000000n && bn < 3000000000000n) {
      return new Date(Number(bn)).toLocaleTimeString()
    }
    if (bn > 1000000000n && bn < 3000000000n) {
      return new Date(Number(bn) * 1000).toLocaleTimeString()
    }
  } catch {
    // Ignore parse errors and display PID only
  }
  return null
}

interface ActiveOperation {
  id: string
  target: KernelTarget
  action: KernelAction
  startTime: number
}

interface ConfirmationState {
  target: KernelTarget
  action: KernelAction
  coords: ConfirmationCoordinates
  title: string
  message: string
  confirmLabel: string
  confirmColor?: string
}

export interface KernelManagementPanelProps {
  pollIntervalMs?: number
  timeoutMs?: number
}

export function KernelManagementPanel({
  pollIntervalMs = 2000,
  timeoutMs = 180000,
}: KernelManagementPanelProps) {
  const [status, setStatus] = useState<KernelStatusResponse | null>(null)
  const [loadingStatus, setLoadingStatus] = useState(false)
  const [statusError, setStatusError] = useState<string | null>(null)

  const [activeOp, setActiveOp] = useState<ActiveOperation | null>(() => {
    try {
      const raw = sessionStorage.getItem(STORAGE_KEY)
      if (!raw) return null
      const parsed = JSON.parse(raw) as ActiveOperation
      if (parsed?.id && parsed?.target && parsed?.action && parsed?.startTime) {
        return parsed
      }
    } catch {
      safeRemoveStorage(STORAGE_KEY)
    }
    return null
  })

  const [confirmation, setConfirmation] = useState<ConfirmationState | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [opMessage, setOpMessage] = useState<string | null>(null)
  const [opError, setOpError] = useState<string | null>(null)

  const activeOpRef = useRef<ActiveOperation | null>(activeOp)
  const pollingInFlightRef = useRef(false)

  useEffect(() => {
    activeOpRef.current = activeOp
  }, [activeOp])

  const fetchStatus = useCallback(async () => {
    setLoadingStatus(true)
    setStatusError(null)
    try {
      const data = await api.getKernelStatus()
      setStatus(data)
    } catch (err) {
      setStatus(null)
      setStatusError(err instanceof Error ? err.message : '获取内核状态失败')
    } finally {
      setLoadingStatus(false)
    }
  }, [])

  useEffect(() => {
    void fetchStatus()
  }, [fetchStatus])

  useEffect(() => {
    if (!activeOp) return

    let timer: ReturnType<typeof setInterval> | null = null
    let cancelled = false

    const poll = async () => {
      const current = activeOpRef.current
      if (!current || cancelled || pollingInFlightRef.current) return
      pollingInFlightRef.current = true

      const isExpired = Date.now() - current.startTime > timeoutMs

      try {
        const res = await api.getKernelOperation(current.id)
        if (cancelled) return

        if (res.status === 'queued') {
          if (isExpired) {
            if (timer) clearInterval(timer)
            safeRemoveStorage(STORAGE_KEY)
            setActiveOp(null)
            setOpMessage(null)
            setOpError('操作结果超时未确认，请检查服务连接。系统未自动重发请求。')
            return
          }
          setOpMessage('操作已排队，等待执行…')
        } else if (res.status === 'running') {
          if (isExpired) {
            if (timer) clearInterval(timer)
            safeRemoveStorage(STORAGE_KEY)
            setActiveOp(null)
            setOpMessage(null)
            setOpError('操作结果超时未确认，请检查服务连接。系统未自动重发请求。')
            return
          }
          setOpMessage('正在执行内核操作…')
        } else if (res.status === 'success') {
          if (timer) clearInterval(timer)
          safeRemoveStorage(STORAGE_KEY)
          setActiveOp(null)
          setOpMessage('操作已成功完成。')
          setOpError(null)
          void fetchStatus()
        } else if (res.status === 'failed') {
          if (timer) clearInterval(timer)
          safeRemoveStorage(STORAGE_KEY)
          setActiveOp(null)
          setOpMessage(null)
          setOpError(res.detail || res.error || '内核操作执行失败。')
        }
      } catch {
        if (cancelled) return
        if (isExpired) {
          if (timer) clearInterval(timer)
          safeRemoveStorage(STORAGE_KEY)
          setActiveOp(null)
          setOpMessage(null)
          setOpError('操作结果超时未确认，请检查服务连接。系统未自动重发请求。')
          return
        }
        setOpMessage('正在等待服务重新就绪…')
      } finally {
        pollingInFlightRef.current = false
      }
    }

    const initialTimer = setTimeout(() => {
      void poll()
    }, 0)
    timer = setInterval(() => {
      void poll()
    }, pollIntervalMs)

    return () => {
      cancelled = true
      clearTimeout(initialTimer)
      if (timer) clearInterval(timer)
    }
  }, [activeOp, fetchStatus, pollIntervalMs, timeoutMs])

  const requestAction = (
    target: KernelTarget,
    action: KernelAction,
    event: React.MouseEvent<HTMLButtonElement>,
  ) => {
    const coords = confirmationCoordinatesFromEvent(event)

    if (target === 'daemon') {
      const isUpdate = action === 'update'
      const title = isUpdate ? '更新并重启小内核？' : '重启小内核？'
      const confirmLabel = isUpdate ? '确认更新并重启' : '确认重启'

      const activeSessions = status?.daemon?.active_sessions
      const activeCount =
        activeSessions && typeof activeSessions === 'object'
          ? Object.keys(activeSessions).length
          : undefined

      let detail = ''
      if (typeof activeCount === 'number') {
        if (activeCount > 0) {
          detail = `\n当前检测到有 ${activeCount} 个活动会话，将被强制中断。`
        } else {
          detail = '\n当前无活动会话。'
        }
      } else {
        detail = '\n当前无法确认活动会话状态，继续操作可能会中断正在运行的任务。'
      }

      const message = `重启小内核会中断当前进行中的会话与终端进程。${detail}`

      setConfirmation({
        target,
        action,
        coords,
        title,
        message,
        confirmLabel,
        confirmColor: 'red',
      })
    } else {
      setConfirmation({
        target,
        action,
        coords,
        title: '重启大内核？',
        message:
          '重启大内核会导致客户端与服务短暂断开连接，随后自动恢复。小内核及其托管的会话将继续运行，不受影响。',
        confirmLabel: '确认重启',
        confirmColor: 'orange',
      })
    }
  }

  const handleConfirm = async () => {
    if (!confirmation) return
    setSubmitting(true)
    setOpError(null)
    setOpMessage(null)
    const { target, action } = confirmation

    try {
      const created: KernelOperationCreatedResponse = await api.createKernelOperation({
        target,
        action,
        confirm_active: true,
      })
      const newOp: ActiveOperation = {
        id: created.id,
        target,
        action,
        startTime: Date.now(),
      }
      safeSetStorage(STORAGE_KEY, JSON.stringify(newOp))
      setActiveOp(newOp)
      setOpMessage('操作已排队，等待执行…')
    } catch (err) {
      setOpError(err instanceof Error ? err.message : '提交操作失败')
    } finally {
      setSubmitting(false)
      setConfirmation(null)
    }
  }

  const handleCancel = () => {
    if (!submitting) {
      setConfirmation(null)
    }
  }

  const isInFlight = submitting || activeOp !== null
  const updateAvailable = !statusError && status?.update ? Boolean(status.update.update_available) : null

  const daemonActiveCount =
    status?.daemon?.active_sessions && typeof status.daemon.active_sessions === 'object'
      ? Object.keys(status.daemon.active_sessions).length
      : null

  const appCreationTime = formatCreationTime(status?.app?.process?.creation_time)
  const daemonCreationTime = formatCreationTime(status?.daemon?.process?.creation_time)

  return (
    <Stack gap="md" p="xs">
      <Group justify="space-between" align="center">
        <Text fw={600} size="sm">
          内核运行时状态
        </Text>
        <Button
          variant="subtle"
          size="compact-xs"
          leftSection={<IconRefresh size={14} />}
          onClick={() => void fetchStatus()}
          loading={loadingStatus}
        >
          刷新
        </Button>
      </Group>

      {statusError && (
        <Alert
          icon={<IconAlertCircle size={16} />}
          color="red"
          variant="light"
          title="状态获取失败"
        >
          {statusError}
        </Alert>
      )}

      {opError && (
        <Alert
          role="alert"
          icon={<IconAlertCircle size={16} />}
          color="red"
          variant="light"
          title="操作失败"
        >
          {opError}
        </Alert>
      )}

      {opMessage && (
        <Alert
          role="status"
          icon={<IconCheck size={16} />}
          color="teal"
          variant="light"
        >
          {opMessage}
        </Alert>
      )}

      <Paper withBorder p="sm" radius="md">
        <Group justify="space-between" mb={6}>
          <Group gap={6}>
            <IconServer size={16} />
            <Text fw={500} size="sm">
              大内核
            </Text>
          </Group>
          <Badge color={status?.app?.healthy ? 'teal' : 'red'} size="sm" variant="light">
            {status?.app?.healthy ? '运行正常' : status?.app?.state || '状态未知'}
          </Badge>
        </Group>
        <Text size="xs" c="dimmed">
          进程 PID：{status?.app?.process?.pid ?? '未知'}
          {appCreationTime ? ` · 启动时间：${appCreationTime}` : ''}
        </Text>
      </Paper>

      <Paper withBorder p="sm" radius="md">
        <Group justify="space-between" mb={6}>
          <Group gap={6}>
            <IconCpu size={16} />
            <Text fw={500} size="sm">
              小内核
            </Text>
          </Group>
          <Badge
            color={status?.daemon?.state === 'running' ? 'teal' : 'gray'}
            size="sm"
            variant="light"
          >
            {status?.daemon?.state === 'running' ? '运行正常' : status?.daemon?.state || '状态未知'}
          </Badge>
        </Group>
        <Text size="xs" c="dimmed">
          进程 PID：{status?.daemon?.process?.pid ?? '未知'}
          {daemonActiveCount !== null
            ? ` · 活动会话：${daemonActiveCount}`
            : ' · 活动会话：未知'}
          {daemonCreationTime ? ` · 启动时间：${daemonCreationTime}` : ''}
        </Text>
      </Paper>

      <Paper withBorder p="sm" radius="md">
        <Group justify="space-between">
          <Text size="sm">小内核更新状态</Text>
          <Badge
            color={updateAvailable === true ? 'blue' : 'gray'}
            size="sm"
            variant="light"
          >
            {updateAvailable === null
              ? '未确认'
              : updateAvailable
                ? '有可用更新'
                : '无可用更新'}
          </Badge>
        </Group>
      </Paper>

      <Stack gap="xs" mt="xs">
        <Button
          color="blue"
          disabled={isInFlight || updateAvailable !== true}
          onClick={(e) => requestAction('daemon', 'update', e)}
        >
          更新并重启小内核
        </Button>
        <Button
          color="red"
          variant="light"
          disabled={isInFlight || loadingStatus}
          onClick={(e) => requestAction('daemon', 'restart', e)}
        >
          重启小内核
        </Button>
        <Button
          color="orange"
          variant="light"
          disabled={isInFlight || loadingStatus}
          onClick={(e) => requestAction('app', 'restart', e)}
        >
          重启大内核
        </Button>
      </Stack>

      <ConfirmPopover
        opened={confirmation !== null}
        coords={confirmation?.coords}
        title={confirmation?.title || ''}
        message={confirmation?.message || ''}
        confirmLabel={confirmation?.confirmLabel || '确认'}
        confirmColor={confirmation?.confirmColor || 'red'}
        loading={submitting}
        onConfirm={handleConfirm}
        onCancel={handleCancel}
      />
    </Stack>
  )
}
