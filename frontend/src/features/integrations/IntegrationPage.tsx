import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { Alert, Button, Center, Group, Paper, Stack, Text } from '@mantine/core'
import { IconExternalLink, IconPencil, IconRefresh, IconSettings } from '@tabler/icons-react'
import { SIDEBAR_INTEGRATIONS_PLUGIN_ID, usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { IntegrationFormModal } from './IntegrationFormModal'
import { IntegrationIcon } from './integrationIcons'
import { useIntegrationsStore } from './integrationStore'
import { NativeEmbeddedView, resolveNativeEmbeddedBridge, type EmbeddedDesktopBridge } from './NativeEmbeddedView'
import './integrations.css'

export function IntegrationPage() {
  const { integrationId } = useParams<{ integrationId: string }>()
  const pluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(SIDEBAR_INTEGRATIONS_PLUGIN_ID))
  const items = useIntegrationsStore((s) => s.items)
  const hydrated = useIntegrationsStore((s) => s.hydrated)
  const hydrate = useIntegrationsStore((s) => s.hydrate)

  const [revision, setRevision] = useState(0)
  const [error, setError] = useState('')
  const [editOpened, setEditOpened] = useState(false)

  useEffect(() => {
    void hydrate()
  }, [hydrate])

  const item = items.find((entry) => entry.id === integrationId)

  useEffect(() => {
    setError('')
    setRevision(0)
  }, [integrationId, item?.url])

  const openSettingsIntegrations = () => {
    window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: 'integrations' } }))
  }

  const desktop = (window as unknown as { astrorderDesktop?: EmbeddedDesktopBridge }).astrorderDesktop
  const nativeBridge = useMemo(() => resolveNativeEmbeddedBridge(desktop), [desktop])

  const openExternal = async (event?: React.MouseEvent) => {
    if (!item) return
    if (desktop?.openExternal) {
      event?.preventDefault()
      try {
        await desktop.openExternal(item.url)
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : '打开外部页面失败')
      }
    } else if (!event) {
      window.open(item.url, '_blank', 'noopener,noreferrer')
    }
  }

  if (!pluginEnabled) {
    return (
      <div className="route-page integration-embedded-page" style={{ maxWidth: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <Center style={{ flex: 1, padding: 24 }}>
          <Paper withBorder radius="lg" p="xl" maw={460}>
            <Stack gap="sm" align="center">
              <Text fw={600}>侧边栏外链集成插件已停用</Text>
              <Text size="sm" c="dimmed" ta="center">
                可在「设置 → 插件」中重新启用侧边栏外链集成。
              </Text>
              <Button
                variant="light"
                color="violet"
                leftSection={<IconSettings size={15} />}
                onClick={() => window.dispatchEvent(new CustomEvent('astrorder:open-settings', { detail: { tab: 'plugins' } }))}
              >
                前往插件管理
              </Button>
            </Stack>
          </Paper>
        </Center>
      </div>
    )
  }

  if (!hydrated && !item) {
    return (
      <div className="route-page integration-embedded-page" style={{ maxWidth: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <Text role="status" p="md" size="sm" c="dimmed">正在读取集成页面…</Text>
      </div>
    )
  }

  if (!item) {
    return (
      <div className="route-page integration-embedded-page" style={{ maxWidth: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
        <Center style={{ flex: 1, padding: 24 }}>
          <Paper withBorder radius="lg" p="xl" maw={460}>
            <Stack gap="sm" align="center">
              <Text fw={600}>未找到对应的集成页面</Text>
              <Text size="sm" c="dimmed" ta="center">该集成可能已被删除或尚未同步。</Text>
              <Button variant="light" color="violet" leftSection={<IconSettings size={15} />} onClick={openSettingsIntegrations}>
                打开集成管理
              </Button>
            </Stack>
          </Paper>
        </Center>
      </div>
    )
  }

  const displayTitle = item.name.trim() || (() => {
    try { return new URL(item.url).hostname || item.url } catch { return item.url }
  })()

  return (
    <div className="route-page integration-embedded-page" style={{ maxWidth: 'none', margin: 0, minHeight: 0, padding: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
      <Group
        className="integration-embedded-toolbar"
        justify="space-between"
        px="md"
        py={6}
        wrap="nowrap"
        style={{
          flexShrink: 0,
          borderBottom: '1px solid var(--astr-border)',
          background: 'color-mix(in oklab, var(--astr-surface-muted) 45%, var(--astr-surface))',
        }}
      >
        <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
          <IntegrationIcon name={item.name} url={item.url} iconType={item.icon_type} iconValue={item.icon_value} size={18} />
          <Text size="sm" fw={600} truncate>{displayTitle}</Text>
          <Text size="xs" c="dimmed" truncate visibleFrom="sm">{item.url}</Text>
        </Group>
        <Group gap={6} wrap="nowrap" style={{ flexShrink: 0 }}>
          {item.open_mode === 'embed' && (
            <Button
              size="compact-xs"
              variant="subtle"
              color="violet"
              leftSection={<IconRefresh size={14} />}
              onClick={() => setRevision((value) => value + 1)}
            >
              刷新
            </Button>
          )}
          <Button
            component="a"
            href={item.url}
            onClick={(e) => { void openExternal(e) }}
            target="_blank"
            rel="noopener noreferrer"
            size="compact-xs"
            variant="subtle"
            color="violet"
            leftSection={<IconExternalLink size={14} />}
          >
            外部打开
          </Button>
          <Button
            size="compact-xs"
            variant="subtle"
            color="gray"
            leftSection={<IconPencil size={14} />}
            onClick={() => setEditOpened(true)}
          >
            编辑
          </Button>
        </Group>
      </Group>

      {error ? (
        <Alert m="md" color="red">{error}</Alert>
      ) : item.open_mode === 'external' ? (
        <Center style={{ flex: 1, padding: 24 }}>
          <Paper withBorder radius="lg" p="xl" maw={480}>
            <Stack gap="sm" align="center">
              <IntegrationIcon name={item.name} url={item.url} iconType={item.icon_type} iconValue={item.icon_value} size={40} />
              <Text fw={600}>{displayTitle}</Text>
              <Text size="xs" c="dimmed" ta="center" style={{ wordBreak: 'break-all' }}>{item.url}</Text>
              <Text size="xs" c="dimmed" ta="center">该集成配置为“外部浏览器打开”。</Text>
              <Button color="violet" leftSection={<IconExternalLink size={15} />} onClick={() => { void openExternal() }}>
                在外部浏览器打开
              </Button>
            </Stack>
          </Paper>
        </Center>
      ) : (
        <>
          {nativeBridge ? (
            <NativeEmbeddedView
              url={item.url}
              title={displayTitle}
              revision={revision}
              desktop={nativeBridge}
              onError={setError}
            />
          ) : (
            <iframe
              key={`${item.id}:${item.url}:${revision}`}
              src={item.url}
              title={displayTitle}
              referrerPolicy="no-referrer"
              sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-downloads"
              style={{ flex: 1, minHeight: 0, width: '100%', border: 0 }}
            />
          )}
          {!nativeBridge && (
            <Text px="sm" py={4} size="xs" c="dimmed" style={{ flexShrink: 0 }}>
              浏览器模式部分站点可能限制跨站 Cookie 或 iframe 嵌入；遇到无法加载请点击右上角“外部打开”，或在桌面端使用原生视图。
            </Text>
          )}
        </>
      )}

      <IntegrationFormModal
        opened={editOpened}
        onClose={() => setEditOpened(false)}
        editing={item}
      />
    </div>
  )
}
