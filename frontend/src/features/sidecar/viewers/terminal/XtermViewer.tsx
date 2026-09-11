import { useEffect, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Paper, Text, Tooltip } from '@mantine/core'
import { IconClearAll, IconPlayerPlay, IconTerminal2 } from '@tabler/icons-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import type { ViewerContext } from '../../types'

export function XtermViewer({ artifact }: ViewerContext) {
  const terminalRef = useRef<HTMLDivElement>(null)
  const termInstance = useRef<Terminal | null>(null)
  const fitAddon = useRef<FitAddon | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const [connected, setConnected] = useState(false)

  const initTerminal = () => {
    if (!terminalRef.current) return
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
    fit.fit()

    termInstance.current = term
    fitAddon.current = fit

    term.writeln('\x1b[36m=== 星序交互式终端 (Astrorder Terminal) ===\x1b[0m')
    term.writeln(`\x1b[90m工作区目标: ${artifact.name || '默认环境'}\x1b[0m`)
    term.writeln('')

    // 连接真实后端 WebSocket 终端路由
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws/v1/terminal?session_id=${encodeURIComponent(artifact.sessionId)}`
    
    let ws: WebSocket
    try {
      ws = new WebSocket(wsUrl)
      wsRef.current = ws

      ws.onopen = () => {
        setConnected(true)
        // 挂载后立即发送视口大小
        if (term.cols && term.rows) {
          ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
        }
        // 自动聚焦
        term.focus()
      }

      ws.onmessage = (event) => {
        term.write(event.data)
      }

      ws.onerror = () => {
        setConnected(false)
        term.writeln('\r\n\x1b[33m[提示: 终端服务待连接]\x1b[0m\r\n$ ')
      }

      ws.onclose = () => {
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

    const handleResize = () => {
      fitAddon.current?.fit()
      if (wsRef.current?.readyState === WebSocket.OPEN && termInstance.current) {
        wsRef.current.send(
          JSON.stringify({
            type: 'resize',
            cols: termInstance.current.cols,
            rows: termInstance.current.rows,
          }),
        )
      }
    }
    window.addEventListener('resize', handleResize)

    return () => {
      window.removeEventListener('resize', handleResize)
      wsRef.current?.close()
      termInstance.current?.dispose()
    }
  }, [artifact.id])

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
