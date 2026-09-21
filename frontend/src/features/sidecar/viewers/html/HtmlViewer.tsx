import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ActionIcon, Group, LoadingOverlay, Paper, SegmentedControl, TextInput, Tooltip } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowRight,
  IconDeviceDesktop,
  IconExternalLink,
  IconRefresh,
  IconShieldLock,
  IconWorld,
} from '@tabler/icons-react'
import type { ViewerContext } from '../../types'

export function HtmlViewer({ artifact, toolbarAction }: ViewerContext) {
  const iframeRef = useRef<HTMLIFrameElement>(null)
  const [loading, setLoading] = useState(true)
  const [urlInput, setUrlInput] = useState(artifact.readUrl || 'about:blank')
  const [currentUrl, setCurrentUrl] = useState(artifact.readUrl || 'about:blank')
  // 模式选择：'proxy' (免跨域反向代理，默认) 或 'direct' (直连)
  const [mode, setMode] = useState<'proxy' | 'direct'>('proxy')

  useEffect(() => {
    const raw = artifact.readUrl || 'about:blank'
    setUrlInput(raw)
    setCurrentUrl(raw)
  }, [artifact.readUrl])

  const handleNavigate = (targetUrl?: string) => {
    let toGo = (targetUrl !== undefined ? targetUrl : urlInput).trim()
    if (!toGo) return
    if (
      !toGo.startsWith('http://') &&
      !toGo.startsWith('https://') &&
      !toGo.startsWith('/') &&
      toGo !== 'about:blank'
    ) {
      toGo = `http://${toGo}`
    }
    setUrlInput(toGo)
    setCurrentUrl(toGo)
    setLoading(true)
  }

  // 计算最终给 iframe 的 src 地址
  const effectiveIframeSrc = useMemo(() => {
    if (!currentUrl || currentUrl === 'about:blank') return 'about:blank'
    // 如果是内部路由或本地相对地址，直接渲染
    if (currentUrl.startsWith('/') || currentUrl.startsWith('blob:') || currentUrl.startsWith('data:')) {
      return currentUrl
    }
    // 代理模式：剥离 X-Frame-Options 和 CSP，支持大部分网站在 iframe 顺利嵌入
    if (mode === 'proxy') {
      return `/api/v1/browser/proxy?url=${encodeURIComponent(currentUrl)}`
    }
    return currentUrl
  }, [currentUrl, mode])

  const handleRefresh = useCallback(() => {
    if (iframeRef.current) {
      setLoading(true)
      const base = effectiveIframeSrc
      const sep = base.includes('?') ? '&' : '?'
      iframeRef.current.src = `${base}${sep}_t=${Date.now()}`
    }
  }, [effectiveIframeSrc])

  // 调用后端唤起系统设备原生浏览器（Edge/Chrome/默认浏览器）
  const handleOpenDeviceBrowser = async () => {
    let target = currentUrl.trim()
    if (!target || target === 'about:blank') return
    if (!target.startsWith('http://') && !target.startsWith('https://') && !target.startsWith('/') && target !== 'about:blank') {
      target = `http://${target}`
    }
    try {
      let localToken: string | null = null
      try {
        if (typeof localStorage !== 'undefined') {
          localToken = localStorage.getItem('astrorder:token')
        }
      } catch {}
      const headers: Record<string, string> = { 'Content-Type': 'application/json' }
      if (localToken) {
        headers['Authorization'] = `Bearer ${localToken}`
      }
      const resp = await fetch('/api/v1/system/open-browser', {
        method: 'POST',
        headers,
        body: JSON.stringify({ url: target }),
      })
      if (!resp.ok) {
        const data = await resp.json().catch(() => ({}))
        notifications.show({
          color: 'red',
          message: data?.detail || '唤起系统设备浏览器失败',
        })
      }
    } catch (err: unknown) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '网络通信异常',
      })
    }
  }

  return (
    <div className="html-viewer-pane" style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <Paper p="xs" withBorder style={{ borderBottom: '1px solid var(--astr-border)', borderRadius: 0 }}>
        <Group justify="space-between" wrap="nowrap" gap="xs">
          {/* 刷新按钮 */}
          <ActionIcon
            variant="subtle"
            size="sm"
            title="刷新页面"
            onClick={handleRefresh}
          >
            <IconRefresh size={14} />
          </ActionIcon>

          {/* URL 输入框 */}
          <TextInput
            size="xs"
            leftSection={<IconWorld size={14} color="var(--astr-muted)" />}
            rightSection={
              <ActionIcon size="xs" variant="subtle" onClick={() => handleNavigate()}>
                <IconArrowRight size={12} />
              </ActionIcon>
            }
            value={urlInput}
            onChange={(e) => setUrlInput(e.currentTarget.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                handleNavigate()
              }
            }}
            placeholder="输入 URL，例如 http://localhost:3000 或 https://github.com"
            style={{ flex: 1 }}
          />

          {/* 模式选择：免跨域代理 vs 直连 */}
          <Tooltip label="代理模式通过后端剥离跨域限制头；直连模式保持原生加载">
            <SegmentedControl
              size="xs"
              value={mode}
              onChange={(val) => {
                setMode(val as 'proxy' | 'direct')
                setLoading(true)
              }}
              data={[
                {
                  label: (
                    <Group gap={4} wrap="nowrap">
                      <IconShieldLock size={12} />
                      <span>免跨域</span>
                    </Group>
                  ),
                  value: 'proxy',
                },
                { label: '直连', value: 'direct' },
              ]}
            />
          </Tooltip>

          <Group gap={4} wrap="nowrap">
            {toolbarAction}
            {/* 思路 1：唤起宿主设备原生浏览器 */}
            <Tooltip label="在电脑设备默认浏览器中打开（原生 Edge / Chrome / Safari）">
              <ActionIcon
                variant="subtle"
                size="sm"
                color="blue"
                title="在设备浏览器中打开"
                onClick={handleOpenDeviceBrowser}
              >
                <IconDeviceDesktop size={15} />
              </ActionIcon>
            </Tooltip>

            {/* 新标签页打开 */}
            <Tooltip label="在当前浏览器新标签页中打开">
              <ActionIcon
                variant="subtle"
                size="sm"
                title="新标签页打开"
                onClick={() => window.open(currentUrl, '_blank')}
              >
                <IconExternalLink size={14} />
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </Paper>

      <div style={{ flex: 1, position: 'relative', minHeight: 0, background: '#fff' }}>
        <LoadingOverlay visible={loading} />
        <iframe
          ref={iframeRef}
          src={effectiveIframeSrc}
          style={{ width: '100%', height: '100%', border: 'none' }}
          onLoad={() => setLoading(false)}
          sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals"
          title={artifact.name || '内置浏览器'}
        />
      </div>
    </div>
  )
}
