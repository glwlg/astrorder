import { Fragment, useEffect, useMemo, useState } from 'react'
import { Button, Menu, Paper, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconPuzzle } from '@tabler/icons-react'
import {
  extensionsApi,
  type DeclarativePillPayload,
  type PluginView,
  type SlotClickAction,
  type UISlotContribution,
} from '../../api/extensions'
import type { Session } from '../../domain/types'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import type { ViewerContext } from '../sidecar/types'
import { extensionRegistry, useExtensionRegistryVersion } from './extensionRegistry'
import { useExtensionStore } from './extensionStore'
import { SandboxedPluginFrame } from './SandboxedPluginFrame'
import {
  parsePluginPanelArtifact,
  type OpenPluginPanelEventDetail,
  type StatusBarSlotContext,
  type WelcomeMenuExtraItem,
} from './types'
import './extensions.css'

function joinUiEntryUrl(uiBaseUrl: string, uiEntry: string): string {
  const base = uiBaseUrl.replace(/\/+$/, '')
  const entry = uiEntry.replace(/^\/+/, '')
  return `${base}/${entry}`
}

function dispatchOpenPluginPanel(
  plugin: PluginView,
  panelId: string,
  session?: Session | null,
  params?: Record<string, unknown>,
) {
  const panelMeta = plugin.contributes?.sidecar_panels?.find((panel) => panel.id === panelId)
  const detail: OpenPluginPanelEventDetail = {
    pluginId: plugin.id,
    panelId,
    params,
    title: panelMeta?.title || plugin.name,
    mediaType: panelMeta?.media_type || `application/x-astrorder-ext.${plugin.id}.${panelId}`,
    session,
  }
  window.dispatchEvent(new CustomEvent<OpenPluginPanelEventDetail>('astrorder:open-plugin-panel', { detail }))
}

function triggerSlotClickAction(
  plugin: PluginView,
  action: SlotClickAction | undefined,
  session?: Session | null,
  panelParams?: Record<string, unknown>,
) {
  if (!action) return
  if (action.open_panel) {
    dispatchOpenPluginPanel(plugin, action.open_panel, session, panelParams)
    return
  }
  if (action.url) {
    try {
      const parsed = new URL(action.url, window.location.origin)
      if (parsed.protocol === 'http:' || parsed.protocol === 'https:') {
        window.open(parsed.toString(), '_blank', 'noopener,noreferrer')
      }
    } catch {
      // ignore invalid url
    }
    return
  }
  if (action.endpoint) {
    void extensionsApi
      .fetchExtensionEndpoint(plugin.id, action.endpoint, {
        workspace: session?.workspace || undefined,
        connection_id: session?.connection_id || undefined,
        session_id: session?.id,
      })
      .catch((err) => {
        notifications.show({
          color: 'red',
          message: err instanceof Error ? err.message : `调用 ${plugin.name} 失败`,
        })
      })
  }
}

function isExternalPluginActive(plugin: PluginView, enabledPlugins: Record<string, boolean>): boolean {
  if (plugin.source !== 'external') return false
  if (!plugin.enabled) return false
  if (plugin.permissions_granted === false) return false
  if (enabledPlugins[plugin.id] === false) return false
  return true
}

