import { useEffect, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Paper, Text, Tooltip } from '@mantine/core'
import { IconClearAll, IconPlayerPlay, IconRefresh, IconTerminal2 } from '@tabler/icons-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { getBackendWsHost, getWsProtocol } from '../../desktop/adapter'

interface EmbeddedTerminalPaneProps {
  environmentId: string
  environmentLabel?: string
  command?: string
  autoSend?: boolean
  height?: number
  onVerify?: () => void
  verifying?: boolean
}

export function EmbeddedTerminalPane({
  environmentId,
  environmentLabel,
  command,
  autoSend = false,
  height = 320,
  onVerify,
  verifying = false,
}: EmbeddedTerminalPaneProps) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const [connected, setConnected] = useState(false)
  const [sentInitial, setSentInitial] = useState(false)

  const sendResize = () => {
    fitRef.current?.fit()
    const term = termRef.current
    const ws = wsRef.current
    if (ws?.readyState === WebSocket.OPEN && term?.cols && term.rows) {
      ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
    }
  }

  const detach = () => {
    const ws = wsRef.current
    wsRef.current = null
    if (ws) {
      ws.onopen = null
      ws.onmessage = null
      ws.onerror = null
      ws.onclose = null
      if (ws.readyState === WebSocket.OPEN) {
        try {
          ws.send(JSON.stringify({ type: 'close' }))
        } catch {}
      }
      ws.close()
    }
    termRef.current?.dispose()
    termRef.current = null
    fitRef.current = null
  }

  const sendCommand = (cmd?: string) => {
    const text = (cmd ?? command ?? '').trim()
    const ws = wsRef.current
    if (!text || !ws || ws.readyState !== WebSocket.OPEN) return
    ws.send(text + '\r')
    termRef.current?.focus()
  }

  const connect = () => {
    if (!containerRef.current) return
    detach()
    setConnected(false)
    setSentInitial(false)
    containerRef.current.innerHTML = ''

    const term = new Terminal({
      cursorBlink: true,
      fontFamily: 'Consolas, "Courier New", monospace',
      fontSize: 12.5,
      theme: {
        background: '#0d1117',
        foreground: '#e6edf3',
        cursor: '#58a6ff',
      },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(containerRef.current)
    fit.fit()

    termRef.current = term
    fitRef.current = fit

    term.writeln(`\x1b[36m=== 星序交互安装终端（${environmentLabel || environmentId}）===\x1b[0m`)
    if (command?.trim()) {
      term.writeln(`\x1b[90m预备命令: ${command.trim()}\x1b[0m`)
    }
    term.writeln('')

    let token: string | null = null
    try {
      if (typeof localStorage !== 'undefined' && typeof localStorage.getItem === 'function') {
        token = localStorage.getItem('astrorder:token')
      }
    } catch {}
    const params = new URLSearchParams({ environment_id: environmentId || 'local' })
    if (token) params.set('token', token)
    const wsUrl = `${getWsProtocol()}//${getBackendWsHost()}/ws/v1/terminal?${params.toString()}`

    try {
      const ws = new WebSocket(wsUrl)
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws

      ws.onopen = () => {
        if (wsRef.current !== ws) return
        setConnected(true)
        sendResize()
        term.focus()
        if (autoSend && command?.trim()) {
          setTimeout(() => {
            if (wsRef.current === ws && ws.readyState === WebSocket.OPEN) {
              ws.send(command.trim() + '\r')
              setSentInitial(true)
            }
          }, 350)
        }
      }
      ws.onmessage = (event) => {
        if (wsRef.current !== ws) return
        term.write(typeof event.data === 'string' ? event.data : new Uint8Array(event.data))
      }
      ws.onerror = () => {
        if (wsRef.current !== ws) return
        setConnected(false)
        term.writeln('\r\n\x1b[33m[终端连接异常，请点击重连或复制命令手动执行]\x1b[0m\r\n')
      }
      ws.onclose = () => {
        if (wsRef.current !== ws) return
        setConnected(false)
      }
      term.onData((data) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(data)
        }
      })
    } catch {
      term.writeln('\x1b[31m[无法建立终端连接]\x1b[0m\r\n')
    }
  }

  useEffect(() => {
    connect()
    const observer = new ResizeObserver(() => sendResize())
    if (containerRef.current) observer.observe(containerRef.current)
    window.addEventListener('resize', sendResize)
    return () => {
      observer.disconnect()
      window.removeEventListener('resize', sendResize)
      detach()
    }
  }, [environmentId])

  return (
    <Paper withBorder radius="md" style={{ overflow: 'hidden', background: '#0d1117' }}>
      <Group
        justify="space-between"
        px="xs"
        py={6}
        style={{ background: 'var(--astr-surface-muted)', borderBottom: '1px solid var(--astr-border)' }}
      >
        <Group gap="xs" wrap="nowrap">
          <IconTerminal2 size={15} style={{ color: 'var(--astr-indigo)' }} />
          <Text size="xs" fw={600}>
            {environmentLabel || environmentId}
          </Text>
          <Badge size="xs" variant="dot" color={connected ? 'teal' : 'gray'}>
            {connected ? '已连接' : '未连接'}
          </Badge>
        </Group>
        <Group gap={6} wrap="nowrap">
          {command?.trim() && (
            <Button
              size="compact-xs"
              variant="light"
              color="indigo"
              leftSection={<IconPlayerPlay size={12} />}
              disabled={!connected}
              onClick={() => {
                sendCommand(command)
                setSentInitial(true)
              }}
            >
              {sentInitial ? '再次发送命令' : '发送执行命令'}
            </Button>
          )}
          {onVerify && (
            <Button
              size="compact-xs"
              variant="light"
              color="teal"
              loading={verifying}
              onClick={onVerify}
            >
              重新检测
            </Button>
          )}
          <Tooltip label="清屏">
            <ActionIcon size="sm" variant="subtle" onClick={() => termRef.current?.clear()}>
              <IconClearAll size={14} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="重连终端">
            <ActionIcon size="sm" variant="subtle" onClick={() => connect()}>
              <IconRefresh size={14} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>
      <div ref={containerRef} style={{ height, width: '100%', padding: '6px 8px' }} />
    </Paper>
  )
}
