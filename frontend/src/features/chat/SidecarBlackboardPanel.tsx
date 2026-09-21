import { useEffect, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Modal, Paper, ScrollArea, Stack, Text, TextInput, Textarea, Tooltip } from '@mantine/core'
import { IconChalkboard, IconPlus, IconRefresh, IconX } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api } from '../../api/client'
import { BlackboardItemRenderer } from '../monitor/BlackboardJsonRender'

export function SidecarBlackboardPanel({
  namespace,
  title = '黑板',
  onClose,
}: {
  namespace: string
  title?: string
  onClose?: () => void
}) {
  const [items, setItems] = useState<Record<string, unknown>>({})
  const [loading, setLoading] = useState(false)
  const [createOpened, setCreateOpened] = useState(false)
  const [newKey, setNewKey] = useState('')
  const [newVal, setNewVal] = useState('')

  const fetchItems = async () => {
    setLoading(true)
    try {
      const res = await api.getBlackboard(namespace)
      setItems(res.items || {})
    } catch {
      // fallback
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void fetchItems()
    const timer = setInterval(() => void fetchItems(), 4000)
    return () => clearInterval(timer)
  }, [namespace])

  const handleSave = async () => {
    if (!newKey.trim()) return
    let parsed: unknown = newVal
    try {
      parsed = JSON.parse(newVal)
    } catch {
      // raw string
    }
    try {
      await api.setBlackboard(newKey.trim(), parsed, namespace)
      notifications.show({ color: 'teal', message: `已写入参数: ${newKey}` })
      setCreateOpened(false)
      setNewKey('')
      setNewVal('')
      void fetchItems()
    } catch (e: any) {
      notifications.show({ color: 'red', message: e.message || '写入黑板失败' })
    }
  }

  const handleDelete = async (key: string) => {
    try {
      await api.deleteBlackboard(key, namespace)
      notifications.show({ color: 'gray', message: `已移除: ${key}` })
      void fetchItems()
    } catch (e: any) {
      notifications.show({ color: 'red', message: e.message || '删除失败' })
    }
  }

  const entries = Object.entries(items)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--astr-surface)' }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          height: 48,
          padding: '0 12px',
          borderBottom: '1px solid var(--astr-border)',
          background: 'var(--astr-surface)',
          flexShrink: 0,
        }}
      >
        <Group gap="xs">
          <IconChalkboard size={18} color="var(--astr-indigo, #6366f1)" />
          <Text fw={600} size="sm">{title}</Text>
          <Badge size="xs" variant="light" color="indigo">{entries.length}</Badge>
        </Group>
        <Group gap={6}>
          <Tooltip label="刷新" withArrow>
            <ActionIcon variant="subtle" color="gray" size="sm" onClick={fetchItems} loading={loading}>
              <IconRefresh size={15} />
            </ActionIcon>
          </Tooltip>
          <Button
            size="compact-xs"
            variant="light"
            color="indigo"
            leftSection={<IconPlus size={13} />}
            onClick={() => setCreateOpened(true)}
          >
            写入
          </Button>
          {onClose && (
            <ActionIcon variant="subtle" color="gray" size="sm" onClick={onClose}>
              <IconX size={15} />
            </ActionIcon>
          )}
        </Group>
      </div>

      <ScrollArea style={{ flex: 1, padding: 12 }}>
        {entries.length === 0 ? (
          <Paper withBorder p="xl" radius="md" style={{ textAlign: 'center', background: 'transparent', marginTop: 24 }}>
            <Text size="sm" c="dimmed">当前黑板暂无共享参数</Text>
            <Text size="xs" c="dimmed" mt={4}>Agent 协同过程中调用 blackboard.set 即可将参数和组件渲染在此处</Text>
          </Paper>
        ) : (
          <Stack gap="xs">
            {entries.slice().reverse().map(([k, v]) => (
              <BlackboardItemRenderer
                key={k}
                itemKey={k}
                itemValue={v}
                namespace={namespace}
                onUpdated={fetchItems}
                onDelete={() => void handleDelete(k)}
              />
            ))}
          </Stack>
        )}
      </ScrollArea>

      <Modal opened={createOpened} onClose={() => setCreateOpened(false)} title="向黑板写入参数" centered size="sm">
        <Stack gap="sm">
          <TextInput
            label="参数 Key"
            placeholder="如 milestone_metrics, service_spec"
            value={newKey}
            onChange={(e) => setNewKey(e.currentTarget.value)}
            required
          />
          <Textarea
            label="参数值 (Value)"
            placeholder="支持任意字符串或合法 JSON"
            minRows={3}
            maxRows={8}
            autosize
            value={newVal}
            onChange={(e) => setNewVal(e.currentTarget.value)}
          />
          <Group justify="flex-end" mt="xs">
            <Button variant="default" size="xs" onClick={() => setCreateOpened(false)}>取消</Button>
            <Button color="indigo" size="xs" onClick={handleSave} disabled={!newKey.trim()}>保存发布</Button>
          </Group>
        </Stack>
      </Modal>
    </div>
  )
}
