import { useEffect, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Paper, Text, Tooltip } from '@mantine/core'
import { IconClearAll, IconPlayerPlay, IconTerminal2 } from '@tabler/icons-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import type { ViewerContext } from '../../types'
import { useSidecarStore } from '../../sidecarStore'
import { getBackendWsHost, getWsProtocol } from '../../../../desktop/adapter'

export function XtermViewer({ artifact, isActive = true }: ViewerContext) {
  const terminalRef = useRef<HTMLDivElement>(null)
  const termInstance = useRef<Terminal | null>(null)
  const fitAddon = useRef<FitAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const [connected, setConnected] = useState(false)
  const activeRef = useRef(isActive)
  activeRef.current = isActive

  const resizeTerminal = () => {
    if (!activeRef.current) return
    fitAddon.current?.fit()
    const term = termInstance.current
    const socket = wsRef.current
    if (socket?.readyState === WebSocket.OPEN && term?.cols && term.rows) {
      socket.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
    }
  }
  const detachTerminal = () => {
    const socket = wsRef.current
    wsRef.current = null
    if (socket) {
      socket.onopen = null
      socket.onmessage = null
      socket.onerror = null
      socket.onclose = null
      socket.close()
    }
    termInstance.current?.dispose()
    termInstance.current = null
    fitAddon.current = null
  }
  const focusTerminal = () => {
    if (activeRef.current && !window.matchMedia('(pointer: coarse)').matches) termInstance.current?.focus()
  }

  const initTerminal = () => {
    if (!terminalRef.current) return
    detachTerminal()
    setConnected(false)
    terminalRef.current.innerHTML = ''

    const term = new Terminal({
      cursorBlink: true,
      fontFamily: 'Consolas, "Courier New", monospace',
      fontSize: 13,
      theme: {
        background: '#121316',
        foreground: '#e6edf3',
        cursor: '#58a6ff',
      },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(terminalRef.current)
    if (activeRef.current) fit.fit()

    termInstance.current = term
    fitAddon.current = fit

    term.writeln('\x1b[36m=== 星序交互式终端 (Astrorder Terminal) ===\x1b[0m')
    term.writeln(`\x1b[90m工作区目标: ${artifact.name || '默认环境'}\x1b[0m`)
    term.writeln('')

    // 连接真实后端 WebSocket 终端路由
    const protocol = getWsProtocol()
    let localToken: string | null = null
    try {
      if (typeof localStorage !== 'undefined' && typeof localStorage.getItem === 'function') {
        localToken = localStorage.getItem('astrorder:token')
      }
    } catch {}
    const tokenParam = localToken ? `&token=${encodeURIComponent(localToken)}` : ''
    const wsUrl = `${protocol}//${getBackendWsHost()}/ws/v1/terminal?session_id=${encodeURIComponent(artifact.sessionId)}&agent_id=${encodeURIComponent(artifact.agentId)}${tokenParam}`
    
    let ws: WebSocket
    try {
      ws = new WebSocket(wsUrl)
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws

      ws.onopen = () => {
        if (wsRef.current !== ws) return
        setConnected(true)
        resizeTerminal()
        focusTerminal()
      }

      ws.onmessage = (event) => {
        if (wsRef.current !== ws) return
        term.write(typeof event.data === 'string' ? event.data : new Uint8Array(event.data))
      }

      ws.onerror = () => {
        if (wsRef.current !== ws) return
        setConnected(false)
        term.writeln('\r\n\x1b[33m[提示: 终端服务待连接]\x1b[0m\r\n$ ')
      }

      ws.onclose = () => {
        if (wsRef.current !== ws) return
        setConnected(false)
      }

      // 监听用户输入并发送到真实 PTY / 子进程
      term.onData((data) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(data)
        }
      })
    } catch {
      term.writeln('\x1b[31m[无法连接终端服务]\x1b[0m\r\n')
    }
  }

  useEffect(() => {
    initTerminal()

    const handleResize = () => resizeTerminal()
    const observer = new ResizeObserver(handleResize)
    if (terminalRef.current) observer.observe(terminalRef.current)
    window.addEventListener('resize', handleResize)

    return () => {
      observer.disconnect()
      window.removeEventListener('resize', handleResize)
      detachTerminal()
    }
  }, [artifact.id, artifact.agentId, artifact.sessionId])

  useEffect(() => useSidecarStore.getState().registerTabCloseHandler(
    artifact.agentId,
    artifact.id,
    () => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(JSON.stringify({ type: 'close' }))
      }
    },
  ), [artifact.agentId, artifact.id])

  useEffect(() => {
    if (!isActive) return
    resizeTerminal()
    focusTerminal()
  }, [isActive])

  const handleClear = () => {
    termInstance.current?.clear()
  }

  return (
    <div className="xterm-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap">
          <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
            <IconTerminal2 size={16} style={{ color: 'var(--astr-indigo)' }} />
            <Text size="xs" fw={600} truncate title={artifact.name}>
              {artifact.name}
            </Text>
            <Badge size="xs" variant="dot" color={connected ? 'green' : 'gray'}>
              {connected ? '在线' : '离线终端'}
            </Badge>
          </Group>
          <Group gap={6} wrap="nowrap">
            <Tooltip label="清空屏幕">
              <ActionIcon variant="subtle" size="sm" onClick={handleClear}>
                <IconClearAll size={15} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label="重新挂载">
              <Button
                size="compact-xs"
                variant="subtle"
                leftSection={<IconPlayerPlay size={12} />}
                onClick={initTerminal}
              >
                重连
              </Button>
            </Tooltip>
          </Group>
        </Group>
      </Paper>

      <div
        ref={terminalRef}
        style={{
          flex: 1,
          minHeight: 0,
          background: '#121316',
          padding: 8,
          overflow: 'hidden',
        }}
      />
    </div>
  )
}
