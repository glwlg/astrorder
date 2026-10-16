import { useEffect, useState } from 'react'
import { Alert, Button, Group, Text } from '@mantine/core'
import { IconExternalLink, IconRefresh } from '@tabler/icons-react'
import { api } from '../../api/client'
import { statisticsPageURL } from './usageFormat'
import { NativeStatisticsView, type StatisticsDesktop } from './NativeStatisticsView'

export function AnalyticsPage() {
  const [url, setUrl] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const refresh = () => setRevision(value => value + 1)
    window.addEventListener('astrorder:gateway-config-changed', refresh)
    return () => window.removeEventListener('astrorder:gateway-config-changed', refresh)
  }, [])
  useEffect(() => {
    let active = true
    setLoading(true)
    setError('')
    api.getAnalyticsConfig().then(config => {
      if (!active) return
      const next = statisticsPageURL(config)
      setUrl(next)
      if (!next) setError('请在设置 → 模型与网关中配置统计页地址。')
    }).catch((cause: unknown) => {
      if (active) setError(cause instanceof Error ? cause.message : '读取统计页地址失败')
    }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [revision])

  const desktop = (window as unknown as { astrorderDesktop?: Partial<StatisticsDesktop> & { openExternal?: (address: string) => Promise<void> } }).astrorderDesktop
  const openExternal = async (event: React.MouseEvent<HTMLAnchorElement>) => {
    if (!desktop?.openExternal) return
    event.preventDefault()
    try { await desktop.openExternal(url) }
    catch (cause: unknown) { setError(cause instanceof Error ? cause.message : '打开统计页失败') }
  }

  return <div className="route-page analytics-page" style={{ maxWidth: 'none', margin: 0, minHeight: 0, padding: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
    <Group justify="space-between" px="sm" py={6} style={{ flexShrink: 0, borderBottom: '1px solid var(--astr-border)' }}>
      <Text size="sm" fw={600}>网关统计</Text>
      <Group gap={6}>
        <Button size="compact-xs" variant="subtle" leftSection={<IconRefresh size={14} />} onClick={() => setRevision(value => value + 1)}>刷新</Button>
        {url && <Button component="a" href={url} onClick={openExternal} target="_blank" rel="noopener noreferrer" size="compact-xs" variant="subtle" leftSection={<IconExternalLink size={14} />}>外部打开</Button>}
      </Group>
    </Group>
    {loading ? <Text role="status" p="md" size="sm" c="dimmed">正在读取统计页地址…</Text> : error ? <Alert m="md" color="red">{error}</Alert> : <>
      {desktop?.showStatistics && desktop.hideStatistics
        ? <NativeStatisticsView url={url} revision={revision} desktop={desktop as StatisticsDesktop} onError={setError} />
        : <iframe key={`${url}:${revision}`} src={url} title="网关统计页" referrerPolicy="no-referrer" sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads" style={{ flex: 1, minHeight: 0, width: '100%', border: 0 }} />}
      {!desktop?.showStatistics && <Text px="sm" py={4} size="xs" c="dimmed" style={{ flexShrink: 0 }}>部分网关的登录 Cookie 不支持跨站内嵌；遇到反复登录请使用“外部打开”。地址可在设置 → 模型与网关中修改。</Text>}
    </>}
  </div>
}
