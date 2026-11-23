import { useEffect, useRef, useState } from 'react'
import {
  Button,
  FileButton,
  Group,
  Modal,
  Paper,
  SegmentedControl,
  Stack,
  Switch,
  Text,
  TextInput,
  Tooltip,
  UnstyledButton,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import { IconRefresh, IconUpload, IconWand } from '@tabler/icons-react'
import {
  api,
  type IntegrationIconType,
  type IntegrationItem,
  type IntegrationOpenMode,
} from '../../api/client'
import { BUILTIN_INTEGRATION_ICONS, IntegrationIcon } from './integrationIcons'
import { useIntegrationsStore } from './integrationStore'

const MAX_UPLOAD_BYTES = 140 * 1024

function looksLikeHttpUrl(raw: string): boolean {
  try {
    const parsed = new URL(raw.trim())
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:') && Boolean(parsed.hostname)
  } catch {
    return false
  }
}

export function IntegrationFormModal({
  opened,
  onClose,
  editing,
  onSaved,
}: {
  opened: boolean
  onClose: () => void
  editing?: IntegrationItem | null
  onSaved?: (item: IntegrationItem) => void
}) {
  const create = useIntegrationsStore((s) => s.create)
  const update = useIntegrationsStore((s) => s.update)

  const [url, setUrl] = useState('')
  const [name, setName] = useState('')
  const [iconTab, setIconTab] = useState<'auto' | 'builtin' | 'custom'>('auto')
  const [customSubMode, setCustomSubMode] = useState<'image' | 'emoji'>('image')
  const [autoIconValue, setAutoIconValue] = useState('')
  const [builtinIcon, setBuiltinIcon] = useState('globe')
  const [customImageValue, setCustomImageValue] = useState('')
  const [emojiValue, setEmojiValue] = useState('')
  const [openMode, setOpenMode] = useState<IntegrationOpenMode>('embed')
  const [visible, setVisible] = useState(true)
  const [fetchingMeta, setFetchingMeta] = useState(false)
  const [metaHint, setMetaHint] = useState('')
  const [discoveredTitle, setDiscoveredTitle] = useState('')
  const [saving, setSaving] = useState(false)
  const lastFetchedUrl = useRef('')

  useEffect(() => {
    if (!opened) return
    setMetaHint('')
    setDiscoveredTitle('')
    setFetchingMeta(false)
    if (editing) {
      setUrl(editing.url)
      setName(editing.name)
      setAutoIconValue(editing.auto_icon_value || (editing.icon_type === 'auto' ? editing.icon_value : ''))
      setOpenMode(editing.open_mode || 'embed')
      setVisible(editing.visible)
      lastFetchedUrl.current = editing.url
      if (editing.icon_type === 'builtin') {
        setIconTab('builtin')
        setBuiltinIcon(editing.icon_value || 'globe')
        setCustomImageValue('')
        setEmojiValue('')
      } else if (editing.icon_type === 'emoji') {
        setIconTab('custom')
        setCustomSubMode('emoji')
        setEmojiValue(editing.icon_value || '')
        setCustomImageValue('')
      } else if (editing.icon_type === 'image') {
        setIconTab('custom')
        setCustomSubMode('image')
        setCustomImageValue(editing.icon_value || '')
        setEmojiValue('')
      } else {
        setIconTab('auto')
        setBuiltinIcon('globe')
        setCustomImageValue('')
        setEmojiValue('')
      }
    } else {
      setUrl('')
      setName('')
      setIconTab('auto')
      setCustomSubMode('image')
      setAutoIconValue('')
      setBuiltinIcon('globe')
      setCustomImageValue('')
      setEmojiValue('')
      setOpenMode('embed')
      setVisible(true)
      lastFetchedUrl.current = ''
    }
  }, [opened, editing])

  const effectiveIconType: IntegrationIconType =
    iconTab === 'auto' ? 'auto' : iconTab === 'builtin' ? 'builtin' : customSubMode

  const effectiveIconValue =
    effectiveIconType === 'auto'
      ? autoIconValue
      : effectiveIconType === 'builtin'
        ? builtinIcon
        : effectiveIconType === 'emoji'
          ? emojiValue.trim()
          : customImageValue.trim()

  const handleFetchMeta = async (explicit = false) => {
    const trimmed = url.trim()
    if (!looksLikeHttpUrl(trimmed)) {
      if (explicit) notifications.show({ color: 'red', message: '请输入完整的 http:// 或 https:// 地址' })
      return
    }
    if (!explicit && trimmed === lastFetchedUrl.current) return
    lastFetchedUrl.current = trimmed
    setFetchingMeta(true)
    setMetaHint('')
    try {
      const meta = await api.fetchIntegrationMeta(trimmed)
      setDiscoveredTitle(meta.title ? meta.title.slice(0, 48) : '')
      if (meta.icon_data_url) {
        setAutoIconValue(meta.icon_data_url)
        setMetaHint('已自动读取站点图标')
      } else if (meta.warning) {
        setMetaHint('未能读取图标，将使用首字母徽章或可手动选择图标')
      } else {
        setMetaHint('该站点未提供图标，可切换内置图标或自定义')
      }
    } catch (error) {
      if (explicit) {
        notifications.show({ color: 'orange', message: `自动读取失败：${error instanceof Error ? error.message : '请手动填写'}` })
      }
    } finally {
      setFetchingMeta(false)
    }
  }

  const handleUploadIcon = (file: File | null) => {
    if (!file) return
    if (file.size > MAX_UPLOAD_BYTES) {
      notifications.show({ color: 'red', message: '图标图片过大，请选择 140KB 以内的 PNG / SVG / ICO / WebP 文件' })
      return
    }
    const reader = new FileReader()
    reader.onload = () => {
      if (typeof reader.result === 'string' && reader.result.startsWith('data:image/')) {
        setCustomImageValue(reader.result)
        setIconTab('custom')
        setCustomSubMode('image')
      } else {
        notifications.show({ color: 'red', message: '仅支持图片文件' })
      }
    }
    reader.readAsDataURL(file)
  }

  const handleSubmit = async () => {
    const trimmedUrl = url.trim()
    if (!looksLikeHttpUrl(trimmedUrl)) {
      notifications.show({ color: 'red', message: '请输入有效的 http:// 或 https:// 外链地址' })
      return
    }
    if (effectiveIconType === 'emoji' && !emojiValue.trim()) {
      notifications.show({ color: 'red', message: '请输入一个 Emoji 图标或切换其他图标模式' })
      return
    }
    setSaving(true)
    try {
      const trimmedName = name.trim()
      const payload = {
        name: trimmedName,
        url: trimmedUrl,
        icon_type: effectiveIconType,
        icon_value: effectiveIconValue,
        auto_icon_value: autoIconValue,
        open_mode: openMode,
        visible,
      }
      const saved = editing
        ? await update(editing.id, payload)
        : await create(payload)
      const label = saved.name.trim() || saved.url
      notifications.show({ color: 'teal', message: editing ? `已更新集成“${label}”` : `已添加侧边栏集成“${label}”` })
      onSaved?.(saved)
      onClose()
    } catch (error) {
      notifications.show({ color: 'red', message: error instanceof Error ? error.message : '保存集成失败' })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={editing ? '编辑侧边栏集成' : '添加侧边栏集成'}
      centered
      size="lg"
    >
      <Stack gap="md">
        <Group align="flex-end" gap="xs" wrap="nowrap">
          <TextInput
            label="外链地址"
            placeholder="http://192.168.1.100:3430 或 https://grafana.example.com"
            value={url}
            onChange={(e) => setUrl(e.currentTarget.value)}
            onBlur={() => { void handleFetchMeta(false) }}
            style={{ flex: 1 }}
            required
            autoFocus={!editing}
          />
          <Button
            variant="light"
            color="violet"
            leftSection={<IconWand size={15} />}
            loading={fetchingMeta}
            disabled={!looksLikeHttpUrl(url)}
            onClick={() => { void handleFetchMeta(true) }}
          >
            自动读取
          </Button>
        </Group>

        <Group align="flex-start" gap="md" wrap="nowrap">
          <Stack gap={4} style={{ flex: 1 }}>
            <TextInput
              label="侧边栏名称（可选）"
              placeholder="可留空，留空则仅显示图标"
              value={name}
              onChange={(e) => setName(e.currentTarget.value)}
              maxLength={64}
            />
            {discoveredTitle && !name.trim() && (
              <Group gap={6} align="center">
                <Text size="xs" c="dimmed">网页标题：{discoveredTitle}</Text>
                <Button size="compact-xs" variant="subtle" color="violet" onClick={() => setName(discoveredTitle)}>
                  填入名称
                </Button>
              </Group>
            )}
          </Stack>
          <Paper withBorder radius="md" p="xs" className="integration-preview-tile">
            <Text size="10px" c="dimmed" ta="center" mb={4}>侧栏预览</Text>
            <div className={`integration-preview-btn${name.trim() ? '' : ' is-icon-only'}`}>
              <IntegrationIcon
                name={name}
                url={url}
                iconType={effectiveIconType}
                iconValue={effectiveIconValue}
                size={name.trim() ? 21 : 24}
              />
              {name.trim() && <span>{name.trim().slice(0, 4)}</span>}
            </div>
          </Paper>
        </Group>

        <Stack gap="xs">
          <Group justify="space-between" wrap="wrap" gap="xs">
            <Text size="sm" fw={500}>图标</Text>
            {metaHint && <Text size="xs" c="dimmed" style={{ minWidth: 0 }}>{metaHint}</Text>}
          </Group>
          <SegmentedControl
            fullWidth
            size="xs"
            value={iconTab}
            onChange={(v) => setIconTab(v as 'auto' | 'builtin' | 'custom')}
            data={[
              { value: 'auto', label: '自动读取站点图标' },
              { value: 'builtin', label: '内置图标' },
              { value: 'custom', label: '自定义图标' },
            ]}
          />

          {iconTab === 'auto' && (
            <Paper withBorder radius="md" p="sm">
              <Group justify="space-between" wrap="nowrap">
                <Group gap="sm">
                  <IntegrationIcon name={name} url={url} iconType="auto" iconValue={autoIconValue} size={28} />
                  <div>
                    <Text size="xs" fw={500}>
                      {autoIconValue ? '已获取站点 Favicon' : '尚未获取到图标，将自动显示首字母徽章'}
                    </Text>
                    <Text size="11px" c="dimmed">
                      图标由后端抓取并缓存在数据库，离线与内网切换均可秒开。
                    </Text>
                  </div>
                </Group>
                <Button
                  size="xs"
                  variant="subtle"
                  leftSection={<IconRefresh size={14} />}
                  loading={fetchingMeta}
                  disabled={!looksLikeHttpUrl(url)}
                  onClick={() => { void handleFetchMeta(true) }}
                >
                  重新抓取
                </Button>
              </Group>
            </Paper>
          )}

          {iconTab === 'builtin' && (
            <div className="integration-builtin-grid">
              {BUILTIN_INTEGRATION_ICONS.map((entry) => {
                const Icon = entry.component
                const selected = builtinIcon === entry.id
                return (
                  <Tooltip key={entry.id} label={entry.label} withArrow openDelay={150}>
                    <UnstyledButton
                      type="button"
                      className={`integration-builtin-cell${selected ? ' is-selected' : ''}`}
                      aria-label={entry.label}
                      aria-pressed={selected}
                      onClick={() => setBuiltinIcon(entry.id)}
                    >
                      <Icon size={20} stroke={1.8} />
                    </UnstyledButton>
                  </Tooltip>
                )
              })}
            </div>
          )}

          {iconTab === 'custom' && (
            <Paper withBorder radius="md" p="sm">
              <Stack gap="xs">
                <SegmentedControl
                  size="xs"
                  value={customSubMode}
                  onChange={(v) => setCustomSubMode(v as 'image' | 'emoji')}
                  data={[
                    { value: 'image', label: '图片地址 / 上传' },
                    { value: 'emoji', label: 'Emoji 表情' },
                  ]}
                />
                {customSubMode === 'image' ? (
                  <Group align="flex-end" gap="xs" wrap="nowrap">
                    <TextInput
                      label="图片 URL 或上传本地图标"
                      placeholder="https://example.com/icon.png 或点击右侧上传"
                      value={customImageValue}
                      onChange={(e) => setCustomImageValue(e.currentTarget.value)}
                      style={{ flex: 1 }}
                      size="xs"
                    />
                    <FileButton onChange={handleUploadIcon} accept="image/png,image/jpeg,image/svg+xml,image/webp,image/x-icon,image/gif">
                      {(props) => (
                        <Button {...props} size="xs" variant="light" leftSection={<IconUpload size={14} />}>
                          上传图片
                        </Button>
                      )}
                    </FileButton>
                  </Group>
                ) : (
                  <TextInput
                    label="输入 Emoji"
                    placeholder="例如：🚀、📊、🧭"
                    value={emojiValue}
                    onChange={(e) => setEmojiValue(e.currentTarget.value)}
                    size="xs"
                    maxLength={16}
                  />
                )}
              </Stack>
            </Paper>
          )}
        </Stack>

        <Group justify="space-between" align="center" wrap="wrap" gap="sm">
          <div>
            <Text size="xs" fw={500} mb={4}>打开方式</Text>
            <SegmentedControl
              size="xs"
              value={openMode}
              onChange={(v) => setOpenMode(v as IntegrationOpenMode)}
              data={[
                { value: 'embed', label: '应用内嵌' },
                { value: 'external', label: '外部浏览器' },
              ]}
            />
          </div>
          <Switch
            label="在侧边栏显示"
            checked={visible}
            onChange={(e) => setVisible(e.currentTarget.checked)}
          />
        </Group>

        <Group justify="flex-end" mt="xs">
          <Button variant="default" onClick={onClose}>取消</Button>
          <Button color="violet" loading={saving} disabled={!looksLikeHttpUrl(url)} onClick={() => { void handleSubmit() }}>
            {editing ? '保存修改' : '添加到侧边栏'}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
