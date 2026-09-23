import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Alert, Badge, Center, Group, Loader, Modal, Paper, ScrollArea, Text, TextInput, Tooltip } from '@mantine/core'
import {
  IconArrowLeft,
  IconArrowRight,
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconLock,
  IconMaximize,
  IconMessagePlus,
  IconPlus,
  IconPointer,
  IconRefresh,
  IconTerminal2,
  IconTrash,
  IconWorld,
  IconX,
  IconZoomIn,
  IconZoomOut,
} from '@tabler/icons-react'
import { ApiError, api, type BrowserLog, type BrowserSnapshot } from '../../../../api/client'
import { useAstrorderStore } from '../../../../state/store'
import type { ViewerContext } from '../../types'

export function BrowserMirrorPanel({ agentId, sessionId, active = true }: { agentId: string; sessionId: string; active?: boolean }) {
  const [snapshot, setSnapshot] = useState<BrowserSnapshot | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [preview, setPreview] = useState(false)
  const [zoom, setZoom] = useState(1)
  const [urlInput, setUrlInput] = useState('')
  const [interactive, setInteractive] = useState(true)
  const [interacting, setInteracting] = useState(false)
  const [quoting, setQuoting] = useState(false)
  const [quotedSuccess, setQuotedSuccess] = useState(false)
  const [showLogs, setShowLogs] = useState(false)
  const [logs, setLogs] = useState<BrowserLog[]>([])
  const [loadingLogs, setLoadingLogs] = useState(false)
  const wheelTimeoutRef = useRef<number | null>(null)

  const load = useCallback(async (refresh: boolean, targetId?: string) => {
    setLoading(true)
    setError('')
    try {
      const next = await api.getBrowserScreenshot(agentId, sessionId, refresh, targetId)
      setSnapshot(next)
      setUrlInput(next.url === 'about:blank' ? '' : next.url)
    } catch (value) {
      if (value instanceof ApiError && (value.status === 404 || value.status === 409)) {
        setSnapshot(null)
      } else {
        setError(value instanceof Error ? value.message : '截图加载失败')
      }
    } finally {
      setLoading(false)
    }
  }, [agentId, sessionId])

  const loadLogs = useCallback(async () => {
    setLoadingLogs(true)
    try {
      const data = await api.getBrowserDiagnostics(agentId, sessionId)
      setLogs(data.logs || [])
    } catch {
      // 忽略日志读取失败
    } finally {
      setLoadingLogs(false)
    }
  }, [agentId, sessionId])

  useEffect(() => {
    if (active) void load(true)
  }, [active, load])

  useEffect(() => {
    if (showLogs) void loadLogs()
  }, [showLogs, loadLogs])

  useEffect(() => {
    const update = (event: Event) => {
      const detail = (event as CustomEvent<{ session_key?: string }>).detail
      if (detail?.session_key === `${agentId}::${sessionId}`) {
        void load(false)
        if (showLogs) void loadLogs()
      }
    }
    window.addEventListener('astrorder:browser-mirror-updated', update)
    return () => window.removeEventListener('astrorder:browser-mirror-updated', update)
  }, [agentId, load, loadLogs, sessionId, showLogs])

  const changeZoom = (next: number) => setZoom(Math.max(0.5, Math.min(3, next)))
  const source = snapshot?.screenshot ? `data:${snapshot.mime_type};base64,${snapshot.screenshot}` : ''

  const handleGoBack = async () => {
    setLoading(true)
    try {
      const next = await api.interactBrowser(agentId, sessionId, 'back')
      setSnapshot(next)
      setUrlInput(next.url === 'about:blank' ? '' : next.url)
    } catch (err) {
      setError(err instanceof Error ? err.message : '后退失败')
    } finally {
      setLoading(false)
    }
  }

  const handleGoForward = async () => {
    setLoading(true)
    try {
      const next = await api.interactBrowser(agentId, sessionId, 'forward')
      setSnapshot(next)
      setUrlInput(next.url === 'about:blank' ? '' : next.url)
    } catch (err) {
      setError(err instanceof Error ? err.message : '前进失败')
    } finally {
      setLoading(false)
    }
  }

  const navigate = async () => {
    let target = urlInput.trim()
    if (!target) return
    if (!/^https?:\/\//i.test(target)) target = `https://${target}`
    setLoading(true)
    setError('')
    try {
      const next = await api.navigateBrowser(agentId, sessionId, target)
      setSnapshot(next)
      setUrlInput(next.url)
    } catch (value) {
      setError(value instanceof Error ? value.message : '网页打开失败')
    } finally {
      setLoading(false)
    }
  }

  const handleSelectTab = async (targetId: string) => {
    if (targetId === snapshot?.active_target_id) return
    setLoading(true)
    setError('')
    try {
      const next = await api.selectBrowserTab(agentId, sessionId, targetId)
      setSnapshot(next)
      setUrlInput(next.url === 'about:blank' ? '' : next.url)
    } catch (value) {
      setError(value instanceof Error ? value.message : '切换标签页失败')
    } finally {
      setLoading(false)
    }
  }

  const handleNewTab = async () => {
    setLoading(true)
    setError('')
    try {
      const next = await api.newBrowserTab(agentId, sessionId)
      setSnapshot(next)
      setUrlInput('')
    } catch (value) {
      setError(value instanceof Error ? value.message : '新建标签页失败')
    } finally {
      setLoading(false)
    }
  }

  const handleCloseTab = async (targetId: string, event: React.MouseEvent) => {
    event.stopPropagation()
    setLoading(true)
    setError('')
    try {
      const next = await api.closeBrowserTab(agentId, sessionId, targetId)
      setSnapshot(next.tabs && next.tabs.length > 0 ? next : null)
      setUrlInput(next.url === 'about:blank' ? '' : next.url)
    } catch (value) {
      setError(value instanceof Error ? value.message : '关闭标签页失败')
    } finally {
      setLoading(false)
    }
  }

  const handleImageClick = async (event: React.MouseEvent<HTMLImageElement>) => {
    if (!interactive) {
      setZoom(1)
      setPreview(true)
      return
    }
    const img = event.currentTarget
    const rect = img.getBoundingClientRect()
    const ratio_x = Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width))
    const ratio_y = Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height))
    setInteracting(true)
    try {
      const next = await api.interactBrowser(agentId, sessionId, 'click', { ratio_x, ratio_y })
      setSnapshot(next)
      setUrlInput(next.url === 'about:blank' ? '' : next.url)
    } catch (err) {
      setError(err instanceof Error ? err.message : '交互执行失败')
    } finally {
      setInteracting(false)
    }
  }

  const handleImageWheel = (event: React.WheelEvent<HTMLDivElement>) => {
    if (!interactive) return
    const deltaY = event.deltaY
    if (wheelTimeoutRef.current) window.clearTimeout(wheelTimeoutRef.current)
    wheelTimeoutRef.current = window.setTimeout(async () => {
      try {
        const next = await api.interactBrowser(agentId, sessionId, 'wheel', { delta_y: deltaY > 0 ? 120 : -120 })
        setSnapshot(next)
      } catch {
        // 忽略滚动异常
      }
    }, 100)
  }

  const handleQuoteToChat = async () => {
    setQuoting(true)
    try {
      const content = await api.getBrowserPageContent(agentId, sessionId)
      const store = useAstrorderStore.getState()
      const draftKey = agentId + '::' + sessionId
      const currentText = store.drafts[draftKey]?.text || ''
      const bodyText = content.text ? content.text.trim().slice(0, 1800) : ''
      const summary = bodyText ? String.fromCharCode(10) + 'Q�[�dX���' + String.fromCharCode(10) + bodyText : ''
      const quoteBlock = '0SQ�u�' + (content.title || 'e�h��') + '0' + String.fromCharCode(10) + 'W0W@�' + content.url + summary
      store.setDraftText(agentId, sessionId, currentText ? currentText + String.fromCharCode(10, 10) + quoteBlock : quoteBlock)
      setQuotedSuccess(true)
      setTimeout(() => setQuotedSuccess(false), 2000)
    } catch (err) {
      setError(err instanceof Error ? err.message : '网页内容提取失败')
    } finally {
      setQuoting(false)
    }
  }

  const tabs = snapshot?.tabs || []
  const errorCount = snapshot?.error_count ?? 0

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0, background: 'var(--astr-bg)' }}>
      <Paper p={6} withBorder style={{ borderRadius: 0, borderBottom: '1px solid var(--astr-border)', background: 'var(--astr-surface)' }}>
        {tabs.length > 0 && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 4, overflowX: 'auto', marginBottom: 6, paddingBottom: 2 }}>
            {tabs.map((tab) => {
              const isActive = tab.active
              return (
                <div
                  key={tab.id}
                  onClick={() => void handleSelectTab(tab.id)}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 6,
                    padding: '3px 8px',
                    borderRadius: 6,
                    cursor: 'pointer',
                    fontSize: 12,
                    maxWidth: 180,
                    minWidth: 80,
                    background: isActive ? 'var(--astr-surface-muted)' : 'transparent',
                    color: isActive ? 'var(--astr-text)' : 'var(--astr-muted)',
                    border: isActive ? '1px solid var(--astr-border)' : '1px solid transparent',
                    boxShadow: isActive ? 'var(--astr-shadow-soft)' : 'none',
                    transition: 'all 0.15s ease',
                  }}
                >
                  <IconWorld size={12} style={{ flexShrink: 0, opacity: isActive ? 1 : 0.7 }} />
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1, fontWeight: isActive ? 500 : 400 }}>
                    {tab.title || '新标签页'}
                  </span>
                  <ActionIcon
                    size={14}
                    variant="subtle"
                    color="gray"
                    onClick={(e) => void handleCloseTab(tab.id, e)}
                    aria-label="关闭标签页"
                    style={{ borderRadius: 3, flexShrink: 0 }}
                  >
                    <IconX size={10} />
                  </ActionIcon>
                </div>
              )
            })}
            <Tooltip label="新建标签页">
              <ActionIcon variant="subtle" size="xs" onClick={() => void handleNewTab()} aria-label="新建标签页" color="gray">
                <IconPlus size={13} />
              </ActionIcon>
            </Tooltip>
          </div>
        )}
        <Group wrap="nowrap" gap={6}>
          <Tooltip label="后退">
            <ActionIcon variant="subtle" size="sm" onClick={() => void handleGoBack()} aria-label="后退" disabled={loading}>
              <IconArrowLeft size={15} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="前进">
            <ActionIcon variant="subtle" size="sm" onClick={() => void handleGoForward()} aria-label="前进" disabled={loading}>
              <IconArrowRight size={15} />
            </ActionIcon>
          </Tooltip>
          <TextInput
            size="xs"
            value={urlInput}
            onChange={(event) => setUrlInput(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                void navigate()
              }
            }}
            leftSection={<IconLock size={12} style={{ color: 'var(--astr-muted)' }} />}
            rightSection={(
              <ActionIcon variant="subtle" size="xs" onClick={() => void navigate()} aria-label="访问网址">
                <IconArrowRight size={13} />
              </ActionIcon>
            )}
            placeholder="输入网址并回车访问"
            aria-label="当前网址"
            style={{ flex: 1 }}
          />
          {tabs.length === 0 && (
            <Tooltip label="新建标签页">
              <ActionIcon variant="subtle" size="sm" onClick={() => void handleNewTab()} aria-label="新建标签页">
                <IconPlus size={15} />
              </ActionIcon>
            </Tooltip>
          )}
          {snapshot && (
            <>
              <Tooltip label={interactive ? '点击/滚轮操作已启用' : '点击穿透已关闭'}>
                <ActionIcon
                  variant={interactive ? 'light' : 'subtle'}
                  color={interactive ? 'blue' : 'gray'}
                  size="sm"
                  onClick={() => setInteractive(!interactive)}
                  aria-label="切换交互穿透"
                >
                  <IconPointer size={15} />
                </ActionIcon>
              </Tooltip>
              <Tooltip label={quotedSuccess ? '已引用到对话框！' : '引用网页内容到对话框'}>
                <ActionIcon
                  variant="subtle"
                  color={quotedSuccess ? 'teal' : 'gray'}
                  size="sm"
                  loading={quoting}
                  onClick={() => void handleQuoteToChat()}
                  aria-label="引用到对话框"
                >
                  {quotedSuccess ? <IconCheck size={15} /> : <IconMessagePlus size={15} />}
                </ActionIcon>
              </Tooltip>
              <Tooltip label="放大全屏画面">
                <ActionIcon variant="subtle" size="sm" onClick={() => { setZoom(1); setPreview(true) }} aria-label="放大全屏画面">
                  <IconMaximize size={15} />
                </ActionIcon>
              </Tooltip>
            </>
          )}
          <Tooltip label="刷新截图">
            <ActionIcon variant="subtle" size="sm" onClick={() => void load(true)} aria-label="刷新截图">
              <IconRefresh size={15} />
            </ActionIcon>
          </Tooltip>
        </Group>
        {snapshot?.title && <Text size="xs" c="dimmed" mt={4} truncate>{snapshot.title}</Text>}
      </Paper>

      <div
        onWheel={handleImageWheel}
        style={{
          flex: 1,
          minHeight: 0,
          overflow: 'auto',
          background: 'var(--astr-bg)',
          position: 'relative',
        }}
      >
        {loading && !snapshot && <Center h="100%"><Loader size="sm" /></Center>}
        {error && <Alert color="red" m="md">{error}</Alert>}
        {!loading && !snapshot && !error && (
          <Center h="100%" style={{ flexDirection: 'column', gap: 8, color: 'var(--astr-muted)' }}>
            <IconWorld size={34} stroke={1.4} />
            <Text size="sm">输入网址或点击加号新建标签页</Text>
          </Center>
        )}
        {snapshot && source && (
          <div style={{ position: 'relative', display: 'inline-block', width: '100%' }}>
            <img
              src={source}
              alt="真实浏览器画面"
              onClick={(e) => void handleImageClick(e)}
              style={{
                display: 'block',
                width: '100%',
                height: 'auto',
                cursor: interactive ? 'pointer' : 'zoom-in',
                opacity: interacting ? 0.7 : 1,
                transition: 'opacity 0.15s ease',
              }}
            />
            {interacting && (
              <div
                style={{
                  position: 'absolute',
                  top: 10,
                  right: 10,
                  background: 'var(--astr-surface)',
                  padding: '4px 8px',
                  borderRadius: 4,
                  boxShadow: 'var(--astr-shadow)',
                  fontSize: 11,
                  display: 'flex',
                  alignItems: 'center',
                  gap: 6,
                }}
              >
                <Loader size={11} />
                <span>执行中...</span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* 底部精简 DevTools 抽屉 */}
      <div style={{ borderTop: '1px solid var(--astr-border)', background: 'var(--astr-surface)' }}>
        <div
          onClick={() => setShowLogs(!showLogs)}
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '4px 10px',
            cursor: 'pointer',
            fontSize: 11,
            color: 'var(--astr-muted)',
            userSelect: 'none',
          }}
        >
          <Group gap={6}>
            <IconTerminal2 size={13} />
            <span>控制台与网络</span>
            {errorCount > 0 ? (
              <Badge size="xs" color="red" variant="filled" style={{ height: 16, padding: '0 5px' }}>
                {errorCount} 个异常
              </Badge>
            ) : (
              <Text size="xs" c="dimmed">正常</Text>
            )}
          </Group>
          <Group gap={4}>
            {showLogs && (
              <ActionIcon
                size={16}
                variant="subtle"
                color="gray"
                onClick={(e) => {
                  e.stopPropagation()
                  setLogs([])
                }}
                title="清空记录"
              >
                <IconTrash size={11} />
              </ActionIcon>
            )}
            {showLogs ? <IconChevronDown size={14} /> : <IconChevronUp size={14} />}
          </Group>
        </div>

        {showLogs && (
          <ScrollArea h={120} p={6} style={{ background: 'var(--astr-surface-muted)', borderTop: '1px solid var(--astr-border)' }}>
            {loadingLogs && <Center p="xs"><Loader size="xs" /></Center>}
            {!loadingLogs && logs.length === 0 && (
              <Text size="xs" c="dimmed" p="xs" ta="center">暂无控制台日志或异常</Text>
            )}
            {!loadingLogs && logs.length > 0 && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 3, fontFamily: 'ui-monospace, Consolas, monospace', fontSize: 11 }}>
                {logs.map((item, idx) => {
                  const isErr = item.type === 'error' || item.type === 'network_error'
                  return (
                    <div
                      key={idx}
                      style={{
                        display: 'flex',
                        gap: 8,
                        color: isErr ? 'var(--astr-red, #ef4444)' : 'var(--astr-text)',
                        background: isErr ? 'rgba(239, 68, 68, 0.08)' : 'transparent',
                        padding: '2px 4px',
                        borderRadius: 3,
                        wordBreak: 'break-all',
                      }}
                    >
                      <span style={{ color: 'var(--astr-muted)', flexShrink: 0 }}>
                        {new Date(item.time * 1000).toLocaleTimeString()}
                      </span>
                      <span style={{ fontWeight: 600, flexShrink: 0, textTransform: 'uppercase' }}>
                        [{item.type}]
                      </span>
                      <span style={{ flex: 1 }}>{item.text}</span>
                    </div>
                  )
                })}
              </div>
            )}
          </ScrollArea>
        )}
      </div>

      <Modal
        opened={preview}
        onClose={() => setPreview(false)}
        title={snapshot?.title || '真实浏览器画面'}
        size="calc(100vw - 24px)"
        centered
        styles={{
          content: { height: 'calc(100dvh - 24px)', background: 'var(--astr-surface)', color: 'var(--astr-text)' },
          body: { height: 'calc(100% - 60px)', padding: 0 },
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0, background: 'var(--astr-bg)' }}>
          <Group justify="center" gap={6} p={6} style={{ borderBottom: '1px solid var(--astr-border)', background: 'var(--astr-surface)' }}>
            <ActionIcon variant="subtle" onClick={() => changeZoom(zoom - 0.25)} disabled={zoom <= 0.5} aria-label="缩小">
              <IconZoomOut size={17} />
            </ActionIcon>
            <button type="button" onClick={() => setZoom(1)} style={{ minWidth: 58, background: 'none', border: 0, color: 'inherit' }}>
              {Math.round(zoom * 100)}%
            </button>
            <ActionIcon variant="subtle" onClick={() => changeZoom(zoom + 0.25)} disabled={zoom >= 3} aria-label="放大">
              <IconZoomIn size={17} />
            </ActionIcon>
          </Group>
          <div
            style={{ flex: 1, minHeight: 0, overflow: 'auto', background: 'var(--astr-bg)' }}
            onWheel={(event) => {
              if (!event.ctrlKey) return
              event.preventDefault()
              changeZoom(zoom + (event.deltaY < 0 ? 0.25 : -0.25))
            }}
          >
            {snapshot && source && <img src={source} alt="真实浏览器放大画面" style={{ display: 'block', width: `${zoom * 100}%`, maxWidth: 'none', height: 'auto' }} />}
          </div>
        </div>
      </Modal>
    </div>
  )
}

export function BrowserMirrorViewer({ artifact, isActive }: ViewerContext) {
  return <BrowserMirrorPanel agentId={artifact.agentId} sessionId={artifact.sessionId} active={isActive !== false} />
}
