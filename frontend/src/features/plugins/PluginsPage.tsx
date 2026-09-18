import { useState } from 'react'
import {
  ActionIcon,
  Badge,
  Button,
  Card,
  Collapse,
  Divider,
  Group,
  Modal,
  Paper,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import {
  IconAdjustments,
  IconChevronDown,
  IconChevronUp,
  IconInfoCircle,
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
  const [infoOpened, setInfoOpened] = useState(false)
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
            {viewer.id === 'blackboard-viewer' && (
              <Group gap={6} mt={6}>
                <Badge size="xs" color="indigo" variant="light">
                  星序多 Agent 核心总线
                </Badge>
                <Badge size="xs" color="cyan" variant="light">
                  json-render 视觉引擎
                </Badge>
                <Badge size="xs" color="teal" variant="light">
                  星系自动物理隔离
                </Badge>
              </Group>
            )}
          </div>
        </Group>

        <Group gap="xs" wrap="nowrap" align="center">
          {/* 叹号说明按钮：点击弹窗查看能力与支持组件 */}
          <Tooltip label="查看插件说明与支持能力" withArrow>
            <ActionIcon
              size="sm"
              variant="subtle"
              color="gray"
              onClick={() => setInfoOpened(true)}
              aria-label={`查看 ${viewer.title} 说明`}
            >
              <IconInfoCircle size={17} />
            </ActionIcon>
          </Tooltip>

          <Switch
            checked={isEnabled}
            onChange={(e) => togglePlugin(viewer.id, e.currentTarget.checked)}
            size="sm"
            color="indigo"
            aria-label={`启用或禁用 ${viewer.title}`}
          />
        </Group>
      </Group>

      {/* 参数配置折叠区 */}
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

      {/* 插件能力说明与支持组件清单弹窗 */}
      <Modal
        opened={infoOpened}
        onClose={() => setInfoOpened(false)}
        size="lg"
        radius="lg"
        centered
        title={
          <Group gap="xs">
            <div
              style={{
                width: 28,
                height: 28,
                borderRadius: 6,
                background: 'rgba(99, 102, 241, 0.1)',
                color: 'var(--astr-indigo, #5B5BD6)',
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'center',
              }}
            >
              <Icon size={18} />
            </div>
            <Text fw={700} size="md">
              {viewer.title} · 能力说明
            </Text>
            {viewer.version && (
              <Badge size="xs" variant="light" color="indigo">
                v{viewer.version}
              </Badge>
            )}
          </Group>
        }
      >
        <Stack gap="md">
          <Text size="sm" c="dimmed" style={{ lineHeight: 1.6 }}>
            {viewer.documentation?.summary || viewer.description || '为工作区提供专有交互呈现与处理能力。'}
          </Text>

          {/* 专属能力章节 */}
          {viewer.documentation?.sections?.map((sec, idx) => (
            <div
              key={idx}
              style={{
                padding: '10px 12px',
                borderRadius: 8,
                background: 'var(--astr-surface-muted, #F8FAFC)',
                border: '1px solid var(--astr-border, #E5E7EB)',
              }}
            >
              <Text size="xs" fw={700} c="var(--astr-text, #1E293B)" mb={4}>
                {sec.title}
              </Text>
              <Text size="11.5px" c="dimmed" style={{ whiteSpace: 'pre-wrap', lineHeight: 1.55 }}>
                {sec.content}
              </Text>
            </div>
          ))}

          {/* 支持的组件清单 (主要针对黑板等 json-render 插件) */}
          {viewer.documentation?.supportedComponents && viewer.documentation.supportedComponents.length > 0 && (
            <div>
              <Group justify="space-between" mb={8}>
                <Text size="xs" fw={700} c="dimmed" style={{ letterSpacing: '0.4px', textTransform: 'uppercase' }}>
                  当前支持的可视化组件清单 (json-render 组件库)
                </Text>
                <Badge size="xs" variant="light" color="indigo">
                  共 {viewer.documentation.supportedComponents.length} 项组件
                </Badge>
              </Group>

              <div
                style={{
                  maxHeight: 280,
                  overflowY: 'auto',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 6,
                  paddingRight: 4,
                }}
              >
                {viewer.documentation.supportedComponents.map((comp) => (
                  <div
                    key={comp.name}
                    style={{
                      padding: '8px 10px',
                      borderRadius: 6,
                      background: 'var(--astr-surface-muted, #F8FAFC)',
                      border: '1px solid var(--astr-border, #E5E7EB)',
                      fontSize: 11.5,
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 2 }}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <code style={{ fontFamily: 'monospace', fontWeight: 700, color: '#5B5BD6' }}>
                          {comp.name}
                        </code>
                        <span style={{ fontWeight: 600, color: '#1E293B' }}>- {comp.label}</span>
                      </div>
                      {comp.triggerKeys && comp.triggerKeys.length > 0 && (
                        <div style={{ display: 'flex', gap: 4 }}>
                          {comp.triggerKeys.map((tk, tIdx) => (
                            <Badge key={tIdx} size="xs" variant="outline" color="gray" style={{ fontSize: 9.5 }}>
                              {tk}
                            </Badge>
                          ))}
                        </div>
                      )}
                    </div>
                    <Text size="11px" c="dimmed" style={{ lineHeight: 1.45 }}>
                      {comp.description}
                    </Text>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* 支持的文件扩展名 */}
          {viewer.extensions && viewer.extensions.length > 0 && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 11 }}>
              <span style={{ color: '#64748B' }}>关联文件类型:</span>
              <Group gap={4}>
                {viewer.extensions.map((ext) => (
                  <Badge key={ext} size="xs" variant="light" color="gray">
                    {ext}
                  </Badge>
                ))}
              </Group>
            </div>
          )}

          <Group justify="flex-end" mt="xs">
            <Button size="xs" variant="default" onClick={() => setInfoOpened(false)}>
              关闭
            </Button>
          </Group>
        </Stack>
      </Modal>
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
