import { useRef, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  FileInput,
  Group,
  Modal,
  Stack,
  Tabs,
  Text,
  TextInput,
} from '@mantine/core'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconFolder,
  IconPackageImport,
  IconRefresh,
  IconShieldLock,
  IconUpload,
} from '@tabler/icons-react'
import type { PluginView } from '../../api/extensions'
import { useExtensionStore } from './extensionStore'
import './extensions.css'

export interface InstallPluginModalProps {
  opened: boolean
  onClose: () => void
  onInstalled?: (plugin: PluginView, requestEnable: boolean) => void
  onReloaded?: (items: PluginView[]) => void
}

export function InstallPluginModal({
  opened,
  onClose,
  onInstalled,
  onReloaded,
}: InstallPluginModalProps) {
  const installFromPath = useExtensionStore((s) => s.installFromPath)
  const installArchive = useExtensionStore((s) => s.installArchive)
  const reload = useExtensionStore((s) => s.reload)

  const [tab, setTab] = useState<string | null>('path')
  const [sourcePath, setSourcePath] = useState('')
  const [archiveFile, setArchiveFile] = useState<File | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [installedPlugin, setInstalledPlugin] = useState<PluginView | null>(null)
  const fileInputRef = useRef<HTMLButtonElement | null>(null)

  const resetAndClose = () => {
    setError(null)
    setInstalledPlugin(null)
    setSourcePath('')
    setArchiveFile(null)
    onClose()
  }

  const handleInstallPath = async () => {
    const clean = sourcePath.trim()
    if (!clean || submitting) return
    setSubmitting(true)
    setError(null)
    try {
      const plugin = await installFromPath(clean)
      setInstalledPlugin(plugin)
      notifications.show({
        color: 'teal',
        message: `已安装扩展「${plugin.name}」（默认停用）`,
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : '从目录安装扩展失败')
    } finally {
      setSubmitting(false)
    }
  }

  const handleInstallArchive = async () => {
    if (!archiveFile || submitting) return
    setSubmitting(true)
    setError(null)
    try {
      const plugin = await installArchive(archiveFile)
      setInstalledPlugin(plugin)
      notifications.show({
        color: 'teal',
        message: `已安装扩展包「${plugin.name}」（默认停用）`,
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : '上传安装扩展包失败')
    } finally {
      setSubmitting(false)
    }
  }

  const handleRescan = async () => {
    if (reloading) return
    setReloading(true)
    setError(null)
    try {
      const items = await reload()
      onReloaded?.(items)
      const extCount = items.filter((item) => item.source === 'external').length
      notifications.show({
        color: 'teal',
        message: `已完成目录扫描，发现 ${extCount} 个外部扩展`,
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : '重新扫描插件目录失败')
    } finally {
      setReloading(false)
    }
  }

  return (
    <Modal
      opened={opened}
      onClose={resetAndClose}
      size="md"
      radius="lg"
      centered
      title={
        <Group gap="xs">
          <IconPackageImport size={18} color="var(--astr-indigo, #5B5BD6)" />
          <Text fw={650} size="md">
            安装外部插件
          </Text>
        </Group>
      }
    >
      {installedPlugin ? (
        <Stack gap="md">
          <Alert color="teal" variant="light" radius="md" icon={<IconCheck size={16} />}>
            <Text size="sm" fw={600}>
              扩展「{installedPlugin.name}」安装成功
            </Text>
            <Text size="xs" mt={4}>
              出于安全保护，新安装的外部扩展默认处于停用状态。
              {installedPlugin.permissions && installedPlugin.permissions.length > 0
                ? ` 该扩展声明了 ${installedPlugin.permissions.length} 项权限，启用前需经过您的授权确认。`
                : ' 您可以立即启用该扩展。'}
            </Text>
          </Alert>

          <Group gap="xs">
            <Badge size="xs" variant="light" color="indigo">
              {installedPlugin.id}
            </Badge>
            <Badge size="xs" variant="outline" color="gray">
              v{installedPlugin.version}
            </Badge>
            <Badge size="xs" variant="light" color="gray">
              后端: {installedPlugin.backend_type}
            </Badge>
          </Group>

          <Group justify="flex-end" gap="xs" mt="xs">
            <Button
              size="xs"
              variant="default"
              onClick={() => {
                const target = installedPlugin
                resetAndClose()
                onInstalled?.(target, false)
              }}
            >
              稍后启用
            </Button>
            <Button
              size="xs"
              color="indigo"
              leftSection={<IconShieldLock size={14} />}
              onClick={() => {
                const target = installedPlugin
                resetAndClose()
                onInstalled?.(target, true)
              }}
            >
              查看权限并启用
            </Button>
          </Group>
        </Stack>
      ) : (
        <Stack gap="md">
          {error && (
            <Alert color="red" variant="light" radius="md">
              {error}
            </Alert>
          )}

          <Tabs value={tab} onChange={(next) => { setTab(next); setError(null) }}>
            <Tabs.List mb="md">
              <Tabs.Tab value="path" leftSection={<IconFolder size={14} />}>
                本地目录安装
              </Tabs.Tab>
              <Tabs.Tab value="archive" leftSection={<IconUpload size={14} />}>
                上传插件包
              </Tabs.Tab>
            </Tabs.List>

            <Tabs.Panel value="path">
              <Stack gap="sm">
                <TextInput
                  label="插件目录路径"
                  description="输入包含 plugin.json 的本机文件夹绝对路径，系统会校验清单并复制到扩展目录"
                  placeholder="/path/to/hello-plugin 或 C:\plugins\hello-plugin"
                  value={sourcePath}
                  onChange={(e) => setSourcePath(e.currentTarget.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && sourcePath.trim()) {
                      e.preventDefault()
                      void handleInstallPath()
                    }
                  }}
                />
                <Group justify="flex-end">
                  <Button
                    size="xs"
                    color="indigo"
                    loading={submitting}
                    disabled={!sourcePath.trim()}
                    onClick={() => {
                      void handleInstallPath()
                    }}
                  >
                    从目录安装
                  </Button>
                </Group>
              </Stack>
            </Tabs.Panel>

            <Tabs.Panel value="archive">
              <Stack gap="sm">
                <div
                  className="ext-dropzone"
                  onDragOver={(e) => e.preventDefault()}
                  onDrop={(e) => {
                    e.preventDefault()
                    const dropped = e.dataTransfer.files?.[0]
                    if (dropped) {
                      setArchiveFile(dropped)
                      setError(null)
                    }
                  }}
                >
                  <Stack gap={6} align="center">
                    <IconUpload size={22} color="var(--astr-indigo, #5B5BD6)" />
                    <Text size="xs" fw={500}>
                      拖拽 .zip / .astrpkg 插件包到此处，或在下方选择文件
                    </Text>
                    <Text size="11px" c="dimmed">
                      安装器会自动校验清单结构与路径安全性，不会自动运行未授权代码
                    </Text>
                  </Stack>
                </div>

                <FileInput
                  ref={fileInputRef}
                  label="插件压缩包"
                  placeholder="点击选择 .zip 或 .astrpkg 文件"
                  accept=".zip,.astrpkg,application/zip"
                  value={archiveFile}
                  onChange={(file) => {
                    setArchiveFile(file)
                    setError(null)
                  }}
                  clearable
                />

                <Group justify="flex-end">
                  <Button
                    size="xs"
                    color="indigo"
                    loading={submitting}
                    disabled={!archiveFile}
                    onClick={() => {
                      void handleInstallArchive()
                    }}
                  >
                    上传并安装
                  </Button>
                </Group>
              </Stack>
            </Tabs.Panel>
          </Tabs>

          <Group
            justify="space-between"
            pt="sm"
            style={{ borderTop: '1px solid var(--st-hairline, var(--astr-border))' }}
          >
            <Button
              size="xs"
              variant="subtle"
              color="gray"
              leftSection={<IconRefresh size={14} />}
              loading={reloading}
              onClick={() => {
                void handleRescan()
              }}
            >
              重新扫描插件目录
            </Button>
            <Button size="xs" variant="default" onClick={resetAndClose}>
              关闭
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}