function DeclarativePillSlot({
  plugin,
  slot,
  session,
  branch,
  statusUnavailable,
  statusLoading,
}: {
  plugin: PluginView
  slot: UISlotContribution
  session: Session
  branch: string | null
  statusUnavailable: boolean
  statusLoading: boolean
}) {
  const [payload, setPayload] = useState<DeclarativePillPayload | null>(null)
  const blockedByWorkspace = Boolean(slot.requires_workspace && (!session.workspace || statusUnavailable))

  useEffect(() => {
    if (blockedByWorkspace || statusLoading || !slot.poll_endpoint) {
      return
    }
    let active = true
    const poll = async () => {
      try {
        const next = await extensionsApi.fetchExtensionEndpoint<DeclarativePillPayload>(
          plugin.id,
          slot.poll_endpoint!,
          {
            workspace: session.workspace || undefined,
            connection_id: session.connection_id || undefined,
            session_id: session.id,
            ref: branch || undefined,
          },
        )
        if (active) {
          setPayload(next && typeof next === 'object' ? next : null)
        }
      } catch {
        if (active) {
          setPayload(null)
        }
      }
    }
    void poll()
    const intervalMs = Math.max(3000, slot.poll_interval_ms || 15000)
    const timer = window.setInterval(() => {
      void poll()
    }, intervalMs)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [
    blockedByWorkspace,
    statusLoading,
    plugin.id,
    slot.poll_endpoint,
    slot.poll_interval_ms,
    session.workspace,
    session.connection_id,
    session.id,
    branch,
  ])

  const activePayload = blockedByWorkspace || statusLoading || !slot.poll_endpoint ? null : payload
  if (!activePayload || !activePayload.visible) {
    return null
  }

  const label = activePayload.label || slot.label || plugin.name
  const button = (
    <UnstyledButton
      className={`composer-ext-pill${activePayload.tone ? ` is-${activePayload.tone}` : ''}`}
      aria-label={activePayload.tooltip || label}
      onClick={() => triggerSlotClickAction(plugin, slot.on_click, session, activePayload.panel_params)}
    >
      {activePayload.dot_color ? (
        <span
          className={`composer-ext-dot${activePayload.pulsing ? ' is-pulsing' : ''}`}
          style={{ backgroundColor: activePayload.dot_color }}
          aria-hidden="true"
        />
      ) : (
        <IconPuzzle size={13} aria-hidden="true" />
      )}
      {activePayload.sublabel && <span className="composer-ext-sublabel">{activePayload.sublabel}</span>}
      <span>{label}</span>
    </UnstyledButton>
  )

  if (activePayload.tooltip) {
    return <Tooltip label={activePayload.tooltip}>{button}</Tooltip>
  }
  return button
}

export function ComposerStatusBarSlots({
  session,
  statusUnavailable,
  statusLoading,
  branch,
}: StatusBarSlotContext) {
  const registryVersion = useExtensionRegistryVersion()
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const externalItems = useExtensionStore((s) => s.items)

  const builtinSlots = useMemo(() => {
    void registryVersion
    return extensionRegistry
      .getAllBuiltins()
      .filter((reg) => Boolean(reg.statusBarComponent) && enabledPlugins[reg.pluginId] !== false)
  }, [registryVersion, enabledPlugins])

  const externalSlots = useMemo(() => {
    const list: Array<{ plugin: PluginView; slot: UISlotContribution }> = []
    for (const plugin of externalItems) {
      if (!isExternalPluginActive(plugin, enabledPlugins)) continue
      for (const slot of plugin.contributes?.ui_slots || []) {
        if (slot.slot === 'chat.composer.status_bar' && (!slot.renderer || slot.renderer === 'declarative_pill')) {
          list.push({ plugin, slot })
        }
      }
    }
    return list
  }, [externalItems, enabledPlugins])

  if (builtinSlots.length === 0 && externalSlots.length === 0) {
    return null
  }

  return (
    <>
      {builtinSlots.map((reg) => {
        const SlotComponent = reg.statusBarComponent!
        return (
          <SlotComponent
            key={`builtin-status:${reg.pluginId}`}
            session={session}
            statusUnavailable={statusUnavailable}
            statusLoading={statusLoading}
            branch={branch}
          />
        )
      })}
      {externalSlots.map(({ plugin, slot }) => (
        <DeclarativePillSlot
          key={`ext-status:${plugin.id}:${slot.id}`}
          plugin={plugin}
          slot={slot}
          session={session}
          branch={branch}
          statusUnavailable={statusUnavailable}
          statusLoading={statusLoading}
        />
      ))}
    </>
  )
}

export function GitMenuExtensionItems({ session }: { session: Session }) {
  const registryVersion = useExtensionRegistryVersion()
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const externalItems = useExtensionStore((s) => s.items)

  const builtinGroups = useMemo(() => {
    void registryVersion
    const groups: Array<{
      key: string
      label?: string
      items: Array<{
        key: string
        label: string
        icon?: React.ComponentType<{ size?: number; color?: string }>
        iconColor?: string
        onClick: () => void
      }>
    }> = []

    for (const reg of extensionRegistry.getAllBuiltins()) {
      if (enabledPlugins[reg.pluginId] === false) continue
      const visibleItems = (reg.gitMenuItems || []).filter(
        (item) => !item.requiresWorkspace || Boolean(session.workspace),
      )
      if (visibleItems.length === 0) continue

      const byLabel = new Map<string, typeof groups[number]['items']>()
      for (const item of visibleItems) {
        const groupLabel = item.groupLabel || ''
        const arr = byLabel.get(groupLabel) || []
        arr.push({
          key: `${reg.pluginId}:${item.id}`,
          label: item.label,
          icon: item.icon,
          iconColor: item.iconColor,
          onClick: () => item.onClick(session),
        })
        byLabel.set(groupLabel, arr)
      }
      for (const [label, items] of byLabel.entries()) {
        groups.push({
          key: `builtin:${reg.pluginId}:${label}`,
          label: label || undefined,
          items,
        })
      }
    }
    return groups
  }, [registryVersion, enabledPlugins, session])

  const externalGroups = useMemo(() => {
    const groups: Array<{
      key: string
      label: string
      items: Array<{ key: string; label: string; onClick: () => void }>
    }> = []
    for (const plugin of externalItems) {
      if (!isExternalPluginActive(plugin, enabledPlugins)) continue
      const matching = (plugin.contributes?.ui_slots || []).filter(
        (slot) =>
          slot.slot === 'chat.git_menu.items' &&
          (!slot.requires_workspace || Boolean(session.workspace)),
      )
      if (matching.length === 0) continue
      groups.push({
        key: `ext:${plugin.id}`,
        label: matching[0].badge || plugin.name,
        items: matching.map((slot) => ({
          key: `${plugin.id}:${slot.id}`,
          label: slot.label || plugin.name,
          onClick: () => triggerSlotClickAction(plugin, slot.on_click, session),
        })),
      })
    }
    return groups
  }, [externalItems, enabledPlugins, session])

  if (builtinGroups.length === 0 && externalGroups.length === 0) {
    return null
  }

  return (
    <>
      {builtinGroups.map((group) => (
        <Fragment key={group.key}>
          <Menu.Divider />
          {group.label && <Menu.Label>{group.label}</Menu.Label>}
          {group.items.map((item) => {
            const Icon = item.icon
            return (
              <Menu.Item
                key={item.key}
                leftSection={Icon ? <Icon size={16} color={item.iconColor} /> : <IconPuzzle size={16} />}
                onClick={item.onClick}
              >
                <Text size="xs">{item.label}</Text>
              </Menu.Item>
            )
          })}
        </Fragment>
      ))}
      {externalGroups.map((group) => (
        <Fragment key={group.key}>
          <Menu.Divider />
          <Menu.Label>{group.label}</Menu.Label>
          {group.items.map((item) => (
            <Menu.Item
              key={item.key}
              leftSection={<IconPuzzle size={16} color="var(--astr-indigo, #5B5BD6)" />}
              onClick={item.onClick}
            >
              <Text size="xs">{item.label}</Text>
            </Menu.Item>
          ))}
        </Fragment>
      ))}
    </>
  )
}

export function useWelcomeMenuExtensions(
  session: Session | null | undefined,
  isOfficeMode = false,
): WelcomeMenuExtraItem[] {
  const registryVersion = useExtensionRegistryVersion()
  const enabledPlugins = usePluginSettingsStore((s) => s.enabledPlugins)
  const externalItems = useExtensionStore((s) => s.items)

  return useMemo<WelcomeMenuExtraItem[]>(() => {
    void registryVersion
    const items: WelcomeMenuExtraItem[] = []
    const hasWorkspace = Boolean(session?.workspace)

    for (const reg of extensionRegistry.getAllBuiltins()) {
      if (enabledPlugins[reg.pluginId] === false) continue
      for (const entry of reg.welcomeMenuItems || []) {
        if (entry.requiresWorkspace && !hasWorkspace) continue
        if (entry.hideInOfficeMode && isOfficeMode) continue
        const Icon = entry.icon
        items.push({
          id: `${reg.pluginId}:${entry.id}`,
          icon: Icon ? (
            <Icon size={20} color={entry.iconColor} />
          ) : (
            <IconPuzzle size={20} color={entry.iconColor || 'var(--astr-indigo, #5B5BD6)'} />
          ),
          label: entry.label,
          shortcut: entry.shortcut,
          onClick: () => {
            if (session) entry.onClick(session)
          },
        })
      }
    }

    for (const plugin of externalItems) {
      if (!isExternalPluginActive(plugin, enabledPlugins)) continue
      for (const slot of plugin.contributes?.ui_slots || []) {
        if (slot.slot !== 'sidecar.welcome_menu') continue
        if (slot.requires_workspace && !hasWorkspace) continue
        if (isOfficeMode && plugin.category !== 'office') continue
        items.push({
          id: `ext:${plugin.id}:${slot.id}`,
          icon: <IconPuzzle size={20} color="var(--astr-indigo, #5B5BD6)" />,
          label: slot.label || plugin.name,
          shortcut: slot.badge || '扩展',
          onClick: () => triggerSlotClickAction(plugin, slot.on_click, session),
        })
      }
    }

    return items
  }, [registryVersion, enabledPlugins, externalItems, session, isOfficeMode])
}

export function PluginPanelViewer({
  artifact,
  session,
  isActive,
  surface,
  onClose,
}: ViewerContext) {
  const registryVersion = useExtensionRegistryVersion()
  const externalItems = useExtensionStore((s) => s.items)
  const hydrate = useExtensionStore((s) => s.hydrate)
  const hydrated = useExtensionStore((s) => s.hydrated)

  const parsed = useMemo(() => parsePluginPanelArtifact(artifact), [artifact])

  const builtinMatch = useMemo(() => {
    void registryVersion
    return extensionRegistry.findBuiltinPanel(parsed.pluginId, parsed.panelId, artifact.mediaType)
  }, [registryVersion, parsed.pluginId, parsed.panelId, artifact.mediaType])

  useEffect(() => {
    if (!builtinMatch && !hydrated) {
      void hydrate()
    }
  }, [builtinMatch, hydrated, hydrate])

  if (builtinMatch) {
    const PanelComponent = builtinMatch.panel.component
    return (
      <PanelComponent
        pluginId={builtinMatch.pluginId}
        panelId={builtinMatch.panelId}
        artifact={artifact}
        session={session}
        isActive={isActive}
        surface={surface}
        onClose={onClose}
      />
    )
  }

  const plugin = externalItems.find((item) => item.id === parsed.pluginId)
  const panel = plugin?.contributes?.sidecar_panels?.find(
    (entry) =>
      (parsed.panelId && entry.id === parsed.panelId) ||
      (entry.media_type && entry.media_type === artifact.mediaType),
  )

  if (!plugin || !panel || !plugin.enabled || plugin.permissions_granted === false || !panel.ui_entry || !plugin.ui_base_url) {
    return (
      <div className="ext-panel-fallback">
        <Paper withBorder radius="md" p="lg" style={{ maxWidth: 420, width: '100%' }}>
          <Stack gap="sm" align="center" ta="center">
            <IconPuzzle size={28} color="var(--astr-muted)" />
            <Text size="sm" fw={600}>
              {plugin ? `${plugin.name} 面板暂不可用` : '未找到对应的扩展面板'}
            </Text>
            <Text size="xs" c="dimmed">
              {!plugin
                ? '该扩展可能尚未安装或已被卸载。'
                : !plugin.enabled
                  ? '该扩展当前处于停用状态，请在插件管理中启用后再打开。'
                  : '该扩展缺少可用的界面入口或尚未完成权限授权。'}
            </Text>
            <Button
              size="xs"
              variant="light"
              onClick={() => {
                window.dispatchEvent(
                  new CustomEvent('astrorder:open-settings', {
                    detail: { tab: 'plugins', pluginId: parsed.pluginId || undefined },
                  }),
                )
              }}
            >
              前往插件管理
            </Button>
          </Stack>
        </Paper>
      </div>
    )
  }

  const frameSrc = joinUiEntryUrl(plugin.ui_base_url, panel.ui_entry)
  return (
    <div className="ext-panel-host">
      <SandboxedPluginFrame
        pluginId={plugin.id}
        src={frameSrc}
        title={panel.title || plugin.name}
        session={session}
        params={parsed.params}
      />
    </div>
  )
}
