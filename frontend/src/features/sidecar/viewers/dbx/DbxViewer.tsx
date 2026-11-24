import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Button, Group, Text } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconCode,
  IconCopy,
  IconDatabase,
  IconExternalLink,
  IconPlayerPlay,
  IconRefresh,
  IconSettings,
  IconX,
} from '@tabler/icons-react'
import { authenticatedApiFetch } from '../../../../api/client'
import { resolveArtifactFromPath } from '../../resolver'
import { artifactViewerRegistry } from '../../registry'
import { useSidecarStore } from '../../sidecarStore'
import type { ViewerContext } from '../../types'
import { usePluginSettingsStore } from '../../../plugins/pluginSettingsStore'
import { injectSqlIntoDbxFrame } from './dbxBridge'
import { useDbxSidecarStore, type DbxSqlEntry } from './dbxStore'
import './dbxViewer.css'

export const DBX_PLUGIN_ID = 'dbx-viewer'
export const DBX_DEFAULT_UPSTREAM_URL = 'http://127.0.0.1:14224'
export const DBX_PROXY_ENTRY_URL = '/api/v1/plugins/dbx/'

const WSLC_START_CMD =
  'wslc run -d --name astrorder-dbx --restart always -p 127.0.0.1:14224:4224 -e DBX_DISABLE_PASSWORD=true -e DBX_PUBLIC_BASE_PATH=/api/v1/plugins/dbx -v astrorder-dbx-data:/app/data t8y2/dbx:latest'

interface DbxStatusPayload {
  configured: boolean
  upstream_url: string
  base_path: string
  reachable: boolean
  authenticated: boolean
  password_required: boolean
  setup_required: boolean
  context_path_mismatch?: boolean
  error?: string
}

const EMPTY_SQL_LIST: DbxSqlEntry[] = []

function buildProxyEntryUrl(revision: number): string {
  const params = new URLSearchParams()
  try {
    const token = typeof localStorage !== 'undefined' ? localStorage.getItem('astrorder:token') : null
    if (token) params.set('token', token)
  } catch {}
  if (revision > 0) params.set('_r', String(revision))
  const qs = params.toString()
  return qs ? `${DBX_PROXY_ENTRY_URL}?${qs}` : DBX_PROXY_ENTRY_URL
}

