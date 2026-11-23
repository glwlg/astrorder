import { useCallback, useEffect, useRef, useState } from 'react'
import { ActionIcon, Badge, Button, Group, Modal, Paper, ScrollArea, Stack, Text, TextInput, Textarea, Tooltip } from '@mantine/core'
import { IconChalkboard, IconDownload, IconPlus, IconRefresh, IconX } from '@tabler/icons-react'
import { notifications } from '@mantine/notifications'
import { api } from '../../api/client'
import { GroupBlackboardItem as BlackboardItemRenderer } from './GroupBlackboardItem'

const BLACKBOARD_FALLBACK_POLL_MS = 30_000
const BLACKBOARD_EVENT_DEBOUNCE_MS = 180

export function SidecarBlackboardPanel({
  namespace,
  title = '黑板',
  onClose,
  focusKey,
  focusRequest,
}: {
  namespace: string
  title?: string
  onClose?: () => void
  focusKey?: string
  focusRequest?: number
}) {
  const [items, setItems] = useState<Record<string, unknown>>({})
  const [loading, setLoading] = useState(false)
  const [createOpened, setCreateOpened] = useState(false)
  const [newKey, setNewKey] = useState('')
  const [newVal, setNewVal] = useState('')
  const itemRefs = useRef(new Map<string, HTMLDivElement>())
  const mountedRef = useRef(true)
  const inFlightRef = useRef(false)
  const lastFetchedAtRef = useRef(0)
  const focusedItemAvailable = Boolean(focusKey && Object.hasOwn(items, focusKey))

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    if (!focusKey || !focusedItemAvailable) return
    const entry = itemRefs.current.get(focusKey)
    entry?.scrollIntoView({ block: 'nearest', behavior: 'instant' })
    entry?.focus({ preventScroll: true })
  }, [focusKey, focusRequest, focusedItemAvailable])

  const fetchItems = useCallback(async (force = false) => {
    if (inFlightRef.current && !force) return
    inFlightRef.current = true
    setLoading(true)
    try {
      const res = await api.getBlackboard(namespace)
      if (!mountedRef.current) return
      lastFetchedAtRef.current = Date.now()
      setItems(res.items || {})
    } catch {
      // fallback
    } finally {
      inFlightRef.current = false
      if (mountedRef.current) setLoading(false)
    }
  }, [namespace])

  useEffect(() => {
    let debounceTimer: ReturnType<typeof setTimeout> | undefined
    void fetchItems(true)

    const onBlackboardChange = (event: Event) => {
      const detail = (event as CustomEvent<{ namespace?: string } | undefined>).detail
      if (detail?.namespace && detail.namespace !== namespace) return
      clearTimeout(debounceTimer)
      debounceTimer = setTimeout(() => {
        void fetchItems()
      }, BLACKBOARD_EVENT_DEBOUNCE_MS)
    }

    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible' && Date.now() - lastFetchedAtRef.current >= 10_000) {
        void fetchItems()
      }
    }

    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') {
        void fetchItems()
      }
    }, BLACKBOARD_FALLBACK_POLL_MS)

    window.addEventListener('astrorder:blackboard-change', onBlackboardChange)
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      clearTimeout(debounceTimer)
      clearInterval(timer)
      window.removeEventListener('astrorder:blackboard-change', onBlackboardChange)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [fetchItems, namespace])

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

  const handleExportMarkdown = () => {
    const lines = [
      `# 星序作战黑板导出报告 (${namespace})`,
      `> 导出时间: ${new Date().toLocaleString()}`,
      '',
    ]
    for (const [k, v] of Object.entries(items)) {
      lines.push(`## ${k}`)
      if (typeof v === 'string') {
        lines.push(v)
      } else {
        lines.push('```json', JSON.stringify(v, null, 2), '```')
      }
      lines.push('')
    }
    const blob = new Blob([lines.join('\n')], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `blackboard-${namespace.replace(/[^a-zA-Z0-9_-]/g, '_')}.md`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
    notifications.show({ color: 'teal', message: '黑板内容已成功导出为本地 Markdown 文档' })
  }

  const entries = Object.entries(items)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', background: 'var(--astr-surface)' }}>
      <div
        className="sidecar-blackboard-header"
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          gap: 8,
          minHeight: 48,
          padding: '6px 12px',
          borderBottom: '1px solid var(--astr-border)',
          background: 'var(--astr-surface)',
          flexShrink: 0,
          minWidth: 0,
        }}
      >
        <Group gap={6} wrap="nowrap" style={{ minWidth: 0, flex: 1 }}>
          <IconChalkboard size={18} color="var(--astr-indigo, #6366f1)" style={{ flexShrink: 0 }} />
          <Text fw={600} size="sm" truncate="end" title={title} style={{ minWidth: 0 }}>{title}</Text>
          <Badge size="xs" variant="light" color="indigo" style={{ flexShrink: 0 }}>{entries.length}</Badge>
        </Group>
        <Group gap={4} wrap="nowrap" style={{ flexShrink: 0 }}>
          <Tooltip label="导出 Markdown 文档" withArrow>
            <ActionIcon variant="subtle" color="indigo" size="sm" aria-label="导出 Markdown 文档" onClick={handleExportMarkdown} disabled={entries.length === 0}>
              <IconDownload size={15} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="刷新" withArrow>
            <ActionIcon variant="subtle" color="gray" size="sm" aria-label="刷新黑板" onClick={() => void fetchItems(true)} loading={loading}>
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
            <ActionIcon className="sidecar-blackboard-close" variant="subtle" color="gray" size="sm" aria-label="关闭黑板" onClick={onClose}>
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
              <div
                key={k}
                className="group-blackboard-entry"
                data-focused={focusKey === k || undefined}
                tabIndex={focusKey === k ? -1 : undefined}
                aria-label={`黑板条目 ${k}`}
                ref={(element) => { if (element) itemRefs.current.set(k, element); else itemRefs.current.delete(k) }}
              >
                <BlackboardItemRenderer
                  itemKey={k}
                  itemValue={v}
                  namespace={namespace}
                  onUpdated={() => void fetchItems(true)}
                  onDelete={() => void handleDelete(k)}
                />
              </div>
            ))}
          </Stack>
        )}
      </ScrollArea>

      <Modal opened={createOpened} onClose={() => setCreateOpened(false)} title="向黑板写入参数" centered size="sm" zIndex={450}>
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
