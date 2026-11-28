import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Center,
  Group,
  Loader,
  Modal,
  Paper,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconAlertTriangle,
  IconCheck,
  IconCloudDownload,
  IconExternalLink,
  IconFileCode,
  IconPlus,
  IconPuzzle,
  IconRefresh,
  IconSearch,
  IconServer,
  IconShieldCheck,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import {
  extensionsApi,
  type MarketplacePluginEntry,
  type PluginRegistrySource,
  type PluginView,
} from '../../api/extensions'
import { useExtensionStore } from './extensionStore'

export interface PluginMarketplaceViewProps {
  query?: string
  onInstalled?: (plugin: PluginView, requestEnable: boolean) => void
  onSelectInstalled?: (pluginId: string) => void
}

const CATEGORY_LABELS: Record<string, string> = {
  all: '全部',
  workspace: '工作区模块',
  development: '开发与调试',
  office: '办公协作',
  diagram: '图表与白板',
  media: '多媒体与 3D',
  agent: '智能体扩展',
  runtime: '运行时工具',
}

export function PluginMarketplaceView({
  query: externalQuery = '',
  onInstalled,
  onSelectInstalled,
}: PluginMarketplaceViewProps) {
  const refreshExtensions = useExtensionStore((s) => s.refresh)
  const [items, setItems] = useState<MarketplacePluginEntry[]>([])
  const [registries, setRegistries] = useState<PluginRegistrySource[]>([])
  const [errors, setErrors] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [installingId, setInstallingId] = useState<string | null>(null)
  const [localQuery, setLocalQuery] = useState('')
  const [selectedCategory, setSelectedCategory] = useState<string>('all')
  const [registryModalOpen, setRegistryModalOpen] = useState(false)
  const [savingRegistries, setSavingRegistries] = useState(false)
  const [newRegName, setNewRegName] = useState('')
  const [newRegUrl, setNewRegUrl] = useState('')

  const loadMarketplace = useCallback(async () => {
    setLoading(true)
    try {
      const res = await extensionsApi.getMarketplace()
      setItems(Array.isArray(res.items) ? res.items : [])
      setRegistries(Array.isArray(res.registries) ? res.registries : [])
      setErrors(Array.isArray(res.errors) ? res.errors : [])
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '加载插件商店目录失败',
      })
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void loadMarketplace()
  }, [loadMarketplace])

  const effectiveQuery = (localQuery || externalQuery).trim().toLowerCase()

  const categories = useMemo(() => {
    const counts = new Map<string, number>()
    for (const item of items) {
      const cat = item.category || 'workspace'
      counts.set(cat, (counts.get(cat) || 0) + 1)
    }
    return [
      { id: 'all', label: '全部', count: items.length },
      ...Array.from(counts.entries()).map(([id, count]) => ({
        id,
        label: CATEGORY_LABELS[id] || id,
        count,
      })),
    ]
  }, [items])

  const filteredItems = useMemo(() => {
    return items.filter((item) => {
      if (selectedCategory !== 'all' && (item.category || 'workspace') !== selectedCategory) {
        return false
      }
      if (!effectiveQuery) return true
      const haystack = [
        item.id,
        item.name,
        item.description || '',
        item.author || '',
        item.category || '',
        item.registry_name || '',
        ...(item.extensions || []),
      ]
        .join(' ')
        .toLowerCase()
      return haystack.includes(effectiveQuery)
    })
  }, [items, selectedCategory, effectiveQuery])

  const handleInstallRemote = async (entry: MarketplacePluginEntry) => {
    if (!entry.download_url || installingId) return
    setInstallingId(entry.id)
    try {
      const res = await extensionsApi.installRemotePlugin({
        download_url: entry.download_url,
        sha256: entry.sha256,
      })
      await refreshExtensions()
      await loadMarketplace()
      notifications.show({
        color: 'teal',
        message: entry.update_available
          ? `已更新插件「${entry.name}」至 v${entry.version}`
          : `已安装插件「${entry.name}」`,
      })
      if (res?.item) {
        onInstalled?.(res.item, true)
      }
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : `安装插件「${entry.name}」失败`,
      })
    } finally {
      setInstallingId(null)
    }
  }

  const persistRegistries = async (next: PluginRegistrySource[]) => {
    setSavingRegistries(true)
    try {
      const res = await extensionsApi.saveRegistries(next)
      setRegistries(Array.isArray(res.items) ? res.items : next)
      await loadMarketplace()
      notifications.show({ color: 'teal', message: '插件源配置已保存' })
    } catch (err) {
      notifications.show({
        color: 'red',
        message: err instanceof Error ? err.message : '保存插件源失败',
      })
    } finally {
      setSavingRegistries(false)
    }
  }

  const handleAddRegistry = async () => {
    const url = newRegUrl.trim()
    if (!url) return
    const name = newRegName.trim() || url.replace(/^https?:\/\//i, '').split('/')[0] || '自定义源'
    const id = `custom-${Date.now().toString(36)}`
    const next: PluginRegistrySource[] = [
      ...registries,
      { id, name, url, enabled: true, official: false },
    ]
    setNewRegName('')
    setNewRegUrl('')
    await persistRegistries(next)
  }

  return (
    <Stack gap="md">
      <Group justify="space-between" wrap="wrap" gap="xs">
        <Group gap="xs" wrap="wrap">
          <TextInput
            size="xs"
            placeholder="搜索商店插件、文件扩展名…"
            aria-label="搜索插件商店"
            value={localQuery}
            onChange={(e) => setLocalQuery(e.currentTarget.value)}
            leftSection={<IconSearch size={14} />}
            rightSection={
              localQuery ? (
                <ActionIcon
                  size="xs"
                  variant="subtle"
                  color="gray"
                  aria-label="清空商店搜索"
                  onClick={() => setLocalQuery('')}
                >
                  <IconX size={12} />
                </ActionIcon>
              ) : null
            }
            style={{ width: 240 }}
          />
          <Group gap={6} wrap="wrap">
            {categories.map((cat) => (
              <Button
                key={cat.id}
                size="compact-xs"
                variant={selectedCategory === cat.id ? 'filled' : 'light'}
                color={selectedCategory === cat.id ? 'indigo' : 'gray'}
                onClick={() => setSelectedCategory(cat.id)}
              >
                {cat.label} ({cat.count})
              </Button>
            ))}
          </Group>
        </Group>

        <Group gap="xs">
          <Button
            size="xs"
            variant="default"
            leftSection={<IconServer size={14} />}
            onClick={() => setRegistryModalOpen(true)}
          >
            管理插件源 ({registries.filter((r) => r.enabled).length}/{registries.length})
          </Button>
          <Tooltip label="刷新商店目录">
            <ActionIcon
              size="sm"
              variant="subtle"
              color="gray"
              aria-label="刷新插件商店"
              loading={loading}
              onClick={() => void loadMarketplace()}
            >
              <IconRefresh size={15} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>

      {errors.length > 0 && (
        <Alert
          color="yellow"
          variant="light"
          icon={<IconAlertTriangle size={16} />}
          title="部分插件源拉取失败"
        >
          <Stack gap={4}>
            {errors.map((err, idx) => (
              <Text key={idx} size="xs">
                {err}
              </Text>
            ))}
          </Stack>
        </Alert>
      )}

      {loading && items.length === 0 ? (
        <Center py="xl">
          <Loader size="sm" color="indigo" />
        </Center>
      ) : filteredItems.length === 0 ? (
        <Paper withBorder radius="md" p="xl" ta="center" style={{ background: 'var(--astr-surface)' }}>
          <Stack align="center" gap="xs">
            <IconPuzzle size={26} color="var(--astr-muted)" />
            <Text size="sm" c="dimmed">
              {effectiveQuery
                ? `插件商店中未找到匹配「${effectiveQuery}」的扩展`
                : '当前启用的插件源暂无可用插件，可点击右上角「管理插件源」添加企业或社区插件仓库。'}
            </Text>
          </Stack>
        </Paper>
      ) : (
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="sm">
          {filteredItems.map((entry) => {
            const isInstalling = installingId === entry.id
            const canDownload = Boolean(entry.download_url?.trim())
            return (
              <Card key={`${entry.registry_id || 'reg'}:${entry.id}`} withBorder className="plugins-tile">
                <div>
                  <div className="plugins-tile-head">
                    <div className="plugins-tile-title-row">
                      <span className="plugins-tile-icon" aria-hidden="true">
                        <IconPuzzle size={17} />
                      </span>
                      <Text fw={600} size="sm" truncate>
                        {entry.name}
                      </Text>
                      <Badge size="xs" variant="light" color="gray">
                        v{entry.version}
                      </Badge>
                    </div>
                    {entry.official ? (
                      <Badge size="xs" variant="light" color="indigo" leftSection={<IconShieldCheck size={11} />}>
                        官方
                      </Badge>
                    ) : (
                      <Badge size="xs" variant="light" color="teal">
                        {entry.registry_name || '第三方源'}
                      </Badge>
                    )}
                  </div>

                  <div className="plugins-tile-desc">
                    {entry.description || '未提供插件说明'}
                  </div>

                  <Group gap={4} mt={8} wrap="wrap">
                    {entry.frontend_mode && (
                      <Badge size="xs" variant="outline" color="violet">
                        {entry.frontend_mode.toUpperCase()}
                      </Badge>
                    )}
                    {(entry.extensions || []).slice(0, 6).map((ext) => (
                      <Badge
                        key={ext}
                        size="xs"
                        variant="light"
                        color="blue"
                        leftSection={<IconFileCode size={10} />}
                      >
                        {ext}
                      </Badge>
                    ))}
                    {(entry.extensions?.length || 0) > 6 && (
                      <Badge size="xs" variant="light" color="gray">
                        +{(entry.extensions?.length || 0) - 6}
                      </Badge>
                    )}
                    {(entry.permissions?.length || 0) > 0 && (
                      <Tooltip label={`声明权限：${entry.permissions!.join(', ')}`}>
                        <Badge size="xs" variant="light" color="orange">
                          {entry.permissions!.length} 项权限
                        </Badge>
                      </Tooltip>
                    )}
                  </Group>
                </div>

                <div className="plugins-tile-footer is-split">
                  <Group gap={6} wrap="nowrap">
                    {entry.author && (
                      <Text size="11px" c="dimmed" truncate>
                        {entry.author}
                      </Text>
                    )}
                    {entry.homepage && /^https?:\/\//i.test(entry.homepage) && (
                      <Tooltip label="打开主页">
                        <ActionIcon
                          size="xs"
                          variant="subtle"
                          color="gray"
                          component="a"
                          href={entry.homepage}
                          target="_blank"
                          rel="noreferrer"
                          aria-label={`打开 ${entry.name} 主页`}
                        >
                          <IconExternalLink size={12} />
                        </ActionIcon>
                      </Tooltip>
                    )}
                  </Group>

                  <Group gap={6} wrap="nowrap">
                    {entry.installed && !entry.update_available ? (
                      <>
                        <Badge size="xs" variant="light" color="teal" leftSection={<IconCheck size={11} />}>
                          已安装
                        </Badge>
                        {onSelectInstalled && (
                          <Button
                            size="compact-xs"
                            variant="subtle"
                            color="gray"
                            onClick={() => onSelectInstalled(entry.id)}
                          >
                            管理
                          </Button>
                        )}
                      </>
                    ) : canDownload ? (
                      <Button
                        size="compact-xs"
                        variant={entry.update_available ? 'filled' : 'light'}
                        color="indigo"
                        loading={isInstalling}
                        disabled={Boolean(installingId) && !isInstalling}
                        leftSection={<IconCloudDownload size={13} />}
                        onClick={() => void handleInstallRemote(entry)}
                      >
                        {entry.update_available ? `更新至 v${entry.version}` : '安装'}
                      </Button>
                    ) : (
                      <Badge size="xs" variant="light" color="gray">
                        {entry.installed ? '已内置' : '不可下载'}
                      </Badge>
                    )}
                  </Group>
                </div>
              </Card>
            )
          })}
        </SimpleGrid>
      )}

      <Modal
        opened={registryModalOpen}
        onClose={() => setRegistryModalOpen(false)}
        title="管理插件仓库源"
        size="lg"
        radius="md"
      >
        <Stack gap="md">
          <Text size="xs" c="dimmed">
            支持官方内置目录及自定义 HTTP/HTTPS/本地 file:// 插件索引 JSON（包含 plugins[]、download_url 与可选 sha256 校验）。
          </Text>

          <Stack gap="xs">
            {registries.map((reg) => (
              <Paper key={reg.id} withBorder p="xs" radius="sm">
                <Group justify="space-between" wrap="nowrap" gap="xs">
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <Group gap={6} wrap="nowrap">
                      <Text size="xs" fw={600} truncate>
                        {reg.name}
                      </Text>
                      {reg.official && (
                        <Badge size="xs" variant="light" color="indigo">
                          官方内置
                        </Badge>
                      )}
                    </Group>
                    <Text size="11px" c="dimmed" truncate>
                      {reg.url}
                    </Text>
                  </div>
                  <Group gap="xs" wrap="nowrap">
                    <Switch
                      size="xs"
                      aria-label={`启用插件源 ${reg.name}`}
                      checked={reg.enabled}
                      disabled={savingRegistries}
                      onChange={(e) => {
                        const checked = e.currentTarget.checked
                        const next = registries.map((item) =>
                          item.id === reg.id ? { ...item, enabled: checked } : item,
                        )
                        void persistRegistries(next)
                      }}
                    />
                    {!reg.official && (
                      <ActionIcon
                        size="sm"
                        variant="subtle"
                        color="red"
                        aria-label={`删除插件源 ${reg.name}`}
                        disabled={savingRegistries}
                        onClick={() => {
                          const next = registries.filter((item) => item.id !== reg.id)
                          void persistRegistries(next)
                        }}
                      >
                        <IconTrash size={14} />
                      </ActionIcon>
                    )}
                  </Group>
                </Group>
              </Paper>
            ))}
          </Stack>

          <Paper withBorder p="sm" radius="sm" style={{ background: 'var(--astr-surface-muted)' }}>
            <Stack gap="xs">
              <Text size="xs" fw={600}>
                添加自定义插件源
              </Text>
              <Group grow gap="xs">
                <TextInput
                  size="xs"
                  placeholder="插件源名称（例如：团队私有商店）"
                  value={newRegName}
                  onChange={(e) => setNewRegName(e.currentTarget.value)}
                />
                <TextInput
                  size="xs"
                  placeholder="https://example.com/astrorder-registry.json"
                  value={newRegUrl}
                  onChange={(e) => setNewRegUrl(e.currentTarget.value)}
                />
              </Group>
              <Group justify="flex-end">
                <Button
                  size="xs"
                  leftSection={<IconPlus size={13} />}
                  loading={savingRegistries}
                  disabled={!newRegUrl.trim()}
                  onClick={() => void handleAddRegistry()}
                >
                  添加源
                </Button>
              </Group>
            </Stack>
          </Paper>
        </Stack>
      </Modal>
    </Stack>
  )
}