export function DbxViewer({ artifact, session }: ViewerContext) {
  const sessionKey = artifact.sessionId || session?.id || 'global'
  const provider = usePluginSettingsStore((s) =>
    s.getPluginConfig<string>(DBX_PLUGIN_ID, 'provider', 'dbx'),
  )
  const configuredUpstream = usePluginSettingsStore((s) =>
    s.getPluginConfig<string>(DBX_PLUGIN_ID, 'upstreamUrl', DBX_DEFAULT_UPSTREAM_URL),
  )
  const configuredCustomUrl = usePluginSettingsStore((s) =>
    s.getPluginConfig<string>(DBX_PLUGIN_ID, 'customUrl', ''),
  )
  const isCustomProvider = provider === 'custom-web'
  const normalizedCustomUrl = (configuredCustomUrl || '').trim()
  const isValidCustomUrl = /^https?:\/\/[^/\s]+/i.test(normalizedCustomUrl)
  const sqlItems = useDbxSidecarStore((s) => s.itemsBySession[sessionKey] || EMPTY_SQL_LIST)
  const pendingRequest = useDbxSidecarStore((s) => s.pendingRequest)

  const [status, setStatus] = useState<DbxStatusPayload | null>(null)
  const [checking, setChecking] = useState(true)
  const [iframeRevision, setIframeRevision] = useState(0)
  const [iframeLoaded, setIframeLoaded] = useState(false)
  const [liveStatus, setLiveStatus] = useState('')

  const iframeRef = useRef<HTMLIFrameElement | null>(null)
  const toolbarRef = useRef<HTMLDivElement | null>(null)

  const checkStatus = useCallback(async () => {
    if (isCustomProvider) {
      setChecking(false)
      setStatus({
        configured: isValidCustomUrl,
        upstream_url: normalizedCustomUrl,
        base_path: '',
        reachable: isValidCustomUrl,
        authenticated: true,
        password_required: false,
        setup_required: false,
        error: isValidCustomUrl ? '' : '请在「插件管理 → 数据库管理工具 → 参数配置」中填写自定义 Web 工具地址',
      })
      return
    }
    setChecking(true)
    try {
      const res = await authenticatedApiFetch('/api/v1/plugins/dbx-status')
      if (!res.ok) {
        setStatus({
          configured: true,
          upstream_url: configuredUpstream || DBX_DEFAULT_UPSTREAM_URL,
          base_path: '/api/v1/plugins/dbx',
          reachable: false,
          authenticated: false,
          password_required: false,
          setup_required: false,
          error: `状态检查失败（HTTP ${res.status}）`,
        })
        return
      }
      const payload = (await res.json()) as DbxStatusPayload
      setStatus(payload)
    } catch (err) {
      setStatus({
        configured: true,
        upstream_url: configuredUpstream || DBX_DEFAULT_UPSTREAM_URL,
        base_path: '/api/v1/plugins/dbx',
        reachable: false,
        authenticated: false,
        password_required: false,
        setup_required: false,
        error: err instanceof Error ? err.message : '无法检查数据库服务状态',
      })
    } finally {
      setChecking(false)
    }
  }, [configuredUpstream, isCustomProvider, isValidCustomUrl, normalizedCustomUrl])

  useEffect(() => {
    void checkStatus()
  }, [checkStatus])

  // Roving tabindex fallback only when native focusgroup is unavailable.
  useEffect(() => {
    const bar = toolbarRef.current
    if (!bar || 'focusGroup' in HTMLElement.prototype) return

    const getControls = () =>
      Array.from(bar.querySelectorAll<HTMLButtonElement>('button')).filter(
        (btn) => btn.offsetParent !== null || btn.getClientRects().length > 0,
      )

    const syncInitial = () => {
      const controls = getControls()
      if (!controls.some((btn) => btn.tabIndex === 0) && controls[0]) {
        controls.forEach((btn, idx) => {
          btn.tabIndex = idx === 0 ? 0 : -1
        })
      }
    }
    syncInitial()

    const handleFocusIn = (event: FocusEvent) => {
      const controls = getControls()
      const target = event.target as HTMLButtonElement | null
      if (target && controls.includes(target)) {
        controls.forEach((btn) => {
          btn.tabIndex = btn === target ? 0 : -1
        })
      }
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      const controls = getControls()
      if (controls.length === 0) return
      const currentIndex = controls.indexOf(document.activeElement as HTMLButtonElement)
      if (currentIndex < 0) return

      const rtl = getComputedStyle(bar).direction === 'rtl'
      let nextIndex = -1
      if (event.key === 'ArrowRight') nextIndex = (currentIndex + (rtl ? -1 : 1) + controls.length) % controls.length
      else if (event.key === 'ArrowLeft') nextIndex = (currentIndex + (rtl ? 1 : -1) + controls.length) % controls.length
      else if (event.key === 'Home') nextIndex = 0
      else if (event.key === 'End') nextIndex = controls.length - 1
      else return

      event.preventDefault()
      controls.forEach((btn, idx) => {
        btn.tabIndex = idx === nextIndex ? 0 : -1
      })
      controls[nextIndex]?.focus()
    }

    bar.addEventListener('focusin', handleFocusIn)
    bar.addEventListener('keydown', handleKeyDown)
    return () => {
      bar.removeEventListener('focusin', handleFocusIn)
      bar.removeEventListener('keydown', handleKeyDown)
    }
  }, [status?.reachable])

  // Deliver queued SQL into the live same-origin DBX iframe whenever a request
  // arrives or the iframe finishes loading. For custom web providers, keep the
  // SQL ready in the checklist strip for one-click copy.
  useEffect(() => {
    if (!pendingRequest) return
    if (pendingRequest.sessionId !== sessionKey && pendingRequest.sessionId !== 'global') return

    const list = useDbxSidecarStore.getState().itemsBySession[pendingRequest.sessionId] || []
    const entry = list.find((item) => item.id === pendingRequest.entryId)
    if (!entry) {
      useDbxSidecarStore.getState().clearPendingRequest(pendingRequest.requestId)
      return
    }

    if (isCustomProvider) {
      const store = useDbxSidecarStore.getState()
      store.clearPendingRequest(pendingRequest.requestId)
      store.markEntryStatus(pendingRequest.sessionId, entry.id, 'injected')
      const hint = `已将 ${entry.filename} 加入顶部 SQL 清单，点击复制按钮即可粘贴到当前数据库工具执行`
      setLiveStatus(hint)
      notifications.show({ color: 'teal', message: hint })
      return
    }

    if (!status?.reachable || status.context_path_mismatch || !iframeLoaded) return

    const controller = new AbortController()
    void injectSqlIntoDbxFrame(
      iframeRef.current,
      {
        filename: entry.filename,
        path: entry.path,
        content: entry.content,
        line: entry.line,
        forceNew: pendingRequest.forceNew,
      },
      { signal: controller.signal },
    ).then((result) => {
      if (controller.signal.aborted) return
      const store = useDbxSidecarStore.getState()
      store.clearPendingRequest(pendingRequest.requestId)
      if (result.ok) {
        store.markEntryStatus(pendingRequest.sessionId, entry.id, 'injected')
        const hint = result.connectionBound
          ? `已将 ${entry.filename} 载入右侧数据库工具（请确认目标数据库后手动执行）`
          : `已将 ${entry.filename} 载入右侧数据库工具（请先选择目标连接后手动执行）`
        setLiveStatus(hint)
        notifications.show({ color: 'teal', message: hint })
      } else {
        store.markEntryStatus(pendingRequest.sessionId, entry.id, 'failed', result.reason)
        setLiveStatus(result.reason || '载入数据库工具失败')
        notifications.show({
          color: 'yellow',
          title: `未能自动载入 ${entry.filename}`,
          message: result.reason || '可点击上方 SQL 标签重试或复制 SQL 粘贴到查询窗口',
        })
      }
    })

    return () => controller.abort()
  }, [pendingRequest, sessionKey, isCustomProvider, status?.reachable, status?.context_path_mismatch, iframeLoaded])

  const iframeSrc = useMemo(
    () => (isCustomProvider ? normalizedCustomUrl : buildProxyEntryUrl(iframeRevision)),
    [iframeRevision, isCustomProvider, normalizedCustomUrl],
  )

  useEffect(() => {
    setIframeLoaded(false)
  }, [provider, iframeSrc])

  const handleRefresh = () => {
    setIframeLoaded(false)
    setIframeRevision((v) => v + 1)
    void checkStatus()
    setLiveStatus('正在重新连接数据库服务')
  }

  const handleOpenExternal = () => {
    const targetUrl = isCustomProvider && isValidCustomUrl ? normalizedCustomUrl : DBX_PROXY_ENTRY_URL
    window.open(targetUrl, '_blank', 'noopener,noreferrer')
  }

  const handleOpenPluginSettings = () => {
    window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: 'plugins' } }))
  }

  const handleCopySql = async (entry: DbxSqlEntry) => {
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable')
      await navigator.clipboard.writeText(entry.content)
      setLiveStatus(`已复制 ${entry.filename} 的 SQL 内容`)
      notifications.show({ color: 'teal', message: `已复制 ${entry.filename} 的 SQL 内容` })
    } catch {
      setLiveStatus('复制失败')
      notifications.show({ color: 'red', message: '无法写入剪贴板' })
    }
  }

  const handleCopyLaunchCmd = async () => {
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable')
      await navigator.clipboard.writeText(WSLC_START_CMD)
      setLiveStatus('已复制 wslc 启动命令')
      notifications.show({ color: 'teal', message: '已复制 wslc 启动命令' })
    } catch {
      setLiveStatus('复制启动命令失败')
    }
  }

  const handleOpenSourceInMonaco = (entry: DbxSqlEntry) => {
    if (entry.isSnippet) {
      useSidecarStore.getState().openArtifact(
        {
          id: entry.path,
          name: entry.filename,
          kind: 'workspace_file',
          mediaType: 'text/plain',
          readUrl: '',
          writable: false,
          sessionId: entry.sessionId,
          agentId: entry.agentId || artifact.agentId,
          connectionId: entry.connectionId || artifact.connectionId,
          metadata: { inlineContent: entry.content },
        },
        'monaco-viewer',
      )
      return
    }
    const targetSession = session || {
      id: entry.sessionId,
      agent_id: entry.agentId || artifact.agentId,
      connection_id: entry.connectionId || artifact.connectionId,
      title: '',
      workspace: '',
      status: 'idle' as const,
      updated_at: '',
    }
    const sqlArtifact = resolveArtifactFromPath(entry.path, targetSession)
    if (entry.line) {
      sqlArtifact.metadata = { ...(sqlArtifact.metadata || {}), line: entry.line }
    }
    const viewer = artifactViewerRegistry.findViewer(sqlArtifact)
    useSidecarStore.getState().openArtifact(sqlArtifact, viewer?.id || 'monaco-viewer')
  }

  const canRenderFrame = isCustomProvider
    ? isValidCustomUrl
    : Boolean(status?.reachable && !status?.context_path_mismatch)
  const upstreamDisplay = isCustomProvider
    ? normalizedCustomUrl || '未配置 Web 地址'
    : status?.upstream_url || configuredUpstream || DBX_DEFAULT_UPSTREAM_URL
  const statusDotClass = checking
    ? 'is-checking'
    : canRenderFrame
      ? 'is-online'
      : 'is-offline'

  return (
    <div className="sidecar-dbx-viewer">
      <div
        ref={toolbarRef}
        className="sidecar-dbx-toolbar"
        role="toolbar"
        aria-label="数据库工作台工具栏"
        {...({ focusgroup: 'toolbar wrap' } as Record<string, string>)}
      >
        <div className="sidecar-dbx-toolbar-left">
          <span
            className={`sidecar-dbx-status-dot ${statusDotClass}`}
            aria-hidden="true"
            title={canRenderFrame ? '数据库服务已连接' : checking ? '正在检测数据库服务' : '数据库服务未连接'}
          />
          <IconDatabase size={15} aria-hidden="true" style={{ color: 'var(--astr-indigo)', flexShrink: 0 }} />
          <span className="sidecar-dbx-title">数据库</span>
          <span className="sidecar-dbx-upstream" title={upstreamDisplay}>
            {upstreamDisplay}
          </span>
        </div>

        <div className="sidecar-dbx-toolbar-group" role="group" aria-label="数据库页面操作">
          <button
            type="button"
            className="sidecar-dbx-btn"
            aria-label="刷新数据库工作台"
            title="刷新"
            onClick={handleRefresh}
          >
            <IconRefresh size={14} aria-hidden="true" />
            <span className="sidecar-dbx-btn-label">刷新</span>
          </button>
          <button
            type="button"
            className="sidecar-dbx-btn"
            aria-label="在新标签页打开数据库工作台"
            title="在新标签页打开"
            onClick={handleOpenExternal}
          >
            <IconExternalLink size={14} aria-hidden="true" />
            <span className="sidecar-dbx-btn-label">新窗口</span>
          </button>
        </div>
        <span className="sidecar-dbx-separator" role="separator" aria-orientation="vertical" />
        <div className="sidecar-dbx-toolbar-group" role="group" aria-label="数据库插件配置">
          <button
            type="button"
            className="sidecar-dbx-btn"
            aria-label="管理数据库插件"
            title="管理数据库插件"
            onClick={handleOpenPluginSettings}
          >
            <IconSettings size={14} aria-hidden="true" />
          </button>
        </div>
      </div>

      {sqlItems.length > 0 && (
        <div className="sidecar-dbx-sql-strip" role="region" aria-label="本次会话待执行 SQL 清单">
          <span className="sidecar-dbx-sql-strip-label">SQL 清单 ({sqlItems.length})</span>
          {sqlItems.map((entry) => (
            <div
              key={entry.id}
              className={`sidecar-dbx-sql-chip ${entry.executed ? 'is-executed' : ''} ${entry.status === 'failed' ? 'is-failed' : ''}`}
            >
              <button
                type="button"
                className="sidecar-dbx-chip-action"
                aria-pressed={entry.executed}
                aria-label={entry.executed ? `取消标记 ${entry.filename} 已执行` : `标记 ${entry.filename} 已执行`}
                title={entry.executed ? '已标记执行完成（点击取消）' : '标记为已手动执行'}
                onClick={() => useDbxSidecarStore.getState().toggleExecuted(sessionKey, entry.id)}
              >
                <IconCheck size={13} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="sidecar-dbx-sql-name"
                title={`${entry.path}\n点击重新载入到查询标签页`}
                onClick={() => useDbxSidecarStore.getState().requestInject(sessionKey, entry.id)}
              >
                <IconPlayerPlay size={11} aria-hidden="true" />
                <span>{entry.filename}</span>
              </button>
              <button
                type="button"
                className="sidecar-dbx-chip-action"
                aria-label={`复制 ${entry.filename} 的 SQL`}
                title="复制 SQL 内容"
                onClick={() => { void handleCopySql(entry) }}
              >
                <IconCopy size={12} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="sidecar-dbx-chip-action"
                aria-label={`查看 ${entry.filename} 源码`}
                title="在编辑器查看源码"
                onClick={() => handleOpenSourceInMonaco(entry)}
              >
                <IconCode size={12} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="sidecar-dbx-chip-action"
                aria-label={`移除 ${entry.filename}`}
                title="从清单移除"
                onClick={() => useDbxSidecarStore.getState().removeEntry(sessionKey, entry.id)}
              >
                <IconX size={12} aria-hidden="true" />
              </button>
            </div>
          ))}
        </div>
      )}

      <p role="status" className="dbx-sr-only">
        {liveStatus}
      </p>

      <div className="sidecar-dbx-stage">
        {canRenderFrame ? (
          <iframe
            ref={iframeRef}
            key={`${provider}-${iframeRevision}`}
            src={iframeSrc}
            title="数据库工作台"
            className="sidecar-dbx-iframe"
            onLoad={() => setIframeLoaded(true)}
          />
        ) : (
          <div className="sidecar-dbx-fallback">
            <div className="sidecar-dbx-fallback-card">
              <Group gap="xs">
                <IconDatabase size={20} style={{ color: 'var(--astr-indigo)' }} aria-hidden="true" />
                <Text fw={600} size="sm">
                  {checking ? '正在检测数据库服务…' : '数据库服务暂不可用'}
                </Text>
              </Group>
              <Text size="xs" c="dimmed">
                当前插件实现：<code>{isCustomProvider ? '自定义 Web 工具' : 'DBX'}</code> · 服务地址：<code>{upstreamDisplay}</code>
              </Text>
              {status?.error && (
                <Text size="xs" c="red">
                  {status.error}
                </Text>
              )}
              {!isCustomProvider && (
                <>
                  <Text size="xs" c="dimmed">
                    本机可用 Windows 自带 <code>wslc</code> 容器启动 DBX（无密码、仅监听本机回环）：
                  </Text>
                  <pre className="sidecar-dbx-cmd">{WSLC_START_CMD}</pre>
                </>
              )}
              <Group gap="xs">
                <Button size="xs" color="indigo" leftSection={<IconRefresh size={14} />} loading={checking} onClick={() => { void checkStatus() }}>
                  重新检测
                </Button>
                {!isCustomProvider && (
                  <Button size="xs" variant="light" color="gray" leftSection={<IconCopy size={14} />} onClick={() => { void handleCopyLaunchCmd() }}>
                    复制启动命令
                  </Button>
                )}
                <Button size="xs" variant="subtle" color="indigo" leftSection={<IconSettings size={14} />} onClick={handleOpenPluginSettings}>
                  修改工具配置
                </Button>
              </Group>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
