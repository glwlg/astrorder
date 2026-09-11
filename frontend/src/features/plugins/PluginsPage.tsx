import { useState } from 'react'
import {
  Badge,
  Button,
  Card,
  Collapse,
  Divider,
  Group,
  Paper,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import {
  IconAdjustments,
  IconChevronDown,
  IconChevronUp,
  IconPuzzle,
} from '@tabler/icons-react'
import { artifactViewerRegistry } from '../sidecar/registry'
import type { ArtifactViewer, ViewerConfigOption } from '../sidecar/types'
import { usePluginSettingsStore } from './pluginSettingsStore'

function PluginCard({ viewer }: { viewer: ArtifactViewer }) {
  const isEnabled = usePluginSettingsStore((s) => s.isPluginEnabled(viewer.id))
  const togglePlugin = usePluginSettingsStore((s) => s.togglePlugin)
  const setPluginConfig = usePluginSettingsStore((s) => s.setPluginConfig)
  const getPluginConfig = usePluginSettingsStore((s) => s.getPluginConfig)

  const [expanded, setExpanded] = useState(false)
  const Icon = viewer.icon
  const hasOptions = viewer.configOptions && viewer.configOptions.length > 0

  return (
    <Card withBorder radius="md" p="md" style={{ background: 'var(--astr-surface)' }}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Group gap="sm" wrap="nowrap" align="flex-start">
          <div
            style={{
              padding: 8,
              borderRadius: 8,
              background: isEnabled ? 'rgba(99, 102, 241, 0.1)' : 'var(--astr-surface-muted)',
              color: isEnabled ? 'var(--astr-indigo)' : 'var(--astr-muted)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            <Icon size={24} />
          </div>
          <div>
            <Group gap="xs" align="center">
              <Text fw={600} size="sm">
                {viewer.title}
              </Text>
              {viewer.version && (
                <Badge size="xs" variant="outline" color="gray">
                  v{viewer.version}
                </Badge>
              )}
              {viewer.capabilities.canEdit && (
                <Badge size="xs" variant="light" color="blue">
                  支持编辑写回
                </Badge>
              )}
            </Group>
            <Text size="xs" c="dimmed" mt={4}>
              {viewer.description || '为工作区提供专有媒体与格式的交互渲染与处理支持。'}
            </Text>
          </div>
        </Group>

        <Group gap="xs" wrap="nowrap">
          <Switch
            checked={isEnabled}
            onChange={(e) => togglePlugin(viewer.id, e.currentTarget.checked)}
            size="sm"
            color="indigo"
            aria-label={`启用或禁用 ${viewer.title}`}
          />
        </Group>
      </Group>

      {hasOptions && (
        <>
          <Divider my="sm" />
          <Group justify="space-between" align="center">
            <Button
              variant="subtle"
              size="compact-xs"
              color="gray"
              leftSection={<IconAdjustments size={13} />}
              rightSection={expanded ? <IconChevronUp size={13} /> : <IconChevronDown size={13} />}
              onClick={() => setExpanded(!expanded)}
            >
              参数配置 ({viewer.configOptions?.length})
            </Button>
            <Text size="xs" c="dimmed">
              {isEnabled ? '生效中' : '已停用'}
            </Text>
          </Group>

          <Collapse expanded={expanded}>
            <Stack gap="xs" mt="xs" p="xs" style={{ background: 'var(--astr-surface-muted)', borderRadius: 6 }}>
              {viewer.configOptions?.map((opt: ViewerConfigOption) => {
                const currentVal = getPluginConfig(viewer.id, opt.key, opt.defaultValue)

                if (opt.type === 'boolean') {
                  return (
                    <Group key={opt.key} justify="space-between">
                      <div>
                        <Text size="xs" fw={500}>
                          {opt.label}
                        </Text>
                        {opt.description && (
                          <Text size="11px" c="dimmed">
                            {opt.description}
                          </Text>
                        )}
                      </div>
                      <Switch
                        size="xs"
                        checked={Boolean(currentVal)}
                        onChange={(e) => setPluginConfig(viewer.id, opt.key, e.currentTarget.checked)}
                      />
                    </Group>
                  )
                }

                if (opt.type === 'select' && opt.options) {
                  return (
                    <div key={opt.key}>
                      <Text size="xs" fw={500} mb={2}>
                        {opt.label}
                      </Text>
                      {opt.description && (
                        <Text size="11px" c="dimmed" mb={4}>
                          {opt.description}
                        </Text>
                      )}
                      <Select
                        size="xs"
                        data={opt.options}
                        value={String(currentVal)}
                        onChange={(val) => setPluginConfig(viewer.id, opt.key, val)}
                      />
                    </div>
                  )
                }

                return (
                  <TextInput
                    key={opt.key}
                    size="xs"
                    label={opt.label}
                    description={opt.description}
                    value={String(currentVal ?? '')}
                    onChange={(e) => setPluginConfig(viewer.id, opt.key, e.currentTarget.value)}
                  />
                )
              })}
            </Stack>
          </Collapse>
        </>
      )}
    </Card>
  )
}

export function PluginsPage() {
  const viewers = artifactViewerRegistry.getAllViewers()
  const enabledCount = viewers.filter((v) => usePluginSettingsStore.getState().isPluginEnabled(v.id)).length

  return (
    <div className="route-page plugins-page" style={{ padding: '24px 32px', maxWidth: 1100, margin: '0 auto' }}>
      <Paper p="md" withBorder radius="lg" mb="lg" style={{ background: 'var(--astr-surface)' }}>
        <Group justify="space-between" align="center">
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
              <IconPuzzle size={22} />
            </div>
            <div>
              <Title order={2} size="h3">
                插件与工件查看器管理
              </Title>
              <Text size="xs" c="dimmed" mt={2}>
                管理星序会话右侧工作台支持的富媒介渲染器与编辑插件，支持按需启用、停用及个性化参数配置。
              </Text>
            </div>
          </Group>

          <Group gap="xs">
            <Badge size="md" variant="light" color="indigo">
              已启用 {enabledCount} / {viewers.length}
            </Badge>
          </Group>
        </Group>
      </Paper>

      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        {viewers.map((viewer) => (
          <PluginCard key={viewer.id} viewer={viewer} />
        ))}
      </SimpleGrid>
    </div>
  )
}
