import { useEffect, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Paper,
  Stack,
  Switch,
  Text,
  Title,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconArrowDown,
  IconArrowUp,
  IconExternalLink,
  IconGripVertical,
  IconPencil,
  IconPlugConnected,
  IconPlus,
  IconRefresh,
  IconTrash,
} from '@tabler/icons-react'
import { api, type IntegrationItem } from '../../api/client'
import { SIDEBAR_INTEGRATIONS_PLUGIN_ID, usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { IntegrationFormModal } from './IntegrationFormModal'
import { IntegrationIcon } from './integrationIcons'
import { useIntegrationsStore } from './integrationStore'
import './integrations.css'

export function IntegrationsSettingsPage() {
  const pluginEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(SIDEBAR_INTEGRATIONS_PLUGIN_ID))
  const togglePlugin = usePluginSettingsStore((s) => s.togglePlugin)

  const items = useIntegrationsStore((s) => s.items)
  const loading = useIntegrationsStore((s) => s.loading)
  const hydrate = useIntegrationsStore((s) => s.hydrate)
  const update = useIntegrationsStore((s) => s.update)
  const remove = useIntegrationsStore((s) => s.remove)
  const reorder = useIntegrationsStore((s) => s.reorder)

  const [modalOpened, setModalOpened] = useState(false)
  const [editing, setEditing] = useState<IntegrationItem | null>(null)
  const [refreshingId, setRefreshingId] = useState<string | null>(null)
  const [draggingId, setDraggingId] = useState<string | null>(null)

  useEffect(() => {
    void hydrate(true)
  }, [hydrate])

  const visibleCount = items.filter((item) => item.visible).length

  const handleToggleVisible = async (item: IntegrationItem, nextVisible: boolean) => {
    try {
      await update(item.id, { visible: nextVisible })
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '更新显示状态失败' })
    }
  }

  const displayLabel = (item: IntegrationItem) => {
    if (item.name.trim()) return item.name.trim()
    try {
      return new URL(item.url).hostname || item.url
    } catch {
      return item.url
    }
  }

  const handleDelete = async (item: IntegrationItem) => {
    try {
      await remove(item.id)
      notifications.show({ color: 'teal', message: `已移除集成“${displayLabel(item)}”` })
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '删除集成失败' })
    }
  }

  const handleRefreshIcon = async (item: IntegrationItem) => {
    setRefreshingId(item.id)
    try {
      const meta = await api.fetchIntegrationMeta(item.url)
      if (!meta.icon_data_url) {
        notifications.show({ color: 'orange', message: '未从目标站点读取到可用图标' })
        return
      }
      await update(item.id, {
        icon_type: 'auto',
        icon_value: meta.icon_data_url,
        auto_icon_value: meta.icon_data_url,
      })
      notifications.show({ color: 'teal', message: `已刷新“${displayLabel(item)}”的站点图标` })
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '刷新图标失败' })
    } finally {
      setRefreshingId(null)
    }
  }

  const moveItem = async (index: number, delta: -1 | 1) => {
    const target = index + delta
    if (target < 0 || target >= items.length) return
    const nextIds = items.map((item) => item.id)
    const [moved] = nextIds.splice(index, 1)
    nextIds.splice(target, 0, moved)
    try {
      await reorder(nextIds)
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '保存排序失败' })
    }
  }

  const handleDropOn = async (targetId: string) => {
    if (!draggingId || draggingId === targetId) {
      setDraggingId(null)
      return
    }
    const nextIds = items.map((item) => item.id)
    const fromIdx = nextIds.indexOf(draggingId)
    const toIdx = nextIds.indexOf(targetId)
    setDraggingId(null)
    if (fromIdx < 0 || toIdx < 0) return
    const [moved] = nextIds.splice(fromIdx, 1)
    nextIds.splice(toIdx, 0, moved)
    try {
      await reorder(nextIds)
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '保存排序失败' })
    }
  }

  return (
    <div className="route-page integrations-settings-page" style={{ padding: '24px 32px', maxWidth: 1100, margin: '0 auto' }}>
      <Paper p="md" withBorder radius="lg" mb="lg" style={{ background: 'var(--astr-surface)' }}>
        <Group justify="space-between" align="center" wrap="wrap" gap="sm">
          <Group gap="sm">
            <div
              style={{
                width: 38,
                height: 38,
                borderRadius: 10,
                background: 'color-mix(in srgb, var(--astr-indigo) 12%, transparent)',
                color: 'var(--astr-indigo)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <IconPlugConnected size={22} />
            </div>
            <div>
              <Title order={2} size="h3">集成管理</Title>
              <Text size="xs" c="dimmed" mt={2}>
                将第三方网关、监控面板、文档站等外链挂载到侧边栏；支持自动抓取站点图标、自定义图标、排序与显示控制，所有配置保存在数据库。
              </Text>
            </div>
          </Group>

          <Group gap="xs">
            <Badge size="md" variant="light" color="indigo">
              侧栏显示 {visibleCount} / 共 {items.length}
            </Badge>
            <Button
              size="xs"
              color="violet"
              leftSection={<IconPlus size={15} />}
              onClick={() => {
                setEditing(null)
                setModalOpened(true)
              }}
            >
              添加集成
            </Button>
          </Group>
        </Group>
      </Paper>

      {!pluginEnabled && (
        <Alert color="yellow" mb="md" title="侧边栏外链集成插件当前已停用">
          <Group justify="space-between" align="center">
            <Text size="xs">启用插件后，下方开启显示的集成项与侧边栏“+”快捷按钮会立即出现。</Text>
            <Button size="xs" variant="light" color="violet" onClick={() => togglePlugin(SIDEBAR_INTEGRATIONS_PLUGIN_ID, true)}>
              立即启用
            </Button>
          </Group>
        </Alert>
      )}

      {items.length === 0 && !loading ? (
        <Card withBorder radius="md" p="xl" style={{ background: 'var(--astr-surface)', textAlign: 'center' }}>
          <Stack align="center" gap="xs">
            <IconPlugConnected size={32} color="var(--astr-muted)" />
            <Text fw={600} size="sm">还没有配置外部集成</Text>
            <Text size="xs" c="dimmed" maw={420}>
              可以添加 Magpie / OpenCodeX 网关后台、Grafana、GitLab、Portainer 等常用网页工具，点击左侧“+”按钮也能快速添加。
            </Text>
            <Button
              mt="xs"
              size="xs"
              color="violet"
              leftSection={<IconPlus size={15} />}
              onClick={() => {
                setEditing(null)
                setModalOpened(true)
              }}
            >
              添加第一个集成
            </Button>
          </Stack>
        </Card>
      ) : (
        <Stack gap="sm">
          {items.map((item, index) => (
            <Card
              key={item.id}
              withBorder
              radius="md"
              p="sm"
              draggable
              onDragStart={() => setDraggingId(item.id)}
              onDragOver={(e) => e.preventDefault()}
              onDrop={() => { void handleDropOn(item.id) }}
              onDragEnd={() => setDraggingId(null)}
              className={`integration-settings-row${draggingId === item.id ? ' is-dragging' : ''}${!item.visible ? ' is-hidden' : ''}`}
              style={{ background: 'var(--astr-surface)' }}
            >
              <Group justify="space-between" wrap="nowrap" gap="md">
                <Group gap="sm" wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
                  <span className="integration-drag-handle" aria-hidden="true" title="拖拽排序">
                    <IconGripVertical size={16} />
                  </span>
                  <div className="integration-row-icon-box">
                    <IntegrationIcon
                      name={item.name}
                      url={item.url}
                      iconType={item.icon_type}
                      iconValue={item.icon_value}
                      size={22}
                    />
                  </div>
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <Group gap="xs" wrap="nowrap">
                      <Text fw={600} size="sm" truncate>{displayLabel(item)}</Text>
                      {!item.name.trim() && <Badge size="xs" variant="light" color="gray">仅图标</Badge>}
                      <Badge size="xs" variant="light" color={item.open_mode === 'embed' ? 'violet' : 'gray'}>
                        {item.open_mode === 'embed' ? '应用内嵌' : '外部打开'}
                      </Badge>
                      {!item.visible && <Badge size="xs" variant="outline" color="gray">已隐藏</Badge>}
                    </Group>
                    <Text size="xs" c="dimmed" truncate>{item.url}</Text>
                  </div>
                </Group>

                <Group gap="xs" wrap="nowrap" style={{ flexShrink: 0 }}>
                  <Switch
                    size="sm"
                    aria-label={`在侧边栏显示 ${displayLabel(item)}`}
                    checked={item.visible}
                    onChange={(e) => { void handleToggleVisible(item, e.currentTarget.checked) }}
                  />
                  <Tooltip label="上移" withArrow>
                    <ActionIcon
                      variant="subtle"
                      color="gray"
                      size="sm"
                      aria-label={`上移 ${displayLabel(item)}`}
                      disabled={index === 0}
                      onClick={() => { void moveItem(index, -1) }}
                    >
                      <IconArrowUp size={15} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label="下移" withArrow>
                    <ActionIcon
                      variant="subtle"
                      color="gray"
                      size="sm"
                      aria-label={`下移 ${displayLabel(item)}`}
                      disabled={index === items.length - 1}
                      onClick={() => { void moveItem(index, 1) }}
                    >
                      <IconArrowDown size={15} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label="重新抓取站点图标" withArrow>
                    <ActionIcon
                      variant="subtle"
                      color="violet"
                      size="sm"
                      aria-label={`刷新 ${displayLabel(item)} 图标`}
                      loading={refreshingId === item.id}
                      onClick={() => { void handleRefreshIcon(item) }}
                    >
                      <IconRefresh size={15} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label="外部打开" withArrow>
                    <ActionIcon
                      component="a"
                      href={item.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      variant="subtle"
                      color="gray"
                      size="sm"
                      aria-label={`外部打开 ${displayLabel(item)}`}
                    >
                      <IconExternalLink size={15} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label="编辑" withArrow>
                    <ActionIcon
                      variant="subtle"
                      color="blue"
                      size="sm"
                      aria-label={`编辑 ${displayLabel(item)}`}
                      onClick={() => {
                        setEditing(item)
                        setModalOpened(true)
                      }}
                    >
                      <IconPencil size={15} />
                    </ActionIcon>
                  </Tooltip>
                  <Tooltip label="删除" withArrow>
                    <ActionIcon
                      variant="subtle"
                      color="red"
                      size="sm"
                      aria-label={`删除 ${displayLabel(item)}`}
                      onClick={() => { void handleDelete(item) }}
                    >
                      <IconTrash size={15} />
                    </ActionIcon>
                  </Tooltip>
                </Group>
              </Group>
            </Card>
          ))}
        </Stack>
      )}

      <IntegrationFormModal
        opened={modalOpened}
        onClose={() => {
          setModalOpened(false)
          setEditing(null)
        }}
        editing={editing}
      />
    </div>
  )
}
